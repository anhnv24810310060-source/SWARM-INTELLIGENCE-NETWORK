API Gateway
===========

Implemented Capabilities:
- Versioned routing prefix mapping (/api/v1 -> /v1)
- In-memory token bucket limiter (per IP / API key) with metrics `swarm_api_rate_limited_total`
- Authorization stub (Bearer dev or structured JWT placeholder)
- Request metrics counter `swarm_api_requests_total` with latency attr per path
- Structured logging (slog) & OTEL tracing/metrics export
- Health endpoint `/health` and `/metrics` exposition
- Basic payload validation for `/v1/ingest`

Environment Variables:
- RATE_LIMIT_CAPACITY (default 200)
- RATE_LIMIT_REFILL (default 200)
- RATE_LIMIT_INTERVAL_SEC (default 60)
- OTEL_EXPORTER_OTLP_ENDPOINT (default localhost:4317)

Quick Start:
```bash
go run ./main.go
curl -H 'Authorization: Bearer dev' http://localhost:8080/v1/echo
```

Roadmap (Short-Term Enhancements):
1. Replace local limiter with shared `libs/go/core/resilience` RateLimiter for consistency.
2. JWT validation: JWKS fetch, cache, signature & claim verify, key rotation metrics.
3. Reverse proxy layer (per-route upstream config, circuit breaker & adaptive concurrency control).
4. GraphQL federation gateway integration (stitch threat-intel / policy / orchestrator services).
5. Dynamic config reload via SIGHUP or control-plane push (hot update rate limits & routes).
6. Fine-grained rate policies (per tenant, per token, burst vs sustained quotas) with Prometheus exemplars.

Production Hardening Checklist:
- Add correlation ID middleware (traceparent propagation)
- Enforce structured audit logs for auth failures & 5xx responses
- Implement request body size & header count guards
- Integrate WAF rule pre-filter (optional) before routing

Security Notes:
- Replace placeholder auth before exposure
- Enable mTLS for service-to-service in mesh

