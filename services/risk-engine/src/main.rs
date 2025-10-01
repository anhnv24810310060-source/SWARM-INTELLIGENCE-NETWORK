use anyhow::Result;
use tracing::info;

#[tokio::main]
async fn main() -> Result<()> {
    init_tracing();
    info!(target: "risk-engine", "Starting risk-engine service");
    // TODO: Risk scoring model inference & weighting logic
    Ok(())
}

fn init_tracing() {
    let _ = tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .try_init();
}
