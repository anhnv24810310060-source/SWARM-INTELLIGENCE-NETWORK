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

    // In a real system we would consume events from a queue / socket.
    // Placeholder loop omitted for brevity.

    signal::ctrl_c().await?;
    info!("shutdown");
    Ok(())
}
