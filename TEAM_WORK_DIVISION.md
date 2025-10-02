# PHÂN CHIA CÔNG VIỆC CHI TIẾT - SWARM INTELLIGENCE NETWORK
## Nguyên tắc phân chia: Module Ownership + Layer Segregation

---

## **NHÂN VIÊN A - BACKEND CORE & CONSENSUS LAYER**

### Khu vực trách nhiệm:
```
services/consensus/
services/blockchain/
services/validator/
libs/go/core/
libs/rust/core/
internal/consensus/
internal/blockchain/
```

### Chi tiết công việc:

#### 1. **Consensus Engine (PBFT + PoS Hybrid)**
- **File ownership:**
  - `services/consensus/pbft/`
  - `services/consensus/pos/`
  - `internal/consensus/validator.go`
  - `internal/consensus/state_machine.go`

- **Nhiệm vụ cụ thể:**
  - [ ] Triển khai PBFT 3-phase protocol (Pre-Prepare, Prepare, Commit)
  - [ ] Xây dựng validator selection algorithm (PoS weighted random)
  - [ ] Implement view change mechanism khi primary node fail
  - [ ] Tạo checkpoint system (mỗi 100 blocks)
  - [ ] Byzantine fault tolerance testing (chịu được f = (n-1)/3 malicious nodes)
  - [ ] Metrics: `swarm_consensus_round_duration_seconds`, `swarm_consensus_faults_total`

- **Dependencies interface (contract với team khác):**
  ```go
  // Chỉ expose interface này cho team khác
  type ConsensusEngine interface {
      ProposeBlock(ctx context.Context, txs []Transaction) (BlockID, error)
      ValidateBlock(ctx context.Context, block Block) error
      GetValidators(epoch uint64) ([]ValidatorInfo, error)
  }
  ```

#### 2. **Blockchain Storage & State Management**
- **File ownership:**
  - `services/blockchain/store/`
  - `services/blockchain/state/`
  - `internal/blockchain/merkle.go`

- **Nhiệm vụ cụ thể:**
  - [ ] LevelDB/BadgerDB wrapper cho block storage
  - [ ] Merkle tree implementation cho state verification
  - [ ] Block pruning strategy (keep last 10,000 blocks + checkpoints)
  - [ ] State snapshot/restore cho node sync nhanh
  - [ ] Transaction indexing (by hash, by address, by timestamp)
  - [ ] Metrics: `swarm_blockchain_height`, `swarm_blockchain_sync_lag_blocks`

- **API Contract:**
  ```go
  type BlockchainStore interface {
      SaveBlock(ctx context.Context, block *Block) error
      GetBlock(ctx context.Context, height uint64) (*Block, error)
      GetLatestBlock(ctx context.Context) (*Block, error)
      SaveState(ctx context.Context, stateRoot Hash) error
  }
  ```

#### 3. **Core Libraries (Go + Rust)**
- **File ownership:**
  - `libs/go/core/resilience/`
  - `libs/go/core/otelinit/`
  - `libs/rust/core/src/crypto.rs`
  - `libs/rust/core/src/consensus.rs`

- **Nhiệm vụ cụ thể:**
  - [ ] Circuit breaker với adaptive threshold (dựa trên error rate 5 phút gần nhất)
  - [ ] Retry policy với exponential backoff + jitter
  - [ ] Cryptographic primitives (BLS signatures, threshold signatures)
  - [ ] Zero-knowledge proof helpers (zk-SNARK wrapper)
  - [ ] Rate limiter (token bucket + sliding window)

#### 4. **Testing & Documentation**
- [ ] Unit tests: coverage >= 80% cho consensus logic
- [ ] Integration tests: 5-node local cluster test
- [ ] Chaos testing: random node kill, network partition simulation
- [ ] API documentation: Swagger/OpenAPI specs cho consensus APIs
- [ ] Runbook: consensus failure recovery procedures

---

## **NHÂN VIÊN B - SECURITY & INTELLIGENCE LAYER**

### Khu vực trách nhiệm:
```
services/threat-intel/
services/audit-trail/
services/signature-engine/
services/anomaly-detection/
ml/
libs/python/core/
internal/security/
```

### Chi tiết công việc:

#### 1. **Threat Intelligence Engine**
- **File ownership:**
  - `services/threat-intel/collectors/`
  - `services/threat-intel/correlation/`
  - `internal/security/threat_scoring.go`

- **Nhiệm vụ cụ thể:**
  - [ ] Tích hợp external threat feeds (MITRE ATT&CK, AlienVault OTX, VirusTotal)
  - [ ] Real-time threat correlation engine (match indicators across feeds)
  - [ ] Threat scoring algorithm (CVSS + custom risk factors)
  - [ ] IoC (Indicators of Compromise) database với TTL
  - [ ] Automated threat report generation (daily digest)
  - [ ] Metrics: `swarm_threats_detected_total`, `swarm_threat_feeds_sync_lag_seconds`

- **API Contract:**
  ```go
  type ThreatIntelService interface {
      QueryThreat(ctx context.Context, indicator string) (*ThreatInfo, error)
      ReportThreat(ctx context.Context, threat *ThreatReport) error
      GetActiveThreats(ctx context.Context, severity Severity) ([]*Threat, error)
  }
  ```

#### 2. **Signature Engine (YARA + Custom Rules)**
- **File ownership:**
  - `services/signature-engine/rules/`
  - `services/signature-engine/scanner/`
  - `internal/security/pattern_matcher.go`

- **Nhiệm vụ cụ thể:**
  - [ ] YARA rule compiler và runtime
  - [ ] Custom signature DSL (JSON-based rules)
  - [ ] Multi-threaded scanning engine (mỗi worker handle 1 file/stream)
  - [ ] Rule versioning và A/B testing (test new rules trên 10% traffic trước)
  - [ ] False positive feedback loop (admin mark FP → auto-tune threshold)
  - [ ] Metrics: `swarm_signatures_matched_total`, `swarm_scan_duration_seconds`

#### 3. **Anomaly Detection (ML Models)**
- **File ownership:**
  - `ml/anomaly_detection/`
  - `services/anomaly-detection/inference/`
  - `libs/python/core/ml_utils.py`

- **Nhiệm vụ cụ thể:**
  - [ ] Isolation Forest cho network traffic anomaly
  - [ ] Autoencoder (TensorFlow/PyTorch) cho behavioral analysis
  - [ ] Online learning pipeline (retrain model mỗi 24h với new data)
  - [ ] Model versioning (MLflow tracking)
  - [ ] Feature engineering pipeline (extract 50+ features từ raw logs)
  - [ ] A/B testing framework cho model deployment
  - [ ] Metrics: `swarm_anomaly_score_percentile`, `swarm_ml_inference_latency_ms`

- **API Contract:**
  ```python
  class AnomalyDetector:
      def predict(self, features: pd.DataFrame) -> List[AnomalyScore]:
          """Return anomaly scores [0-1] for each sample"""
          pass
      
      def explain(self, sample_id: str) -> ExplanationReport:
          """SHAP values for model interpretability"""
          pass
  ```

#### 4. **Audit Trail & Compliance**
- **File ownership:**
  - `services/audit-trail/storage/`
  - `services/audit-trail/query/`
  - `internal/security/compliance_checker.go`

- **Nhiệm vụ cụ thể:**
  - [ ] Immutable audit log (append-only, tamper-proof với Merkle tree)
  - [ ] PII redaction engine (auto-detect và mask SSN, credit cards, emails)
  - [ ] Compliance policy engine (GDPR, HIPAA, SOC2 rules)
  - [ ] Advanced query DSL (filter by user/action/resource/timerange)
  - [ ] Export to SIEM (Splunk, ELK) qua Kafka
  - [ ] Metrics: `swarm_audit_events_total`, `swarm_compliance_violations_total`

#### 5. **Testing & Documentation**
- [ ] Unit tests: ML model accuracy >= 95% on test set
- [ ] Security tests: penetration testing cho threat intel APIs
- [ ] Performance tests: scan 10,000 files/sec target
- [ ] Documentation: threat hunting playbooks, model cards cho ML models

##### (Progress Auto-Generated - 2025-10-02)
Hiện đã scaffold các thành phần cốt lõi:
- signature-engine service: hot reload rule store (in-memory) + KMP matcher (roadmap Aho-Corasick / Hyperscan)
- anomaly-detection service: FastAPI skeleton + metrics + daily retrain stub
- threat-intel service: internal store (sharded in-memory TTL), correlator + scoring primitives
- audit-trail service: append-only Merkle-chained log + verification endpoint
Metrics ban đầu đã expose: swarm_signatures_matched_total, swarm_ml_inference_latency_ms, swarm_audit_events_total.
Roadmap kế tiếp: tối ưu multi-pattern search, IsolationForest real model, feed collectors (OTX, VirusTotal), persistent segment store cho audit.

---

## **NHÂN VIÊN C - ORCHESTRATION & USER-FACING LAYER**

### Khu vực trách nhiệm:
```
services/api-gateway/
services/orchestrator/
services/billing-service/
services/policy-service/
services/federation/
web/
deployments/
infrastructure/
```

### Chi tiết công việc:

#### 1. **API Gateway & Service Mesh**
- **File ownership:**
  - `services/api-gateway/routing/`
  - `services/api-gateway/auth/`
  - `infrastructure/istio/`

- **Nhiệm vụ cụ thể:**
  - [ ] Rate limiting (per-user, per-IP, per-API-key)
  - [ ] JWT authentication & authorization middleware
  - [ ] Request validation (JSON schema validation)
  - [ ] GraphQL federation gateway (stitch multiple services)
  - [ ] API versioning strategy (v1, v2 parallel support)
  - [ ] Circuit breaker cho downstream services
  - [ ] Metrics: `swarm_api_requests_total`, `swarm_api_latency_p99_ms`

- **API Contract:**
  ```yaml
  # OpenAPI 3.0 spec
  paths:
    /api/v1/threats:
      get:
        summary: List threats
        parameters:
          - name: severity
            in: query
            schema: {type: string, enum: [low, medium, high, critical]}
        responses:
          200: {description: Success}
  ```

#### 2. **Orchestrator (Workflow Engine)**
- **File ownership:**
  - `services/orchestrator/workflows/`
  - `services/orchestrator/executor/`
  - `internal/orchestration/dag.go`

- **Nhiệm vụ cụ thể:**
  - [ ] DAG-based workflow engine (Airflow-like)
  - [ ] Built-in tasks: HTTP request, SQL query, Python script, model inference
  - [ ] Cron scheduler + event-driven triggers (Kafka message → workflow)
  - [ ] Workflow versioning (blue-green deployment cho workflows)
  - [ ] Retry policy per-task (max 3 retries với backoff)
  - [ ] Web UI cho workflow visualization (React + D3.js)
  - [ ] Metrics: `swarm_workflow_runs_total`, `swarm_workflow_duration_seconds`

- **Workflow Definition Example:**
  ```yaml
  # threat_response_workflow.yaml
  name: auto_threat_response
  trigger: {type: kafka, topic: threats.detected}
  tasks:
    - id: enrich
      type: http
      url: "http://threat-intel/api/enrich"
      retry: {max: 3}
    - id: score
      type: python
      script: "score_threat.py"
      depends_on: [enrich]
    - id: block
      type: http
      url: "http://firewall/api/block"
      condition: "score.result.risk > 0.8"
      depends_on: [score]
  ```

#### 3. **Policy Engine (OPA Integration)**
- **File ownership:**
  - `services/policy-service/opa/`
  - `services/policy-service/rules/`
  - `internal/policy/evaluator.go`

- **Nhiệm vụ cụ thể:**
  - [ ] Open Policy Agent (OPA) wrapper service
  - [ ] Policy as Code (Rego language rules)
  - [ ] Dynamic policy updates (hot reload không cần restart)
  - [ ] Policy testing framework (unit tests cho Rego rules)
  - [ ] Decision logging (record mọi policy evaluation)
  - [ ] Policy versioning với Git integration
  - [ ] Metrics: `swarm_policy_evaluations_total`, `swarm_policy_denials_total`

- **Example Policy:**
  ```rego
  # block_high_risk_transfers.rego
  package swarm.authorization

  default allow = false

  allow {
      input.action == "transfer"
      input.amount < 10000
      input.user.risk_score < 0.5
  }
  ```

#### 4. **Federation & Multi-Tenancy**
- **File ownership:**
  - `services/federation/sync/`
  - `services/federation/trust/`
  - `internal/federation/topology.go`

- **Nhiệm vụ cụ thể:**
  - [ ] Cross-swarm communication protocol (gRPC + mTLS)
  - [ ] Trust establishment (certificate exchange, revocation)
  - [ ] Data synchronization (CRDTs cho conflict resolution)
  - [ ] Tenant isolation (namespace-based, network policies)
  - [ ] Federated query engine (query multiple swarms, merge results)
  - [ ] Metrics: `swarm_federation_peers_total`, `swarm_federation_sync_lag_seconds`

#### 5. **Billing & Metering**
- **File ownership:**
  - `services/billing-service/metering/`
  - `services/billing-service/invoicing/`

- **Nhiệm vụ cụ thể:**
  - [ ] Usage metering (count API calls, storage GB, compute hours)
  - [ ] Tiered pricing engine (free tier, pro tier, enterprise)
  - [ ] Invoice generation (PDF reports qua LaTeX)
  - [ ] Payment gateway integration (Stripe, PayPal sandbox)
  - [ ] Usage alerts (email khi user reach 80% quota)
  - [ ] Metrics: `swarm_billing_revenue_usd`, `swarm_usage_api_calls_total`

#### 6. **Web Dashboard (React + TypeScript)**
- **File ownership:**
  - `web/dashboard/`
  - `web/components/`

- **Nhiệm vụ cụ thể:**
  - [ ] Real-time threat visualization (WebSocket updates, heatmap)
  - [ ] Admin panel (user management, policy editor)
  - [ ] Workflow builder (drag-and-drop UI)
  - [ ] Audit log viewer (pagination, filtering)
  - [ ] Metrics dashboard (Grafana embed hoặc custom charts)
  - [ ] Accessibility (WCAG 2.1 AA compliance)

#### 7. **Infrastructure & Deployment**
- **File ownership:**
  - `deployments/kubernetes/`
  - `deployments/terraform/`
  - `infrastructure/monitoring/`

- **Nhiệm vụ cụ thể:**
  - [ ] Kubernetes manifests (Helm charts cho từng service)
  - [ ] Terraform modules (provision GKE/EKS/AKS clusters)
  - [ ] CI/CD pipelines (GitHub Actions: build → test → deploy)
  - [ ] Monitoring stack (Prometheus + Grafana + Alertmanager)
  - [ ] Log aggregation (Loki hoặc ELK stack)
  - [ ] Disaster recovery plan (automated backups, restore procedures)

#### 8. **Testing & Documentation**
- [ ] E2E tests: Playwright tests cho web UI
- [ ] Load tests: 10,000 concurrent users target
- [ ] Deployment runbooks: rollback procedures, scaling guides
- [ ] User documentation: API tutorials, video guides

---

## **COORDINATION PROTOCOLS (Tránh Xung Đột)**

### 1. **Git Branch Strategy**
```
main (protected)
├── dev-backend-core     (Nhân viên A)
├── dev-security         (Nhân viên B)
└── dev-orchestration    (Nhân viên C)
```

**Rules:**
- Mỗi người chỉ push vào branch của mình
- Merge vào `main` qua Pull Request, cần 1 approval từ 2 người còn lại
- Daily merge `main` → dev branches (10:00 AM) để sync
- Conflict resolution: người tạo PR chịu trách nhiệm resolve

### 2. **File Ownership Matrix**
```yaml
# CODEOWNERS file
/services/consensus/**           @nhanvien-a
/services/blockchain/**          @nhanvien-a
/libs/go/core/**                 @nhanvien-a
/libs/rust/core/**               @nhanvien-a

/services/threat-intel/**        @nhanvien-b
/services/audit-trail/**         @nhanvien-b
/services/signature-engine/**    @nhanvien-b
/ml/**                           @nhanvien-b
/libs/python/core/**             @nhanvien-b

/services/api-gateway/**         @nhanvien-c
/services/orchestrator/**        @nhanvien-c
/services/policy-service/**      @nhanvien-c
/services/federation/**          @nhanvien-c
/web/**                          @nhanvien-c
/deployments/**                  @nhanvien-c
/infrastructure/**               @nhanvien-c

# Shared files cần approval từ cả 3
/go.work                         @nhanvien-a @nhanvien-b @nhanvien-c
/docker-compose.yml              @nhanvien-a @nhanvien-b @nhanvien-c
/README.md                       @nhanvien-a @nhanvien-b @nhanvien-c
```

### 3. **Interface Contract System**
Mỗi service expose interface qua:
- **gRPC proto files** (trong `proto/`)
- **OpenAPI specs** (trong `api/openapi/`)

**Protocol:**
1. Khi cần thay đổi interface:
   - Tạo issue mô tả breaking change
   - Tag 2 người còn lại trong issue
   - Chờ approval (timeout 2 ngày → auto-approve)
2. Versioning: thêm field mới OK, xóa field → bump major version

### 4. **Shared Database Schema Changes**
- Migration files trong `migrations/` theo naming: `YYYYMMDD_HHMM_author_description.sql`
- Nhân viên A: tables `consensus_*`, `blocks`, `validators`
- Nhân viên B: tables `threats`, `audit_logs`, `signatures`, `anomalies`
- Nhân viên C: tables `users`, `policies`, `workflows`, `billing`

**Rule:**
- Không xóa column trong 30 ngày (deprecation period)
- Foreign keys giữa các khu vực → cần sync trước qua Slack

### 5. **Communication Channels**
- **Slack channels:**
  - `#swarm-backend-core` (Nhân viên A)
  - `#swarm-security` (Nhân viên B)
  - `#swarm-orchestration` (Nhân viên C)
  - `#swarm-integration` (discuss cross-team issues)

- **Daily standup (async):**
  - Post update lúc 9:00 AM mỗi ngày trong channel của mình
  - Format: "Yesterday: ..., Today: ..., Blockers: ..."

- **Weekly sync call:**
  - Thứ 2, 2:00 PM
  - Agenda: demo features, discuss integration issues, plan next week

### 6. **Testing Isolation**
```yaml
# GitHub Actions workflow
jobs:
  test-backend-core:
    runs-on: ubuntu-latest
    if: contains(github.event.head_commit.modified, 'services/consensus') || 
        contains(github.event.head_commit.modified, 'services/blockchain')
    steps:
      - run: cd services/consensus && go test ./...
  
  test-security:
    runs-on: ubuntu-latest
    if: contains(github.event.head_commit.modified, 'services/threat-intel')
    steps:
      - run: cd services/threat-intel && pytest tests/
  
  test-orchestration:
    runs-on: ubuntu-latest
    if: contains(github.event.head_commit.modified, 'services/api-gateway')
    steps:
      - run: cd services/api-gateway && npm test
```

Mỗi người chỉ chịu trách nhiệm fix tests trong khu vực của mình.

---

## **MILESTONES & TIMELINE**

### Phase 1: Foundation (Weeks 1-4)
- **Nhân viên A:** PBFT basic implementation (3-node cluster), blockchain storage
- **Nhân viên B:** Threat intel feed integration (3 sources), YARA engine basic
- **Nhân viên C:** API gateway setup, Kubernetes manifests cho core services

**Integration checkpoint (Week 4):**
- Deploy 3-node cluster locally
- API gateway → consensus service health check works

### Phase 2: Core Features (Weeks 5-10)
- **Nhân viên A:** Validator selection, view change, state snapshots
- **Nhân viên B:** Anomaly detection model training, audit trail immutability
- **Nhân viên C:** Orchestrator DAG engine, policy service OPA integration

**Integration checkpoint (Week 10):**
- End-to-end workflow: API call → threat detection → policy evaluation → consensus

### Phase 3: Advanced Features (Weeks 11-16)
- **Nhân viên A:** Byzantine fault tolerance testing, cryptographic optimizations
- **Nhân viên B:** ML model A/B testing, compliance automation
- **Nhân viên C:** Federation protocol, web dashboard, billing system

**Integration checkpoint (Week 16):**
- Multi-tenant demo: 2 swarms communicate, federated query works

### Phase 4: Production Readiness (Weeks 17-20)
- **All:** Load testing, security audit, documentation
- **Nhân viên C:** CI/CD hardening, monitoring/alerting setup

**Final checkpoint (Week 20):**
- Production deployment, chaos engineering tests pass

---

## **CONFLICT RESOLUTION PROCESS**

### Scenario 1: Overlapping File Edits
**Example:** Cả A và B edit `internal/security/common.go`

**Resolution:**
1. Git conflict → người PR sau resolve
2. Nếu không tự resolve được → escalate to team lead (trong 4h)
3. Team lead quyết định: split file thành 2 modules hoặc merge manually

### Scenario 2: API Contract Dispute
**Example:** A muốn thêm field `validator_pubkey` vào Block struct, B dùng Block trong audit trail

**Resolution:**
1. A tạo RFC (Request for Comments) trong `docs/rfcs/`
2. Tag B, 48h comment period
3. Nếu không có objection → proceed
4. Nếu có objection → sync call trong 24h, vote (majority wins)

### Scenario 3: Performance Bottleneck
**Example:** C's API gateway gọi B's threat intel service → timeout

**Resolution:**
1. C open issue, tag B
2. B profile service, identify bottleneck (within 1 day)
3. Options:
   - B optimize service
   - C add caching layer
   - Both agree on async pattern (Kafka)
4. Implement + verify fix trong 3 ngày

---

## **TOOLING & AUTOMATION**

### Pre-commit Hooks (Enforced)
```yaml
# .pre-commit-config.yaml
repos:
  - repo: local
    hooks:
      - id: check-file-ownership
        name: Verify file ownership
        entry: python scripts/check_ownership.py
        language: python
        pass_filenames: true
```

Script kiểm tra: nếu commit file ngoài khu vực của mình → warning (không block nhưng log vào Slack).

### Automated Dependency Updates
- Dependabot tự tạo PR cho dependency updates
- Assign PR dựa trên CODEOWNERS
- Auto-merge nếu tests pass + security scan OK

### Merge Queue
- Merge Fridays only (trừ hotfix)
- Batch merges để giảm conflict frequency

---

## **SUCCESS METRICS**

### Team Efficiency
- [ ] Average PR merge time < 24h
- [ ] Merge conflict rate < 5%
- [ ] Code review response time < 4h (business hours)

### Code Quality
- [ ] Test coverage: A >= 80%, B >= 85% (ML critical), C >= 75%
- [ ] Zero high-severity security vulnerabilities (Snyk scan)
- [ ] Cyclomatic complexity < 15 per function

### System Performance
- [ ] Consensus latency < 2s (P99)
- [ ] Threat detection throughput > 10,000 events/sec
- [ ] API gateway P99 latency < 200ms

---

## **EMERGENCY PROTOCOLS**

### Production Incident
1. **Nhân viên A:** Handle consensus/blockchain failures
2. **Nhân viên B:** Handle security breaches, data leaks
3. **Nhân viên C:** Handle API outages, infrastructure issues

**On-call rotation:** Weekly (A → B → C → repeat)

### Hotfix Process
1. Create branch from `main`: `hotfix/description`
2. Fix + test locally
3. Direct push to `main` (skip PR for critical issues)
4. Post-mortem trong 48h

---

Bản phân chia này đảm bảo:
✅ **Zero overlap:** Mỗi file chỉ thuộc 1 người (trừ shared configs)  
✅ **Clear interfaces:** Contract-driven development  
✅ **Automated guards:** Pre-commit hooks + CODEOWNERS  
✅ **Explicit protocols:** RFC cho breaking changes  
✅ **Fast resolution:** 48h timeout cho conflicts  