package scanner

import (
	"crypto/rand"
	"fmt"
	"testing"
)

// BenchmarkAhoScan measures Aho-Corasick scan performance
func BenchmarkAhoScan(b *testing.B) {
	// Create 1000 rules
	rules := make([]ExtendedRule, 1000)
	for i := 0; i < 1000; i++ {
		rules[i] = ExtendedRule{
			Rule: Rule{
				ID:      fmt.Sprintf("rule-%d", i),
				Type:    "malware",
				Pattern: fmt.Sprintf("malware_pattern_%d", i),
				Enabled: true,
				Version: 1,
			},
			SamplePercent: 100,
			Severity:      "high",
		}
	}

	auto, err := BuildAho(rules)
	if err != nil {
		b.Fatalf("build aho: %v", err)
	}

	scanner := NewAhoScanner(auto)

	// Generate 1MB random data
	data := make([]byte, 1024*1024)
	rand.Read(data)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		scanner.Scan(data)
	}

	// Report throughput
	b.SetBytes(int64(len(data)))
}

// BenchmarkAhoBuild measures automaton build time
func BenchmarkAhoBuild(b *testing.B) {
	rules := make([]ExtendedRule, 5000)
	for i := 0; i < 5000; i++ {
		rules[i] = ExtendedRule{
			Rule: Rule{
				ID:      fmt.Sprintf("rule-%d", i),
				Type:    "signature",
				Pattern: fmt.Sprintf("pattern_%d_with_longer_content_%d", i, i*7),
				Enabled: true,
				Version: 1,
			},
			SamplePercent: 100,
			Severity:      "medium",
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := BuildAho(rules)
		if err != nil {
			b.Fatalf("build: %v", err)
		}
	}
}

// BenchmarkBloomFilter measures bloom filter performance
func BenchmarkBloomFilter(b *testing.B) {
	bf := NewBloomFilter(10000, 0.01)

	// Add 5000 patterns
	for i := 0; i < 5000; i++ {
		bf.Add([]byte(fmt.Sprintf("pattern-%d", i)))
	}

	testData := []byte("pattern-2500")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		bf.MayContain(testData)
	}
}

// BenchmarkAhoWithBloom measures combined Aho+Bloom performance
func BenchmarkAhoWithBloom(b *testing.B) {
	rules := make([]ExtendedRule, 2000)
	for i := 0; i < 2000; i++ {
		rules[i] = ExtendedRule{
			Rule: Rule{
				ID:      fmt.Sprintf("rule-%d", i),
				Type:    "signature",
				Pattern: fmt.Sprintf("sig_%d", i),
				Enabled: true,
				Version: 1,
			},
			SamplePercent: 100,
			Severity:      "high",
		}
	}

	auto, _ := BuildAho(rules)
	scanner := NewAhoScanner(auto)

	// 10MB data
	data := make([]byte, 10*1024*1024)
	rand.Read(data)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		scanner.Scan(data)
	}

	b.SetBytes(int64(len(data)))
}

// BenchmarkParallelScan measures concurrent scanning performance
func BenchmarkParallelScan(b *testing.B) {
	rules := make([]ExtendedRule, 1000)
	for i := 0; i < 1000; i++ {
		rules[i] = ExtendedRule{
			Rule: Rule{
				ID:      fmt.Sprintf("rule-%d", i),
				Type:    "pattern",
				Pattern: fmt.Sprintf("ptn%d", i),
				Enabled: true,
				Version: 1,
			},
			SamplePercent: 100,
			Severity:      "medium",
		}
	}

	auto, _ := BuildAho(rules)
	scanner := NewAhoScanner(auto)

	data := make([]byte, 1024*1024)
	rand.Read(data)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			scanner.Scan(data)
		}
	})

	b.SetBytes(int64(len(data)))
}
