# Billing Service

Endpoints:
- GET /health
- GET /metrics
- GET /v1/usage (current un-aggregated usage counts)
- POST /v1/call?key=K (simulate a metered call)

Metrics:
- swarm_usage_api_calls_total{key=""}
- swarm_billing_revenue_usd (observable gauge)

Aggregation:
- Every 30s: sums usage map, resets, applies rate 0.001 USD / call, accumulates totalRevenue.

Planned:
- Persistent storage (PostgreSQL) for invoices
- Tiered pricing + discounts
- Stripe integration
