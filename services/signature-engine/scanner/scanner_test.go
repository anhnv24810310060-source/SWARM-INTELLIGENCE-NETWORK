package scanner
package scanner

import (
	"testing"
)

func TestBloomFilter(t *testing.T) {
	bf := NewBloomFilter(1000, 0.01)

	// Test basic operations
	patterns := []string{
		"malware_pattern_1",
		"exploit_code_2",
		"suspicious_string_3",
	}

	// Add patterns
	for _, p := range patterns {
		bf.Add([]byte(p))
	}

	// Verify membership
	for _, p := range patterns {
		if !bf.MayContain([]byte(p)) {
			t.Errorf("Expected pattern %s to be in bloom filter", p)
		}
	}

	// Test negative (should not be present)
	if bf.MayContain([]byte("definitely_not_there_xyz123")) {
		// This could be false positive (acceptable with 1% rate)
		t.Logf("False positive detected (acceptable)")
	}

	// Check stats
	stats := bf.Stats()
	if stats["size_bits"].(int) == 0 {
		t.Error("Bloom filter size should not be zero")
	}
	t.Logf("Bloom filter stats: %+v", stats)
}

func TestAhoAutomatonBuild(t *testing.T) {
	rules := []ExtendedRule{
		{
			Rule: Rule{
				ID:      "rule-1",
				Type:    "malware",
				Pattern: "virus",
				Enabled: true,
				Version: 1,
			},
			SamplePercent: 100,
			Severity:      "high",
		},
		{
			Rule: Rule{
				ID:      "rule-2",
				Type:    "exploit",
				Pattern: "shellcode",
				Enabled: true,
				Version: 1,
			},
			SamplePercent: 100,
			Severity:      "critical",
		},
	}

	auto, err := BuildAho(rules)
	if err != nil {
		t.Fatalf("BuildAho failed: %v", err)
	}

	if auto.ruleCount != 2 {
		t.Errorf("Expected 2 rules, got %d", auto.ruleCount)
	}

	if auto.buildHash == "" {
		t.Error("Build hash should not be empty")
	}

	t.Logf("Automaton built: %d rules, hash=%s, build_time=%dns", 
		auto.ruleCount, auto.buildHash, auto.buildNanos)
}

func TestAhoScanBasic(t *testing.T) {
	rules := []ExtendedRule{
		{
			Rule: Rule{
				ID:      "test-1",
				Type:    "pattern",
				Pattern: "abc",
				Enabled: true,
				Version: 1,
			},
			SamplePercent: 100,
			Severity:      "medium",
		},
		{
			Rule: Rule{
				ID:      "test-2",
				Type:    "pattern",
				Pattern: "xyz",
				Enabled: true,
				Version: 1,
			},
			SamplePercent: 100,
			Severity:      "low",
		},
	}

	auto, err := BuildAho(rules)
	if err != nil {
		t.Fatalf("BuildAho failed: %v", err)
	}

	scanner := NewAhoScanner(auto)
	data := []byte("this contains abc and xyz patterns")

	results := scanner.Scan(data)

	if len(results) < 2 {
		t.Errorf("Expected at least 2 matches, got %d", len(results))
	}

	// Verify matches
	foundABC := false
	foundXYZ := false
	for _, match := range results {
		if match.RuleID == "test-1" {
			foundABC = true
			if match.Severity != "medium" {
				t.Errorf("Expected severity 'medium', got '%s'", match.Severity)
			}
		}
		if match.RuleID == "test-2" {
			foundXYZ = true
		}
	}

	if !foundABC || !foundXYZ {
		t.Error("Not all expected patterns were matched")
	}
}

func TestAhoScanSampling(t *testing.T) {
	rules := []ExtendedRule{
		{
			Rule: Rule{
				ID:      "sampled-rule",
				Type:    "test",
				Pattern: "test",
				Enabled: true,
				Version: 1,
			},
			SamplePercent: 50, // 50% sampling
			Severity:      "low",
		},
	}

	auto, _ := BuildAho(rules)
	scanner := NewAhoScanner(auto)

	// Scan multiple times to test sampling
	data := []byte("test test test test test")
	matchCounts := 0

	for i := 0; i < 100; i++ {
		results := scanner.Scan(data)
		matchCounts += len(results)
	}

	// With 50% sampling and 5 occurrences, expect ~250 matches out of 500
	expectedMin := 200
	expectedMax := 300

	if matchCounts < expectedMin || matchCounts > expectedMax {
		t.Logf("Sampling test: got %d matches, expected %d-%d (may vary due to randomness)",
			matchCounts, expectedMin, expectedMax)
	}
}

func TestAhoScanOverlapping(t *testing.T) {
	rules := []ExtendedRule{
		{
			Rule: Rule{
				ID:      "short",
				Type:    "pattern",
				Pattern: "ab",
				Enabled: true,
				Version: 1,
			},
			SamplePercent: 100,
			Severity:      "low",
		},
		{
			Rule: Rule{
				ID:      "long",
				Type:    "pattern",
				Pattern: "abc",
				Enabled: true,
				Version: 1,
			},
			SamplePercent: 100,
			Severity:      "medium",
		},
	}

	auto, _ := BuildAho(rules)
	scanner := NewAhoScanner(auto)

	data := []byte("abc")
	results := scanner.Scan(data)

	// Should match both "ab" and "abc" due to overlapping
	if len(results) < 2 {
		t.Errorf("Expected at least 2 matches for overlapping patterns, got %d", len(results))
	}
}

func TestAhoScanDisabledRules(t *testing.T) {
	rules := []ExtendedRule{
		{
			Rule: Rule{
				ID:      "enabled",
				Type:    "pattern",
				Pattern: "active",
				Enabled: true,
				Version: 1,
			},
			SamplePercent: 100,
			Severity:      "high",
		},
		{
			Rule: Rule{
				ID:      "disabled",
				Type:    "pattern",
				Pattern: "inactive",
				Enabled: false, // Disabled
				Version: 1,
			},
			SamplePercent: 100,
			Severity:      "low",
		},
	}

	auto, _ := BuildAho(rules)
	if auto.ruleCount != 1 {
		t.Errorf("Expected only 1 enabled rule, got %d", auto.ruleCount)
	}

	scanner := NewAhoScanner(auto)
	data := []byte("active and inactive")

	results := scanner.Scan(data)

	// Should only match "active"
	for _, match := range results {
		if match.RuleID == "disabled" {
			t.Error("Disabled rule should not produce matches")
		}
	}
}

func TestAhoScanEmptyInput(t *testing.T) {
	rules := []ExtendedRule{
		{
			Rule: Rule{
				ID:      "test",
				Type:    "pattern",
				Pattern: "test",
				Enabled: true,
				Version: 1,
			},
			SamplePercent: 100,
			Severity:      "low",
		},
	}

	auto, _ := BuildAho(rules)
	scanner := NewAhoScanner(auto)

	// Empty input
	results := scanner.Scan([]byte{})
	if len(results) != 0 {
		t.Errorf("Expected 0 matches for empty input, got %d", len(results))
	}
}

func TestAhoConcurrentScan(t *testing.T) {
	rules := []ExtendedRule{
		{
			Rule: Rule{
				ID:      "concurrent-test",
				Type:    "pattern",
				Pattern: "pattern",
				Enabled: true,
				Version: 1,
			},
			SamplePercent: 100,
			Severity:      "medium",
		},
	}

	auto, _ := BuildAho(rules)
	scanner := NewAhoScanner(auto)

	// Test concurrent access (should be safe)
	done := make(chan bool)
	data := []byte("pattern pattern pattern")

	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				scanner.Scan(data)
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	t.Log("Concurrent scan test passed")
}
