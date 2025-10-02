package otelinit

import (
	"context"
	"testing"
)

func TestInitMetricsNoExporter(t *testing.T) {
	ctx := context.Background()
	shutdown, _, m := InitMetrics(ctx, "test-service")
	// Should provide counters that can increment without panic
	m.RetryAttempts.Add(ctx, 1)
	m.CircuitOpenTransitions.Add(ctx, 1)
	_ = shutdown(ctx) // Ignore error; no collector likely present in test env
}
