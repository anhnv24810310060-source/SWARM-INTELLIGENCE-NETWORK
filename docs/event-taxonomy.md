# Event Taxonomy (Versioned Subjects)

Principles:
- Subjects are namespaced: `<domain>.<version>.<entity>[.<action>]`.
- Version bump on backward-incompatible schema change.
- Payload MUST include `proto_schema_version` (build hash) when produced by services with protobuf context.

Current Subjects:
- `consensus.v1.height.changed` : Emitted when consensus height increments. Payload fields: height, round, leader, proto_schema_version.
- `consensus.v1.round.changed` : Emitted when consensus round changes (leader rotation or vote progress).
- `ingest.v1.raw` : RawEvent protobuf (swarm.ingestion.RawEvent) frames prior to normalization.
- `ingest.v1.status` : Plain text status signal (online/offline) from sensor-gateway.

Reserved / Planned:
- `policy.v1.applied`
- `threat.v1.alert` (post detection pipeline)
- `fl.v1.round.completed`

Versioning Rules:
1. Additive (new optional field) → keep version; document in CHANGELOG.
2. Field semantic change / removal → create new version (e.g. `ingest.v2.raw`), old producer supported for deprecation window.
3. Consumers must tolerate unknown fields (protobuf) or ignore extra JSON fields.
4. No reuse of subject names across versions once retired; maintain alias mapping optional.

Delivery & Durability:
- Currently NATS core (at-least-best-effort). For durability, future JetStream streams:
  - Stream `CONSENSUS_EVENTS` subjects: `consensus.v1.*`
  - Stream `INGEST_RAW` subjects: `ingest.v1.raw`

Schema Governance:
- All proto changes require PR label `schema-change` with diff review.
- Hash (PROTO_SCHEMA_VERSION) embedded at build for provenance.

Deprecation Policy:
- Minimum 2 release cycles support for previous version after new major subject introduced.
