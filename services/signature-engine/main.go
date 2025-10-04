package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	corelog "github.com/swarmguard/libs/go/core/logging"
	"github.com/swarmguard/libs/go/core/otelinit"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/swarmguard/signature-engine/scanner"
)

// Rule represents a compiled signature rule (YARA or custom JSON DSL simplified placeholder)
type Rule struct {
	ID            string    `json:"id"`
	Type          string    `json:"type"` // "yara" | "dsl"
	Pattern       string    `json:"pattern"`
	Version       int       `json:"version"`
	Enabled       bool      `json:"enabled"`
	UpdatedAt     time.Time `json:"updated_at"`
	Severity      string    `json:"severity,omitempty"`
	Tags          []string  `json:"tags,omitempty"`
	SamplePercent int       `json:"sample_percent,omitempty"`
}

// MatchResult is emitted when a rule matches a payload
type MatchResult struct {
	RuleID    string `json:"rule_id"`
	RuleType  string `json:"rule_type"`
	Offset    int    `json:"offset"`
	Length    int    `json:"length"`
	Severity  string `json:"severity,omitempty"`
	Version   int    `json:"version,omitempty"`
	Sampled   bool   `json:"sampled"`
	Automaton string `json:"automaton_hash,omitempty"`
}

// RuleStore provides rule retrieval & versioning semantics
type RuleStore interface {
	All() []Rule
	ByID(id string) (Rule, bool)
	Reload() error
	Version() string
}

// MemoryRuleStore implements RuleStore backed by files on disk
// It watches the rules directory mtime periodically (low overhead) for hot reload.
type MemoryRuleStore struct {
	dir      string
	lastHash atomic.Value // store string
	cache    atomic.Value // store []Rule
	lastLoad time.Time
	interval time.Duration
	version  atomic.Value // semantic/hash version string
}

func NewMemoryRuleStore(dir string, interval time.Duration) *MemoryRuleStore {
	rs := &MemoryRuleStore{dir: dir, interval: interval}
	rs.cache.Store([]Rule{})
	rs.lastHash.Store("")
	rs.version.Store("")
	return rs
}

func (m *MemoryRuleStore) All() []Rule { return m.cache.Load().([]Rule) }
func (m *MemoryRuleStore) ByID(id string) (Rule, bool) {
	for _, r := range m.All() {
		if r.ID == id {
			return r, true
		}
	}
	return Rule{}, false
}

func (m *MemoryRuleStore) Version() string { return m.version.Load().(string) }

// dirCompositeHash builds a deterministic hash across rule JSON files contents (excluding manifest itself)
func dirCompositeHash(dir string) (string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() && strings.HasSuffix(info.Name(), ".json") && info.Name() != "index.json" {
			rel := path
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// manifest structure (optional): index.json
type manifest struct {
	Version   string `json:"version"`
	CreatedAt string `json:"created_at"`
	Hash      string `json:"hash"` // expected composite hash
}

func (m *MemoryRuleStore) Reload() error {
	composite, err := dirCompositeHash(m.dir)
	if err != nil {
		return err
	}
	if composite == m.lastHash.Load().(string) && time.Since(m.lastLoad) < m.interval { // throttle unchanged
		return nil
	}
	// Load manifest if present
	var man manifest
	manPath := filepath.Join(m.dir, "index.json")
	if b, err2 := os.ReadFile(manPath); err2 == nil {
		if err3 := json.Unmarshal(b, &man); err3 != nil {
			slog.Warn("manifest parse failed", "error", err3)
		} else if man.Hash != "" && man.Hash != composite {
			return fmt.Errorf("rule manifest hash mismatch expected=%s got=%s", man.Hash, composite)
		}
	}
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return err
	}
	var rules []Rule
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || e.Name() == "index.json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(m.dir, e.Name()))
		if err != nil {
			return err
		}
		var r Rule
		if err := json.Unmarshal(b, &r); err != nil {
			return err
		}
		if r.Enabled {
			if r.SamplePercent < 0 || r.SamplePercent > 100 { // normalize / guard
				slog.Warn("invalid sample_percent; forcing 100", "rule", r.ID)
				r.SamplePercent = 100
			} else if r.SamplePercent == 0 {
				r.SamplePercent = 100
			}
			rules = append(rules, r)
		}
	}
	m.cache.Store(rules)
	m.lastHash.Store(composite)
	m.lastLoad = time.Now()
	ver := man.Version
	if ver == "" {
		ver = composite[:12]
	}
	m.version.Store(ver)
	slog.Info("rules reloaded", "count", len(rules), "version", ver)
	return nil
}

// Scanner performs signature matching against byte payloads (placeholder naive implementation)
// Scanner interface implemented by Aho-Corasick engine (multi-pattern) or fallback naive.
// Scanner interface retained for potential polymorphism
type Scanner interface {
	Scan(data []byte) []MatchResult
}

// --- Aho-Corasick implementation (compact) ---
// For production we may swap with a SIMD / hyperscan binding; this keeps zero deps.
// (Old inline Aho code removed; now using scanner package implementation)

func main() {
	service := "signature-engine"
	corelog.Init(service)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	shutdownTrace := otelinit.InitTracer(ctx, service)
	shutdownMetrics, promHandler, _ := otelinit.InitMetrics(ctx, service)

	ruleDir := os.Getenv("SIGNATURE_RULE_DIR")
	if ruleDir == "" {
		ruleDir = "./rules"
	}
	// Initialize hot-reload scanner with advanced features
	loader := scanner.NewFileRuleLoader(filepath.Join(ruleDir, "default_rules.json"))
	hotReloadScanner, err := scanner.NewHotReloadScanner(loader, 5*time.Second)
	if err != nil {
		slog.Error("failed to initialize hot reload scanner", "error", err)
		os.Exit(1)
	}
	defer hotReloadScanner.Stop()
	
	// Initialize metrics collector
	metricsCollector := scanner.NewMetricsCollector()
	
	// Legacy store for backward compatibility
	store := NewMemoryRuleStore(ruleDir, 3*time.Second)
	if err := store.Reload(); err != nil {
		slog.Warn("legacy rule store load failed", "error", err)
	}
	
	slog.Info("signature engine initialized", 
		"rules", hotReloadScanner.GetMetadata().RuleCount,
		"version", hotReloadScanner.GetMetadata().Version)

	// metrics instruments
	meter := otel.Meter("swarm-go")
	matchCounter, _ := meter.Int64Counter("swarm_signature_match_total")
	latencyHist, _ := meter.Float64Histogram("swarm_scan_duration_seconds")
	bytesHist, _ := meter.Int64Histogram("swarm_scan_bytes")
	ruleGauge, _ := meter.Int64UpDownCounter("swarm_signature_rules_loaded")
	reloadCounter, _ := meter.Int64Counter("swarm_signatures_reloads_total")
	reloadDur, _ := meter.Float64Histogram("swarm_signatures_reload_duration_seconds")
	scanErrors, _ := meter.Int64Counter("swarm_scan_errors_total")
	scanActive, _ := meter.Int64UpDownCounter("swarm_scan_active")
	loadErrors, _ := meter.Int64Counter("swarm_signatures_load_errors_total")
	buildDur, _ := meter.Float64Histogram("swarm_signature_automaton_build_seconds")
	ruleGauge.Add(ctx, int64(len(store.All())))

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// Enhanced scan endpoint with streaming support and advanced metrics
	mux.HandleFunc("/scan", func(w http.ResponseWriter, r *http.Request) {
		scanStart := time.Now()
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			scanErrors.Add(r.Context(), 1)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		scanActive.Add(r.Context(), 1)
		defer scanActive.Add(r.Context(), -1)
		
		// Use hot reload scanner and record metrics
		matches := hotReloadScanner.Scan(body)
		durationUs := time.Since(scanStart).Microseconds()
		metricsCollector.RecordScan(durationUs, matches, int64(len(body)))
		
		// Convert to API response format
		apiMatches := make([]MatchResult, len(matches))
		for i, m := range matches {
			apiMatches[i] = MatchResult{
				RuleID:    m.RuleID,
				RuleType:  m.RuleType,
				Offset:    m.Offset,
				Length:    m.Length,
				Severity:  m.Severity,
				Version:   m.Version,
				Sampled:   m.Sampled,
				Automaton: m.Automaton,
			}
			
			attrs := metric.WithAttributes(attribute.String("rule_type", m.RuleType))
			if m.Severity != "" {
				attrs = metric.WithAttributes(
					attribute.String("rule_type", m.RuleType), 
					attribute.String("severity", m.Severity),
				)
			}
			matchCounter.Add(r.Context(), 1, attrs)
		}
		
		bytesHist.Record(r.Context(), int64(len(body)))
		latencyHist.Record(r.Context(), time.Since(scanStart).Seconds())
		
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Rule-Count", fmt.Sprintf("%d", hotReloadScanner.GetMetadata().RuleCount))
		w.Header().Set("X-Scanner-Version", hotReloadScanner.GetMetadata().Version)
		_ = json.NewEncoder(w).Encode(apiMatches)
	})
	reloadHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		t0 := time.Now()
		
		// Force reload the hot-reload scanner
		if err := hotReloadScanner.ForceReload(); err != nil {
			reloadCounter.Add(r.Context(), 1, metric.WithAttributes(attribute.String("status", "failure")))
			loadErrors.Add(r.Context(), 1)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(err.Error()))
			return
		}
		
		meta := hotReloadScanner.GetMetadata()
		dur := time.Since(t0).Seconds()
		reloadDur.Record(r.Context(), dur)
		buildDur.Record(r.Context(), float64(meta.BuildDurationMs)/1000.0)
		reloadCounter.Add(r.Context(), 1, metric.WithAttributes(attribute.String("status", "success")))
		ruleGauge.Add(r.Context(), int64(meta.RuleCount))
		
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":           "ok",
			"duration_seconds": dur,
			"rules":            meta.RuleCount,
			"version":          meta.Version,
			"reload_count":     meta.ReloadCount,
		})
	}
	mux.HandleFunc("/reload", reloadHandler)                                // backward compatible
	mux.HandleFunc("/v1/rules/reload", reloadHandler)                       // versioned
	mux.HandleFunc("/rules", func(w http.ResponseWriter, r *http.Request) { // legacy
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(store.All())
	})
	mux.HandleFunc("/v1/rules", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"version": store.Version(), "rules": store.All()})
	})
	// Enhanced stats endpoint with detailed metrics
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		meta := hotReloadScanner.GetMetadata()
		metrics := metricsCollector.GetStats()
		
		st := map[string]any{
			"rules":      meta.RuleCount,
			"goroutines": runtime.NumGoroutine(),
			"version":    meta.Version,
			"scanner": map[string]any{
				"loaded_at":          meta.LoadedAt.Format(time.RFC3339),
				"last_reload_at":     meta.LastReloadAt.Format(time.RFC3339),
				"reload_count":       meta.ReloadCount,
				"build_duration_ms":  meta.BuildDurationMs,
			},
			"metrics": map[string]any{
				"total_scans":          metrics.TotalScans,
				"total_matches":        metrics.TotalMatches,
				"total_bytes_scanned":  metrics.TotalBytesScanned,
				"total_errors":         metrics.TotalErrors,
				"recent_throughput":    metrics.RecentThroughputBPS,
				"recent_scans_per_sec": metrics.RecentScansPerSec,
				"top_rules":            metrics.TopRules,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(st)
	})
	if promHandler != nil {
		if h, ok := promHandler.(http.Handler); ok {
			mux.Handle("/metrics", h)
		}
	}

	srv := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
			cancel()
		}
	}()
	// Background adaptive reload is now handled by HotReloadScanner automatically
	// Legacy background reload removed - HotReloadScanner has its own watcher

	slog.Info("service started")
	<-ctx.Done()
	slog.Info("shutdown initiated")
	ctxSd, c2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer c2()
	_ = srv.Shutdown(ctxSd)
	otelinit.Flush(ctxSd, shutdownTrace)
	_ = shutdownMetrics(ctxSd)
	slog.Info("shutdown complete")
}
