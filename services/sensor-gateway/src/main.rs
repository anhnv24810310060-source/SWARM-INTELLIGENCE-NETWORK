use anyhow::Result;
use tracing::info;
use swarm_core::{init_tracing, start_health_server};

#[tokio::main]
async fn main() -> Result<()> {
    init_tracing("sensor-gateway")?;
    start_health_server(8080).await?;
    info!(target: "sensor-gateway", "Starting sensor-gateway service");
    // Connect to NATS (stub)
    if let Ok(nc) = async_nats::connect("127.0.0.1:4222").await {
        info!(target:"sensor-gateway", "Connected to NATS");
        let _ = nc.publish("swarm.events.bootstrap".into(), "online".into()).await;
    } else {
        info!(target:"sensor-gateway", "NATS unavailable - running degraded mode");
    }
    // TODO: Implement packet capture, log ingest, metrics pipeline
    Ok(())
}

// Tracing handled by swarm-core
