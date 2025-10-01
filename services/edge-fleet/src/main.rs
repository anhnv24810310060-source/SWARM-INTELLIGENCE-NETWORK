use anyhow::Result;
use tracing::info;

#[tokio::main]
async fn main() -> Result<()> {
    init_tracing();
    info!(target: "edge-fleet", "Starting edge-fleet service");
    // TODO: Agent version tracking & rollout manager
    Ok(())
}

fn init_tracing() {
    let _ = tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .try_init();
}
