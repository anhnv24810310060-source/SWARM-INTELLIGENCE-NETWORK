# SWARM INTELLIGENCE NETWORK

Nền tảng bảo mật phân tán mô phỏng hệ miễn dịch số, kiến trúc microservices đa ngôn ngữ (Rust / Go / Python) với cơ chế học liên kết (federated learning), đồng thuận PBFT tùy biến và tiến hóa tự động (evolutionary optimization).

## 1. Mục tiêu
- Phát hiện & phản ứng mối đe dọa thời gian gần thực.
- Tự cải thiện mô hình phát hiện thông qua federated & evolution engine.
- Khả năng mở rộng từ hàng trăm tới hàng nghìn node edge.

## 2. Cấu trúc thư mục
```
proto/                # Định nghĩa protobuf (consensus, events, federation, common)
services/             # Từng microservice chuyên biệt
	sensor-gateway/     # Ingestion (Rust)
	node-runtime/       # Edge agent runtime (Rust)
	swarm-gossip/       # Gossip QUIC layer (Rust)
	consensus-core/     # PBFT variant (Rust)
	identity-ca/        # PKI & hybrid PQC (Rust)
	inference-gateway/  # Model inference (Rust)
	risk-engine/        # Risk scoring (Rust)
	edge-fleet/         # Edge device orchestrator (Rust)
	policy-service/     # Policy CRUD & rollout (Go)
	control-plane/      # Global config orchestration (Go)
	billing-service/    # Usage & billing (Go)
	audit-trail/        # Immutable audit log (Go)
	threat-intel/       # Threat intelligence aggregator (Go)
	model-registry/     # Model artifact & versioning (Python)
	federated-orchestrator/ # FL coordinator (Python)
	evolution-core/     # Evolutionary optimization (Python)
libs/
	rust/core/          # Thư viện chung Rust
	go/core/            # Thư viện chung Go (placeholder)
	python/core/        # Helper Python chung
infra/                # Hạ tầng IaC / manifests (tương lai)
.github/workflows/    # CI pipelines
Makefile              # Build orchestration đa ngôn ngữ
roadmap-12-months.md  # Lộ trình phát triển 12 tháng
```

## 3. Nguyên tắc kiến trúc
- Stateless ưu tiên, stateful có replication ≥3.
- gRPC nội bộ; event-driven dùng NATS JetStream / Redpanda.
- Tracing & metrics mặc định (OpenTelemetry).
- Policy & config distribution thông qua control-plane + GitOps.

## 4. Quy ước code
| Ngôn ngữ | Style | Tool đề xuất |
|----------|-------|--------------|
| Rust | Clippy + rustfmt | cargo clippy / fmt |
| Go | Go fmt / vet | golangci-lint (tương lai) |
| Python | Ruff + Black | ruff / black |

## 5. Build
Yêu cầu: Rust stable, Go 1.22, Python 3.11.

Lệnh tổng quát:
```
make all        # Build proto (placeholder) + services
make rust
make go
make python
```

## 6. Testing (dự kiến)
- Unit test: mỗi service directory.
- Integration: test cluster PBFT giả lập (consensus-core + swarm-gossip).
- Performance: benchmark ingest & inference.

## 7. Proto Generation (kế hoạch)
Sinh mã đặt tại `proto/gen/<lang>/...` thông qua script (chưa tích hợp).

## 8. Bảo mật
- Container signing (Cosign) & SBOM scan (Trivy) sẽ tích hợp vào CI giai đoạn sau.
- Secret quản lý qua Vault (kế hoạch infra).

## 9. Roadmap
Xem `roadmap-12-months.md` để biết chi tiết milestone theo tháng.

## 10. Đóng góp
1. Fork / branch feature
2. Viết test hoặc cập nhật test nếu thay đổi hành vi
3. PR với mô tả rõ ràng + liên kết issue / roadmap item

## 11. Tương lai gần
- Thêm script generate proto.
- Thêm test harness cluster mini.
- Triển khai config dynamic + policies.
- Hợp nhất metrics qua OpenTelemetry -> Prometheus exporter (hiện `consensus-core` tạm dùng prometheus crate + /metrics cổng 9102; sẽ chuyển sang otel-prometheus để thống nhất pipeline.)

---
Tài liệu sẽ tiếp tục được mở rộng cùng tiến trình dự án.
