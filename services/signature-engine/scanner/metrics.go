package scanner

import (
	"context"
	"sync"
	"time"
)

// Metrics collector for signature engine performance tracking.
// Integrates with Prometheus via global metrics registry.
type MetricsCollector struct {
	mu sync.RWMutex
	
	// Cumulative counters
	TotalScans       int64
	TotalMatches     int64
	TotalBytesScanned int64
	TotalErrors      int64
	
	// Latency tracking (microseconds)
	LatencyHistogram []int64 // buckets: <1ms, <10ms, <100ms, <1s, >1s
	
	// Rule-level metrics
	RuleHits map[string]int64 // rule_id -> hit count
	
	// Time window stats (last 60 seconds)
	RecentScans   []scanStat
	WindowSize    time.Duration
}

type scanStat struct {
	Timestamp time.Time
	DurationUs int64
	Matches   int
	BytesScanned int64
}

// NewMetricsCollector initializes a metrics collector.
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		LatencyHistogram: make([]int64, 5),
		RuleHits:         make(map[string]int64),
		RecentScans:      make([]scanStat, 0, 1000),
		WindowSize:       60 * time.Second,
	}
}

// RecordScan records a single scan operation.
func (m *MetricsCollector) RecordScan(durationUs int64, matches []MatchResult, bytesScanned int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	m.TotalScans++
	m.TotalMatches += int64(len(matches))
	m.TotalBytesScanned += bytesScanned
	
	// Update latency histogram
	bucket := m.latencyBucket(durationUs)
	m.LatencyHistogram[bucket]++
	
	// Update rule hits
	for _, match := range matches {
		m.RuleHits[match.RuleID]++
	}
	
	// Add to recent window
	stat := scanStat{
		Timestamp: time.Now(),
		DurationUs: durationUs,
		Matches: len(matches),
		BytesScanned: bytesScanned,
	}
	m.RecentScans = append(m.RecentScans, stat)
	
	// Prune old stats
	m.pruneOldStats()
}

// latencyBucket maps duration to histogram bucket index.
func (m *MetricsCollector) latencyBucket(us int64) int {
	switch {
	case us < 1000: // <1ms
		return 0
	case us < 10000: // <10ms
		return 1
	case us < 100000: // <100ms
		return 2
	case us < 1000000: // <1s
		return 3
	default: // >1s
		return 4
	}
}

// pruneOldStats removes stats older than window size.
func (m *MetricsCollector) pruneOldStats() {
	cutoff := time.Now().Add(-m.WindowSize)
	i := 0
	for i < len(m.RecentScans) && m.RecentScans[i].Timestamp.Before(cutoff) {
		i++
	}
	if i > 0 {
		m.RecentScans = m.RecentScans[i:]
	}
}

// RecordError increments error counter.
func (m *MetricsCollector) RecordError() {
	m.mu.Lock()
	m.TotalErrors++
	m.mu.Unlock()
}

// GetStats returns a snapshot of current metrics.
func (m *MetricsCollector) GetStats() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	snapshot := MetricsSnapshot{
		TotalScans:        m.TotalScans,
		TotalMatches:      m.TotalMatches,
		TotalBytesScanned: m.TotalBytesScanned,
		TotalErrors:       m.TotalErrors,
		LatencyHistogram:  make([]int64, len(m.LatencyHistogram)),
		TopRules:          m.topNRules(10),
	}
	
	copy(snapshot.LatencyHistogram, m.LatencyHistogram)
	
	// Calculate recent throughput
	if len(m.RecentScans) > 0 {
		var totalBytes int64
		for _, s := range m.RecentScans {
			totalBytes += s.BytesScanned
		}
		elapsed := time.Since(m.RecentScans[0].Timestamp).Seconds()
		if elapsed > 0 {
			snapshot.RecentThroughputBPS = float64(totalBytes) / elapsed
			snapshot.RecentScansPerSec = float64(len(m.RecentScans)) / elapsed
		}
	}
	
	return snapshot
}

// topNRules returns the N rules with most hits.
func (m *MetricsCollector) topNRules(n int) []RuleHitStat {
	type kv struct {
		ruleID string
		hits   int64
	}
	
	kvs := make([]kv, 0, len(m.RuleHits))
	for k, v := range m.RuleHits {
		kvs = append(kvs, kv{k, v})
	}
	
	// Simple bubble sort for top N (ok for small N)
	for i := 0; i < n && i < len(kvs); i++ {
		for j := i + 1; j < len(kvs); j++ {
			if kvs[j].hits > kvs[i].hits {
				kvs[i], kvs[j] = kvs[j], kvs[i]
			}
		}
	}
	
	result := make([]RuleHitStat, 0, n)
	for i := 0; i < n && i < len(kvs); i++ {
		result = append(result, RuleHitStat{
			RuleID: kvs[i].ruleID,
			Hits:   kvs[i].hits,
		})
	}
	
	return result
}

// MetricsSnapshot is a point-in-time view of metrics.
type MetricsSnapshot struct {
	TotalScans        int64         `json:"total_scans"`
	TotalMatches      int64         `json:"total_matches"`
	TotalBytesScanned int64         `json:"total_bytes_scanned"`
	TotalErrors       int64         `json:"total_errors"`
	LatencyHistogram  []int64       `json:"latency_histogram"` // [<1ms, <10ms, <100ms, <1s, >1s]
	RecentThroughputBPS float64     `json:"recent_throughput_bps"`
	RecentScansPerSec float64       `json:"recent_scans_per_sec"`
	TopRules          []RuleHitStat `json:"top_rules"`
}

// RuleHitStat represents a single rule's hit statistics.
type RuleHitStat struct {
	RuleID string `json:"rule_id"`
	Hits   int64  `json:"hits"`
}

// InstrumentedScanner wraps a scanner with automatic metrics collection.
type InstrumentedScanner struct {
	scanner   *AhoScanner
	metrics   *MetricsCollector
}

// NewInstrumentedScanner creates a scanner with metrics tracking.
func NewInstrumentedScanner(scanner *AhoScanner, metrics *MetricsCollector) *InstrumentedScanner {
	return &InstrumentedScanner{
		scanner: scanner,
		metrics: metrics,
	}
}

// Scan performs scanning and records metrics.
func (is *InstrumentedScanner) Scan(data []byte) []MatchResult {
	start := time.Now()
	
	matches := is.scanner.Scan(data)
	
	durationUs := time.Since(start).Microseconds()
	is.metrics.RecordScan(durationUs, matches, int64(len(data)))
	
	return matches
}

// ScanWithContext performs scanning with cancellation support.
func (is *InstrumentedScanner) ScanWithContext(ctx context.Context, data []byte) ([]MatchResult, error) {
	// Check cancellation before starting
	select {
	case <-ctx.Done():
		is.metrics.RecordError()
		return nil, ctx.Err()
	default:
	}
	
	// TODO: Implement actual interruptible scanning for very large payloads
	// For now, just do synchronous scan
	matches := is.Scan(data)
	
	return matches, nil
}

// GetMetrics returns current metrics snapshot.
func (is *InstrumentedScanner) GetMetrics() MetricsSnapshot {
	return is.metrics.GetStats()
}
