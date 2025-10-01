## [2025-10-01] Quorum, leader mock, schema hash, event taxonomy, control-plane cache

### Thay đổi
- Thêm quorum + leader election mock (round-robin theo (height+round) % validators) trong `PbftService`.
- Theo dõi phiếu bầu (HashSet per (height,round)) + log quorum_reached.
- Build script `swarm-proto` tính SHA256 toàn bộ `.proto` → export `PROTO_SCHEMA_VERSION` env.
- Sự kiện đổi taxonomy: `consensus.v1.height.changed`, `consensus.v1.round.changed` (versioned prefix + namespace ổn định).
- Payload sự kiện thêm `proto_schema_version`.
- `control-plane` thêm NATS subscribe cache height/round + fallback gRPC fetch ban đầu.
- Thêm integration test (feature `integration`) kiểm tra quorum với 4 validators (3 phiếu đạt quorum).

### Lợi ích
- Tạo nền móng cho logic PBFT thật (quorum & leader rotation) có thể cắm sâu thêm view change.
- Version schema đồng bộ qua env build-time giúp audit & debug mismatch giữa services.
- Event taxonomy chuẩn hoá (namespace + version) hỗ trợ phát triển backward-compatible.
- Control-plane có kênh đẩy thay vì chỉ pull gRPC (giảm latency cập nhật trạng thái).

### Việc tiếp theo (gợi ý)
1. Persist quorum votes ephemeral để phục vụ recovery (in-memory hiện mất khi restart).
2. Thêm round escalation logic (timeout -> round+1 -> re-elect leader).
3. Kết hợp metrics: xuất quorum achievement counter & leader change counter.
4. Tạo subject policy doc chuẩn hóa naming toàn hệ thống events.
5. Thêm integration test multi-height (height progression + multiple quorum cycles).

---
## [2025-10-01] Pbft refactor, OTEL metrics, retries, versioned events

### Thay đổi
- Refactor: tách `PbftService` & `PbftState` sang `consensus-core/src/lib.rs` + thêm snapshot API.
- Thêm unit tests thực (propose tăng height; cast_vote cập nhật round; negative get_state).
- Thay hardcode metrics port bằng env `CONSENSUS_METRICS_PORT` (mặc định 9102).
- Thay prometheus crate bằng OpenTelemetry Prometheus exporter (`/metrics`).
- Control-plane gRPC client thêm exponential backoff (tối đa 5 attempts, delay nhân đôi capped).
- Sự kiện NATS đổi subject `consensus.height.changed.v1` + payload thêm `proto_schema_version`.
- Enrich tracing spans: thêm field proposal.id, vote.proposal_id, query.height.

### Lợi ích
- Dễ test & mở rộng logic PBFT (service di chuyển ra lib).
- Hợp nhất metrics pipeline (chuẩn hóa theo OTEL, tránh dual stack).
- Control-plane khởi động ổn định hơn khi consensus chậm sẵn sàng.
- Versioned events cho phép mở rộng backward compatible.
- Logging giàu ngữ cảnh giúp debug state races.

### Việc tiếp theo (gợi ý)
1. Thêm quorum logic & leader election mock.
2. Expose gauge/counter qua OTEL semantic conventions (naming review).
3. Add gRPC client pooling + health check circuit breaker.
4. Thêm end-to-end integration test: propose -> vote -> state height & round.
5. Proto version embed: derive từ commit hash hoặc buf schema digest.

---
## [2025-10-01] Consensus client, metrics, NATS broadcast, graceful shutdown

### Thay đổi
- `control-plane` bổ sung gRPC client (Go) gọi `GetState` từ `consensus-core` (tạm placeholder proto gen Go – cần chạy `buf generate`).
- `consensus-core` thêm metrics Prometheus (/metrics cổng 9102) với `consensus_height`, `consensus_round`, `consensus_proposals_total`.
- Broadcast NATS topic `consensus.height.changed` (JSON {height, round}) khi height tăng.
- Thêm graceful shutdown (SIGINT/SIGTERM) cho gRPC server + flush tracer qua `shutdown_tracer()`.
- Placeholder test state progression (cần refactor `PbftService` ra lib để test sâu hơn).
- README cập nhật định hướng hợp nhất metrics qua OpenTelemetry Prometheus exporter.

### Lợi ích
- Control-plane có thể quan sát trạng thái consensus ngay từ đầu (khởi tạo orchestration logic sau này).
- Metrics cho phép thiết lập alert / dashboard sớm (height stall, proposal throughput).
- Sự kiện height thay đổi mở đường replication / trigger hành vi khác (ví dụ flush pending votes).
- Đảm bảo dừng dịch vụ an toàn và không gây mất span telemetry.

### Hạn chế / Việc dời lại
- Go proto client đang placeholder: cần chạy `buf generate` để thay thế file giả.
- Chưa có test logic end-to-end propose→vote (phụ thuộc vào mở rộng service logic PBFT thật).
- Metrics hiện không qua OTEL pipeline — sẽ chuyển đổi để thống nhất (tránh dual instrumentation).

### Việc tiếp theo (đề xuất)
1. Refactor `PbftService` sang `lib.rs` để unit test nội bộ real transitions.
2. Thêm `CastVote` path logic cập nhật leader selection & quorum (mock validator set).
3. Tích hợp OpenTelemetry metrics exporter Prometheus cho toàn bộ services.
4. Thêm client retry + backoff cho control-plane khi consensus chưa sẵn sàng.
5. Ghi version proto trong log ở startup (giúp debug mismatch).

---
## [2025-10-01] gRPC Pbft server, integration tests, security extended, config reload

### Thay đổi
- Thêm gRPC Pbft server trong `consensus-core` (tonic) + health riêng cổng `8081`, cổng gRPC cấu hình qua env `CONSENSUS_GRPC_PORT`.
- Cập nhật crate `swarm-proto` export modules bằng `include_proto!` (common, consensus, events, federation) thay thế include thủ công.
- Thêm test tích hợp `startup_integration.rs` (feature `integration`) chạy song song `consensus-core` + `swarm-gossip` kiểm tra `/healthz`.
- Makefile: thêm targets `security-cargo-audit`, `security-govulncheck`, `security-pip-audit` và meta-target `security`.
- Workflow mới: `.github/workflows/security-extended.yml` (cron hằng ngày) chạy audit Rust / Go / Python.
- Script bootstrap: `scripts/bootstrap-pre-commit.sh` cài & chạy pre-commit hooks tự động.
- Script `scripts/fix-license.sh` chèn header Apache 2.0 nếu thiếu (idempotent) cho `.rs .go .py .sh`.
- Nâng cấp `swarm-core` hỗ trợ cache config với TTL (`SWARM_CONFIG_TTL_SECS`, mặc định 30s), reload file tự động (notify watcher), hàm `force_reload`.

### Lợi ích
- Nền tảng consensus đã có endpoint gRPC tối giản → sẵn sàng cấy logic PBFT thực.
- Tăng độ tin cậy CI qua test khởi động đồng thời nhiều service.
- Khuếch trương phạm vi bảo mật phụ thuộc (đa ngôn ngữ) dưới dạng workflow định kỳ.
- Giảm ma sát onboarding dev (một lệnh kích hoạt pre-commit).
- License compliance tự động hóa giảm noise review.
- Config động có cache & reload giảm áp lực HTTP fetch loop và hỗ trợ thay đổi nóng.

### Việc tiếp theo (đề xuất)
1. Thêm client gRPC trong các service cần query trạng thái consensus.
2. Bổ sung metrics (Prometheus exporter) cho consensus vòng/leader.
3. Thêm test validate propose/cast_vote flow + state progression.
4. Thêm broadcast kênh sự kiện (NATS / gossip) khi height thay đổi.
5. Triển khai graceful shutdown cho server (listen SIGTERM) + flush tracer.

---

## [2025-10-01] Bổ sung proto codegen, telemetry, health, NATS stub, security CI

## [2025-10-01] Thêm dev-up/dev-down & crate proto Rust
## [2025-10-01] Integration test NATS sensor-gateway
## [2025-10-01] License, pre-commit & dynamic config
## [2025-10-01] Dockerfiles đồng bộ & SBOM script

### Thay đổi
- Thêm Dockerfile cho toàn bộ services còn thiếu (Rust, Go, Python) theo mẫu multi-stage → distroless/nonroot.
- Thêm script `scripts/syft-sbom.sh` tạo SBOM (JSON) bằng Syft.
- Makefile thêm target `sbom` (placeholder dùng script).

### Lợi ích
- Chuẩn hóa build container → thuận lợi cho scan bảo mật, runtime tối giản.
- Tạo nền tảng supply-chain (SBOM) sớm.

## [2025-10-01] Proto crate sửa & enable gRPC server

### Thay đổi
- Sửa lỗi dependency `tonic` (typo) + thêm `walkdir`.
- `build.rs` bật build server stub cho toàn bộ proto (tạm thời) – có Pbft service.

### Lợi ích
- Sẵn sàng tích hợp gRPC server cho `consensus-core`.

---

### Thay đổi
- Thêm `LICENSE` (Apache-2.0 scaffold) & `.github/CODEOWNERS`.
- Script `scripts/check-license.sh` + Make target `license-check`.
- Thêm `.pre-commit-config.yaml` (black, ruff, cargo fmt/clippy, license check).
- Dynamic config loader trong `swarm-core`: ưu tiên (ENV > file YAML (SWARM_CONFIG_FILE) > HTTP fetch (SWARM_CONFIG_HTTP) > default).
- Thêm module `DynamicConfig` + hàm `load_config` trả về cấu trúc hợp nhất.

### Lợi ích
- Chuẩn hóa baseline tuân thủ giấy phép & trách nhiệm code.
- Tự động hóa chất lượng commit (format/lint/license) sớm.
- Cho phép triển khai config linh hoạt (kết nối remote control plane sau này).

### Việc tiếp theo (gợi ý)
- Thêm cache & TTL cho HTTP config.
- Bổ sung validation schema (serde + custom validator).
- Tích hợp config reload (SIGHUP hoặc kênh broadcast).

---

### Thay đổi
- Thêm feature `integration` trong `sensor-gateway/Cargo.toml`.
- Thêm test `tests/integration_nats.rs` kiểm tra publish NATS (skip mềm nếu không có server).

### Lợi ích
- Cho phép chạy `cargo test --features integration` để xác thực kết nối hạ tầng local.
- Giảm false negative trên CI không có NATS.

### Việc tiếp theo
- Thêm macro skip_if_no_service() tái sử dụng.
- Mở rộng test cho swarm-gossip.

---

### Thay đổi
- Makefile: thêm target `dev-up` / `dev-down` (wrapper docker compose).
- Tạo crate `libs/rust/proto` (prost + tonic) + build.rs tự động dò toàn bộ `.proto`.
- Chuẩn bị nền tảng cho tích hợp gRPC client (server build=false tạm thời).

### Lợi ích
- Nâng tốc độ khởi động môi trường dev một lệnh.
- Chuẩn hóa đường build proto Rust để tái dùng trong services khác.

### Việc tiếp theo (liên quan proto)
- Thêm feature build server cho những service cung cấp gRPC.
- Mapping include!(...) động theo file (cần script gen mod list) – deferred.

---

### Tóm tắt
Hoàn thiện bước ưu tiên cao: tự động sinh proto bằng buf, chuẩn hóa telemetry OpenTelemetry, health endpoint thống nhất, kết nối NATS stub, skeleton test đa ngôn ngữ, môi trường phát triển docker-compose và workflow bảo mật.

### Thay đổi chi tiết
- Thêm `buf.yaml`, `buf.gen.yaml`, script `scripts/generate-proto.sh` + cập nhật target `proto` trong `Makefile`.
- Mở rộng `swarm-core` với OpenTelemetry (OTLP exporter) + health server (axum) + hàm `start_health_server`.
- Cập nhật `sensor-gateway` & `swarm-gossip` dùng `swarm-core`, thêm health trên cổng 8080/8081.
- Thêm NATS stub (async-nats) publish sự kiện bootstrap.
- Thêm test skeleton: Rust (`libs/rust/core/tests/basic.rs`), Go (`policy-service/main_test.go`), Python (`model-registry/tests/test_health.py`).
- Thêm `infra/docker-compose.dev.yml` (NATS, MinIO, Postgres, OTEL collector) + `otel-config.yaml`.
- Thêm workflow bảo mật: `codeql.yml`, `trivy.yml`.
- Cập nhật phụ thuộc Rust (axum, otel) và bổ sung dependency async-nats vào hai service.

### Lợi ích
- Chuẩn hóa nền tảng quan sát & bảo mật sớm.
- Giảm lặp code tracing và health check giữa services.
- Tạo tiền đề mở rộng event-driven (NATS JetStream sau này).

### Việc tiếp theo (đề xuất)
1. Dockerfile chuẩn cho mỗi service (multi-stage + non-root + SBOM).
2. Thêm script launch dev cluster (make dev-up / dev-down).
3. Bổ sung proto codegen cho Rust & gRPC server stub.
4. Thêm integration test mini (spin up nats + 2 service).
5. Thêm license header & code owners.

---
## [2025-10-01] Khởi tạo cấu trúc dự án & scaffold microservices

### Tóm tắt
Thiết lập nền tảng ban đầu cho Swarm Intelligence Network theo kiến trúc microservices đa ngôn ngữ (Rust / Go / Python) nhằm chuẩn bị thực thi Phase 1 (Tháng 1–3) trong roadmap.

### Các thay đổi chính
- Tạo cấu trúc thư mục chuẩn: `services/`, `libs/`, `proto/`, `infra/`, `.github/workflows/`.
- Scaffold 16 services:
	- Rust: `sensor-gateway`, `node-runtime`, `swarm-gossip`, `consensus-core`, `identity-ca`, `inference-gateway`, `risk-engine`, `edge-fleet`.
	- Go: `policy-service`, `control-plane`, `billing-service`, `audit-trail`, `threat-intel`.
	- Python: `model-registry`, `federated-orchestrator`, `evolution-core`.
- Thêm thư viện chung: `libs/rust/core` (init tracing); placeholder README cho Go/Python core libs.
- Khởi tạo proto definitions:
	- `common/health.proto`
	- `consensus/pbft.proto`
	- `events/security_event.proto`
	- `federation/federated_round.proto`
- Thêm CI workflow (`.github/workflows/ci.yml`) build đa ngôn ngữ cơ bản.
- Thêm `Makefile` điều phối build (placeholder cho proto & security).
- Cập nhật `README.md` mô tả kiến trúc, cấu trúc, nguyên tắc & kế hoạch.
- Hoàn thiện lộ trình 12 tháng trong `roadmap-12-months.md` + bổ sung cross-cutting standards.
- Tạo `.gitignore`, `.editorconfig` chuẩn dùng chung.

### Lý do / Mục tiêu
- Chuẩn hóa cơ sở để tránh nợ kỹ thuật giai đoạn sau.
- Cho phép nhóm bắt đầu implement logic nghiệp vụ mà không phải tranh luận lại cấu trúc.
- Tạo nền tảng để tích hợp tiếp: proto codegen, observability, bảo mật chuỗi cung ứng.

### Việc tiếp theo (đề xuất)
1. Thêm script generate proto (buf + protoc) và cập nhật Makefile target `proto`.
2. Thêm OpenTelemetry tracing init vào từng service (tránh lặp code bằng shared lib).
3. Viết test skeleton (Rust/Go/Python) + tích hợp vào CI.
4. Thêm Dockerfile chuẩn (labels, non-root user) mỗi service.
5. Thiết lập môi trường local (docker-compose: NATS + MinIO + Postgres).
6. Bổ sung CodeQL + Trivy workflow bảo mật.
7. Chuẩn hóa health endpoint (HTTP + gRPC) dùng chung schema.

### Ghi chú
- Một số dependency & feature (PQC, WASM plugin, inference ONNX) mới ở mức placeholder → sẽ triển khai dần theo roadmap.
- Chưa tạo auto codegen proto: tránh noise commit trước khi thống nhất spec.

---
