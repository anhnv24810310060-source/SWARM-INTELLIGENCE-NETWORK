package internal

import "math"

// Risk scoring uses sigmoid normalization for bounded output.
func ComputeRisk(baseScore float64, freshnessHours float64, weight float64) float64 {
	// freshness decay reduces score exponentially
	freshnessFactor := math.Exp(-0.05 * freshnessHours) // tune constant later
	raw := baseScore * freshnessFactor * weight
	// squash 0..inf -> 0..1
	return 1.0 / (1.0 + math.Exp(-raw/10.0))
}
