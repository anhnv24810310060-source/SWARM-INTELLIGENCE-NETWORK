package scanner

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/rand"
	"sync"
	"time"
)

// ExtendedRule augments Rule with optional metadata used only at runtime.
type ExtendedRule struct {
	Rule
	SamplePercent int      // 1..100 (default 100)
	Severity      string   // low|medium|high|critical
	Tags          []string // optional classification labels
	rawPatternLen int
}

// acNode internal automaton node
type acNode struct {
	next map[byte]*acNode
	fail *acNode
	out  []*ExtendedRule
}

// AhoAutomaton encapsulates compiled multi-pattern matcher
type AhoAutomaton struct {
	root       *acNode
	ruleCount  int
	buildHash  string // fingerprint of rule set (for diagnostics)
	buildNanos int64
}

// AhoScanner is concurrency-safe after construction; cloning not required.
type AhoScanner struct {
	automaton *AhoAutomaton
	rngPool   sync.Pool // per-scan RNG for sampling
}

// BuildAho builds an automaton from ExtendedRule slice; returns error if invalid rule encountered.
func BuildAho(rules []ExtendedRule) (*AhoAutomaton, error) {
	start := time.Now()
	root := &acNode{next: make(map[byte]*acNode)}
	h := sha256.New()
	added := 0
	for _, r := range rules {
		if !r.Enabled || r.Pattern == "" {
			continue
		}
		if r.SamplePercent <= 0 || r.SamplePercent > 100 {
			return nil, errors.New("invalid sample_percent for rule " + r.ID)
		}
		added++
		h.Write([]byte(r.ID))
		h.Write([]byte{0})
		h.Write([]byte(r.Pattern))
		cur := root
		for i := 0; i < len(r.Pattern); i++ {
			b := r.Pattern[i]
			nxt, ok := cur.next[b]
			if !ok {
				nxt = &acNode{next: make(map[byte]*acNode)}
				cur.next[b] = nxt
			}
			cur = nxt
		}
		// store pointer copy to keep metadata
		rr := r
		rr.rawPatternLen = len(r.Pattern)
		cur.out = append(cur.out, &rr)
	}
	// BFS failure links
	queue := make([]*acNode, 0, len(root.next))
	for _, n := range root.next {
		n.fail = root
		queue = append(queue, n)
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for b, nxt := range n.next {
			f := n.fail
			for f != nil && f.next[b] == nil {
				f = f.fail
			}
			if f == nil {
				nxt.fail = root
			} else {
				nxt.fail = f.next[b]
			}
			if nxt.fail != nil && len(nxt.fail.out) > 0 {
				nxt.out = append(nxt.out, nxt.fail.out...)
			}
			queue = append(queue, nxt)
		}
	}
	fp := hex.EncodeToString(h.Sum(nil))[:16]
	return &AhoAutomaton{root: root, ruleCount: added, buildHash: fp, buildNanos: time.Since(start).Nanoseconds()}, nil
}

// NewAhoScanner constructs scanner; expects non-nil automaton
func NewAhoScanner(auto *AhoAutomaton) *AhoScanner {
	s := &AhoScanner{automaton: auto}
	s.rngPool.New = func() any { return rand.New(rand.NewSource(time.Now().UnixNano())) }
	return s
}

// MatchResult enriched version for rule metadata (maps to public struct in main service layer)
type MatchResult struct {
	RuleID    string `json:"rule_id"`
	RuleType  string `json:"rule_type"`
	Offset    int    `json:"offset"`
	Length    int    `json:"length"`
	Severity  string `json:"severity,omitempty"`
	Version   int    `json:"version,omitempty"`
	Sampled   bool   `json:"sampled"` // true if included after sampling gate
	Automaton string `json:"automaton_hash,omitempty"`
}

// Scan performs multi-pattern search with sampling.
func (s *AhoScanner) Scan(data []byte) []MatchResult {
	if s.automaton == nil || s.automaton.root == nil {
		return nil
	}
	rng := s.rngPool.Get().(*rand.Rand)
	defer s.rngPool.Put(rng)
	var results []MatchResult
	n := s.automaton.root
	for i, b := range data {
		for n != nil && n.next[b] == nil {
			n = n.fail
		}
		if n == nil { // restart
			n = s.automaton.root
			continue
		}
		n = n.next[b]
		if len(n.out) == 0 {
			continue
		}
		for _, er := range n.out {
			// sampling gate
			sampled := true
			if er.SamplePercent < 100 {
				if rng.Intn(100) >= er.SamplePercent { // drop
					continue
				}
			}
			offset := i - er.rawPatternLen + 1
			if offset < 0 { // safety
				continue
			}
			results = append(results, MatchResult{
				RuleID:    er.ID,
				RuleType:  er.Type,
				Offset:    offset,
				Length:    er.rawPatternLen,
				Severity:  er.Severity,
				Version:   er.Version,
				Sampled:   sampled,
				Automaton: s.automaton.buildHash,
			})
		}
	}
	return results
}
