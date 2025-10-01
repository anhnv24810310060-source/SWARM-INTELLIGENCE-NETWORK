# ADR 0001: Monorepo Strategy Adoption

Date: 2025-10-01
Status: Accepted

## Context
Original roadmap mentioned potential polyrepo. Rapid iteration across Rust/Go/Python services with shared proto, resilience, and core telemetry utilities would have high coordination overhead split across repos.

## Decision
Adopt a single monorepo for the first 6–9 months to accelerate:
- Cross-language proto/schema evolution
- Shared libraries (resilience, core telemetry, config)
- Consistent CI/security enforcement

## Consequences
Positive:
- Faster refactors, atomic multi-service changes
- Centralized dependency + license governance
Negative:
- Larger repo size, longer CI if not optimized
- Harder to delegate repo ownership boundaries

## Mitigations
- Introduce path-based CODEOWNERS
- Optional later extraction (services stable) with release automation

## Status Review Criteria
Revisit at end of Phase 2 (Month 6) with metrics: build duration, change coupling score, contribution velocity.
