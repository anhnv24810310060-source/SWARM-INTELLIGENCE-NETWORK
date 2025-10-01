use anyhow::Result;
use tracing::info;
use tokio::signal;
use detection_service::pipeline::{DetectionPipeline, ThreatEvent};
use swarm_core::{init_tracing, init_metrics, start_health_server, mark_ready, load_config};

mod pipeline;

#[tokio::main]
async fn main() -> Result<()> {
    init_tracing("detection-service")?;
    init_metrics()?;
    info!("detection-service starting");

    let cfg = load_config("detection-service").await?;
    info!(?cfg, "config_loaded");

    start_health_server(9091).await?;
    let pipeline = DetectionPipeline::new()?;
    info!("pipeline_initialized");
    mark_ready();

    // Demo: process a dummy event once at startup (can be removed later)
    let dummy = ThreatEvent { id: "evt-1".into(), source_ip: "1.1.1.1".into(), destination_ip: "2.2.2.2".into(), timestamp: chrono::Utc::now().timestamp(), raw_data: vec![] };
    let _ = pipeline.process(dummy).await?;

    signal::ctrl_c().await?;
    info!("shutdown");
    Ok(())
}
