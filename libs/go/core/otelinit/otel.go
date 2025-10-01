package otelinit
package otelinit

import (
	"context"
	"os"
	"time"
	"log/slog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc"
)

// InitTracer configures a global tracer provider with OTLP gRPC exporter.
func InitTracer(ctx context.Context, service string) func(context.Context) error {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" { endpoint = "localhost:4317" }
	dialOpts := []grpc.DialOption{grpc.WithInsecure()}
	exp, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(endpoint), otlptracegrpc.WithDialOption(dialOpts...))
	if err != nil { slog.Warn("otel exporter init failed", "error", err); return func(context.Context) error { return nil } }
	res, _ := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(service),
	))
	tp := trace.NewTracerProvider(
		trace.WithBatcher(exp),
		trace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	slog.Info("otel tracer initialized", "endpoint", endpoint)
	return tp.Shutdown
}

// WithSpan helper creates a span and returns a context and end function.
func WithSpan(ctx context.Context, name string) (context.Context, func()) {
	tr := otel.Tracer("swarm-go")
	ctx, span := tr.Start(ctx, name)
	return ctx, func() { span.End() }
}

// Graceful flush on shutdown.
func Flush(ctx context.Context, shutdown func(context.Context) error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}
