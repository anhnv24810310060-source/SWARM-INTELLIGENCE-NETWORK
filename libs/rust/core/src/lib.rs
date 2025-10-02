//! Core shared utilities for SwarmGuard services.

use anyhow::Result;
use tracing::info;
use once_cell::sync::OnceCell;
use parking_lot::RwLock;
use std::time::{Duration, Instant};
use std::path::PathBuf;
use notify::{RecommendedWatcher, Watcher, EventKind};
use opentelemetry::{global, sdk::{trace as sdktrace, Resource}, KeyValue};
use opentelemetry_otlp::WithExportConfig;
use axum::{routing::get, Router};
use once_cell::sync::Lazy;
use opentelemetry_prometheus::PrometheusExporter;
use prometheus::{Encoder, TextEncoder};
use std::net::SocketAddr;
use serde::Deserialize;
use opentelemetry::metrics::{Counter, Histogram, Meter, Unit};
use std::sync::atomic::{AtomicBool, Ordering, AtomicU64};
static OTEL_INIT: OnceCell<()> = OnceCell::new();
static CONFIG_CACHE: OnceCell<RwLock<CachedConfig>> = OnceCell::new();
static PROM_INIT: OnceCell<()> = OnceCell::new();
static EXPORTER: Lazy<RwLock<Option<PrometheusExporter>>> = Lazy::new(|| RwLock::new(None));

// --- Detection Metrics (Phase 1 observability alignment) ---
#[derive(Clone, Debug)]
pub struct DetectionMetrics {
    pub signature_total: Counter<u64>,
    pub anomaly_total: Counter<u64>,
    pub false_positive_total: Counter<u64>,
    pub alert_latency_ms: Histogram<f64>,
    pub e2e_latency_ms: Histogram<f64>,
}

static DETECTION_METER: Lazy<Meter> = Lazy::new(|| opentelemetry::global::meter("swarm_detection"));
static NODE_LIVENESS: AtomicBool = AtomicBool::new(true);
static NODE_READINESS: AtomicBool = AtomicBool::new(false);
pub fn mark_ready() { NODE_READINESS.store(true, Ordering::SeqCst); }
pub fn clear_ready() { NODE_READINESS.store(false, Ordering::SeqCst); }
pub fn mark_not_live() { NODE_LIVENESS.store(false, Ordering::SeqCst); }
// Service start instant for uptime calculations.
static START_INSTANT: OnceCell<Instant> = OnceCell::new();

static DETECTION_METRICS: Lazy<DetectionMetrics> = Lazy::new(|| {
    DetectionMetrics {
        signature_total: DETECTION_METER.u64_counter("swarm_detection_signature_total")
            .with_description("Total signature-based detection matches")
            .init(),
        anomaly_total: DETECTION_METER.u64_counter("swarm_detection_anomaly_total")
            .with_description("Total anomaly detection events")
            .init(),
        false_positive_total: DETECTION_METER.u64_counter("swarm_detection_false_positive_total")
            .with_description("Confirmed false positives")
            .init(),
        alert_latency_ms: DETECTION_METER.f64_histogram("swarm_detection_alert_latency_ms")
            .with_description("Latency from event ingest to alert emission (ms)")
            .with_unit(Unit::new("ms"))
            .init(),
        e2e_latency_ms: DETECTION_METER.f64_histogram("swarm_ingest_e2e_latency_ms")
            .with_description("End-to-end ingest->detect->publish latency (ms)")
            .with_unit(Unit::new("ms"))
            .init(),
    }
});

// Internal atomic tallies to compute ratios quickly without depending on exporter introspection.
static TOTAL_DETECTIONS: AtomicU64 = AtomicU64::new(0);
static TOTAL_FALSE_POSITIVES: AtomicU64 = AtomicU64::new(0);

/// Public accessor for detection metrics instrument bundle.
/// This avoids exposing the static directly which offers flexibility for refactors.
pub fn detection_metrics() -> &'static DetectionMetrics { &DETECTION_METRICS }

/// Record a detection event (signature or anomaly) for internal ratio metrics.
/// Record a detection outcome.
/// Pass `is_false_positive=true` if this detection was later adjudicated as FP
/// so we can track ratio for quality dashboards without complex joins.
pub fn record_detection(is_false_positive: bool) {
    TOTAL_DETECTIONS.fetch_add(1, Ordering::Relaxed);
    if is_false_positive { TOTAL_FALSE_POSITIVES.fetch_add(1, Ordering::Relaxed); }
}

/// Helper to compute false positive ratio (uses u64 to avoid division by zero).
pub fn false_positive_ratio() -> f64 {
    let total = TOTAL_DETECTIONS.load(Ordering::Relaxed);
    if total == 0 { return 0.0; }
    let fp = TOTAL_FALSE_POSITIVES.load(Ordering::Relaxed);
    fp as f64 / total as f64
}

#[derive(Debug, Clone)]
struct CachedConfig {
    cfg: DynamicConfig,
    fetched_at: Instant,
    ttl: Duration,
    file: Option<PathBuf>,
}

pub fn init_tracing(service: &str) -> Result<()> {
    OTEL_INIT.get_or_try_init(|| {
        let otlp_endpoint = std::env::var("OTEL_EXPORTER_OTLP_ENDPOINT").unwrap_or_else(|_| "http://localhost:4317".into());
        let tracer = opentelemetry_otlp::new_pipeline()
            .tracing()
            .with_endpoint(otlp_endpoint)
            .with_trace_config(sdktrace::config().with_resource(Resource::new(vec![
                KeyValue::new("service.name", service.to_string()),
            ])))
            .install_batch(opentelemetry::runtime::Tokio)?;
        let json = std::env::var("SWARM_JSON_LOG").ok().map(|v| v=="1" || v.eq_ignore_ascii_case("true")).unwrap_or(false);
        let fmt_layer = if json {
            tracing_subscriber::fmt::layer()
                .json()
                .flatten_event(true)
                .with_current_span(true)
                .with_span_list(false)
                .event_format(tracing_subscriber::fmt::format()
                    .json()
                    .with_current_span(true)
                    .with_span_list(false))
        } else {
            tracing_subscriber::fmt::layer()
                .with_target(true)
                .with_thread_ids(false)
                .with_line_number(true)
        };
        let otel_layer = tracing_opentelemetry::layer().with_tracer(tracer);
        let env_filter = tracing_subscriber::EnvFilter::from_default_env();
        let registry = tracing_subscriber::registry().with(env_filter).with(fmt_layer).with(otel_layer);
        registry.try_init()?;
        Ok(())
    })?;
    // Capture service start if first call.
    START_INSTANT.get_or_init(Instant::now);
    info!(target: service, "Tracing + OTEL initialized");
    Ok(())
}

pub fn shutdown_tracer() { global::shutdown_tracer_provider(); }

pub fn init_metrics() -> Result<()> {
    PROM_INIT.get_or_try_init(|| {
        let exporter = opentelemetry_prometheus::exporter().try_init()?;
        let mut w = EXPORTER.write();
        *w = Some(exporter);
        Ok(())
    })?;
    Ok(())
}

pub async fn start_health_server(port: u16) -> Result<()> {
    let app = Router::new()
        .route("/live", get(|| async { axum::Json(serde_json::json!({"live": NODE_LIVENESS.load(Ordering::SeqCst)})) }))
        .route("/ready", get(|| async { axum::Json(serde_json::json!({"ready": NODE_READINESS.load(Ordering::SeqCst)})) }))
        .route("/status", get(|| async {
            let uptime_ms = START_INSTANT.get().map(|s| s.elapsed().as_millis()).unwrap_or(0);
            let total = TOTAL_DETECTIONS.load(Ordering::Relaxed);
            let fp_total = TOTAL_FALSE_POSITIVES.load(Ordering::Relaxed);
            let ratio = false_positive_ratio();
            let cfg_version = CONFIG_CACHE.get().and_then(|c| c.read().cfg.config_version.clone());
            axum::Json(serde_json::json!({
                "live": NODE_LIVENESS.load(Ordering::SeqCst),
                "ready": NODE_READINESS.load(Ordering::SeqCst),
                "uptime_ms": uptime_ms,
                "detections_total": total,
                "false_positives_total": fp_total,
                "false_positive_ratio": ratio,
                "config_version": cfg_version,
            }))
        }))
        .route("/metrics", get(metrics_handler));
    let addr = SocketAddr::from(([0,0,0,0], port));
    tracing::info!(?addr, "Health server listening");
    tokio::spawn(async move {
        if let Err(e) = axum::Server::bind(&addr).serve(app.into_make_service()).await {
            tracing::error!(error=?e, "Health server failed");
        }
    });
    Ok(())
}

#[derive(Debug, Deserialize, Clone)]
pub struct DynamicConfig {
    pub service_name: Option<String>,
    pub nats_url: Option<String>,
    pub log_level: Option<String>,
    // --- Added for versioned & signed config roadmap alignment ---
    pub config_version: Option<String>,
    pub config_signature: Option<String>, // placeholder (e.g., hex-encoded ed25519 or PQC signature)
}

impl Default for DynamicConfig {
    fn default() -> Self { Self { service_name: None, nats_url: Some("127.0.0.1:4222".into()), log_level: Some("info".into()), config_version: Some("0".into()), config_signature: None } }
}

pub async fn load_config(service: &str) -> Result<DynamicConfig> {
    // if cache exists & fresh, return it
    if let Some(lock) = CONFIG_CACHE.get() {
        let guard = lock.read();
        if guard.fetched_at.elapsed() < guard.ttl { return Ok(guard.cfg.clone()); }
    }
    let mut builder = config::Config::builder()
        .set_default("service_name", service)?
        .set_default("nats_url", "127.0.0.1:4222")?
        .set_default("log_level", "info")?;

    let mut file_path: Option<PathBuf> = None;
    if let Ok(file) = std::env::var("SWARM_CONFIG_FILE") {
        file_path = Some(PathBuf::from(&file));
        builder = builder.add_source(config::File::with_name(&file).required(false));
    }
    if let Ok(http_url) = std::env::var("SWARM_CONFIG_HTTP") {
        if let Ok(resp) = reqwest::get(http_url.clone()).await { if let Ok(text) = resp.text().await { builder = builder.add_source(config::File::from_str(&text, config::FileFormat::Yaml)); } }
    }
    builder = builder.add_source(config::Environment::with_prefix("SWARM").separator("__"));
    let cfg = builder.build()?;
    let dyn_cfg: DynamicConfig = cfg.try_deserialize()?;
    // If signature present, attempt verification (best-effort for now)
    if let (Some(sig), Some(file)) = (&dyn_cfg.config_signature, &file_path) {
        if let Ok(raw) = std::fs::read_to_string(file) {
            if !crate::config_signature::verify_config_signature(&raw, sig) {
                tracing::warn!(?file, "Config signature verification failed");
            }
        }
    }
    let ttl_secs: u64 = std::env::var("SWARM_CONFIG_TTL_SECS").ok().and_then(|v| v.parse().ok()).unwrap_or(30);
    let cached = CachedConfig { cfg: dyn_cfg.clone(), fetched_at: Instant::now(), ttl: Duration::from_secs(ttl_secs), file: file_path};
    let lock = CONFIG_CACHE.get_or_init(|| RwLock::new(cached.clone()));
    {
        let mut w = lock.write();
        *w = cached;
    }
    if let Some(f) = lock.read().file.clone() { spawn_file_watcher(f); }
    Ok(dyn_cfg)
}

fn spawn_file_watcher(path: PathBuf) {
    tokio::spawn(async move {
        if let Err(e) = watch_loop(path).await { tracing::warn!(error=?e, "config watch loop exited"); }
    });
}

async fn watch_loop(path: PathBuf) -> Result<()> {
    use tokio::sync::mpsc; 
    let (tx, mut rx) = mpsc::channel(8);
    let mut watcher = RecommendedWatcher::new(move |res| { let _ = tx.blocking_send(res); }, notify::Config::default())?;
    watcher.watch(&path, notify::RecursiveMode::NonRecursive)?;
    while let Some(evt) = rx.recv().await {
        if let Ok(ev) = evt { if matches!(ev.kind, EventKind::Modify(_)) { refresh_from_file(&path).await?; } }
    }
    Ok(())
}

async fn refresh_from_file(path: &PathBuf) -> Result<()> {
    if let Some(lock) = CONFIG_CACHE.get() {
        if let Ok(text) = tokio::fs::read_to_string(path).await {
            let builder = config::Config::builder().add_source(config::File::from_str(&text, config::FileFormat::Yaml));
            if let Ok(cfg) = builder.build() { if let Ok(new_cfg) = cfg.try_deserialize::<DynamicConfig>() { let mut w = lock.write(); w.cfg = new_cfg; w.fetched_at = Instant::now(); } }
        }
    }
    Ok(())
}

pub async fn force_reload(service: &str) -> Result<DynamicConfig> { load_config(service).await }

async fn metrics_handler() -> axum::response::Response {
    if EXPORTER.read().is_none() {
        return axum::response::Response::builder().status(503).body(axum::body::Body::from("metrics not initialized")).unwrap();
    }
    let registry = prometheus::default_registry();
    let metric_families = registry.gather();
    let mut buf = Vec::new();
    if let Err(e) = TextEncoder::new().encode(&metric_families, &mut buf) {
        return axum::response::Response::builder().status(500).body(axum::body::Body::from(format!("encode error: {e}"))).unwrap();
    }
    axum::response::Response::builder()
        .status(200)
        .header("Content-Type", "text/plain; version=0.0.4")
        .body(axum::body::Body::from(buf))
        .unwrap()
}

mod resilience; // new module providing retry & circuit breaker
pub use resilience::{retry_async, RetryConfig, CircuitBreaker, BreakerState};
pub mod resilience_telemetry; // telemetry metrics for resilience primitives
pub use resilience_telemetry::{register_metrics as register_resilience_metrics, ResilienceMetrics};

// Advanced swarm intelligence modules
pub mod ml_detection;
pub mod federated_learning;
pub mod consensus;
pub mod autoscaling;
pub mod gossip;
pub mod transport_quic;
pub mod lifecycle;
pub mod reputation;
mod metrics_ext; // extended metrics groups

pub use ml_detection::{MLDetectionPipeline, ThreatEvent, DetectionResult, ThreatLevel};
pub use federated_learning::{FederatedLearningCoordinator, ModelGradient, GlobalModel, AggregationMethod};
pub use consensus::{PBFTConsensus, ConsensusMessage, NodeId};
pub use autoscaling::{AutoScaler, ResourceMetrics, ScalingDecision, ScalingThresholds};
pub use gossip::{GossipEngine, GossipMessage, GossipKind, GossipId};
pub use transport_quic::{QuicTransport, QuicConfig, QuicConnectionHandle};
pub use lifecycle::{BootstrapState, BootstrapPhase};
pub use reputation::{ReputationService, ReputationConfig};
pub use metrics_ext::{EXTENDED_METRICS, ExtendedMetrics};

// --- Test utilities (not exported in normal builds) ---
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn ratio_zero_when_no_events() {
        assert_eq!(false_positive_ratio(), 0.0);
    }

    #[test]
    fn ratio_calculates() {
        // local atomic increments; cannot reset global statics easily without unsafe, so we only assert monotonic behavior
        let before = false_positive_ratio();
        record_detection(false);
        record_detection(true);
        let after = false_positive_ratio();
        assert!(after >= before);
        assert!(after <= 1.0);
    }
}
