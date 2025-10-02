package otelinit

import (
	"context"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"google.golang.org/grpc"
)

// Metrics holds common resilience instruments.
type Metrics struct {
	RetryAttempts          metric.Int64Counter
	CircuitOpenTransitions metric.Int64Counter
}

// InitMetrics sets up a global OTLP metrics exporter (push). Returns shutdown function.
func InitMetrics(ctx context.Context, service string) (shutdown func(context.Context) error, promHandler any, m Metrics) {
	res, _ := sdkresource.Merge(sdkresource.Default(), sdkresource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(service),
		attribute.String("service", service),
	))
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")
	if endpoint == "" {
		endpoint = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	if endpoint == "" {
		endpoint = "localhost:4317"
	}
	ctxInit, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	exp, err := otlpmetricgrpc.New(ctxInit,
		otlpmetricgrpc.WithEndpoint(endpoint),
		otlpmetricgrpc.WithDialOption(grpc.WithInsecure()),
	)
	if err != nil {
		slog.Warn("metrics exporter init failed", "error", err)
		return func(context.Context) error { return nil }, nil, createCommonInstruments()
	}
	reader := sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(10*time.Second))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithResource(res))
	otel.SetMeterProvider(mp)
	slog.Info("metrics initialized", "endpoint", endpoint)
	return mp.Shutdown, nil, createCommonInstruments()
}

func createCommonInstruments() Metrics {
	meter := otel.Meter("swarm-go")
	retry, _ := meter.Int64Counter("swarm_resilience_retry_attempts_total")
	circuit, _ := meter.Int64Counter("swarm_resilience_circuit_open_total")
	return Metrics{RetryAttempts: retry, CircuitOpenTransitions: circuit}
}
