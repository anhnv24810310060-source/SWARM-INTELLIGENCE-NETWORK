use anyhow::{Context, Result};
use tracing::{info, warn, error};
use swarm_core::{init_tracing, start_health_server, init_metrics};
use swarm_proto::ingestion::RawEvent;
mod detection;
mod nats_pool;
use detection::{RuleSet, load_rules, AnomalyDetector, AnomalyConfig, DetectionEngine};
use nats_pool::NatsPool;
use swarm_resilience::{retry_async, CircuitBreaker};
use std::time::{SystemTime, UNIX_EPOCH};
use std::path::Path;
use std::io::Write; // for detection log append
use std::sync::Arc;
use tokio::io::{AsyncBufReadExt, BufReader};
use tokio::fs::File;
use prost::Message;
use opentelemetry::{global, metrics::{Counter, Histogram}};
use opentelemetry::metrics::Meter;

pub struct Metrics {
    events_total: Counter<u64>,
    errors_total: Counter<u64>,
    degraded_total: Counter<u64>,
    encode_latency_ms: Histogram<f64>,
    payload_bytes: Histogram<u64>,
}

pub fn init_metrics() -> Metrics {
    let meter: Meter = global::meter("sensor-gateway");
    let events_total = meter.u64_counter("swarm_ingest_events_total").with_description("Total raw events ingested").init();
    let errors_total = meter.u64_counter("swarm_ingest_errors_total").with_description("Total ingestion errors").init();
    let degraded_total = meter.u64_counter("swarm_ingest_degraded_mode_total").with_description("Times ingestion entered degraded mode due to broker unavailability").init();
    // Histograms for latency (ms) and payload size (bytes) to baseline throughput & encoding cost.
    let encode_latency_ms = meter.f64_histogram("swarm_ingest_encode_latency_ms").with_description("Time to protobuf-encode a RawEvent in milliseconds").init();
    let payload_bytes = meter.u64_histogram("swarm_ingest_payload_bytes").with_description("Payload size of ingested RawEvent in bytes").init();
    Metrics { events_total, errors_total, degraded_total, encode_latency_ms, payload_bytes }
}

#[tokio::main]
async fn main() -> Result<()> { run().await }

pub async fn run() -> Result<()> {
    init_tracing("sensor-gateway")?;
    let _ = init_metrics();
    start_health_server(8080).await?;
    info!(target: "sensor-gateway", "Starting sensor-gateway service");
    let metrics = init_metrics();
    // Initialize NATS connection pool with resilience
    let nats_url = std::env::var("NATS_URL").unwrap_or_else(|_| "nats://localhost:4222".into());
    let pool_size = std::env::var("NATS_POOL_SIZE").ok().and_then(|s| s.parse().ok()).unwrap_or(4);
    let mut nats_pool = match NatsPool::new(&nats_url, pool_size).await {
        Ok(p) => { info!(target:"sensor-gateway", %nats_url, pool_size, "Connected to NATS with connection pool"); Some(Arc::new(p)) },
        Err(e) => { warn!(error=?e, "NATS unavailable - degraded mode"); metrics.degraded_total.add(1, &[]); None }
    };
    // Detection engine setup
    let rules_path = std::env::var("DETECTION_RULES_PATH").unwrap_or_else(|_| "configs/detection-rules.yaml".into());
    let verify_rules = std::env::var("DETECTION_RULES_VERIFY").ok().map(|v| v=="1" || v.eq_ignore_ascii_case("true")).unwrap_or(false);
    let external_pk = std::env::var("DETECTION_RULES_PUBKEY").ok();
    let ruleset = RuleSet::new();
    if let Ok(rules) = load_rules(&rules_path, verify_rules, external_pk.as_deref()) { ruleset.swap(rules, "initial".into()); }
    let anomaly_cfg = AnomalyConfig::default();
    let detection_enabled = std::env::var("DETECTION_ENABLED").ok().map(|v| v=="1" || v.eq_ignore_ascii_case("true")).unwrap_or(true);
    let engine = DetectionEngine::new(ruleset.clone(), AnomalyDetector::new(anomaly_cfg), detection_enabled, true, true);
    if !detection_enabled { info!("detection disabled via DETECTION_ENABLED"); }
    // Hot reload watcher
    tokio::spawn(watch_rules(rules_path.clone(), ruleset.clone(), verify_rules, external_pk.clone()));
    let cb = CircuitBreaker::new(3, std::time::Duration::from_secs(5));
    if let Some(pool) = &nats_pool { 
        if let Err(e) = pool.publish("ingest.v1.status", b"online").await {
            warn!(error=?e, "failed to publish online status");
        }
    }
    let ingest_file = std::env::var("INGEST_FILE").ok();
    if let Some(f) = ingest_file { if Path::new(&f).exists() { ingest_file_loop(&f, &mut nats_pool, &metrics, &engine).await?; return Ok(()); } }
    let run_once = std::env::var("SWARM_RUN_ONCE").ok().map(|v| v=="1" || v.eq_ignore_ascii_case("true")).unwrap_or(false);
    synthetic_loop(&mut nats_pool, &metrics, run_once, &engine).await;
    Ok(())
}

// Tracing handled by swarm-core

async fn ingest_file_loop(path: &str, nats: &mut Option<Arc<NatsPool>>, metrics: &Metrics, engine: &DetectionEngine) -> Result<()> {
    info!(target:"sensor-gateway", %path, "Starting file ingestion loop");
    let file = File::open(path).await.with_context(|| format!("open ingest file {path}"))?;
    let reader = BufReader::new(file);
    let mut lines = reader.lines();
    while let Ok(Some(line)) = lines.next_line().await {
        if line.trim().is_empty() { continue; }
    if let Err(e) = process_line(&line, nats, metrics, engine).await { metrics.errors_total.add(1, &[]); warn!(error=?e, "failed processing line"); }
    }
    Ok(())
}

async fn synthetic_loop(nats: &mut Option<Arc<NatsPool>>, metrics: &Metrics, run_once: bool, engine: &DetectionEngine) {
    info!(target:"sensor-gateway", "Starting synthetic event generation loop");
    let mut i: u64 = 0;
    loop {
        let payload = format!("synthetic-event-{i}");
        if let Err(e) = process_line(&payload, nats, metrics, engine).await { metrics.errors_total.add(1, &[]); error!(error=?e, "failed processing synthetic"); }
        i += 1;
        if run_once { break; }
        tokio::time::sleep(std::time::Duration::from_millis(500)).await;
    }
}

async fn process_line(line: &str, nats: &mut Option<Arc<NatsPool>>, metrics: &Metrics, engine: &DetectionEngine) -> Result<()> {
    let start_e2e = std::time::Instant::now();
    let ts = SystemTime::now().duration_since(UNIX_EPOCH)?.as_millis() as u64;
    let evt = RawEvent {
        id: format!("{}-{}", ts, fxhash::hash32(line.as_bytes())),
        observed_ts: ts,
        source_type: "file".into(),
        origin: hostname::get().ok().and_then(|h| h.into_string().ok()).unwrap_or_else(|| "unknown".into()),
        payload: line.as_bytes().to_vec(),
        content_type: "text/plain".into(),
    };
    let mut buf = Vec::with_capacity(evt.encoded_len());
    let start = std::time::Instant::now();
    evt.encode(&mut buf)?;
    let elapsed = start.elapsed().as_secs_f64() * 1000.0; // ms
    metrics.encode_latency_ms.record(elapsed, &[]);
    metrics.payload_bytes.record(buf.len() as u64, &[]);
    if let Some(pool) = nats { 
        if let Err(e) = pool.publish("ingest.v1.raw", &buf).await {
            warn!(error=?e, "failed to publish raw event");
        }
    }
    // Detection
    let detections = engine.scan(line);
    // Ground truth heuristic: lines containing token MALICIOUS are considered true threats
    let gt_positive = line.contains("MALICIOUS");
    let meter = opentelemetry::global::meter("sensor-gateway");
    let sig_ctr = meter.u64_counter("swarm_detection_signature_total").init();
    let anom_ctr = meter.u64_counter("swarm_detection_anomaly_total").init();
    let tp_ctr = meter.u64_counter("swarm_detection_true_positives_total").init();
    let fp_ctr = meter.u64_counter("swarm_detection_false_positives_total").init();
    let fn_ctr = meter.u64_counter("swarm_detection_false_negatives_total").init();
    let fp_ratio_gauge = meter.f64_gauge("swarm_detection_false_positive_ratio").init();
    let detection_rate_gauge = meter.f64_gauge("swarm_detection_detection_rate").init();
    // Severity-specific counters for detailed analysis
    let critical_ctr = meter.u64_counter("swarm_detection_critical_total").with_description("Detections with critical severity").init();
    let high_ctr = meter.u64_counter("swarm_detection_high_total").with_description("Detections with high severity").init();
    let medium_ctr = meter.u64_counter("swarm_detection_medium_total").with_description("Detections with medium severity").init();
    let low_ctr = meter.u64_counter("swarm_detection_low_total").with_description("Detections with low severity").init();

    if !detections.is_empty() {
        for det in &detections {
            match det.kind.as_str() {
                "signature" => sig_ctr.add(1, &[]),
                "anomaly" => anom_ctr.add(1, &[]),
                _ => {}
            }
            // Track by severity
            match det.severity.to_lowercase().as_str() {
                "critical" => critical_ctr.add(1, &[]),
                "high" => high_ctr.add(1, &[]),
                "medium" => medium_ctr.add(1, &[]),
                "low" | "info" => low_ctr.add(1, &[]),
                _ => {}
            }
        }
        // Compute TP / FP
        if gt_positive { tp_ctr.add(1, &[]); } else { fp_ctr.add(1, &[]); }
    } else if gt_positive {
        // Missed detection => FN
        fn_ctr.add(1, &[]);
    }
    // Approximate ratios using cumulative counters (snapshots not atomic but acceptable for early stage)
    // In absence of reading counters back (OTel API lacks direct read), emit heuristic based on last classification event
    if gt_positive && detections.is_empty() { fp_ratio_gauge.record( fp_ctr.as_any().type_id() == fp_ctr.as_any().type_id() /* noop */ as i32 as f64, &[]); }
    if gt_positive && !detections.is_empty() { detection_rate_gauge.record(1.0, &[]); }
    for det in detections {
        if let Some(pool) = nats {
            if let Ok(json) = serde_json::to_vec(&det) {
                if let Err(e) = pool.publish("threat.v1.alert.detected", &json).await {
                    warn!(error=?e, "failed to publish detection alert");
                }
                if let Ok(path) = std::env::var("DETECTION_LOG_PATH") {
                    if let Ok(mut f) = std::fs::OpenOptions::new().create(true).append(true).open(&path) {
                        let _ = writeln!(f, "{}", String::from_utf8_lossy(&json));
                    }
                }
            }
        }
    }
    // Record E2E latency for performance tracking
    let e2e_elapsed = start_e2e.elapsed().as_secs_f64() * 1000.0; // ms
    let meter = opentelemetry::global::meter("sensor-gateway");
    let e2e_histogram = meter.f64_histogram("swarm_ingest_e2e_latency_ms")
        .with_description("End-to-end latency from ingest to detection publish")
        .init();
    e2e_histogram.record(e2e_elapsed, &[]);
    metrics.events_total.add(1, &[]);
    Ok(())
}

use notify::{Watcher, RecommendedWatcher, RecursiveMode, EventKind};
use std::sync::mpsc::channel;

async fn watch_rules(path: String, ruleset: RuleSet, verify: bool, external_pk: Option<String>) {
    tokio::task::spawn_blocking(move || {
        if Path::new(&path).exists() { if let Ok(rules) = load_rules(&path, verify, external_pk.as_deref()) { ruleset.swap(rules, "initial".into()); } }
        let (tx, rx) = channel();
        let mut watcher: RecommendedWatcher = notify::recommended_watcher(move |res| {
            let _ = tx.send(res);
        }).expect("create watcher");
        if watcher.watch(Path::new(&path), RecursiveMode::NonRecursive).is_err() { return; }
        while let Ok(ev) = rx.recv() {
            if let Ok(event) = ev { if matches!(event.kind, EventKind::Modify(_)|EventKind::Create(_)) {
                if let Ok(rules) = load_rules(&path, verify, external_pk.as_deref()) { ruleset.swap(rules, "reload".into()); }
            }}
        }
    }).await.ok();
}
