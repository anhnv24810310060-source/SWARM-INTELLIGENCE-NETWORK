use anyhow::Result;
use tracing::{info, error};
use swarm_core::{init_tracing, init_metrics, start_health_server, mark_ready, load_config};
use axum::{Router, routing::get, Json};
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
    let anomaly_dbg = pipe.anomaly_debug_snapshot();
    let router = Router::new().route("/debug/anomaly", get(move || {
        let snap = anomaly_dbg();
        async move { Json(snap) }
    }));
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
