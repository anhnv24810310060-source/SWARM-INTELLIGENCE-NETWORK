use anyhow::Result;
use tracing::{info, error};
use swarm_core::{init_tracing, init_metrics, start_health_server, mark_ready, load_config};
use axum::{Router, routing::get, Json, extract::State, http::StatusCode, response::IntoResponse};
use std::time::{Instant, Duration};
use std::collections::VecDeque;
use std::sync::Arc;
use tokio::signal;

mod pipeline;
mod signature_db;
mod anomaly;
mod ingest;
#[cfg(feature = "onnx")] mod ml;

#[tokio::main]
async fn main() -> Result<()> {
    init_tracing("detection-service")?;
    init_metrics()?;

    let cfg = load_config("detection-service").await?;
    info!(?cfg, "config loaded");

    start_health_server(9091).await?;

    let pipe = Arc::new(pipeline::DetectionPipeline::new().await?);
    // Attempt optional rules load with retry if env set
    if let Ok(rules_path) = std::env::var("SWARM__DETECTION__RULES_PATH") {
        let mut attempts = 0;
        loop {
            attempts+=1;
            match pipe.signature_load(&rules_path) {
                Ok(_) => { info!(path=%rules_path, attempts, "rules loaded"); break; }
                Err(e) => {
                    if attempts >=3 { error!(error=?e, "rules load failed after retries"); break; }
                    let backoff = attempts * 500;
                    tokio::time::sleep(std::time::Duration::from_millis(backoff as u64)).await;
                }
            }
        }
    }
    let anomaly_dbg = pipe.anomaly_debug_snapshot();
    let api_key = std::env::var("SWARM__DETECTION__DEBUG_API_KEY").ok();
    let rate_limit = std::env::var("SWARM__DETECTION__DEBUG_RPS").ok().and_then(|v| v.parse::<u32>().ok()).unwrap_or(5);
    #[derive(Clone)]
    struct DebugState { api_key: Option<String>, anomaly: Box<dyn Fn() -> pipeline::AnomalyDebug + Send + Sync>, window: Duration, hits: Arc<tokio::sync::Mutex<VecDeque<Instant>>>, max: u32 }
    let state = DebugState { api_key, anomaly: Box::new(anomaly_dbg), window: Duration::from_secs(60), hits: Arc::new(tokio::sync::Mutex::new(VecDeque::with_capacity(128))), max: rate_limit*60 };
    async fn dbg_handler(State(st): State<DebugState>, headers: axum::http::HeaderMap) -> impl IntoResponse {
        if let Some(expected) = &st.api_key { match headers.get("x-api-key").and_then(|v| v.to_str().ok()) { Some(got) if got==expected => {}, _ => return (StatusCode::UNAUTHORIZED, "missing or invalid api key").into_response() } }
        // rate limiting: count last 60s hits
        let now = Instant::now();
        let mut guard = st.hits.lock().await;
        while let Some(front) = guard.front() { if now.duration_since(*front) > st.window { guard.pop_front(); } else { break; } }
        if guard.len() as u32 >= st.max { return (StatusCode::TOO_MANY_REQUESTS, "rate limit").into_response(); }
        guard.push_back(now);
        let snap = (st.anomaly)();
        Json(snap).into_response()
    }
    let router = Router::new().route("/debug/anomaly", get(dbg_handler)).with_state(state);
    // spawn debug server (separate from health)
    let r2 = router.clone();
    tokio::spawn(async move { axum::Server::bind(&"0.0.0.0:9092".parse().unwrap()).serve(r2.into_make_service()).await.ok(); });
    mark_ready();
    info!("service ready");

    // Ingest subsystem: bounded channel + token bucket rate limiter
    let ingest_cap: usize = std::env::var("SWARM__DETECTION__INGEST_CAP").ok().and_then(|v| v.parse().ok()).unwrap_or(2048);
    let ingest_rps: u64 = std::env::var("SWARM__DETECTION__INGEST_RPS").ok().and_then(|v| v.parse().ok()).unwrap_or(10_000);
    let max_event: usize = std::env::var("SWARM__DETECTION__INGEST_MAX_EVENT").ok().and_then(|v| v.parse().ok()).unwrap_or(128*1024);
    let (ing_handle, mut rx) = ingest::start_ingest(ingest_cap, ingest_rps, max_event);

    // Example synthetic feeder (would be replaced by real socket/kafka, etc.)
    let pipe_clone = pipe.clone();
    tokio::spawn(async move {
        while let Some(raw) = rx.recv().await {
            ingest::on_dequeue();
            let p = pipe_clone.clone();
            tokio::spawn(async move { let _ = p.process(raw).await; });
        }
    });

    // Synthetic generator for demonstration (can be disabled via env flag)
    let ing_clone = ing_handle.clone();
    tokio::spawn(async move {
        let mut i: u64 = 0;
        loop {
            i+=1;
            let ev = pipeline::RawEvent { id: format!("synthetic-{i}"), bytes: format!("hello-{i}").into_bytes(), ts: i as i64 };
            let _ = ing_clone.try_push(ev);
            tokio::time::sleep(Duration::from_millis(500)).await;
        }
    });

    signal::ctrl_c().await?;
    info!("shutdown");
    Ok(())
}
