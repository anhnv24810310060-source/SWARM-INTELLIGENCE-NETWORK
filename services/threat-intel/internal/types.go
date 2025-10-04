package internal

import "time"

// IndicatorType enumerates supported IoC categories.
type IndicatorType string

const (
	IndicatorIP        IndicatorType = "ip"
	IndicatorDomain    IndicatorType = "domain"
	IndicatorHash      IndicatorType = "hash"
	IndicatorTechnique IndicatorType = "technique" // MITRE ATT&CK technique
	IndicatorURL       IndicatorType = "url"
)

// Indicator represents a normalized threat indicator record
// TTL enforcement handled by store, expired indicators skipped from correlation.
type Indicator struct {
	Value     string        `json:"value"`
	Type      IndicatorType `json:"type"`
	Source    string        `json:"source"`
	FirstSeen time.Time     `json:"first_seen"`
	LastSeen  time.Time     `json:"last_seen"`
	Score     float64       `json:"score"` // raw feed score (0-10 or mapped)
	TTL       time.Duration `json:"ttl"`
}

// Threat aggregates correlated indicators into a higher-level entity.
type Threat struct {
	ID         string      `json:"id"`
	Indicators []Indicator `json:"indicators"`
	Risk       float64     `json:"risk"` // computed risk 0..1
	Severity   string      `json:"severity"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

// ScoringWeights define dynamic factors for risk computation.
type ScoringWeights struct {
	Base           float64
	FreshnessDecay float64 // multiplier per hour
	SourceWeight   map[string]float64
}

// Correlator ties indicators referencing same entity (e.g., hash+domain) into threats.
type Correlator interface {
	Correlate(ind Indicator) ([]Threat, error)
}

// Store abstraction for indicator lifecycle and querying.
type IndicatorStore interface {
	Upsert(ind Indicator) error
	Get(value string) (Indicator, bool)
	Iter(func(Indicator) bool)
	PurgeExpired()
}
