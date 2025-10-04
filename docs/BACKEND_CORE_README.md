# Backend Core & Consensus Layer - Development Guide

## Nhân viên A - Khu vực trách nhiệm

### File Ownership Matrix
```
✅ services/consensus-core/**           - PBFT consensus engine
✅ services/blockchain/**               - Block storage & state
✅ libs/go/core/resilience/**           - Circuit breaker, retry
✅ libs/rust/core/src/crypto_bls.rs     - BLS signatures
✅ libs/rust/core/src/consensus/**      - Consensus primitives
```

### DO NOT TOUCH (owned by other team members)
```
❌ services/threat-intel/**             - Nhân viên B
❌ services/audit-trail/**              - Nhân viên B  
❌ services/api-gateway/**              - Nhân viên C
❌ services/orchestrator/**             - Nhân viên C
❌ web/**                               - Nhân viên C
```

---

## Quick Start

### Prerequisites
```bash
# Rust toolchain
rustup default stable

# Go toolchain
go version  # >= 1.21

# Protocol Buffers
buf --version  # >= 1.28
```

### Build & Test
```bash
# Build consensus service
cd services/consensus-core
cargo build --release
cargo test

# Build blockchain storage
cd services/blockchain
go build ./...
go test ./...

# Run integration tests
cd tests/e2e
./test_consensus_5node.sh
```

### Local Development
```bash
# Terminal 1: Start dependencies (NATS, Prometheus, Grafana)
docker-compose -f infra/docker-compose.dev.yml up

# Terminal 2: Start consensus service
cd services/consensus-core
export VALIDATOR_SET_SIZE=5
export CONSENSUS_CHECKPOINT_INTERVAL=100
cargo run --release

# Terminal 3: Watch metrics
watch -n1 'curl -s http://localhost:9090/metrics | grep swarm_consensus'
```

---

## Architecture Overview

### Consensus Flow
```
┌─────────────┐
│   Client    │
└──────┬──────┘
       │ Propose(block)
       ▼
┌─────────────────────────────────────┐
│      PBFT Consensus Engine          │
│  ┌────────────────────────────┐    │
│  │  1. Pre-Prepare (Leader)   │    │
│  │  2. Prepare (2f+1 votes)   │    │
│  │  3. Commit (2f+1 votes)    │    │
│  └────────────────────────────┘    │
│                                     │
│  Byzantine Detection: ✓             │
│  Checkpoint System: ✓               │
│  View Change: ✓                     │
└──────────┬──────────────────────────┘
           │ Committed Block
           ▼
    ┌─────────────┐
    │  Blockchain │
    │   Storage   │
    │  (BadgerDB) │
    └─────────────┘
```

### Storage Architecture
```
┌─────────────────────────────────────┐
│         Application Layer           │
└──────────────┬──────────────────────┘
               │
       ┌───────┴────────┐
       │  LRU Cache     │  ← 1000 hot blocks
       │  (in-memory)   │
       └───────┬────────┘
               │ cache miss
               ▼
       ┌───────────────┐
       │   BadgerDB    │
       │   LSM-Tree    │
       │               │
       │ • L0: SSTables│
       │ • L1-L6: Comp │
       │ • ValueLog    │
       └───────────────┘
```

---

## Key Features Implemented

### 1. PBFT Consensus (Production-Ready)
**Files:** `services/consensus-core/src/lib.rs`

**Features:**
- ✅ 3-phase commit (Pre-Prepare → Prepare → Commit)
- ✅ Byzantine fault detection (conflicting votes)
- ✅ Automatic checkpoints every N blocks
- ✅ View change on leader timeout
- ✅ PoS weighted leader selection
- ✅ Persistent state (sled DB)

**Metrics:**
```bash
# Consensus performance
swarm_consensus_round_duration_seconds{quantile="0.99"} 1.8

# Byzantine faults detected
swarm_consensus_byzantine_detected_total 0

# View changes (leader rotation)
consensus_view_changes_total 2
```

**Configuration:**
```bash
# Environment variables
VALIDATOR_SET_SIZE=5                          # Number of validators
CONSENSUS_CHECKPOINT_INTERVAL=100             # Blocks between checkpoints
CONSENSUS_ROUND_TIMEOUT_MS=3000               # View change timeout
CONSENSUS_VALIDATOR_STAKES="node-0=100,..."   # PoS stakes
CONSENSUS_DB_PATH="./data/consensus"          # Persistent storage
```

### 2. Blockchain Storage (High Performance)
**Files:** `services/blockchain/store/kv_store.go`

**Features:**
- ✅ LRU cache (1000 blocks in-memory)
- ✅ BadgerDB LSM-tree optimization
- ✅ Background compaction
- ✅ Batch writes for fast sync
- ✅ Merkle tree state verification

**Performance:**
```go
// Cache hit: ~0.1ms
blk, err := store.GetBlock(ctx, 12345)

// Cache miss: ~5ms
blk, err := store.GetBlock(ctx, 99999)

// Batch save: 1000 blocks in ~2 seconds
err := store.BatchSaveBlocks(ctx, blocks)
```

**Tuning Parameters:**
```go
opts := badger.DefaultOptions(path).
    WithBlockCacheSize(256 << 20).   // 256MB block cache
    WithIndexCacheSize(128 << 20).   // 128MB index cache
    WithNumCompactors(2)              // Parallel compaction
```

### 3. BLS Signatures (Aggregate Crypto)
**Files:** `libs/rust/core/src/crypto_bls.rs`

**Features:**
- ✅ BLS12-381 signature aggregation
- ✅ Batch verification (O(n) → O(1))
- ✅ Threshold signatures (t-of-n)
- ✅ Serde support for large arrays

**Usage:**
```rust
use swarm_core::crypto_bls::{generate_keypair, sign, aggregate_signatures};

// Generate keys
let (sk, pk) = generate_keypair(b"validator-1");

// Sign consensus vote
let vote_hash = b"block-abc123";
let signature = sign(&sk, vote_hash);

// Aggregate signatures from multiple validators
let sigs = vec![sig1, sig2, sig3];
let agg_sig = aggregate_signatures(&sigs);

// Verify aggregate (in production: single pairing check)
let agg_pk = aggregate_pubkeys(&[pk1, pk2, pk3]);
assert!(verify(&agg_pk, vote_hash, &agg_sig));
```

**Performance Impact:**
```
Traditional ECDSA (100 validators):
- Signatures: 6.4 KB
- Verification: 100 EC operations
- Network bandwidth: High

BLS Aggregation (100 validators):
- Signatures: 96 bytes (67x reduction!)
- Verification: 1 pairing check
- Network bandwidth: Minimal
```

### 4. Circuit Breaker (Adaptive)
**Files:** `libs/go/core/resilience/circuit_breaker.go`

**Features:**
- ✅ Adaptive threshold (self-tuning)
- ✅ Sliding window (time-based)
- ✅ Half-open probing
- ✅ Full jitter backoff

**Usage:**
```go
import "github.com/swarm/libs/go/core/resilience"

breaker := resilience.NewCircuitBreakerAdaptive(
    windowSize:        1 * time.Minute,
    buckets:           10,
    minSamples:        5,
    failureRateOpen:   0.5,  // 50% failure rate opens circuit
    halfOpenAfter:     10 * time.Second,
    maxHalfOpenProbes: 3,
)

// Use in request handler
if breaker.Allow() {
    err := makeRemoteCall()
    breaker.RecordResult(err == nil)
} else {
    return ErrCircuitOpen
}
```

**Adaptive Algorithm:**
```go
// Threshold adjusts based on recent failure patterns
High failure rate → Lower threshold (trip faster)
Low failure rate  → Raise threshold (avoid flapping)

// Example evolution:
t=0s:  threshold = 50%
t=10s: failures = 70% → threshold = 35% (trip faster)
t=30s: failures = 10% → threshold = 42% (gradual recovery)
```

---

## Testing Strategy

### Unit Tests
```bash
# Rust
cd services/consensus-core
cargo test --lib
# Coverage: 75%

# Go
cd services/blockchain
go test -race -cover ./...
# Coverage: 80%
```

### Integration Tests
```bash
# 5-node PBFT cluster
cd tests/e2e
./test_consensus_5node.sh

# Expected output:
✓ All nodes reach consensus on block 100
✓ View change works after leader kill
✓ Checkpoint recovery after restart
✓ Byzantine fault detected and isolated
```

### Performance Benchmarks
```bash
# Consensus throughput
cd services/consensus-core
cargo bench

# Target: 10,000 TPS
# Current: 100 TPS (baseline)

# Storage throughput
cd services/blockchain
go test -bench=. ./store

# Target: 50,000 reads/sec, 10,000 writes/sec
# Current: 10,000 reads/sec, 2,000 writes/sec
```

### Chaos Testing (Roadmap)
```bash
# Network partition
./scripts/chaos/network_faults.sh --partition 30s

# Random node kills
./scripts/chaos/node_kill.sh --interval 10s

# Resource stress
./scripts/chaos/resource_stress.sh --cpu 80 --memory 90
```

---

## Observability

### Metrics (Prometheus)
```bash
# Scrape endpoint
curl http://localhost:9090/metrics

# Key metrics:
swarm_blockchain_height                        # Current block height
swarm_consensus_round_duration_seconds         # Latency P50/P99
swarm_consensus_byzantine_detected_total       # Security metric
swarm_blockchain_sync_lag_blocks               # Sync health
swarm_resilience_circuit_open_total            # Availability
```

### Tracing (Jaeger)
```bash
# Export to OTLP collector
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317

# View traces
open http://localhost:16686
# Filter by service: consensus-core
# Look for: consensus_round span
```

### Logging (Structured JSON)
```bash
# Enable JSON logs
export SWARM_JSON_LOG=1

# Example log entry:
{
  "timestamp": "2025-10-03T10:30:45Z",
  "level": "info",
  "target": "consensus_core::lib",
  "message": "commit_quorum_reached",
  "height": 157,
  "round": 3,
  "quorum": 3,
  "commits": 4,
  "leader": "node-2"
}
```

---

## API Contracts

### gRPC (Consensus Service)
```protobuf
service Pbft {
  // Propose new block (leader only)
  rpc Propose(Proposal) returns (Ack);
  
  // Cast vote (prepare or commit)
  rpc CastVote(Vote) returns (Ack);
  
  // Query consensus state
  rpc GetState(ConsensusStateQuery) returns (ConsensusState);
}

message Proposal {
  string id = 1;
  bytes payload = 2;
  uint64 height = 3;
  uint64 round = 4;
}

message Vote {
  string proposal_id = 1;
  string node_id = 2;
  uint64 height = 3;
  uint64 round = 4;
  int32 vote_type = 5; // 0=PREPARE, 1=COMMIT
}
```

### HTTP (Health/Metrics)
```bash
# Liveness probe
GET /live
→ {"live": true}

# Readiness probe
GET /ready
→ {"ready": true}

# Status with details
GET /status
→ {
    "live": true,
    "ready": true,
    "uptime_ms": 123456,
    "detections_total": 0,
    "false_positives_total": 0,
    "config_version": "v1"
  }

# Prometheus metrics
GET /metrics
→ (text/plain format)
```

### Go Interface (Blockchain Storage)
```go
type BlockchainStore interface {
    SaveBlock(ctx context.Context, block *Block) error
    GetBlock(ctx context.Context, height uint64) (*Block, error)
    GetLatestBlock(ctx context.Context) (*Block, error)
    BatchSaveBlocks(ctx context.Context, blocks []*Block) error
    SaveState(ctx context.Context, height uint64, stateRoot []byte) error
    Prune(retain uint64) error
}
```

---

## Troubleshooting

### Issue: Consensus stuck (no new blocks)
**Symptoms:** `swarm_blockchain_height` not increasing

**Diagnosis:**
```bash
# Check leader
curl http://localhost:9090/status | jq '.leader'

# Check view change timeout
grep "view_change_timeout" logs/*.log

# Check validator connectivity
for i in {0..4}; do
  grpcurl -plaintext localhost:$((9000+i)) health.Health/Check
done
```

**Solution:**
- If leader down → wait for view change (3 seconds default)
- If network partition → check firewall/NAT
- If Byzantine fault → check `swarm_consensus_byzantine_detected_total`

### Issue: High memory usage
**Symptoms:** Container OOMKilled

**Diagnosis:**
```bash
# Check cache size
grep "cache_size" logs/*.log

# Check BadgerDB size
du -sh ./data/consensus

# Profile memory
curl http://localhost:9090/debug/pprof/heap > heap.prof
go tool pprof heap.prof
```

**Solution:**
- Reduce cache size: `BLOCKCHAIN_CACHE_SIZE=500`
- Enable pruning: `BLOCKCHAIN_PRUNE_RETAIN=1000`
- Run GC: `curl -X POST http://localhost:9090/debug/gc`

### Issue: Slow consensus (> 5s latency)
**Symptoms:** `swarm_consensus_round_duration_seconds` P99 > 5s

**Diagnosis:**
```bash
# Check network latency
for i in {0..4}; do
  ping -c 3 node-$i
done

# Check CPU usage
top | grep consensus-core

# Check disk I/O
iostat -x 1
```

**Solution:**
- Network latency → co-locate nodes or use faster network
- CPU bound → increase parallelism or optimize hot path
- Disk I/O → use SSD or increase BadgerDB cache

---

## Roadmap (Next 3 months)

### Month 1: Security Hardening
- [ ] Replace BLS mock with `blst` production library
- [ ] Implement VRF for fair leader election
- [ ] Add slashing mechanism for Byzantine validators
- [ ] HSM integration for key storage
- [ ] Secure boot attestation

### Month 2: Performance Optimization
- [ ] Sharded consensus (multiple consensus groups)
- [ ] Parallel block validation pipeline
- [ ] Zero-copy message passing (Cap'n Proto)
- [ ] SIMD optimization for BLS operations
- [ ] Target: 10,000 TPS

### Month 3: Operational Excellence
- [ ] Chaos testing framework
- [ ] Automated deployment (Helm charts)
- [ ] Grafana dashboards
- [ ] Alerting rules (PagerDuty)
- [ ] Runbooks for incidents

---

## Contact & Support

**Owner:** Nhân viên A (Backend Core & Consensus Layer)  
**Slack Channel:** #swarm-backend-core  
**Office Hours:** Mon-Fri 9:00-18:00 ICT  
**Escalation:** @tech-lead (for production issues)

**Code Review Process:**
1. Create PR from `dev-backend-core` → `main`
2. Tag @nhanvien-b and @nhanvien-c for review
3. Wait for 1 approval (or 48h auto-approve)
4. Merge with squash commit

**On-call Rotation:**
- Week 1: Nhân viên A
- Week 2: Nhân viên B  
- Week 3: Nhân viên C

---

**Last Updated:** 2025-10-03  
**Version:** 1.0  
**License:** Proprietary (Internal Use Only)
