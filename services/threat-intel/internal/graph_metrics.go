package internal

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// GraphMetrics provides observability for ThreatGraph operations
type GraphMetrics struct {
	nodesTotal        metric.Int64ObservableGauge
	edgesTotal        metric.Int64ObservableGauge
	nodesByType       metric.Int64ObservableGauge
	anomalousCount    metric.Int64Counter
	pathCalculations  metric.Int64Counter
	scoreCalculations metric.Int64Counter
	pruneOperations   metric.Int64Counter
	pruneLatency      metric.Float64Histogram

	graph *ThreatGraph
}

func NewGraphMetrics(graph *ThreatGraph) (*GraphMetrics, error) {
	meter := otel.GetMeterProvider().Meter("swarm-threat-intel")

	gm := &GraphMetrics{graph: graph}

	// Observable gauges for current state
	var err error

	gm.nodesTotal, err = meter.Int64ObservableGauge(
		"swarm_threat_graph_nodes_total",
		metric.WithDescription("Total number of nodes in threat graph"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	gm.edgesTotal, err = meter.Int64ObservableGauge(
		"swarm_threat_graph_edges_total",
		metric.WithDescription("Total number of edges in threat graph"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	gm.nodesByType, err = meter.Int64ObservableGauge(
		"swarm_threat_graph_nodes_by_type",
		metric.WithDescription("Number of nodes by type"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	// Counters for operations
	gm.anomalousCount, err = meter.Int64Counter(
		"swarm_threat_graph_anomalous_patterns_total",
		metric.WithDescription("Total anomalous patterns detected"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	gm.pathCalculations, err = meter.Int64Counter(
		"swarm_threat_graph_path_calculations_total",
		metric.WithDescription("Total attack path calculations"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	gm.scoreCalculations, err = meter.Int64Counter(
		"swarm_threat_graph_score_calculations_total",
		metric.WithDescription("Total threat score calculations"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	gm.pruneOperations, err = meter.Int64Counter(
		"swarm_threat_graph_prune_operations_total",
		metric.WithDescription("Total prune operations"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	// Histogram for prune latency
	gm.pruneLatency, err = meter.Float64Histogram(
		"swarm_threat_graph_prune_latency_seconds",
		metric.WithDescription("Prune operation latency distribution"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	// Register callbacks for observable gauges
	_, err = meter.RegisterCallback(
		func(ctx context.Context, observer metric.Observer) error {
			stats := gm.graph.GetStats()

			observer.ObserveInt64(gm.nodesTotal, int64(stats["total_nodes"].(int)))
			observer.ObserveInt64(gm.edgesTotal, int64(stats["total_edges"].(int)))

			// Observe nodes by type
			if typeCount, ok := stats["nodes_by_type"].(map[string]int); ok {
				for nodeType, count := range typeCount {
					observer.ObserveInt64(
						gm.nodesByType,
						int64(count),
						metric.WithAttributes(attribute.String("type", nodeType)),
					)
				}
			}

			return nil
		},
		gm.nodesTotal,
		gm.edgesTotal,
		gm.nodesByType,
	)

	return gm, err
}

// RecordAnomalousPattern records detection of anomalous pattern
func (gm *GraphMetrics) RecordAnomalousPattern(ctx context.Context, count int) {
	gm.anomalousCount.Add(ctx, int64(count))
}

// RecordPathCalculation records an attack path calculation
func (gm *GraphMetrics) RecordPathCalculation(ctx context.Context) {
	gm.pathCalculations.Add(ctx, 1)
}

// RecordScoreCalculation records a threat score calculation
func (gm *GraphMetrics) RecordScoreCalculation(ctx context.Context) {
	gm.scoreCalculations.Add(ctx, 1)
}

// RecordPruneOperation records a prune operation with latency
func (gm *GraphMetrics) RecordPruneOperation(ctx context.Context, pruned int, duration time.Duration) {
	gm.pruneOperations.Add(ctx, 1, metric.WithAttributes(
		attribute.Int("pruned_count", pruned),
	))
	gm.pruneLatency.Record(ctx, duration.Seconds())
}
