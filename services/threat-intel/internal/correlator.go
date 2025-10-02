package internal

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"time"
)

// SimpleCorrelator currently groups by exact value and domain root (if indicator is domain with subdomains)
// Future: graph-based entity linking (hash/domain/IP clusters).
type SimpleCorrelator struct{ store IndicatorStore }

func NewSimpleCorrelator(store IndicatorStore) *SimpleCorrelator {
	return &SimpleCorrelator{store: store}
}

func (c *SimpleCorrelator) Correlate(ind Indicator) ([]Threat, error) {
	// For now: produce single threat for the indicator plus (if domain) root grouping.
	id := threatID(ind)
	sev := classify(ind.Score)
	threats := []Threat{{ID: id, Indicators: []Indicator{ind}, Risk: ind.Score / 10.0, Severity: sev, UpdatedAt: time.Now()}}
	if ind.Type == IndicatorDomain {
		root := rootDomain(ind.Value)
		if root != "" && root != ind.Value {
			rid := threatHash(root + ":root")
			threats = append(threats, Threat{ID: rid, Indicators: []Indicator{ind}, Risk: ind.Score / 12.0, Severity: sev, UpdatedAt: time.Now()})
		}
	}
	return threats, nil
}

func threatID(ind Indicator) string { return threatHash(ind.Value + string(ind.Type)) }

func threatHash(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:8])
}

func classify(score float64) string {
	switch {
	case score >= 8:
		return "critical"
	case score >= 6:
		return "high"
	case score >= 3:
		return "medium"
	default:
		return "low"
	}
}

func rootDomain(d string) string {
	parts := strings.Split(d, ".")
	if len(parts) < 2 {
		return d
	}
	return strings.Join(parts[len(parts)-2:], ".")
}
