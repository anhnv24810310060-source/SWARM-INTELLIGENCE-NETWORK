# LỘ TRÌNH PHÁT TRIỂN 12 THÁNG – SWARMGUARD INTELLIGENCE NETWORK

Phiên bản: 1.0  
Ngày lập: 2025-10-01  
Ngôn ngữ: Tiếng Việt  
Phạm vi: Xây dựng nền tảng vi kiến trúc (microservices) mở rộng, bảo mật, sẵn sàng sản xuất trong 12 tháng.

---
## 1. MỤC TIÊU CHIẾN LƯỢC
- Tạo nền tảng bảo mật phân tán hoạt động theo mô hình “miễn dịch số” với khả năng tự thích nghi.  
- Kiến trúc microservices linh hoạt, có thể mở rộng tuyến tính và hỗ trợ triển khai edge + cloud.  
- Đạt chuẩn sẵn sàng Production cuối tháng 12 với các chỉ số:  
  - Detection Rate ≥ 98% (MVP) sau tháng 6, ≥ 99% sau tháng 12  
  - False Positive ≤ 0.5% (MVP), ≤ 0.1% Production  
  - Mean Response Time < 50ms (MVP), < 10ms (Production cluster)  
  - Uptime ≥ 99.95% (beta), ≥ 99.99% (production)  

---
## 2. PHÂN RÃ MICROservices (BOUNDED CONTEXTS)
| Domain | Service | Mô tả chính | Công nghệ chính | Ghi chú ưu tiên |
|--------|---------|-------------|-----------------|-----------------|
| Ingestion | sensor-gateway | Thu thập packet/log/metrics | Rust (Tokio), eBPF | Tháng 1-3 |
| Gossip & P2P | swarm-gossip | Truyền bá thông tin nguy cơ | Rust + QUIC | Tháng 2-4 |
| Consensus | consensus-core | PBFT sửa đổi quyết định chung | Rust + libp2p/quic | Tháng 3-6 |
| Node Agent | node-runtime | Agent chạy tại edge, thực thi phòng thủ | Rust + WASM sandbox | Tháng 1-6 |
| Threat Intelligence | threat-intel | Tổng hợp IOC, enrichment, reputation | Go + RocksDB + API | Tháng 4-7 |
| Model Registry | model-registry | Quản lý version ML/ONNX | Python FastAPI + MinIO | Tháng 4-6 |
| Federated Learning | federated-orchestrator | Điều phối vòng FL phân tầng | Python (PyTorch) + gRPC | Tháng 5-9 |
| ML Inference | inference-gateway | Phục vụ model tối ưu hóa (ONNX Runtime) | Rust + ONNX Runtime | Tháng 5-8 |
| Evolution Engine | evolution-core | GA/PSO/ACO điều chỉnh chiến lược | Python + Ray | Tháng 7-11 |
| Policy & Rules | policy-service | Quản lý policy động, versioning | Go + Postgres/CockroachDB | Tháng 5-8 |
| Identity & PKI | identity-ca | Quản lý chứng chỉ, attestation TPM | Rust + PKI + Kyber/Dilithium | Tháng 2-5 |
| Security Events | event-bus | Streaming sự kiện (threat, audit) | NATS JetStream / Redpanda | Tháng 2-4 |
| Observability | telemetry-stack | Metrics, logs, traces | OpenTelemetry + Tempo + Loki + Prometheus | Xuyên suốt |
| Control Plane | control-plane | Orchestrate cấu hình, rollout | Go + gRPC + Operator K8s | Tháng 6-10 |
| Edge Manager | edge-fleet | Quản lý device, bootstrap, cập nhật | Rust + gRPC | Tháng 6-9 |
| Billing & Usage | billing-service | Tính toán usage & pricing tiers | Go + ClickHouse | Tháng 8-11 |
| Web/API Portal | admin-portal | Quản lý, dashboard, RBAC | Next.js + GraphQL + Keycloak | Tháng 6-12 |
| Audit & Compliance | audit-trail | Lưu trữ sự kiện chuẩn hóa, WORM | Go + Append-only log | Tháng 7-11 |
| Risk Scoring | risk-engine | Tính điểm rủi ro, weighting consensus | Rust + WASM plugin | Tháng 7-10 |

---
## 3. PHÂN CHIA THEO GIAI ĐOẠN (PHASES)
| Phase | Thời gian | Trọng tâm | Chỉ số Exit Criteria |
|-------|-----------|-----------|----------------------|
| Phase 1 – Foundation & POC | Tháng 1-3 | Core agent, ingestion, gossip, observability, CI/CD, bảo mật nền | Demo end-to-end threat capture + basic detection; >85% unit test coverage core libs; Deployment tự động staging |
| Phase 2 – Consensus & Security Core | Tháng 4-6 | Consensus PBFT, identity/PKI, model registry, FL baseline, threat-intel | PBFT ổn định (≤ 300ms round trong cluster 25 nodes); mTLS + PQC handshake PoC; Model update federated vòng 1 |
| Phase 3 – Scaling & Intelligence | Tháng 7-9 | Evolution engine, inference optimization, control plane, edge manager, policy | Latency < 50ms P95; Federated learning đa tầng; Policy dynamic rollout < 5m; 500 nodes beta |
| Phase 4 – Hardening & Production | Tháng 10-12 | Billing, portal, audit, compliance, performance tuning, SRE playbooks | 99.99% HA tests; Detection ≥ 99%; False positive < 0.1%; Chaos tests pass; Ready for GA |

### 3.1 HÀNH ĐỘNG ƯU TIÊN (CẬP NHẬT 01/10/2025)
Mục tiêu: Hoàn tất các thành phần còn thiếu để đạt Exit Criteria Phase 1 trong 4 tuần tới và chuẩn bị nền tảng Phase 2.

#### P0-1: Detection Pipeline (Deadline: 08/10)
Owner: Rust Team  | Trạng thái: Pending
Tasks:
- Signature matching engine (YAML rule: id, pattern, severity, action)
- Baseline anomaly heuristic (sliding window 1m/5m/15m, thresholds env-driven)
- Publish `threat.v1.alert.detected` (NATS) với trace context
- Metrics: `swarm_detection_anomaly_total`, `swarm_detection_signature_total`
Exit Criteria:
- Detection rate ≥ 85% (synthetic dataset)
- False positive < 2%
- Throughput ≥ 10K events/s với detection bật
- Integration test alert flow pass

#### P0-2: End-to-End Integration Test (Deadline: 11/10)
Owner: QA / Platform  | Trạng thái: Pending
Scope:
- Docker compose full stack: sensor-gateway, swarm-gossip, consensus-core, control-plane, NATS
- Test script: inject malicious event → expect alert → assert latency & trace continuity
- CI workflow `e2e-test.yml` (PR + nightly)
Exit Criteria:
- ≥95% pass rate
- P95 end-to-end latency < 500ms
- Trace context preserved across 4 hops

#### P0-3: Identity/PKI Core (Deadline: 15/10)
Owner: Security Team  | Trạng thái: Pending
Tasks:
- Root & intermediate CA (offline root simulation)
- gRPC IssueCertificate(CSR) + CRL endpoint
- Secure join: token → signed cert → mTLS handshake
- PQC hybrid stub (X25519 + Kyber768) benchmark (PoC only)
Exit Criteria:
- 1000 certs/min issuance sustained
- Join latency < 2s P95
- mTLS established between 2 sample services
- CRL checked on validation path

#### P1-4: Consensus Hardening (Deadline: 15/10)
Owner: Consensus Team  | Trạng thái: Pending
Tasks:
- Persist votes (RocksDB) & recovery after crash
- View change timeout & leader re-election logic
- Metrics: `consensus_round_duration_ms`, `consensus_view_changes_total`
- Safety property tests (agreement, termination) + simulated partition
Exit Criteria:
- No stall after single leader failure (f=1, n=4)
- View change completes < 2x timeout
- Latency ≤ 300ms P95 (simulated 25 nodes)

#### P1-5: Chaos Testing Framework (Deadline: 18/10)
Owner: SRE Team  | Trạng thái: Pending
Scenarios:
- Network: +200ms latency, 10% loss, node partition
- Process: random crash 30% nodes
- Resource: CPU throttle, memory pressure
- Disk: fill to 95% then release
Exit Criteria:
- Pass ≥ 8/10 scenarios
- MTTR < 30s single node crash
- No cascading failure observed

Timeline (tổng quan):
```
Week 1 (01-08/10): Detection Pipeline
Week 2 (08-15/10): E2E Test + Identity/PKI + Consensus Hardening
Week 3 (15-18/10): Chaos Framework
Week 4 (18-29/10): Stabilize & Phase 1 Exit Review
```

Risk Notes:
- Chưa có detection logic → blocker demo end-to-end.
- PKI chậm sẽ trì hoãn adoption mTLS ở Phase 2.
- Thiếu persistence consensus → nguy cơ state loss sau restart.
- Chưa có chaos validation → rủi ro ẩn trong failure modes.

Review mốc: Phase 1 Exit Review (29/10/2025)


#### 3.1.1 NEXT STEPS (Incremental Additions 01/10/2025 - Bổ sung)
Các hạng mục dưới đây được phân tầng theo mức độ ưu tiên (P0 > P1 > P2) nhằm thu hẹp khoảng cách còn lại của Exit Criteria Phase 1 và chuẩn bị nền tảng mở rộng Phase 2. Đây là phần bổ sung động và sẽ được cập nhật tuần/lần.

| Priority | Task | Purpose / Lý do | Phạm vi chính | Success / Exit Criteria | Owner (dự kiến) | Target Start |
|----------|------|-----------------|---------------|-------------------------|-----------------|--------------|
| P0 | Integration test detection + NATS | Đảm bảo đường đi alert end-to-end hoạt động thực tế (ingest → detect → publish) | Compose stack tối thiểu (sensor-gateway, NATS) + script inject payload + assert subject `threat.v1.alert.detected` | Test script PASS trong CI (PR + nightly), latency alert publish < 150ms P95 | QA / Platform + Rust | 02/10 |
| P0 | Benchmark overhead detection (trước/sau) | Kiểm soát regression hiệu năng khi bật detection | Criterion benchmark + runtime bench (10K events/s synthetic) so sánh CPU%, throughput | Overhead CPU ≤ +15% so baseline; Throughput giữ ≥ 10K ev/s | Rust Perf | 03/10 |
| P1 | Thêm metrics detection & expose dashboard | Quan sát & cảnh báo sớm drift hoặc FP tăng | Instrument counters (`swarm_detection_signature_total`, `swarm_detection_anomaly_total`, FP ratio) + Grafana panel + alert rule | Dashboard xuất hiện trong folder Security; Alert rule firing nếu FP > 2% 10m | Observability | 05/10 |
| P1 | Add view change timeout & test | Hoàn thiện resilience consensus (leader failure) | Thêm timer round, trigger view change, unit + sim test (crash leader) | View change < 2x timeout; test pass n=4 với 1 leader crash | Consensus Team | 07/10 |
| P2 | Chaos workflow (weekly) | Bắt đầu baseline resilience & thu thập MTTR lịch sử | GH workflow chạy script `scripts/chaos/*.sh` chế độ dry-run + optional real run nightly hoặc weekly | Workflow artifact log + report MTTR initial; Không phá vỡ pipeline | SRE | 12/10 |

Ghi chú triển khai:
- Hai hạng mục P0 chạy song song nhưng Integration test merge trước để phục vụ benchmark alert latency.
- Benchmark sẽ sinh thêm cột `detection_enabled` vào `perf-trend.csv` để so sánh trực quan.
- Metrics detection cần thống nhất naming convention (tiền tố `swarm_detection_`).
- View change test dùng mô phỏng crash: dừng thread leader hoặc inject fault handler.
- Chaos workflow giai đoạn đầu có thể chạy chế độ "validate scripts" (exit 0) trước khi kích hoạt thật.

Rủi ro & Giảm thiểu:
- Test flakiness NATS: thêm retry publish/subscribe (backoff) trong test harness.
- Benchmark sai lệch do warmup: chạy 2 pass (discard pass 1) giữ số liệu ổn định.
- View change timer drift: dùng `tokio::time::sleep` + cấu hình timeout qua ENV để dễ điều chỉnh.
- Chaos script có thể yêu cầu quyền cao: chế độ dry-run trong CI để tránh failure permission.

Liên kết KPI:
- P0 tasks trực tiếp unlock KPI Detection Rate & Throughput Phase 1.
- P1 tasks đóng góp vào giảm FP & đảm bảo latency consensus.
- P2 chaos workflow tạo dữ liệu baseline cho Reliability KPI (availability & MTTR) các phase sau.


---
## 4. LỘ TRÌNH THEO THÁNG (DETAILED TIMELINE)
### Tháng 1
- Thành lập nhóm kỹ thuật, kiến trúc mục tiêu, chuẩn code & security baseline (Rust + Go guidelines).
- Xây dựng chiến lược repo: ban đầu chọn polyrepo nhưng điều chỉnh sang monorepo (hiện tại) để tăng tốc độ refactor & chia sẻ thư viện giai đoạn nền tảng; sẽ xem xét tách dần khi biên độ thay đổi giảm.
- Dựng CI/CD: Build (Rust, Go, Python), test, security scan (SAST + Dependency), container signing (Cosign).
- Implement: `sensor-gateway` (ingest gói cơ bản TCP/HTTP), `node-runtime` skeleton (plugin WASM sandbox), Observability stack (Prometheus, Grafana, Loki, Tempo, OpenTelemetry collector).
- Kết quả: Agent gửi telemetry và sự kiện thô vào event-bus mock.

### Tháng 2
- Triển khai `swarm-gossip` với QUIC + anti-flood (rate limit + bloom duplicate). 
- `event-bus`: NATS JetStream hoặc Redpanda cluster nhỏ.
- Bắt đầu `identity-ca`: CA root, issue cert x509, chuẩn bị PQC test (Kyber key exchange stub).
- Bootstrap secure join flow cho node (attestation stub, token exchange).
- Thêm test hiệu năng ingest 10K events/s.
- Kết quả: Các node gossip metadata & health; secure join cơ bản hoạt động.

### Tháng 3
- Hoàn thiện pipeline detection giai đoạn 1–2 (signature & baseline anomaly heuristic).
- Hardening gossip (adaptive fanout, backpressure).
- Thiết kế chi tiết PBFT variant + prototyping consensus state machine (mô phỏng 7–25 nodes).
- Bổ sung chaos test cơ bản (network latency injection, node crash).
- Kết quả: POC end-to-end: Ingest → Anomaly heuristic → Alert publish.

### Tháng 4
- Phát triển `consensus-core` (Prepare/Commit flow, view change, BFT safety tests).
- `model-registry` + MinIO + versioning + signature model artifact.
- `threat-intel` service: API nhận IOC, caching, reputation lookup.
- Tích hợp PQC handshake thử nghiệm (Kyber + Dilithium song song TLS 1.3 hybrid suite).
- Kết quả: Alert classification qua consensus demo; model fetch có chữ ký.

### Tháng 5
- `federated-orchestrator`: vòng FL đơn tầng (FedAvg), differential privacy noise stub.
- `policy-service`: CRUD policy, version snapshot, rollout cơ bản.
- `inference-gateway`: ONNX Runtime phục vụ model baseline, quantization thử nghiệm int8.
- Bổ sung benchmark inference (RT < 20ms / request).
- Kết quả: Cập nhật model qua registry → deploy tới inference → agent sử dụng.

### Tháng 6
- Mở rộng PBFT: batching, signature aggregation (BLS hoặc Dilithium aggregate mô phỏng).
- FL nâng cấp: hierarchical aggregation (edge → region → global).
- Edge bootstrap nâng cao: `edge-fleet` tracking phiên bản agent + canary rollout.
- Bổ sung bảo mật: mTLS rotation tự động, revocation list.
- Kết quả: Beta milestone 1: 100–150 nodes lab cluster, detection ≥ 90%.

### Tháng 7
- Khởi động `evolution-core`: GA cho rule tuning, PSO cho hyperparam anomaly model.
- `risk-engine`: scoring model ảnh hưởng tới consensus weighting.
- `control-plane`: orchestrate config distribution event-sourced.
- Bổ sung audit log schema chuẩn (OpenCyber schema internal).
- Kết quả: Policy thay đổi ảnh hưởng tới risk scoring & hành vi chiến lược.

### Tháng 8
- Tối ưu inference: batching micro-batch, edge quantization, cold-start < 100ms.
- Federated learning thêm FedProx & SCAFFOLD hỗ trợ drift.
- Policy engine: AB test rollout, constraint validator.
- `billing-service`: usage metering (events, inference calls, storage) + Kafka/NATS consumer.
- Kết quả: Beta mở rộng 300–400 nodes, Latency < 60ms P95.

### Tháng 9
- `audit-trail`: append-only log WORM + retention encryption.
- Hardening consensus chống DoS (quota, view change adaptive).
- Edge offline sync (delta state replication khi reconnect).
- SRE playbook draft + runbook incident P0/P1.
- Kết quả: 500 nodes mô phỏng multi-region; FL hierarchical ổn định.

### Tháng 10
- Portal `admin-portal`: Dashboard threat, policy, model lifecycle, RBAC (Keycloak integrate).
- Compliance module: export báo cáo (JSON + PDF) chuẩn ISO-like.
- Performance tuning: zero-copy ingest, lock contention profiling.
- Chaos engineering mở rộng (latency, partition, model corruption test).
- Kết quả: Detection ≥ 97%, False Positive ≤ 0.25%.

### Tháng 11
- Tối ưu GA/PSO/ACO pipeline phân tán (Ray cluster autoscale).
- PQC production readiness (hybrid cert issuance pilot).
- Multi-tenant isolation: namespace policies & resource quota.
- Stress test: 1000 nodes synthetic, 50K alerts/minute.
- Kết quả: Detection ≥ 98.5%, FP ≤ 0.15%, HA failover < 5s.

### Tháng 12
- Final hardening: security pen-test remediation, supply chain attestation (SLSA level 2–3).
- Capacity plan GA: cost model tối ưu container packing.
- SLA/SLO publish + error budgets + on-call rotation finalize.
- Launch readiness review (architecture, risk, docs, training).
- Kết quả: GA Candidate đạt các chỉ số Production.

---
## 5. MA TRẬN PHỤ THUỘC (DEPENDENCY MATRIX)
| Service | Phụ thuộc chính | Lý do |
|---------|-----------------|-------|
| consensus-core | identity-ca, event-bus | Chữ ký, truyền broadcast |
| federated-orchestrator | model-registry, inference-gateway, event-bus | Phân phối & thu tham số |
| evolution-core | threat-intel, inference-gateway, policy-service | Fitness feedback |
| control-plane | identity-ca, policy-service, edge-fleet | Phân phối cấu hình an toàn |
| billing-service | event-bus, policy-service | Thu thập usage & mapping tier |
| admin-portal | control-plane, billing-service, audit-trail | Hiển thị & quản trị |
| audit-trail | identity-ca | Chứng thực nguồn gốc sự kiện |
| risk-engine | threat-intel, consensus-core | Điều chỉnh trọng số bỏ phiếu |

---
## 6. CHỈ SỐ (KPIs) THEO PHASE
| KPI | P1 (M1-3) | P2 (M4-6) | P3 (M7-9) | P4 (M10-12) |
|-----|-----------|-----------|-----------|-------------|
| Detection Rate | 70→85% | 85→92% | 92→97% | 97→99% |
| False Positive | <2% | <1% | <0.5% | <0.1% |
| Consensus Latency (25 nodes) | — | ≤ 400ms | ≤ 300ms | ≤ 250ms |
| FL Round Time | — | 15m | 10m | 5-7m |
| Mean Response Time | <150ms | <100ms | <60ms | <10-30ms |
| Availability | 99% | 99.5% | 99.9% | 99.99% |

---
## 7. DEVSECOPS & CHẤT LƯỢNG
- Security shift-left: SAST (CodeQL), Dependency scan (Trivy), IaC scan (Checkov), Container signing (Cosign) + attest (in-toto).
- Branch policy: main protected, PR cần: tests pass, coverage ≥ 80%, security scan clean (no High/Critical).
- Observability SLA: 100% trace sampling trong canary, 10% production; RED + USE metrics chuẩn.
- Chaos schedule: mỗi tháng ít nhất 2 kịch bản.
- Backup & DR: MinIO object store replication multi-region (RPO 15m, RTO 1h target).

### 7.1 Cross-cutting Standards
- API Contracts: tất cả gRPC proto version hoá (semantic: major.minor.patch); backward compatibility kiểm tra tự động.
- Config Management: sử dụng declarative CRD (K8s) + GitOps (ArgoCD); mọi thay đổi phải ký (commit signature + provenance build).
- Secrets: Vault + auto rotation (TLS cert 30 ngày, token 24h); không commit secret, kiểm soát bằng detect-secrets hook.
- Data Governance: phân loại dữ liệu (Public/Confidential/Secret) tag trong metadata store; lineage tracking bằng OpenLineage.
- Authorization: RBAC + ABAC kết hợp; policy viết bằng OPA/Rego versioned trong repo policy.git.
- Rate Limiting: mỗi public API đặt global + per-tenant quota; token bucket + leaky bucket hybird.
- Multi-tenancy: logical isolation namespace; resource quota (CPU/Mem/IO) + network policy (Cilium) enforced.
- Compliance Logging: audit-trail tạo hash chain (Merkle root lưu định kỳ CockroachDB + offsite archive).
- Performance Budgets: ingest pipeline < 30% CPU budget node; consensus thread pool separation.

### 7.2 Quality Gates
- Unit tests ≥ 80% (core), ≥ 70% (peripheral) trước merge.
- Integration test matrix: 3 size cluster (5, 15, 25 nodes) nightly.
- Performance regression: benchmark guardrail ±10% so với baseline tuần trước.
- Security gate: không chấp nhận CVE High/Critical chưa có workaround.
- License compliance: allowlist (Apache2, MIT, BSD, MPL2); tự động fail nếu vi phạm GPLv3 (trừ tooling isolated).

### 7.3 Observability Standards
- Tracing: Bắt buộc span cho boundary (ingress, consensus round, model fetch, policy apply).
- Metrics chuẩn tiền tố: svc_<name>_*; label cardinality kiểm soát (< 50 giá trị/label).
- Logging: JSON structured, 5 level (TRACE, DEBUG, INFO, WARN, ERROR); PII scrubber pipeline.
- Alert Hygiene: không > 5% alert noise (auto-suppress repeating > 20 lần/giờ).

### 7.4 OKR Theo Phase (Tóm tắt)
| Phase | Objective chính | Key Results |
|-------|-----------------|-------------|
| P1 | Nền tảng kỹ thuật vững | 5 core service chạy ổn định, CI/CD < 10m build, Ingest 10K ev/s |
| P2 | Bổ sung đồng thuận & bảo mật | PBFT ≤ 400ms, PQC hybrid demo, FL vòng đầu 50 nodes |
| P3 | Mở rộng thông minh & điều phối | 500 nodes stable, Policy rollout < 5m, Drift detect < 15m |
| P4 | Sẵn sàng sản xuất | FP < 0.1%, HA 99.99%, Chaos suite pass 95% case |

---
## 8. RỦI RO & GIẢM THIỂU
| Rủi ro | Mô tả | Ảnh hưởng | Giảm thiểu |
|--------|-------|-----------|------------|
| Độ trễ consensus cao | PBFT mở rộng | Chậm quyết định | Shard / hierarchical consensus |
| Poisoning FL | Gradient độc hại | Sai lệch model | Robust aggregation (Krum, Trimmed Mean) |
| Edge không ổn định | Mất kết nối | Thiếu dữ liệu | Buffer cục bộ + delta sync |
| FP cao đầu kỳ | Heuristic chưa tinh | Mất niềm tin | Active learning + feedback loop |
| PQC overhead | Chi phí handshake | Tăng latency | Hybrid handshake + session resumption |
| Data drift | Mô hình tụt hiệu năng | Giảm detection | Drift detection + auto retrain trigger |

---
## 9. NHÂN SỰ & TỔ CHỨC
| Vai trò | Số lượng (đỉnh) | Ghi chú |
|---------|------------------|--------|
| Rust Engineer (systems) | 6-8 | Networking, agent, consensus |
| Go Engineer | 4-5 | Control plane, policy, billing |
| ML Engineer | 4-6 | FL, inference, evolution |
| Security Engineer | 3-4 | PKI, hardening, pen-test |
| SRE / Platform | 3-5 | K8s, observability, chaos |
| Frontend / Fullstack | 2-3 | Admin portal |
| QA / Automation | 2-3 | Test harness, perf rigs |
| Product + PM/TPM | 2 | Roadmap & alignment |

---
## 10. EXIT CHECKLIST PRODUCTION (THÁNG 12)
- [ ] Pen-test & remediation hoàn tất
- [ ] Chaos suite pass (partition, 30% node crash, latency spike, model corruption)
- [ ] SLO đạt & error budget policy áp dụng
- [ ] Policy version rollback < 2 phút
- [ ] Model rollback < 5 phút
- [ ] DR drill thành công (failover region trong < 60 phút)
- [ ] Audit trail immutable chứng minh bằng hash chain
- [ ] PQC hybrid certs triển khai ≥ 75% internal traffic
- [ ] Documentation: Architecture, Runbook, Playbook SRE, Security Guidelines

---
## 11. GHI CHÚ THIẾT KẾ KIẾN TRÚC MICROservices
- Chuẩn giao tiếp nội bộ: gRPC + protobuf; external API: GraphQL/REST tuỳ trường hợp.
- Event-driven: ưu tiên publish/subscribe thay vì synchronous chaining.
- Circuit breaker + retry idempotency key trong các call quan trọng.
- Partitioning chiến lược: region → cluster → swarm segment.
- Tách compute vs stateful service; stateful có replication factor ≥ 3.
- Mã hoá: TLS 1.3 + hybrid PQC (Kyber + Dilithium) roadmap.
- Mỗi service có: README, OpenAPI/Proto spec, SLA, dashboard mặc định.

---
## 12. KẾT LUẬN
Lộ trình trên bảo đảm tiếp cận lặp - tăng trưởng, giảm rủi ro thông qua phân tầng tính năng và kiểm soát chất lượng sớm. Sau 12 tháng hệ thống đạt độ trưởng thành kỹ thuật để mở rộng quy mô toàn cầu và thương mại hóa với mức tin cậy cao.

---
Tài liệu này sẽ được cập nhật định kỳ mỗi tháng hoặc khi thay đổi chiến lược quan trọng.
