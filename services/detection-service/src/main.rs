use anyhow::Result;
use tracing::{info, error};
use swarm_core::{init_tracing, init_metrics, start_health_server, mark_ready, load_config, detection_metrics, record_detection};
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

    let pipe = pipeline::DetectionPipeline::new().await?;
    mark_ready();
    info!("service ready");

    // In a real system we would consume events from a queue / socket.
    // Placeholder loop omitted for brevity.

    signal::ctrl_c().await?;
    info!("shutdown");
    Ok(())
}
