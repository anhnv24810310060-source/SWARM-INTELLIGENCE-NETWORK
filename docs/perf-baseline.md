# Performance Baseline (Initial)

Component: sensor-gateway RawEvent encode
Date: 2025-10-01
Environment: (dev container) CPU ~shared GitHub Codespaces (non-dedicated) – numbers indicative only.

## Benchmarks (criterion) – Placeholder
Run via:
```
cargo bench -p sensor-gateway -- benches::ingest
```

Expected functions:
- raw_event_encode_256B
- raw_event_encode_1KB
- raw_event_encode_batch_100

(Actual numeric results to be pasted after first real run and updated over time.)

## KPIs Tracked
- Encode latency p50/p95 per size bucket.
- Throughput events/sec (derived from batch test loop iterations).
- Impact on histogram `swarm_ingest_encode_latency_ms` distribution.

## Next Steps
- Add NATS publish micro-benchmark (async_nats) with local server.
- Add end-to-end ingest -> encode -> publish pipeline benchmark.
- Record memory allocations (enable `--features track-alloc` future) & flamegraph.

