package internal

import (
	"math"
	"strings"
	"time"
)

// ThreatScorer implements advanced scoring algorithm
// Combines: base score, recency, source reputation, context signals
type ThreatScorer struct {
	sourceWeights map[string]float64 // source reputation weights
}

func NewThreatScorer() *ThreatScorer {
	return &ThreatScorer{
		sourceWeights: map[string]float64{
			"virustotal":   1.0,
			"otx":          0.9,
			"mitre-attack": 1.0,
			"internal":     0.7, // user-submitted indicators
			"unknown":      0.5,
		},
	}
}

// Score calculates comprehensive threat score (0-10)
// Factors: base score, time decay, source credibility, pattern matching
func (ts *ThreatScorer) Score(ind Indicator) float64 {
	// Base score from indicator
	baseScore := ind.Score
	if baseScore < 0 {
		baseScore = 5.0 // default medium
	}
	if baseScore > 10 {
		baseScore = 10.0
	}

	// Source reputation weight
	sourceWeight := ts.sourceWeights[ind.Source]
	if sourceWeight == 0 {
		sourceWeight = ts.sourceWeights["unknown"]
	}

	// Time decay: older indicators less reliable
	timeFactor := ts.calculateTimeDecay(ind.ExpiresAt)

	// Context boost: certain patterns increase score
	contextBoost := ts.calculateContextBoost(ind)

	// Final score = base * source_weight * time_factor + context_boost
	score := (baseScore * sourceWeight * timeFactor) + contextBoost

	// Clamp to 0-10 range
	if score < 0 {
		score = 0
	}
	if score > 10 {
		score = 10
	}

	return score
}

// calculateTimeDecay returns multiplier based on indicator age
func (ts *ThreatScorer) calculateTimeDecay(expiresAt time.Time) float64 {
	now := time.Now()
	if expiresAt.Before(now) {
		return 0.1 // expired indicators: very low confidence
	}

	createdAt := expiresAt.Add(-7 * 24 * time.Hour)
	totalLifetime := expiresAt.Sub(createdAt).Seconds()
	age := now.Sub(createdAt).Seconds()

	if totalLifetime <= 0 {
		return 1.0
	}

	ageFraction := age / totalLifetime
	decay := math.Exp(-2.0 * ageFraction)

	return decay
}

// calculateContextBoost adds score based on indicator patterns
func (ts *ThreatScorer) calculateContextBoost(ind Indicator) float64 {
	boost := 0.0

	for _, value := range ind.Metadata {
		lower := strings.ToLower(value)

		// APT groups
		aptGroups := []string{"apt28", "apt29", "apt40", "lazarus", "fancy bear"}
		for _, apt := range aptGroups {
			if strings.Contains(lower, apt) {
				boost += 2.0
				break
			}
		}

		// Ransomware families
		ransomware := []string{"wannacry", "petya", "ryuk", "lockbit", "conti", "blackcat"}
		for _, rw := range ransomware {
			if strings.Contains(lower, rw) {
				boost += 2.5
				break
			}
		}

		// Zero-day mentions
		if strings.Contains(lower, "zero-day") || strings.Contains(lower, "0day") {
			boost += 3.0
		}
	}

	return boost
}

// AggregateScore combines multiple indicators
func (ts *ThreatScorer) AggregateScore(indicators []Indicator) float64 {
	if len(indicators) == 0 {
		return 0.0
	}

	totalScore := 0.0
	typeCount := make(map[string]int)

	for _, ind := range indicators {
		score := ts.Score(ind)
		totalScore += score
		typeCount[ind.Type]++
	}

	avgScore := totalScore / float64(len(indicators))
	diversityBonus := float64(len(typeCount)) * 0.3

	finalScore := avgScore + diversityBonus
	if finalScore > 10 {
		finalScore = 10
	}

	return finalScore
}

// RiskLevel converts score to categorical risk level
func RiskLevel(score float64) string {
	switch {
	case score >= 8.0:
		return "critical"
	case score >= 6.0:
		return "high"
	case score >= 4.0:
		return "medium"
	case score >= 2.0:
		return "low"
	default:
		return "info"
	}
}
