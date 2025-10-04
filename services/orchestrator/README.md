Orchestrator Service
====================

Current Features:
- In-memory workflow registry (create + fetch)
- Parallel DAG execution (worker pool=4) with dependency tracking
- Basic deadlock detection (unexecuted remaining tasks)
- Run timeout (5s) to prevent runaway workflows
- Metrics: runs, errors, workflow latency, task latency histograms
- Health & metrics endpoints

API Summary:
- POST /v1/workflows  (register new workflow)
- GET  /v1/workflows?name=NAME  (retrieve definition)
- POST /v1/run {"workflow":"sample"}  (execute workflow)

Sample Definition:
```json
{
	"name": "wf1",
	"tasks": [
		{"id": "fetch", "type": "http"},
		{"id": "analyze", "type": "python", "depends_on": ["fetch"]}
	]
}
```

Execution Flow:
1. Build adjacency & indegree counts
2. Seed zero-dependency tasks into a ready channel
3. Workers pull, execute, record metrics
4. On completion, decrement dependents indegree; enqueue when zero
5. Cancel context when all tasks done or timeout triggers

Near-Term Roadmap:
- Persistent store (durable history + Run IDs)
- Task plugins (HTTP, gRPC, model inference, script sandbox)
- Per-task retry/backoff + circuit breaker integration
- Cron + event bus triggers (NATS JetStream / Kafka)
- Cancellation & progress query endpoint (/v1/runs/{id})
- Workflow versioning + blue/green deployments
- Multi-tenant isolation & quotas (rate, concurrent runs)

Resilience Enhancements Planned:
- Adaptive concurrency limiting per workflow
- Backpressure signaling to API gateway when saturation detected

Security Considerations:
- AuthN/Z placeholder; integrate policy-service OPA for run authorization
- Validate task definitions (ID format, DAG acyclicity) on registration

Dev Quick Start:
```bash
go run ./main.go &
curl -X POST localhost:8080/v1/workflows -d '{"name":"wf1","tasks":[{"id":"t1","type":"http"},{"id":"t2","type":"http","depends_on":["t1"]}]}' -H 'Content-Type: application/json'
curl -X POST localhost:8080/v1/run -d '{"workflow":"wf1"}' -H 'Content-Type: application/json'
```

Testing:
- Basic unit tests in `orchestrator/orchestrator/workflow_test.go` (expand with failure paths)

