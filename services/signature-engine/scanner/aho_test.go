package scanner

import (
	"testing"
)

func TestBuildAndScanBasic(t *testing.T) {
	rules := []ExtendedRule{{Rule: Rule{ID: "r1", Type: "dsl", Pattern: "abc", Version: 1, Enabled: true}, SamplePercent: 100}}
	auto, err := BuildAho(rules)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	sc := NewAhoScanner(auto)
	ms := sc.Scan([]byte("zabcx"))
	if len(ms) != 1 || ms[0].RuleID != "r1" {
		t.Fatalf("unexpected matches: %#v", ms)
	}
}

func TestOverlappingPatterns(t *testing.T) {
	rules := []ExtendedRule{
		{Rule: Rule{ID: "r1", Type: "dsl", Pattern: "aba", Version: 1, Enabled: true}, SamplePercent: 100},
		{Rule: Rule{ID: "r2", Type: "dsl", Pattern: "ba", Version: 1, Enabled: true}, SamplePercent: 100},
	}
	auto, err := BuildAho(rules)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	sc := NewAhoScanner(auto)
	ms := sc.Scan([]byte("ababa"))
	if len(ms) == 0 {
		t.Fatalf("expected matches")
	}
	// ensure r1 appears at least once
	found1, found2 := false, false
	for _, m := range ms {
		if m.RuleID == "r1" {
			found1 = true
		}
		if m.RuleID == "r2" {
			found2 = true
		}
	}
	if !found1 || !found2 {
		t.Fatalf("missing overlapping matches: %#v", ms)
	}
}

func TestInvalidSamplePercent(t *testing.T) {
	rules := []ExtendedRule{{Rule: Rule{ID: "bad", Type: "dsl", Pattern: "x", Version: 1, Enabled: true}, SamplePercent: 0}}
	if _, err := BuildAho(rules); err == nil {
		t.Fatalf("expected error for invalid sample percent")
	}
}

func TestSamplingDropsSome(t *testing.T) {
	rules := []ExtendedRule{{Rule: Rule{ID: "r", Type: "dsl", Pattern: "a", Version: 1, Enabled: true}, SamplePercent: 10}}
	auto, err := BuildAho(rules)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	sc := NewAhoScanner(auto)
	total := 0
	for i := 0; i < 200; i++ {
		ms := sc.Scan([]byte("a"))
		if len(ms) > 0 {
			total++
		}
	}
	if total == 0 || total > 150 {
		t.Fatalf("sampling seems off: total=%d", total)
	}
}
