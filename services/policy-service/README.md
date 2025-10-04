# Policy Service

Modes:
- simple (default fallback if POLICY_MODE=simple) – in-memory CRUD policies
- opa (default if unset) – load .rego modules from POLICY_DIR (default ./policies) and evaluate consolidated allow decision

Endpoints:
- GET /health
- GET /metrics
- (simple mode) POST /v1/policies (create policy) JSON: {"name":"n","version":1,"rule":"allow_all"}
- (simple mode) GET /v1/policies?name=NAME
- POST /v1/evaluate {"policy":"NAME","input":{}} (policy ignored in opa mode; uses rego)
- (opa mode) POST /v1/reload forces reload of rego bundle

Metrics:
- swarm_policy_evaluations_total
- swarm_policy_denials_total
- swarm_policy_reloads_total
- swarm_policy_reload_errors_total
- swarm_policy_evaluation_latency_ms (histogram)
- swarm_policy_compile_latency_ms (histogram)

Evaluation:
- simple: rule == "allow_all" → allow, else deny
- opa: naive embedded rego parser placeholder (detects allow { } rules + example action=="read") – to be replaced by full OPA SDK

Metrics Added:
- swarm_policy_reloads_total
- swarm_policy_reload_errors_total

Hot Reload:
- Background poll every 5s (watcher) recompiles rego modules on change
- Manual trigger via /v1/reload (opa mode)

Enhancements Added:
- fsnotify-based hot reload (debounced 200ms)
- Latency histograms for evaluation & compilation
- Decision logging (slog) with allow/deny reason
- pprof endpoint on :6060

Remaining Roadmap:
- Replace lightweight parser with full OPA SDK / WASM
- Structured decision logs to Kafka with sampling
- Bundle signature verification & caching layer
