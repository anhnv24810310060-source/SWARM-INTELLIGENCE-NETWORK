package scanner

import "time"

// Interface-first design for high-performance scanning pipeline.
// Future optimization hooks:
//  - Aho-Corasick automaton for multiple pattern matches
//  - SIMD accelerated byte search (via assembly / CGO)
//  - Streaming API for large payloads

// Rule describes a scanning rule (shared minimal contract)
type Rule struct {
	ID      string
	Type    string // e.g., "yara", "dsl"
	Pattern string
	Version int
	Enabled bool
}

// Store provides retrieval + versioning semantics
type Store interface {
	All() []Rule
	ByID(id string) (Rule, bool)
}

// Match describes a detection result
type Match struct {
	RuleID   string
	RuleType string
	Offset   int
	Length   int
}

// Engine scans byte payloads for matches
type Engine interface {
	Scan(data []byte) []Match
}

// PerfStats holds optional telemetry emitted per batch
type PerfStats struct {
	Duration     time.Duration
	ScannedBytes int
	MatchCount   int
}
