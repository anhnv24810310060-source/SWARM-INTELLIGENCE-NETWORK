use anyhow::{Context, Result};
use tracing::{info, warn, error};
use swarm_core::{init_tracing, start_health_server, init_metrics};
use swarm_proto::ingestion::RawEvent;
use std::time::{SystemTime, UNIX_EPOCH};
use std::path::Path;
use tokio::io::{AsyncBufReadExt, BufReader};
use tokio::fs::File;
use prost::Message;
use opentelemetry::{global, metrics::Counter};
use opentelemetry::metrics::Meter;

struct Metrics {
    events_total: Counter<u64>,
    errors_total: Counter<u64>,
    degraded_total: Counter<u64>,
}

fn init_metrics() -> Metrics {
    let meter: Meter = global::meter("sensor-gateway");
    let events_total = meter.u64_counter("swarm_ingest_events_total").with_description("Total raw events ingested").init();
    let errors_total = meter.u64_counter("swarm_ingest_errors_total").with_description("Total ingestion errors").init();
    let degraded_total = meter.u64_counter("swarm_ingest_degraded_mode_total").with_description("Times ingestion entered degraded mode due to broker unavailability").init();
    Metrics { events_total, errors_total, degraded_total }
}

#[tokio::main]
async fn main() -> Result<()> {
    init_tracing("sensor-gateway")?;
    let _ = init_metrics();
    start_health_server(8080).await?;
    info!(target: "sensor-gateway", "Starting sensor-gateway service");
    let metrics = init_metrics();

    let nats_url = std::env::var("NATS_URL").unwrap_or_else(|_| "127.0.0.1:4222".into());
    let mut nats_conn = match async_nats::connect(&nats_url).await {
        Ok(c) => { info!(target:"sensor-gateway", %nats_url, "Connected to NATS"); Some(c) },
        Err(e) => { warn!(error=?e, "NATS unavailable - degraded mode"); metrics.degraded_total.add(1, &[]); None }
    };

    // Emit bootstrap event (best-effort)
    if let Some(nc) = &nats_conn { let _ = nc.publish("ingest.v1.status".into(), "online".into()).await; }

    let ingest_file = std::env::var("INGEST_FILE").ok();
    if let Some(f) = ingest_file { if Path::new(&f).exists() { ingest_file_loop(&f, &mut nats_conn, &metrics).await?; return Ok(()); } }
    synthetic_loop(&mut nats_conn, &metrics).await;
    Ok(())
}

// Tracing handled by swarm-core

async fn ingest_file_loop(path: &str, nats: &mut Option<async_nats::Client>, metrics: &Metrics) -> Result<()> {
    info!(target:"sensor-gateway", %path, "Starting file ingestion loop");
    let file = File::open(path).await.with_context(|| format!("open ingest file {path}"))?;
    let reader = BufReader::new(file);
    let mut lines = reader.lines();
    while let Ok(Some(line)) = lines.next_line().await {
        if line.trim().is_empty() { continue; }
        if let Err(e) = process_line(&line, nats, metrics).await { metrics.errors_total.add(1, &[]); warn!(error=?e, "failed processing line"); }
    }
    Ok(())
}

async fn synthetic_loop(nats: &mut Option<async_nats::Client>, metrics: &Metrics) {
    info!(target:"sensor-gateway", "Starting synthetic event generation loop");
    let mut i: u64 = 0;
    loop {
        let payload = format!("synthetic-event-{i}");
        if let Err(e) = process_line(&payload, nats, metrics).await { metrics.errors_total.add(1, &[]); error!(error=?e, "failed processing synthetic"); }
        i += 1;
        tokio::time::sleep(std::time::Duration::from_millis(500)).await;
    }
}

async fn process_line(line: &str, nats: &mut Option<async_nats::Client>, metrics: &Metrics) -> Result<()> {
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
    evt.encode(&mut buf)?;
    if let Some(nc) = nats { if let Err(e) = nc.publish("ingest.v1.raw".into(), buf.into()).await { warn!(error=?e, "publish failed"); } }
    metrics.events_total.add(1, &[]);
    Ok(())
}
