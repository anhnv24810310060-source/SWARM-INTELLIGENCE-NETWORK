# Metrics Naming Standard

Prefix strategy:
- Global prefix: `swarm_` for cross-cutting platform metrics.
- Service-scoped operational metrics: `svc_<service>_` (e.g. `svc_consensus_round_duration_ms`).
- Ingestion pipeline counters: `swarm_ingest_...` reserved for raw ingest.

Guidelines:
1. Use nouns with unit suffix when relevant: `_seconds`, `_bytes`, `_total` (counter), `_ratio`.
2. Avoid high cardinality labels (target < 50 distinct values per label).
3. Required labels for service metrics: `service`, optional: `component`, `status` (bounded set).
4. Do not embed environment in metric name; use label `env` if needed.
5. Counters are monotonically increasing; use `_total` suffix.
6. Histograms: prefer explicit buckets defined centrally (future config) - placeholder now.

Current Implemented Metrics:
- `swarm_ingest_events_total`: Count of RawEvent successfully processed.
- `swarm_ingest_errors_total`: Count of ingestion pipeline errors.
- `swarm_ingest_degraded_mode_total`: Times ingestion entered degraded mode (broker unavailable at startup or transient reconnect failure window).
- `swarm_ingest_encode_latency_ms`: Histogram of protobuf encode time per RawEvent (milliseconds) – helps track serialization hotspots.
- `swarm_ingest_payload_bytes`: Histogram of payload size distribution – capacity planning & anomaly detection (sudden spike in size).
 - `swarm_ingest_degraded_mode_total`: Times ingestion entered degraded mode due to missing broker.
 - `swarm_resilience_retry_attempts_total`: Total retry attempts executed (shared resilience library).
 - `swarm_resilience_circuit_open_total`: Number of times a circuit transitions to open state.

Future Reserved Names:
- `svc_consensus_round_total` (counter of rounds)
- `svc_consensus_round_duration_seconds` (histogram)
- `svc_policy_eval_duration_seconds`
- `swarm_wasm_plugin_load_total`

Versioning:
- Backward-compatible label additions allowed (must keep old labels functioning as optional).
- Renames or unit changes require deprecation period and announcement in CHANGELOG.
