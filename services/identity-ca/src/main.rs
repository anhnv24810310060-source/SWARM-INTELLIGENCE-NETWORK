use anyhow::Result;
use tracing::info;

#[tokio::main]
async fn main() -> Result<()> {
    init_tracing();
    info!(target: "identity-ca", "Starting identity-ca service");
    // TODO: Implement certificate issuance + PQC hybrid placeholder
    Ok(())
}

fn init_tracing() {
    let _ = tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .try_init();
}
