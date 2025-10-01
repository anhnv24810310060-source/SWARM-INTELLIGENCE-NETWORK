use anyhow::Result;
use tracing::info;
use swarm_core::{init_tracing, start_health_server};

#[tokio::main]
async fn main() -> Result<()> {
    init_tracing("swarm-gossip")?;
    start_health_server(8081).await?;
    info!(target: "swarm-gossip", "Starting swarm-gossip service");
    if let Ok(nc) = async_nats::connect("127.0.0.1:4222").await {
        info!(target:"swarm-gossip", "Connected to NATS (stub)");
        let _ = nc.publish("swarm.gossip.bootstrap".into(), "hello".into()).await;
    }
    // TODO: Implement gossip fanout, membership, duplicate suppression
    Ok(())
}

// Tracing handled by swarm-core
