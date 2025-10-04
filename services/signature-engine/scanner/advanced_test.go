package scanner

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestHotReloadScanner(t *testing.T) {
	// Create test rules
	rules := []ExtendedRule{
		{
			Rule: Rule{
				ID:      "test-1",
				Type:    "pattern",
				Pattern: "malware",
				Version: 1,
				Enabled: true,
			},
			SamplePercent: 100,
			Severity:      "high",
		},
		{
			Rule: Rule{
				ID:      "test-2",
				Type:    "pattern",
				Pattern: "virus",
				Version: 1,
				Enabled: true,
			},
			SamplePercent: 100,
			Severity:      "critical",
		},
	}
	
	// Mock loader
	loader := &mockLoader{rules: rules}
	
	// Create hot reload scanner
	scanner, err := NewHotReloadScanner(loader, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Failed to create hot reload scanner: %v", err)
	}
	defer scanner.Stop()
	
	// Test initial scan
	testData := []byte("This file contains malware and virus patterns")
	matches := scanner.Scan(testData)
	
	if len(matches) != 2 {
		t.Errorf("Expected 2 matches, got %d", len(matches))
	}
	
	// Verify metadata
	meta := scanner.GetMetadata()
	if meta.RuleCount != 2 {
		t.Errorf("Expected 2 rules, got %d", meta.RuleCount)
	}
	
	// Update rules (add new rule)
	loader.rules = append(loader.rules, ExtendedRule{
		Rule: Rule{
			ID:      "test-3",
			Type:    "pattern",
			Pattern: "trojan",
			Version: 1,
			Enabled: true,
		},
		SamplePercent: 100,
		Severity:      "critical",
	})
	
	// Force reload
	if err := scanner.ForceReload(); err != nil {
		t.Fatalf("Failed to force reload: %v", err)
	}
	
	// Verify reload happened
	meta = scanner.GetMetadata()
	if meta.RuleCount != 3 {
		t.Errorf("Expected 3 rules after reload, got %d", meta.RuleCount)
	}
	
	// Test scan with new rule
	testData2 := []byte("trojan detected")
	matches2 := scanner.Scan(testData2)
	
	if len(matches2) != 1 {
		t.Errorf("Expected 1 match for new rule, got %d", len(matches2))
	}
	
	if matches2[0].RuleID != "test-3" {
		t.Errorf("Expected match for test-3, got %s", matches2[0].RuleID)
	}
}

func TestStreamingScanner(t *testing.T) {
	// Build test scanner
	rules := []ExtendedRule{
		{
			Rule: Rule{
				ID:      "stream-test",
				Type:    "pattern",
				Pattern: "ATTACK",
				Version: 1,
				Enabled: true,
			},
			SamplePercent: 100,
			Severity:      "high",
		},
	}
	
	automaton, err := BuildAho(rules)
	if err != nil {
		t.Fatalf("Failed to build automaton: %v", err)
	}
	
	scanner := NewAhoScanner(automaton)
	streamScanner := NewStreamingScanner(scanner, 1024, 128)
	
	// Create test data with pattern near chunk boundary
	testData := strings.Repeat("X", 512) + "ATTACK" + strings.Repeat("Y", 512) + "ATTACK" + strings.Repeat("Z", 512)
	reader := bytes.NewReader([]byte(testData))
	
	matches, err := streamScanner.ScanStream(reader)
	if err != nil {
		t.Fatalf("Stream scan failed: %v", err)
	}
	
	// Should find both occurrences
	if len(matches) != 2 {
		t.Errorf("Expected 2 matches, got %d", len(matches))
	}
	
	// Verify global offsets are correct
	expectedOffset1 := int64(512)
	expectedOffset2 := int64(512 + 6 + 512)
	
	if matches[0].GlobalOffset != expectedOffset1 {
		t.Errorf("Expected first match at offset %d, got %d", expectedOffset1, matches[0].GlobalOffset)
	}
	
	if len(matches) > 1 && matches[1].GlobalOffset != expectedOffset2 {
		t.Errorf("Expected second match at offset %d, got %d", expectedOffset2, matches[1].GlobalOffset)
	}
}

func TestWorkerPool(t *testing.T) {
	// Build scanner
	rules := []ExtendedRule{
		{
			Rule: Rule{
				ID:      "pool-test",
				Type:    "pattern",
				Pattern: "THREAT",
				Version: 1,
				Enabled: true,
			},
			SamplePercent: 100,
			Severity:      "medium",
		},
	}
	
	automaton, err := BuildAho(rules)
	if err != nil {
		t.Fatalf("Failed to build automaton: %v", err)
	}
	
	scanner := NewAhoScanner(automaton)
	streamScanner := NewStreamingScanner(scanner, 4096, 256)
	pool := NewWorkerPool(streamScanner, 4)
	
	// Submit multiple jobs
	numJobs := 10
	for i := 0; i < numJobs; i++ {
		data := bytes.Repeat([]byte("NORMAL DATA "), 10)
		if i%3 == 0 {
			data = append(data, []byte("THREAT")...)
		}
		pool.Submit(string(rune(i)), bytes.NewReader(data))
	}
	
	// Close and collect results
	pool.Close()
	
	results := make([]scanResult, 0, numJobs)
	for result := range pool.Results() {
		results = append(results, result)
	}
	
	if len(results) != numJobs {
		t.Errorf("Expected %d results, got %d", numJobs, len(results))
	}
	
	// Count matches
	totalMatches := 0
	for _, r := range results {
		totalMatches += len(r.matches)
	}
	
	expectedMatches := (numJobs + 2) / 3 // Every 3rd job has threat
	if totalMatches != expectedMatches {
		t.Errorf("Expected %d total matches, got %d", expectedMatches, totalMatches)
	}
}

func TestMetricsCollector(t *testing.T) {
	collector := NewMetricsCollector()
	
	// Record some scans
	matches := []MatchResult{
		{RuleID: "rule1", Severity: "high"},
		{RuleID: "rule2", Severity: "medium"},
	}
	
	for i := 0; i < 100; i++ {
		durationUs := int64(i * 1000) // 0ms to 99ms
		collector.RecordScan(durationUs, matches, 1024)
	}
	
	// Get stats
	stats := collector.GetStats()
	
	if stats.TotalScans != 100 {
		t.Errorf("Expected 100 scans, got %d", stats.TotalScans)
	}
	
	if stats.TotalMatches != 200 {
		t.Errorf("Expected 200 matches, got %d", stats.TotalMatches)
	}
	
	if stats.TotalBytesScanned != 102400 {
		t.Errorf("Expected 102400 bytes, got %d", stats.TotalBytesScanned)
	}
	
	// Verify latency histogram has data
	hasData := false
	for _, count := range stats.LatencyHistogram {
		if count > 0 {
			hasData = true
			break
		}
	}
	
	if !hasData {
		t.Error("Latency histogram should have data")
	}
	
	// Verify top rules
	if len(stats.TopRules) == 0 {
		t.Error("Should have top rules data")
	}
}

func BenchmarkStreamingScanner(b *testing.B) {
	// Build scanner with 100 rules
	rules := make([]ExtendedRule, 100)
	for i := 0; i < 100; i++ {
		rules[i] = ExtendedRule{
			Rule: Rule{
				ID:      "bench-" + string(rune(i)),
				Type:    "pattern",
				Pattern: "PATTERN" + string(rune(i)),
				Version: 1,
				Enabled: true,
			},
			SamplePercent: 100,
			Severity:      "medium",
		}
	}
	
	automaton, _ := BuildAho(rules)
	scanner := NewAhoScanner(automaton)
	streamScanner := NewStreamingScanner(scanner, 65536, 4096)
	
	// 1MB test data
	testData := bytes.Repeat([]byte("NORMAL DATA CONTENT "), 50000)
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		reader := bytes.NewReader(testData)
		_, _ = streamScanner.ScanStream(reader)
	}
	
	b.SetBytes(int64(len(testData)))
}

// Mock loader for testing
type mockLoader struct {
	rules []ExtendedRule
}

func (m *mockLoader) Load() ([]ExtendedRule, error) {
	return m.rules, nil
}
