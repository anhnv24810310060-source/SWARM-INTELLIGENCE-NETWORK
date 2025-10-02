# Signature Rules

Supported (initial placeholder):
- JSON DSL files (one rule per file)

Example `high_risk_ip.json`:
```json
{
  "id": "high_risk_ip",
  "type": "dsl",
  "pattern": "1.2.3.4",
  "version": 1,
  "enabled": true,
  "severity": "high",
  "sample_percent": 100,
  "tags": ["ip", "reputation"],
  "updated_at": "2025-10-02T00:00:00Z"
}
```

Future:
- YARA compiled rules `.yar` (will add compilation step)
- Rule pack manifest for A/B testing (traffic shard weight)

Reloading:
- Directory polled every 2-3s with cheap hash (size ^ mtime aggregate)
- Only enabled rules loaded into in-memory slice

Runtime behavior:
- Only `enabled` rules loaded.
- `sample_percent` (<100) applies probabilistic sampling to reduce noise / A/B test new signatures.
- Hot reload builds a new automaton off-thread then atomically swaps.
- Metrics increment per match with attributes `rule_type` and `severity` when present.

Metrics Additions:
- `swarm_signature_match_total{rule_type,severity}`
- `swarm_signature_rules_loaded`
- `swarm_signatures_reloads_total{status}`
- `swarm_signatures_reload_duration_seconds`
- `swarm_scan_duration_seconds`
- `swarm_scan_errors_total`
- `swarm_scan_active`

Performance Roadmap:
1. Add optional ASCII indexed array nodes to reduce map overhead.
2. Streaming API for large artifacts (chunk window matching).
3. Bloom pre-filter for very large rule sets.
4. Hyperscan integration (license / CPU feature detection).
5. Automaton compaction & serialization cache.
