# ADR 0002: Event Versioning & Taxonomy

Date: 2025-10-01
Status: Accepted

## Context
Early introduction of versioned subjects (e.g., `consensus.v1.height.changed`, `ingest.v1.raw`) prevents breaking consumers and formalizes evolution.

## Decision
Adopt subject pattern `<domain>.v<major>.<entity>[.<action>]` with protobuf payloads embedding `PROTO_SCHEMA_VERSION` when available.

## Rules
1. Additive fields: no version bump.
2. Backward incompatible removal / semantic change: increment major (`v2`).
3. Deprecation window: minimum 2 release cycles.
4. Changelog must note new fields & removals.
5. Consumers must ignore unknown fields.

## Consequences
- Clear migration story.
- Additional maintenance of deprecated streams.

## Tooling & Enforcement
- CI label `schema-change` triggers review.
- Hash `PROTO_SCHEMA_VERSION` embedded at build.
- Future: auto diff proto to detect breaking changes.

## Alternatives Considered
- Flat subjects without versioning (rejected: brittle)
- Semantic hashing only (rejected: unclear for routing semantics)

## Future Work
- JetStream stream naming convention alignment
- Schema registry (optional) for non-proto payloads
