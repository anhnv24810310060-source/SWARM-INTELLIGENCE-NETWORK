package natsctx

import (
  "context"
  nats "github.com/nats-io/nats.go"
  "go.opentelemetry.io/otel"
  "go.opentelemetry.io/otel/propagation"
  "go.opentelemetry.io/otel/trace"
)

var propagator = propagation.TraceContext{}

// Publish injects traceparent into headers and publishes.
func Publish(ctx context.Context, nc *nats.Conn, subject string, data []byte) error {
  hdr := nats.Header{}
  carrier := propagation.HeaderCarrier(hdr)
  propagator.Inject(ctx, carrier)
  msg := &nats.Msg{Subject: subject, Data: data, Header: hdr}
  return nc.PublishMsg(msg)
}

// Subscribe wraps nc.Subscribe and extracts trace context for each message, starting a child span.
func Subscribe(nc *nats.Conn, subject string, handler func(context.Context, *nats.Msg)) (*nats.Subscription, error) {
  return nc.Subscribe(subject, func(m *nats.Msg) {
    carrier := propagation.HeaderCarrier(m.Header)
    ctx := propagator.Extract(context.Background(), carrier)
    tr := otel.Tracer("swarm-nats")
    ctx, span := tr.Start(ctx, "nats.consume", trace.WithSpanKind(trace.SpanKindConsumer))
    span.SetAttributes()
    defer span.End()
    handler(ctx, m)
  })
}
