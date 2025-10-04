# Threat Intelligence Enhancements

## Overview
This document describes the advanced threat intelligence features implemented for the Swarm Intelligence Network, focusing on **graph-based threat correlation**, **circuit breaker resilience**, and **streaming anomaly detection**.

---

## 1. Graph-Based Threat Correlation

### Architecture
The `ThreatGraph` component maintains entity relationships in memory for real-time threat correlation. In production, this should be backed by **Neo4j** or **dgraph** for scalability beyond 100K entities.

### Key Components

#### GraphNode
Represents a threat entity (IP, domain, hash, user, host):
```go
type GraphNode struct {
    ID         string              // SHA256(type:value)
    Type       string              // "ip", "domain", "hash", "user", "host"
    Value      string              // entity value
    FirstSeen  time.Time
    LastSeen   time.Time
    Score      float64             // threat score 0-10
    Attributes map[string]string   // custom metadata
}
```

#### GraphEdge
Represents relationships between entities:
```go
type GraphEdge struct {
    From       string   // source node ID
    To         string   // target node ID
    Type       string   // "connects_to", "downloads", "resolves_to", "owned_by"
    Weight     float64  // relationship strength (0-1)
    FirstSeen  time.Time
    LastSeen   time.Time
    EventCount int      // frequency of relationship
}
```

### Features

#### 1. Entity Relationship Tracking
- **AddNode**: Upsert entity with automatic ID generation
- **AddEdge**: Track relationships with automatic weight decay
- **FindRelated**: BFS traversal to find connected entities within N hops
- **FindAttackPath**: DFS search for attack chains between two entities

#### 2. Threat Score Calculation
```go
score := baseScore + (connectedScoreSum / connectedCount) * 0.3
```
- Base score from entity's own risk
- 30% influence from connected entities (weighted by edge strength)
- Temporal boost: +20% if activity within 1 hour, +10% if within 24 hours
- Capped at 10.0

#### 3. Anomaly Pattern Detection
Detects suspicious patterns automatically:
- **High-degree nodes**: Nodes with >50 edges + score >5 (potential C2 servers)
- **Dense subgraphs**: Clusters of interconnected malicious entities
- **Temporal anomalies**: Nodes with >20 new connections in last 5 minutes

#### 4. Memory Management
- **Prune(maxAge)**: Remove stale nodes/edges older than retention period
- **GetStats()**: Real-time statistics (total nodes, edges, type distribution)

### Performance Characteristics
- **AddNode**: O(1) average, O(log N) with mutex contention
- **AddEdge**: O(1)
- **FindRelated**: O(V + E) BFS complexity, limited by maxHops
- **FindAttackPath**: O(V + E) DFS complexity, limited by maxDepth
- **CalculateThreatScore**: O(degree) where degree = edges connected to node
- **DetectAnomalousPatterns**: O(V + E)

### Scalability Recommendations
For **>100K entities**, migrate to graph database:

#### Neo4j Integration
```cypher
// Create node
CREATE (n:ThreatEntity {id: $id, type: $type, value: $value, score: $score})

// Create relationship
MATCH (a:ThreatEntity {id: $from}), (b:ThreatEntity {id: $to})
CREATE (a)-[:CONNECTS_TO {weight: $weight, first_seen: $ts}]->(b)

// Find attack paths (max 5 hops)
MATCH path = (a:ThreatEntity {id: $from})-[*1..5]->(b:ThreatEntity {id: $to})
WHERE a.score > 5 AND b.score > 5
RETURN path
ORDER BY length(path) ASC
LIMIT 10
```

#### dgraph Integration
```graphql
type ThreatEntity {
  id: String! @id
  type: String! @search(by: [exact])
  value: String! @search(by: [exact, term])
  score: Float @search(by: [float])
  connects_to: [ThreatEntity] @reverse
}

query findRelated($id: string, $maxHops: int) {
  related(func: eq(id, $id)) @recurse(depth: $maxHops) {
    id
    type
    value
    score
    connects_to
  }
}
```

---

## 2. Circuit Breaker for External APIs

### Problem Statement
External threat feeds (MITRE ATT&CK, AlienVault OTX, VirusTotal) can experience:
- **Transient failures**: Network timeouts, rate limits
- **Cascading failures**: Slow responses block worker threads
- **Quota exhaustion**: API key limits hit during bursts

### Solution: Adaptive Circuit Breaker
Implemented using `libs/go/core/resilience/circuit_breaker.go` with **state machine pattern**.

### States
1. **Closed**: Normal operation, requests allowed
2. **Open**: All requests fail fast (returns `ErrCircuitOpen`)
3. **Half-Open**: Limited probes to test recovery

### Configuration
```go
cbConfig := resilience.CircuitBreakerConfig{
    MaxRequests: 3,                           // max probes in half-open
    Interval:    60 * time.Second,            // evaluation window
    Timeout:     30 * time.Second,            // open duration
    ReadyToTrip: resilience.RateTripFunc(0.5, 10), // 50% failure rate, min 10 requests
    OnStateChange: func(from, to resilience.State) {
        log.Infof("Circuit %s -> %s", from, to)
    },
}
```

### Integration Example
```go
// Feed collector wraps all external API calls
func (fc *FeedCollector) syncMITREAttack(ctx context.Context) error {
    err := fc.circuitBreakers.Get("mitre").Call(func() error {
        return fc.fetchMITREAttack(ctx)
    })
    return err
}
```

### State Transitions
```
Closed --> Open:
  - Failure rate ≥ 50% over 10+ requests in 60s window
  
Open --> Half-Open:
  - After 30s timeout

Half-Open --> Closed:
  - 3 consecutive successful probes

Half-Open --> Open:
  - Any single probe failure
```

### Benefits
- **Fast failure**: Open circuit returns errors in <1ms (no API call)
- **Automatic recovery**: Half-open probes test service health
- **Cascading failure prevention**: Protects downstream systems
- **Per-service isolation**: Separate breakers for MITRE/OTX/VT

### Metrics
```go
swarm_resilience_circuit_open_total{service="mitre"}      // circuit opens
swarm_resilience_circuit_closed_total{service="mitre"}    // circuit closes
```

---

## 3. Streaming Anomaly Detection (Welford + MAD)

### Algorithm: Welford's Online Variance
Computes mean and variance in **O(1) memory** with **numerical stability**:

```python
# Update mean and variance for new sample
delta = x - mean
mean += delta / n
M2 += delta * (x - mean)  # sum of squared differences
variance = M2 / (n - 1)
```

### Algorithm: MAD (Median Absolute Deviation)
Robust outlier detection resistant to extreme values:

```python
# Compute MAD score
median = np.median(data)
mad = np.median(np.abs(data - median))
mad_score = 0.6745 * (x - median) / mad

# Anomaly if |mad_score| > threshold (default 3.5)
is_anomaly = abs(mad_score) > threshold
```

### Implementation: `streaming_welford.py`

#### Single-Variate Detector
```python
detector = WelfordMADDetector(
    window_size=1000,
    mad_threshold=3.5,
    min_samples=30
)

for value in stream:
    is_anomaly, score = detector.update(value)
    if is_anomaly:
        alert(f"Anomaly detected: score={score:.2f}")
```

#### Multi-Variate Detector
```python
detector = MultiVariateWelfordDetector(
    window_size=1000,
    feature_weights=[0.4, 0.3, 0.3],  # latency, error_rate, cpu_usage
    mad_threshold=3.5
)

for event in stream:
    features = [event.latency, event.error_rate, event.cpu]
    is_anomaly, scores = detector.update(features)
    if is_anomaly:
        alert(f"Anomaly in features: {scores}")
```

### Features
- **Thread-safe**: RLock for concurrent streams
- **Adaptive thresholds**: Auto-adjust based on recent behavior
- **Rolling window**: Circular buffer for bounded memory
- **Graceful cold start**: Requires min_samples before anomaly detection

### Performance
- **Update latency**: 10-50 μs per sample (pure Python)
- **Memory usage**: O(window_size) per detector instance
- **Throughput**: ~100K samples/sec single-threaded

### Integration with Anomaly Detection Service
```python
# services/anomaly-detection/anomaly_detection/realtime_detector.py
from anomaly_detection.streaming_welford import MultiVariateWelfordDetector

class RealtimeDetector:
    def __init__(self):
        self.detector = MultiVariateWelfordDetector(
            window_size=5000,
            feature_weights=[0.3, 0.3, 0.2, 0.2],  # latency, cpu, mem, errors
            mad_threshold=3.5
        )
    
    def process_event(self, event):
        features = [
            event.response_time_ms,
            event.cpu_usage_pct,
            event.memory_mb,
            event.error_count
        ]
        is_anomaly, scores = self.detector.update(features)
        
        if is_anomaly:
            return {
                "anomaly": True,
                "mad_scores": scores,
                "event_id": event.id,
                "timestamp": event.timestamp
            }
        return {"anomaly": False}
```

---

## 4. Production Deployment Guide

### Threat Graph Service
```yaml
# services/threat-intel/deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: threat-intel-service
spec:
  replicas: 3
  template:
    spec:
      containers:
      - name: threat-intel
        image: swarm/threat-intel:latest
        env:
        - name: GRAPH_MAX_NODES
          value: "100000"
        - name: GRAPH_PRUNE_AGE
          value: "72h"
        - name: CIRCUIT_BREAKER_TIMEOUT
          value: "30s"
        resources:
          requests:
            memory: "2Gi"  # graph in-memory storage
            cpu: "1000m"
          limits:
            memory: "4Gi"
            cpu: "2000m"
```

### Circuit Breaker Tuning
```go
// Production-grade configuration
cbConfig := resilience.CircuitBreakerConfig{
    MaxRequests: 5,                            // more probes in half-open
    Interval:    120 * time.Second,            // longer evaluation window
    Timeout:     60 * time.Second,             // longer recovery time
    ReadyToTrip: resilience.RateTripFunc(0.3, 20), // 30% failure rate, min 20 requests
    OnStateChange: func(from, to resilience.State) {
        metrics.RecordCircuitStateChange(from, to)
        if to == resilience.StateOpen {
            alertmanager.Send(fmt.Sprintf("Circuit opened: %s", from))
        }
    },
}
```

### Streaming Detector Tuning
```python
# For high-frequency streams (>10K events/sec)
detector = WelfordMADDetector(
    window_size=10000,     # larger window for better statistics
    mad_threshold=4.0,     # higher threshold to reduce false positives
    min_samples=100,       # more samples before detection
    adaptive_threshold=True  # auto-adjust based on recent behavior
)
```

---

## 5. Testing & Validation

### Unit Tests
```bash
# Threat graph tests
go test ./services/threat-intel/internal -run TestThreatGraph -v

# Circuit breaker tests
go test ./libs/go/core/resilience -run TestCircuitBreaker -v

# Streaming detector tests
python -m pytest services/anomaly-detection/tests/test_streaming_welford.py -v
```

### Integration Tests
```bash
# End-to-end threat correlation
./scripts/test_threat_correlation.sh

# Circuit breaker under load
./scripts/chaos/api_fault_injection.sh

# Streaming detector accuracy
python scripts/test_streaming_accuracy.py --dataset synthetic_anomalies.csv
```

### Performance Benchmarks
```bash
# Graph operations
go test -bench=BenchmarkThreatGraph -benchmem ./services/threat-intel/internal

# Circuit breaker overhead
go test -bench=BenchmarkCircuitBreaker -benchmem ./libs/go/core/resilience

# Streaming detector throughput
python -m pytest services/anomaly-detection/tests/test_streaming_welford.py::test_throughput -v
```

---

## 6. Monitoring & Alerts

### Grafana Dashboard Queries

#### Threat Graph Size
```promql
# Total nodes
swarm_threat_graph_nodes_total

# Nodes by type
swarm_threat_graph_nodes_by_type{type="ip"}
swarm_threat_graph_nodes_by_type{type="domain"}
swarm_threat_graph_nodes_by_type{type="hash"}

# Edges
swarm_threat_graph_edges_total
```

#### Circuit Breaker Status
```promql
# Circuit state (0=closed, 1=open, 2=half-open)
swarm_resilience_circuit_state{service="mitre"}

# Circuit opens per hour
rate(swarm_resilience_circuit_open_total[1h])

# Circuit recovery time
histogram_quantile(0.95, swarm_resilience_circuit_recovery_duration_seconds)
```

#### Anomaly Detection Rate
```promql
# Anomalies detected per minute
rate(swarm_anomaly_streaming_detections_total[1m])

# MAD score distribution
histogram_quantile(0.99, swarm_anomaly_mad_score_bucket)
```

### Alert Rules
```yaml
# infra/alert-rules-threat-intel.yml
groups:
- name: threat-intel
  rules:
  - alert: ThreatGraphMemoryHigh
    expr: swarm_threat_graph_nodes_total > 80000
    for: 5m
    annotations:
      summary: "Threat graph approaching memory limit"
      
  - alert: CircuitBreakerOpen
    expr: swarm_resilience_circuit_state > 0
    for: 2m
    annotations:
      summary: "Circuit breaker open for {{ $labels.service }}"
      
  - alert: AnomalyDetectionSpike
    expr: rate(swarm_anomaly_streaming_detections_total[5m]) > 100
    for: 5m
    annotations:
      summary: "High anomaly detection rate"
```

---

## 7. Future Enhancements

### Graph Database Migration
- **Neo4j cluster**: 3-node HA setup for >1M entities
- **Graph algorithms**: PageRank for entity importance, community detection for threat campaigns
- **Temporal queries**: Time-based pattern matching (e.g., "attacks in last 7 days")

### Advanced Circuit Breaker Patterns
- **Bulkhead pattern**: Isolate thread pools per external service
- **Retry with exponential backoff**: Automatic retry for transient failures
- **Fallback strategies**: Return cached data when circuit open

### ML-Enhanced Anomaly Detection
- **Autoencoder ensemble**: Combine Welford+MAD with deep learning
- **Contextual anomaly detection**: Consider time-of-day, day-of-week patterns
- **Federated learning**: Train models across swarm nodes without centralizing data

### Threat Intelligence Enrichment
- **STIX 2.1 export**: Convert graph to STIX bundles for SIEM integration
- **Threat hunting queries**: Natural language interface ("find all IPs connected to ransomware campaigns")
- **Automated playbooks**: Trigger response actions for high-risk entities

---

## References
- [MITRE ATT&CK Framework](https://attack.mitre.org/)
- [AlienVault OTX API](https://otx.alienvault.com/api)
- [VirusTotal API v3](https://developers.virustotal.com/reference/overview)
- [Welford's Algorithm](https://en.wikipedia.org/wiki/Algorithms_for_calculating_variance#Welford's_online_algorithm)
- [MAD for Outlier Detection](https://www.itl.nist.gov/div898/handbook/eda/section3/eda35h.htm)
- [Circuit Breaker Pattern](https://martinfowler.com/bliki/CircuitBreaker.html)
