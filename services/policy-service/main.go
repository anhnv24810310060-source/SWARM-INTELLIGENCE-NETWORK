package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	sloglog "github.com/swarmguard/libs/go/core/logging"
	"github.com/swarmguard/libs/go/core/otelinit"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	// fs notify for efficient reloads
	"github.com/fsnotify/fsnotify"
)

// In-memory policy representation.
type policy struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
	Rule    string `json:"rule"` // simple expression: allow if rule == "allow_all"
}

type store struct {
	mu       sync.RWMutex
	policies map[string]policy
}

func newStore() *store        { return &store{policies: make(map[string]policy)} }
func (s *store) put(p policy) { s.mu.Lock(); defer s.mu.Unlock(); s.policies[p.Name] = p }
func (s *store) get(name string) (policy, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.policies[name]
	return p, ok
}

type evalRequest struct {
	Policy string         `json:"policy"`
	Input  map[string]any `json:"input"`
}
type evalResponse struct {
	Allow  bool   `json:"allow"`
	Reason string `json:"reason"`
}

func main() {
	service := "policy-service"
	sloglog.Init(service)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	shutdownTrace := otelinit.InitTracer(ctx, service)
	shutdownMetrics, promHandler, _ := otelinit.InitMetrics(ctx, service)

	meter := otel.GetMeterProvider().Meter("policy-service")
	evalCounter, _ := meter.Int64Counter("swarm_policy_evaluations_total")
	denyCounter, _ := meter.Int64Counter("swarm_policy_denials_total")
	reloadCounter, _ := meter.Int64Counter("swarm_policy_reloads_total")
	reloadErrCounter, _ := meter.Int64Counter("swarm_policy_reload_errors_total")
	evalLatency, _ := meter.Float64Histogram("swarm_policy_evaluation_latency_ms")
	compileLatency, _ := meter.Float64Histogram("swarm_policy_compile_latency_ms")
	cacheHitCounter, _ := meter.Int64Counter("swarm_policy_cache_hits_total")
	cacheMissCounter, _ := meter.Int64Counter("swarm_policy_cache_misses_total")
	rateLimitedCounter, _ := meter.Int64Counter("swarm_policy_rate_limited_total")

	mode := os.Getenv("POLICY_MODE") // "simple" or "opa"
	if mode == "" {
		mode = "opa"
	}
	policyDir := os.Getenv("POLICY_DIR")
	if policyDir == "" {
		policyDir = "./policies"
	}

	st := newStore()
	st.put(policy{Name: "default", Version: 1, Rule: "allow_all"})

	// Decision cache (LRU) size configurable
	cacheSize := intFromEnv("POLICY_DECISION_CACHE_SIZE", 1024)
	dc := newDecisionCache(cacheSize)
	// Basic in-memory rate limiter (global) to protect evaluation path
	rlCapacity := intFromEnv("POLICY_RATE_LIMIT_CAPACITY", 5000)
	rlRefill := intFromEnv("POLICY_RATE_LIMIT_REFILL", 5000)
	rlIntervalSec := intFromEnv("POLICY_RATE_LIMIT_INTERVAL_SEC", 60)
	limiter := newRateLimiter(rlCapacity, rlRefill, time.Duration(rlIntervalSec)*time.Second)

	opaMgr := newOPAManager(policyDir, compileLatency)
	if mode == "opa" {
		if err := opaMgr.Load(); err != nil {
			slog.Error("initial opa load failed", "error", err)
			reloadErrCounter.Add(ctx, 1)
		} else {
			reloadCounter.Add(ctx, 1)
		}
		go opaMgr.Watch(ctx, func(err error) {
			if err != nil {
				reloadErrCounter.Add(ctx, 1)
				return
			}
			reloadCounter.Add(ctx, 1)
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readiness", func(w http.ResponseWriter, r *http.Request) {
		// readiness = OPA loaded (in opa mode) else always ready
		if mode == "opa" && !opaMgr.IsReady() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})

	mux.HandleFunc("/v1/policies", func(w http.ResponseWriter, r *http.Request) {
		if mode == "opa" {
			http.Error(w, "OPA mode active - direct CRUD disabled", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost {
			var p policy
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if p.Name == "" {
				http.Error(w, "name required", http.StatusBadRequest)
				return
			}
			if p.Version == 0 {
				p.Version = 1
			}
			st.put(p)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(p)
			return
		}
		if r.Method == http.MethodGet {
			name := r.URL.Query().Get("name")
			if name == "" {
				http.Error(w, "name query required", http.StatusBadRequest)
				return
			}
			p, ok := st.get(name)
			if !ok {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(p)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	})

	mux.HandleFunc("/v1/evaluate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !limiter.allow("global") {
			rateLimitedCounter.Add(r.Context(), 1)
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		var req evalRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		ctxEval, spanEnd := otelinit.WithSpan(r.Context(), "policy.evaluate")
		defer spanEnd()
		start := time.Now()
		allow := false
		reason := "deny"
		cacheKey := "" // only set for opa mode with deterministic input
		if mode == "opa" {
			input := req.Input
			if input == nil {
				input = map[string]any{}
			}
			cacheKey = stableCacheKey(input)
			if v, ok := dc.get(cacheKey); ok {
				allow = v
				if allow {
					reason = "cache_allow"
				} else {
					reason = "cache_deny"
				}
				cacheHitCounter.Add(ctxEval, 1)
			} else {
				decision, err := opaMgr.Evaluate(ctxEval, input)
				if err != nil {
					if errors.Is(err, errNoDecision) {
						reason = "no_decision"
					} else {
						reason = "error"
						http.Error(w, "evaluation error", http.StatusInternalServerError)
						return
					}
				} else {
					allow = decision
					if allow {
						reason = "opa_allow"
					} else {
						reason = "opa_deny"
					}
					dc.put(cacheKey, allow)
				}
				cacheMissCounter.Add(ctxEval, 1)
			}
		} else {
			p, ok := st.get(req.Policy)
			if !ok {
				http.Error(w, "policy not found", http.StatusNotFound)
				return
			}
			allow = p.Rule == "allow_all"
			if allow {
				reason = "rule_allow_all"
			} else {
				reason = "rule_deny"
			}
		}
		slog.Info("policy decision", "mode", mode, "allow", allow, "reason", reason)
		evalCounter.Add(ctxEval, 1, metric.WithAttributes(attribute.String("mode", mode)))
		if !allow {
			denyCounter.Add(ctxEval, 1, metric.WithAttributes(attribute.String("mode", mode)))
		}
		evalLatency.Record(ctxEval, float64(time.Since(start).Milliseconds()), metric.WithAttributes(attribute.String("mode", mode)))
		_ = json.NewEncoder(w).Encode(evalResponse{Allow: allow, Reason: reason})
	})

	mux.HandleFunc("/v1/reload", func(w http.ResponseWriter, r *http.Request) {
		if mode != "opa" {
			http.Error(w, "not in opa mode", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := opaMgr.Load(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		reloadCounter.Add(r.Context(), 1)
		w.WriteHeader(http.StatusNoContent)
	})

	if promHandler != nil {
		if h, ok := promHandler.(http.Handler); ok {
			mux.Handle("/metrics", h)
		}
	}

	// ensure policyDir exists so watcher doesn't error
	_ = os.MkdirAll(policyDir, 0o755)
	// seed example policy if empty
	if entries, _ := os.ReadDir(policyDir); len(entries) == 0 {
		_ = os.WriteFile(filepath.Join(policyDir, "allow_read.rego"), []byte("package swarm\n\n# Allow read actions\nallow { input.action == \"read\" }\n"), 0o644)
		_ = opaMgr.Load()
	}

	srv := &http.Server{Addr: ":8080", Handler: mux}
	// expose pprof and internal debug on separate listener (non-blocking)
	go func() { _ = http.ListenAndServe(":6060", nil) }()
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			cancel()
		}
	}()
	slog.Info("service started", "mode", mode, "dir", policyDir)
	<-ctx.Done()
	slog.Info("shutdown initiated")
	ctxSd, c2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer c2()
	_ = srv.Shutdown(ctxSd)
	otelinit.Flush(ctxSd, shutdownTrace)
	_ = shutdownMetrics(ctxSd)
	slog.Info("shutdown complete")
}

// --- OPA Manager Implementation (lightweight) ---

type histogramRecorder interface {
	Record(ctx context.Context, value float64, opts ...metric.RecordOption)
}

type opaManager struct {
	dir            string
	mu             sync.RWMutex
	query          compiledQuery
	compileLatency histogramRecorder
}

func newOPAManager(dir string, h histogramRecorder) *opaManager {
	return &opaManager{dir: dir, compileLatency: h}
}

// compiledQuery abstracts underlying evaluation (allow decision)
type compiledQuery interface {
	Eval(ctx context.Context, input map[string]any) (bool, error)
}

var errNoDecision = errors.New("no decision")

func (m *opaManager) Load() error {
	started := time.Now()
	mods := map[string]string{}
	err := filepath.Walk(m.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".rego" {
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			mods[path] = string(b)
		}
		return nil
	})
	if err != nil {
		return err
	}
	q, err := compileRego(mods)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.query = q
	// always record compile latency (histogram no-op if provider not set)
	m.compileLatency.Record(context.Background(), float64(time.Since(started).Milliseconds()))
	return nil
}

func (m *opaManager) Evaluate(ctx context.Context, input map[string]any) (bool, error) {
	m.mu.RLock()
	q := m.query
	m.mu.RUnlock()
	if q == nil {
		return false, errNoDecision
	}
	return q.Eval(ctx, input)
}

func (m *opaManager) IsReady() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.query != nil
}

func (m *opaManager) Watch(ctx context.Context, cb func(error)) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		cb(err)
		return
	}
	defer watcher.Close()
	if err := watcher.Add(m.dir); err != nil {
		cb(err)
		return
	}
	debounce := time.NewTimer(time.Hour)
	if !debounce.Stop() {
		<-debounce.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-watcher.Events:
			if filepath.Ext(ev.Name) == ".rego" {
				// debounce rapid changes
				debounce.Reset(200 * time.Millisecond)
			}
		case err := <-watcher.Errors:
			cb(err)
		case <-debounce.C:
			if err := m.Load(); err != nil {
				cb(err)
			} else {
				cb(nil)
			}
		}
	}
}

// compileRego builds a tiny evaluator parsing for pattern: default allow = false; allow rule(s)
// For performance we avoid full OPA SDK initially; placeholder for real rego compile.
func compileRego(mods map[string]string) (compiledQuery, error) {
	// naive parse: if any module contains line starting with 'allow {' then treat as allow rule(s).
	allowModules := 0
	for _, src := range mods {
		if containsAllowRule(src) {
			allowModules++
		}
	}
	return simpleQuery{allowModules: allowModules, modules: len(mods)}, nil
}

type simpleQuery struct {
	allowModules int
	modules      int
}

func (s simpleQuery) Eval(_ context.Context, input map[string]any) (bool, error) {
	if s.modules == 0 {
		return false, errNoDecision
	}
	// extremely simplified: allow if input.action == "read" or allow rule existed and action not blocked
	if v, ok := input["action"].(string); ok && v == "read" {
		return true, nil
	}
	if s.allowModules > 0 {
		return false, nil
	}
	return false, errNoDecision
}

func containsAllowRule(src string) bool {
	// minimal substring search
	return bytesContains([]byte(src), []byte("allow {"))
}

func bytesContains(haystack, needle []byte) bool {
	// Boyer-Moore-Horspool for efficiency on larger policy files
	n := len(needle)
	h := len(haystack)
	if n == 0 {
		return true
	}
	if n > h {
		return false
	}
	// build bad char shift table (last occurrence)
	var table [256]int
	for i := 0; i < 256; i++ {
		table[i] = n
	}
	for i := 0; i < n-1; i++ {
		table[needle[i]] = n - 1 - i
	}
	i := 0
	for i <= h-n {
		if haystack[i+n-1] == needle[n-1] && matchForward(haystack[i:i+n], needle) {
			return true
		}
		i += table[haystack[i+n-1]]
	}
	return false
}

func matchForward(a, b []byte) bool {
	for i := range b {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- Decision Cache (LRU) ---
type decisionCache struct {
	mu    sync.Mutex
	size  int
	items map[string]*dcEntry
	head  *dcEntry
	tail  *dcEntry
}
type dcEntry struct {
	k    string
	v    bool
	prev *dcEntry
	next *dcEntry
}

func newDecisionCache(size int) *decisionCache {
	if size <= 0 {
		size = 1
	}
	return &decisionCache{size: size, items: make(map[string]*dcEntry)}
}
func (c *decisionCache) get(k string) (bool, bool) {
	if k == "" {
		return false, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[k]; ok {
		c.moveToFront(e)
		return e.v, true
	}
	return false, false
}
func (c *decisionCache) put(k string, v bool) {
	if k == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[k]; ok {
		e.v = v
		c.moveToFront(e)
		return
	}
	e := &dcEntry{k: k, v: v}
	c.items[k] = e
	c.addFront(e)
	if len(c.items) > c.size {
		// evict tail
		if c.tail != nil {
			del := c.tail
			c.remove(del)
			delete(c.items, del.k)
		}
	}
}
func (c *decisionCache) moveToFront(e *dcEntry) {
	if c.head == e {
		return
	}
	c.remove(e)
	c.addFront(e)
}
func (c *decisionCache) addFront(e *dcEntry) {
	e.prev = nil
	e.next = c.head
	if c.head != nil {
		c.head.prev = e
	}
	c.head = e
	if c.tail == nil {
		c.tail = e
	}
}
func (c *decisionCache) remove(e *dcEntry) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		c.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		c.tail = e.prev
	}
	e.prev, e.next = nil, nil
}

// stableCacheKey builds a deterministic key for the input map (limited scope: flat string->primitive)
func stableCacheKey(m map[string]any) string {
	if len(m) == 0 {
		return "_"
	}
	// gather keys
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	b := make([]byte, 0, 128)
	for _, k := range keys {
		b = append(b, k...)
		b = append(b, '=')
		b = append(b, anyToBytes(m[k])...)
		b = append(b, ';')
	}
	return string(b)
}

func anyToBytes(v any) []byte {
	switch t := v.(type) {
	case string:
		return []byte(t)
	case int:
		return []byte(strconv.Itoa(t))
	case int64:
		return []byte(strconv.FormatInt(t, 10))
	case float64:
		return []byte(strconv.FormatFloat(t, 'f', -1, 64))
	case bool:
		if t { return []byte("true") }; return []byte("false")
	default:
		// fallback JSON
		b, _ := json.Marshal(t)
		return b
	}
}

// minimal in-place insertion sort (few keys typical) to avoid pulling full sort pkg
func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		j := i
		for j > 0 && a[j-1] > a[j] {
			a[j-1], a[j] = a[j], a[j-1]
			j--
		}
	}
}

// --- Simple global rate limiter (token bucket) reused from gateway concept ---
type rateLimiter struct {
	mu       sync.Mutex
	capacity int
	refill   int
	interval time.Duration
	tokens   int
	updated  time.Time
}
func newRateLimiter(capacity, refill int, interval time.Duration) *rateLimiter {
	return &rateLimiter{capacity: capacity, refill: refill, interval: interval, tokens: capacity, updated: time.Now()}
}
func (r *rateLimiter) allow(_ string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if elapsed := now.Sub(r.updated); elapsed >= r.interval {
		periods := int(elapsed / r.interval)
		if periods > 0 {
			r.tokens += periods * r.refill
			if r.tokens > r.capacity {
				r.tokens = r.capacity
			}
			r.updated = now
		}
	}
	if r.tokens <= 0 {
		return false
	}
	r.tokens--
	return true
}

// intFromEnv utility (duplicated local to keep main self-contained; consider moving to shared lib later)
func intFromEnv(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}
