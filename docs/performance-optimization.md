# Performance Optimization Guide

## E2E Latency Profiling

### Running Flamegraph

```bash
# Install flamegraph tooling
cargo install flamegraph

# Run detection overhead benchmark with flamegraph
cd services/sensor-gateway
cargo bench --bench detection_overhead -- --profile-time=10

# Run E2E latency benchmark with profiling
cargo bench --bench e2e_latency

# Flamegraph output: target/criterion/*/profile/flamegraph.svg
```

### Key Hotspots Identified

1. **Regex Compilation** (30-40% of detection time)
   - Mitigation: Cache compiled patterns using `lazy_static` or `once_cell`
   - Current: Compile on every rule load
   - Target: Compile once, reuse across requests

2. **NATS Publish Overhead** (15-20% of E2E time)
   - Mitigation: Connection pooling + message batching
   - Current: Single connection, individual publishes
   - Target: Pool of 4-8 connections, micro-batching (10ms window)

3. **Hash Computation** (10-15% of detection time)
   - Mitigation: Pre-compute hash for repeated payloads (LRU cache)
   - Current: Compute SHA-256 for every event
   - Target: Cache recent 1000 hashes (< 100KB memory)

4. **JSON Serialization** (8-12% of alert publish)
   - Mitigation: Use `simd-json` or pre-serialize common fields
   - Current: Standard `serde_json`
   - Target: Zero-copy serialization where possible

## Optimization Roadmap

### Phase 1: Quick Wins (Current)
- [x] Add profiling infrastructure (pprof + flamegraph)
- [ ] Lazy regex compilation caching
- [ ] NATS connection pooling

### Phase 2: Advanced (Week 2)
- [ ] Payload hash LRU cache
- [ ] Message batching with adaptive window
- [ ] Zero-copy deserialization (flatbuffers experiment)

### Phase 3: System-Level (Week 3-4)
- [ ] CPU pinning for detection threads
- [ ] Lock-free queue for NATS publishing
- [ ] DPDK/io_uring for packet capture (if needed)

## Target Metrics

| Metric | Baseline | Phase 1 Target | Phase 2 Target | Phase 3 Target |
|--------|----------|----------------|----------------|----------------|
| Detection latency (p95) | ~180µs | <120µs | <80µs | <50µs |
| NATS publish (p95) | ~320µs | <200µs | <150µs | <100µs |
| E2E latency (p95) | ~650ms | <500ms | <350ms | <200ms |
| Throughput | 10K ev/s | 15K ev/s | 25K ev/s | 50K ev/s |

## Profiling Commands Reference

```bash
# CPU profiling
cargo flamegraph --bench detection_overhead

# Memory profiling  
cargo build --release --bin sensor-gateway
valgrind --tool=massif ./target/release/sensor-gateway

# Trace profiling
cargo build --release
perf record -F 99 -g ./target/release/sensor-gateway
perf script | stackcollapse-perf.pl | flamegraph.pl > perf.svg

# Continuous profiling (production)
# Use pprof HTTP endpoint: http://localhost:6060/debug/pprof/profile?seconds=30
```

## Regression Prevention

- Benchmark runs in CI on every PR (perf-nightly workflow)
- Alert if p95 regresses >10% vs 7-day baseline
- Flamegraph artifact uploaded for manual inspection
- Performance budget: detection <100µs, E2E <400ms

## Notes

- All timings measured on: 2 vCPU, 4GB RAM (standard CI runner)
- Production expected: 4-8× better (dedicated hardware)
- Network latency excluded from E2E (local NATS)
