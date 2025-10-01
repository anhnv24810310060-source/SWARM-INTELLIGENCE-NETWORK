## 2025-10-01 (ALERTMANAGER INTEGRATION)

### [21:35] 📢 Hoàn thiện incident response infrastructure
- Alertmanager config `infra/alertmanager.yml`: routing rules phân tầng → critical/oncall=page → PagerDuty, warning → Slack; inhibit rules tránh alert storm; group_by alertname+severity+component giảm noise.
- Docker compose: thêm alertmanager service (port 9093), mount config + alert-rules; env vars SLACK_WEBHOOK_URL & PAGERDUTY_SERVICE_KEY cho receiver configs.
- Prometheus config: thêm alerting section với alertmanager target; kết nối alert pipeline end-to-end.
- Runbook `docs/runbooks/critical-severity-surge.md`: chi tiết diagnosis decision tree, immediate actions (verify/identify/mitigate), recovery verification steps, escalation path, post-incident procedures.

Lợi ích: Production-ready alert routing, giảm oncall fatigue (grouping + inhibit), runbook automation-ready (webhook triggers), clear escalation hierarchy.

Next (Phase 1 completion):
1. E2E latency profiling: identify bottlenecks (regex compile, NATS publish) → optimize to p95 <500ms.
2. PKI core skeleton: identity-ca service scaffold, root cert generation, CSR signing endpoint.
3. Performance baseline: run benchmark suite, establish KPI thresholds (10K ev/s, weighted F1 ≥0.90).
4. Phase 1 exit validation: checklist review against roadmap exit criteria.

---
## 2025-10-01 (SEVERITY-WEIGHTED SCORING & ALERT RULES)

### [21:20] ⚖️ Thêm severity-weighted F1 & alert rules nâng cao
- Script `calc_detection_quality.py`: thêm flag `--weighted` tính F1 có trọng số theo severity (critical:3.0, high:2.0, medium:1.0, low:0.5); load severity từ detection log; output weighted_precision/recall/f1 trong JSON + CSV.
- Workflow `detection-quality.yml`: bật `--weighted` flag; delta computation hỗ trợ cả weighted & non-weighted CSV formats; PR comment hiển thị Weighted F1 + delta.
- Alert rules `infra/alert-rules.yml`: thêm 2 rules mới severity-based:
  - `CriticalSeveritySurge`: critical rate >10/s → severity=critical, oncall=page (PagerDuty integration ready).
  - `HighSeveritySpike`: high rate >2× baseline 1h ago → severity=warning (Slack notification).

Lợi ích: Weighted F1 phản ánh business impact chính xác hơn (critical detections quan trọng gấp 3× low); alert rules phân tầng theo urgency cho incident response hiệu quả; chuẩn bị infrastructure cho Phase 1 exit KPI (Weighted F1 ≥0.90).

Next (gợi ý):
1. Alertmanager integration: route critical alerts → PagerDuty, warnings → Slack webhook.
2. Runbook automation: critical alerts trigger auto-diagnostics script (capture logs, metrics snapshot).
3. Severity-specific quality gates: fail PR nếu weighted_f1 <0.85 (stricter cho production deployments).
4. FP root cause analysis: log FP payloads riêng, weekly clustering + auto-suggest rule fixes.
5. Adaptive threshold tuning: offline grid search tìm anomaly thresholds tối ưu per severity tier.

---
## 2025-10-01 (QUALITY GATE & SEVERITY TRACKING)

### [21:05] 🚦 Thêm quality gate soft enforcement & severity metrics
- Workflow `detection-quality.yml`: thêm step `Quality gate check` tính toán và cảnh báo (không fail) nếu Precision <0.85 hoặc ΔF1 <-5%; kết quả hiển thị trong PR comment với section "Quality Gate Warnings".
- Sensor-gateway: thêm 4 counters severity-specific (`swarm_detection_critical_total`, `high`, `medium`, `low`) để phân tích chi tiết phân bố mức độ nghiêm trọng.
- Dashboard `detection_metrics.json`: thêm 2 panels mới — pie chart severity distribution & timeseries severity rate — cho phép operators ưu tiên tuning rules theo impact.

Lợi ích: Early warning hệ thống cho regression mà không block workflow; visibility sâu hơn về threat landscape qua severity breakdown.

Next (gợi ý):
1. Alert rules dựa trên severity: `critical_rate > 10/min` trigger PagerDuty, `high_rate surge >2× baseline` trigger Slack.
2. Severity-weighted F1: tính F1 riêng cho critical/high detections (quan trọng hơn low/info).
3. Auto-tune anomaly thresholds: offline analysis tìm threshold tối ưu cho từng severity tier.
4. Weekly rollup script: aggregate severity stats & quality metrics, commit summary markdown.

---
## 2025-10-01 (DETECTION QUALITY REFINEMENT)

### [20:50] 🔐 Nâng cao độ chính xác đo chất lượng detection
- Detection engine: thêm trường `payload_hash` (SHA-256) vào mỗi `DetectionEvent` → loại bỏ false positive/negative do cắt chuỗi preview.
- Script `calc_detection_quality.py`: ưu tiên so khớp `payload_hash` chính xác, fallback preview cho backward compat; tính toán hash cho labeled dataset.
- Workflow `detection-quality.yml`: tự động tính delta Precision/Recall/F1 so với run trước; PR comment hiển thị kết quả + delta với emoji indicator (✅/⚠️).

Lợi ích: Giảm nhiễu đánh giá (hash canonical), tăng khả năng phát hiện regression qua delta tracking, review dễ dàng hơn trong PR context.

Next (đề xuất):
1. Thêm quality gate nhẹ: cảnh báo (không fail) nếu F1 giảm >5% hoặc Precision <0.85 trong PR.
2. Tích hợp sparkline cho F1 trend (reuse `generate_perf_sparkline.sh`), nhúng vào README badge.
3. Weekly rollup: tổng hợp median precision/recall/F1 tuần → giảm kích thước CSV, highlight long-term trend.
4. Thêm severity breakdown metrics (critical/high/medium) cho phân tích chi tiết hơn.

---
## 2025-10-01 (DETECTION QUALITY AUTOMATION)

### [20:35] 📄 Tự động hoá xu hướng chất lượng detection
- Script `calc_detection_quality.py`: thêm tuỳ chọn `--csv` ghi dòng số liệu (timestamp UTC, tp/fp/fn, precision, recall, f1) vào `docs/detection-quality.csv`.
- Thêm file `docs/detection-quality.csv` (header) để theo dõi trend qua thời gian.
- Workflow mới `detection-quality.yml`: chạy hàng ngày & on-change → sinh dữ liệu synthetic, chạy sensor-gateway (ghi `detections.log`), tính chất lượng, append CSV, artifact + (nếu main) commit cập nhật.
- Synthetic generator: thêm flags `--include-marker/--no-marker` bật/tắt prefix `MALICIOUS` cho thử nghiệm nhạy cảm marker.

Lợi ích: Tạo vòng lặp tự động hoá Precision/Recall/F1 → phát hiện regression sớm, cung cấp baseline tiến tới đặt ngưỡng quality gate (ví dụ: Precision ≥0.9, Recall ≥0.85) trong PR.

Next (gợi ý):
1. Thêm badge hiển thị F1 mới nhất (GitHub Actions + shields.io endpoint).
2. PR comment cải thiện: trích riêng precision/recall & delta so với commit trước.
3. Gate optional: cảnh báo (không fail) nếu F1 giảm >5% so với median 7 ngày.
4. Bổ sung hash stable cho payload (thay match prefix) để giảm noise FP/FN.
5. Kết hợp biểu đồ sparkline (update script `generate_perf_sparkline.sh`) cho detection quality bên cạnh perf.

---
## 2025-10-01 (OBSERVABILITY & QUALITY TOOLING)

### [20:20] 📊 Nâng cao đo lường detection & consensus
- Sensor-gateway: thêm hỗ trợ `DETECTION_LOG_PATH` ghi JSON alert → script mới `scripts/calc_detection_quality.py` tính precision/recall/f1 từ log + tập labeled.
- Consensus-core: metric `consensus_round_progress_ms` (histogram) đo thời gian propose→quorum; tái sử dụng view change metrics.
- Dashboard `ingest_consensus_overview.json`: thêm View Change Rate + Round Progress p95.
- Prometheus: bật load `alert-rules.yml`; thêm cảnh báo FP ratio >2%, detection rate <85%, view changes nhiều, round progress p95 >600ms.

Lợi ích: Đóng vòng phản hồi chất lượng detection và phát hiện sớm bất ổn consensus / latency.

Next (gợi ý): chuẩn hoá output chất lượng (CSV trend), thêm SLO burn alerts, gom nhóm alert label root_cause hint.

---
## 2025-10-01 (DETECTION GROUND TRUTH METRICS)

### [20:05] 🎯 Thêm hệ thống metric TP/FP/FN
- Sensor-gateway: phân loại ground truth qua token `MALICIOUS` → counters: `swarm_detection_true_positives_total`, `swarm_detection_false_positives_total`, `swarm_detection_false_negatives_total` + gauges `swarm_detection_false_positive_ratio`, `swarm_detection_detection_rate` (approx).
- Rules: thêm `R003` bắt token `MALICIOUS` (severity=critical).
- Synthetic generator: thêm prefix `MALICIOUS` vào payload malicious để đồng bộ ground truth.
- Dashboard cập nhật: panel TP/FP/FN rate + FP ratio & detection rate gauges.

Lợi ích: Tạo nền tảng đo chất lượng detection (precision/recall proxy) sớm, phục vụ guardrail regression.

Next (gợi ý): Chuẩn hoá output bench -> JSON + script tính precision/recall chính xác (dựa trên labeled stream), alert nếu FP ratio > 0.02.

---
## 2025-10-01 (INCREMENTAL – METRICS & FLAGS)

### [19:55] 📈 Bổ sung mở rộng quan sát & kiểm soát
- Consensus: thêm metrics `consensus_view_changes_total`, histogram `consensus_view_change_interval_ms` + cờ `CONSENSUS_VIEW_CHANGE_ENABLED` (mặc định bật) trong `view_change.rs`.
- Detection: thêm env `DETECTION_ENABLED` để bật/tắt engine; stub gauge `swarm_detection_false_positive_ratio` (hiện =0 – sẽ cập nhật khi có nhãn FP thực).
- Nightly perf: tích hợp benchmark `detection_overhead` vào workflow `perf-nightly.yml` + artifact `detection-bench`.

Lợi ích: Cho phép so sánh chi phí detection vs baseline, theo dõi tần suất & khoảng cách view change, và tạm thời vô hiệu hoá detection khi cần phân tích hiệu năng.

Next (đề xuất): tính FP ratio thật (compare labeled synthetic), thêm alert rule view change surge, chuẩn hoá bench output thành JSON parse-able.

---
## 2025-10-01 (EXECUTION – NEXT STEPS IMPLEMENTATION)

### [19:40] ⚙️ Triển khai các mục 3.1.1
Hoàn tất 5 hạng mục khởi tạo theo bảng NEXT STEPS:
- P0 Integration test detection + NATS: thêm `configs/detection-rules.yaml`, script `tests/e2e/detection_alert_flow.sh`, workflow `e2e-detection.yml`.
- P0 Benchmark overhead detection: thêm Criterion bench `benches/detection_overhead.rs` + dev-deps.
- P1 Detection metrics & dashboard: bổ sung counters `swarm_detection_signature_total`, `swarm_detection_anomaly_total` + dashboard `infra/dashboards/detection_metrics.json`.
- P1 View change timeout skeleton: module `consensus-core/src/view_change.rs` + spawn task & ENV `CONSENSUS_ROUND_TIMEOUT_MS` (mặc định 3000ms).
- P2 Chaos weekly workflow (dry-run): thêm `.github/workflows/chaos-weekly.yml`.

Kết quả chính: Alert flow E2E harness sẵn sàng CI, nền benchmark so sánh overhead, metrics detection xuất ra Prometheus → có panel, consensus chuẩn bị logic view change, khung chaos an toàn (dry-run) để mở rộng tuần sau.

Việc tiếp theo (gợi ý):
1. Thêm FP ratio metric (TP/FP counters) & alert rule.
2. Ghi nhận round change latency metric (histogram) trong consensus-core.
3. Bổ sung test view change deterministic (mock time) để tránh flaky.
4. Tích hợp bench vào nightly perf workflow (cột detection_enabled).
5. Nâng chaos từ dry-run → selective real (self-host runner) sau 2 tuần ổn định.

---
## 2025-10-01 (NEXT STEPS AUGMENTATION)

### [19:05] 🔁 Bổ sung 3.1.1 NEXT STEPS (Incremental Additions)
Đã cập nhật `roadmap-12-months.md` (mục 3.1.1) thêm bảng nhiệm vụ gia tăng nhằm khoá chặt Exit Criteria Phase 1 và chuẩn bị nền tảng Phase 2.

| Priority | Task | Purpose | Scope chính | Exit / Success | Ghi chú triển khai |
|----------|------|---------|------------|----------------|--------------------|
| P0 | Integration test detection + NATS | Xác thực alert end-to-end | Compose tối thiểu, inject payload, assert subject `threat.v1.alert.detected` | CI test pass (<150ms alert latency P95) | Thêm retry subscribe tránh flake |
| P0 | Benchmark overhead detection (trước/sau) | Kiểm soát regression hiệu năng | Benchmark pipeline on/off 10K ev/s | Overhead CPU ≤ +15%, throughput ≥10K ev/s | Thêm cột detection_enabled vào perf-trend.csv |
| P1 | Detection metrics + dashboard | Quan sát & cảnh báo | Counters + FP ratio panel + alert rule | Dashboard + alert rule active | Tên chuẩn `swarm_detection_*` |
| P1 | View change timeout + test | Resilience leader failure | Timer + simulate leader crash test | View change <2× timeout; test PASS | Env config timeout dễ chỉnh |
| P2 | Chaos workflow (weekly) | Baseline MTTR & resilience | GH workflow chạy scripts (dry-run/real) | Artifact log, không fail pipeline | Giai đoạn 1: dry-run mode |

Rationale tóm tắt:
- P0 trực tiếp unblock Detection Phase 1 KPI.
- P1 cung cấp quan sát & an toàn đồng thuận sau persistence.
- P2 tạo dữ liệu sớm cho đường cong cải thiện MTTR & reliability.

Risks & Mitigation:
- Flaky NATS test → thêm backoff & timeout rõ ràng.
- Benchmark noise → 2-pass warmup, ghép median.
- Timer drift view change → cấu hình qua ENV + test deterministic.
- Chaos permission hạn chế CI → dry-run trước, real run self-host runner sau.

Artifacts sẽ bổ sung:
- `tests/e2e/detection_alert_flow.sh` hoặc tương đương Rust test harness.
- `benches/detection_overhead.rs` + cập nhật script baseline.
- `grafana-provisioning/dashboards/detection.json`.
- `consensus-core/src/view_change.rs` (dự kiến) + unit tests.
- `.github/workflows/chaos-weekly.yml`.

---
## 2025-10-01  (UPDATE PRIORITY SUMMARY)

### [18:30] 🚀 Thêm 5 Hành Động Ưu Tiên Vào Roadmap (Section 3.1)
Đã cập nhật `roadmap-12-months.md` với mục 3.1 mô tả chi tiết 5 ưu tiên trọng yếu để hoàn tất Phase 1.

| Mã | Hành động | Deadline | Owner | Exit Criteria chính |
|----|-----------|----------|-------|---------------------|
| P0-1 | Detection Pipeline | 08/10 | Rust | ≥85% detection, FP <2%, 10K ev/s, alert integration test pass |
| P0-2 | E2E Integration Test | 11/10 | QA/Platform | ≥95% pass, P95 <500ms, trace continuity |
| P0-3 | Identity/PKI Core | 15/10 | Security | 1000 certs/min, join <2s, mTLS + CRL functional |
| P1-4 | Consensus Hardening | 15/10 | Consensus | View change <2× timeout, no stall f=1, ≤300ms P95 |
| P1-5 | Chaos Testing Framework | 18/10 | SRE | ≥8/10 scenarios pass, MTTR <30s, no cascading failure |

Tình trạng: Tất cả ở trạng thái Pending (khởi tạo).  
Rủi ro chính: Detection & PKI là blocker cho Phase 2 readiness nếu trễ.

Next Steps (tuần 1):
- Bắt đầu implement detection engine (rule loader + anomaly windows)
- Chuẩn bị test harness E2E (compose + synthetic payload injector)
- Thiết kế CSR & issuance flow cho identity-ca
- Xác định schema lưu vote persistence (RocksDB key design)
- Soạn kịch bản chaos (network + process crash)

Artifacts cần tạo mới:
- `sensor-gateway/src/detection/` (mod signatures.rs, anomaly.rs, engine.rs)
- `tests/e2e/threat_flow.sh` + GH workflow `e2e-test.yml`
- `services/identity-ca/src/ca/` (root loader, signer, csr handler)
- `consensus-core` persistence layer (votes.db wrapper)
- `scripts/chaos/` (network_faults.sh, node_kill.sh, resource_stress.sh)

Metrics bổ sung dự kiến:
- Detection: `swarm_detection_signature_total`, `swarm_detection_anomaly_total`
- Consensus: `consensus_round_duration_ms`, `consensus_view_changes_total`
- Chaos: synthetic gauge `chaos_scenarios_passed`

Phase 1 Exit Review: 29/10/2025  
Phase 2 Kickoff: 01/11/2025

---
## 2025-10-01

### Tính năng & Tự động hoá mới

- Truy vết liên ngôn ngữ: Thêm stub `libs/python/core/nats_context.py` để chuẩn bị inject/extract traceparent cho NATS trong Python (tương tự Go `natsctx`).
- Hiệu năng: Mở rộng script `update_perf_baseline.sh` để sinh thêm `docs/perf-trend.csv` (dạng long: timestamp, benchmark, min, mid, max, unit) hỗ trợ theo dõi xu hướng.
- Nightly Benchmark Workflow: Thêm workflow `nightly-perf` (02:00 UTC) tự động chạy benchmark và commit thay đổi vào `perf-baseline.md` & `perf-trend.csv`.
- JetStream Guard Rails: Thêm file chuẩn `infra/jetstream-spec.yaml` + script `validate_jetstream.sh` so sánh cấu hình live (storage, replicas) với spec, chỉ cảnh báo (non-fatal).
- CI JetStream Validation: Workflow `jetstream-validate` (push + 02:30 UTC) khởi chạy NATS, provision streams, sau đó chạy validation để phát hiện drift sớm.

### Lợi ích

- Minh bạch hiệu năng theo thời gian (trend CSV) giúp phát hiện regression nhanh hơn.
- Chuẩn hoá cấu hình JetStream và cảnh báo drift trước khi ảnh hưởng sản xuất.
- Chuẩn bị cho propagation trace NATS bên Python mà không gây thêm phụ thuộc ngay lập tức.
- Tự động hoá giảm thao tác thủ công, tiết kiệm thời gian review.

### Ghi chú

- `validate_jetstream.sh` luôn trả mã thoát 0 để không phá vỡ pipeline; drift thể hiện qua dòng WARN.
- Cần `jq` + `nats` CLI cho validation; workflow đã cài tự động.
- Có thể mở rộng so khớp thêm các trường (max_age, subjects) khi cần độ nghiêm ngặt cao hơn.

### Bổ sung sau (Perf & Validation nâng cao)

- Biến môi trường `PERF_REGRESSION_PCT` (mặc định 10%, nightly set 12%) cho phép điều chỉnh ngưỡng cảnh báo regression midpoint.
- Artifact JSON `target/perf-regressions.json` lưu chi tiết (benchmark, current, median_7d, pct_change, threshold_pct) để annotate PR / dashboard.
- Sparkline SVG tự động (`docs/perf-sparkline.svg` + per-benchmark) tạo bởi script `scripts/generate_perf_sparkline.sh` (gnuplot) -> nhúng vào README.
- Workflow nightly cài `gnuplot`, commit badge SVG nếu thay đổi; upload artifact regressions.
- Nâng cấp `validate_jetstream.sh` hỗ trợ parser `yq` (nếu có) dùng khi spec mở rộng (retention/consumers sau này) – fallback shell parser vẫn hoạt động.
	- Mở rộng thêm kiểm tra: retention, discard, dupe_window.

### Lợi ích mở rộng
- Trực quan hoá xu hướng hiệu năng ngay trong repo (badge) không cần mở công cụ ngoài.
- Regression cảnh báo có cấu trúc giúp tự động hoá review (CI comment bot).
- Parser YAML giàu ngữ nghĩa giảm rủi ro bỏ sót trường khi spec phức tạp.
	- Phát hiện drift chi tiết hơn (retention/discard/dupe) bảo đảm tính bền vững stream.

### Bổ sung mới (PR & phân tích sâu)
- Workflow `perf-regression-pr` tự động comment lên PR nếu có regression (bảng markdown).
- Sparkline bổ sung tập trung nhóm encode_1KB (`perf-sparkline-encode_1KB.svg`).
- Cắt gọn `perf-trend.csv` giữ ≤ 6 tháng giúp repo nhẹ, tránh phình dữ liệu lâu dài.
- JetStream validation thêm so khớp retention/discard/dupe_window tăng độ nghiêm ngặt.

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

### Perf Baseline (sensor-gateway encode)
- Thêm benchmark Criterion (`raw_event_encode_256B`, `raw_event_encode_1KB`, `raw_event_encode_batch_100`).
- Histogram mới: `swarm_ingest_encode_latency_ms`, `swarm_ingest_payload_bytes` giúp theo dõi regression.
- Tài liệu baseline ban đầu: `docs/perf-baseline.md` (sẽ cập nhật số liệu thực ở lần chạy đầu trên môi trường ổn định).

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
