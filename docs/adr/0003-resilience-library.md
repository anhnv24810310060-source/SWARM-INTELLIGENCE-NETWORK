# ADR 0003: Resilience Library (Retry & Circuit Breaker)

Date: 2025-10-01
Status: Accepted

## Context
Repeating ad-hoc retry/backoff logic across services leads to inconsistency and hidden failure modes. We introduced a Rust crate `swarm-resilience` and Go package `resilience` providing unified primitives.

## Decision
Centralize retry & circuit breaker implementations:
- `retry_async(fn, attempts, delay)` for simple bounded retries.
- `CircuitBreaker` (failure threshold + half-open duration) for external dependencies (NATS, gRPC calls).

## Design Notes
- Minimal API first; metrics hooks to be added later (`circuit_open_total`, `retry_attempts_total`).
- Keep state lightweight (parking_lot / sync.Mutex).

## Consequences
Positive:
- Consistent failure handling semantics.
- Easier to add global instrumentation later.
Negative:
- Risk of under-powering advanced patterns (bulkhead, rate limiting) initially.

## Future Enhancements
- Exponential backoff strategy injection
- Jitter support
- Metrics + tracing spans for retry attempts
- Open/half-open events published to internal bus

## Migration Strategy
Refactor existing bespoke retry logic (e.g., control-plane dial) to library wrappers; enforce usage via lint/documentation.
