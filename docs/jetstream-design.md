# JetStream Persistence Design (Placeholder)

Goal: Introduce durable, replayable event streams for critical domains (ingestion + consensus) using NATS JetStream while preserving existing subject taxonomy and versioning rules.

## Streams

| Stream | Subjects (wildcards) | Retention | Max Age | Storage | Replicas | Purpose |
|--------|----------------------|-----------|---------|---------|----------|---------|
| INGEST_RAW_V1 | ingest.v1.raw | Limits (size+age) | 7d | File | 3 | Durable storage of raw ingested events for reprocessing / ML feature backfill |
| INGEST_STATUS_V1 | ingest.v1.status | Limits | 24h | Memory | 1 | Lightweight status pings / health markers |
| CONSENSUS_EVENTS_V1 | consensus.v1.* | Limits | 30d | File | 3 | Replay consensus decisions & audit trail |

## Key Configuration Principles

- Version isolation: Each major event version gets its own stream (e.g., ingest.v1.raw vs ingest.v2.raw) to allow parallel migration.
- Bounded retention: Use MaxBytes + MaxAge to cap disk usage; initial sizing to revisit after perf data.
- Replication: 3 replicas for data critical to audit (raw + consensus), 1 for ephemeral status.
- Storage: File for durability; Memory only where loss is acceptable (status heartbeats).
- Consumer namespacing: Prefix durable consumer names with service (e.g., `sensor-gateway_raw_ingest_cursor`).

## Consumers (Initial)

| Consumer | Stream | Mode | Ack Policy | Deliver | Max Inflight | Backoff (ms) | Purpose |
|----------|--------|------|-----------|--------|--------------|--------------|---------|
| ANALYTICS_BATCH | INGEST_RAW_V1 | Pull | Explicit | Batch jobs | 512 | 100,250,500 | Periodic feature extraction / ML |
| REALTIME_PIPE | INGEST_RAW_V1 | Push | Explicit | Service | 256 | 50,100,250 | Realtime enrichment / detection |
| CONSENSUS_AUDIT_TAIL | CONSENSUS_EVENTS_V1 | Pull | Explicit | Auditor | 128 | 200,400 | Audit / reconciliation |

## Subject Mapping Rationale

Keep existing subjects exactly; JetStream binds via stream subject wildcards. No code change to publishers; only enabling JetStream server side + future subscriber adaptation.

## Migration Steps (Planned)

1. Infra: Enable JetStream in dev (nats-server --jetstream) & add docker-compose overrides.
2. Provision streams via `nats` CLI or bootstrap script (idempotent).
3. Add health check verifying required streams exist (control-plane or separate bootstrap service).
4. Introduce a feature flag `USE_JETSTREAM=1` in ingestion & consensus consumers to switch from basic sub to JetStream consumer.
5. Add durability tests: publish N events, restart NATS, ensure replay.
6. Metrics: expose per-stream lag + consumer delivery metrics.
7. Production hardening: tune memory, file storage, compaction, encryption-at-rest (future).

## Open Questions

- Encryption of payload at rest? (Potential envelope encryption before publish.)
- Backpressure strategy when consumer lag grows (auto scale vs dropping lower-priority). 
- Schema evolution impact on replay (need transformation layer?).

## Next Actions

- Draft provisioning script (Month 2).
- Add ADR referencing this placeholder once validated by initial performance tests.

