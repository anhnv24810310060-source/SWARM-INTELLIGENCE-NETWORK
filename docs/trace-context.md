# Trace Context Propagation Plan

Goal: Uniform propagation of W3C traceparent across Rust, Go, Python services for end-to-end tracing + log correlation.

## Current State
- Rust services: tracing + OTLP exporter; logs now include current span (trace_id/span_id implicit in OTEL, JSON layer enriched with current span metadata).
- Go/Python: basic JSON logging without automatic trace context extraction yet.

## Strategy
1. HTTP Ingress (FastAPI / future Axum gateways):
   - On request, extract `traceparent` header; if present, start span with remote context.
   - If absent, create new span and inject header on outbound calls.
2. NATS Messaging:
   - Inject traceparent into message header `traceparent` when publishing.
   - On subscribe, extract and start consumer span as child.
3. gRPC (future):
   - Use OpenTelemetry interceptors (Rust tonic, Go otelgrpc) to propagate context.

## Go Implementation Sketch
```go
import (
  "go.opentelemetry.io/otel/trace"
  "go.opentelemetry.io/otel/propagation"
  "go.opentelemetry.io/otel"
  "context"
  "net/http"
)
var propagator = propagation.TraceContext{}
func InjectTraceparent(ctx context.Context, headerSetter func(string,string)) {
  carrier := propagation.HeaderCarrier{}
  propagator.Inject(ctx, carrier)
  for k,v := range carrier { headerSetter(k, v[0]) }
}
```

## Python Implementation Sketch
```python
from opentelemetry.propagate import inject, extract
from opentelemetry import trace
from opentelemetry.trace import get_tracer

def inject_headers(headers: dict):
    inject(headers)

def start_child_from_headers(headers: dict, name: str):
    ctx = extract(headers)
    tracer = trace.get_tracer("swarm")
    return tracer.start_as_current_span(name, context=ctx)
```

## Next Steps
- Add OpenTelemetry SDK dependencies to Go/Python services.
- Wrap NATS publish/subscribe with context injection/extraction.
- Add log formatter enrichment tying `trace_id` / `span_id` explicitly in JSON for Go & Python.
- Provide integration test: publish message with injected context -> consumer logs include same trace_id.
