module github.com/swarmguard/libs/go/core

go 1.22

require (
	go.opentelemetry.io/otel v1.28.0
	go.opentelemetry.io/otel/sdk v1.28.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.28.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.28.0
	google.golang.org/grpc v1.65.0
	github.com/nats-io/nats.go v1.33.1
)
