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
