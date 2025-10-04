use anyhow::Result;
use swarm_core::{init_tracing, start_health_server, shutdown_tracer};
use swarm_proto::consensus::pbft_server::PbftServer;
use tonic::transport::Server;
use tracing::info;
use std::net::SocketAddr;
use axum::{routing::get, Router};
use opentelemetry_prometheus::PrometheusExporter;
use opentelemetry::{metrics::MeterProvider as _};
use opentelemetry_sdk::metrics::{controllers, processors, selectors};
use consensus_core::{PbftService};


fn spawn_metrics_server(port: u16) {
    tokio::spawn(async move {
        let controller = controllers::basic(processors::factory(selectors::simple::Selector::Exact, opentelemetry::sdk::export::metrics::aggregation::cumulative_temporality_selector()))
            .build();
        let exporter = PrometheusExporter::new(controller);
        let handle = exporter.clone();
        let app = Router::new().route("/metrics", get(move || {
            let h = handle.clone();
            async move { h.render() }
        }));
        let port = std::env::var("CONSENSUS_METRICS_PORT").ok().and_then(|v| v.parse().ok()).unwrap_or(port);
        let addr: SocketAddr = ([0,0,0,0], port).into();
        if let Err(e) = axum::Server::bind(&addr).serve(app.into_make_service()).await { tracing::error!(error=?e, "metrics server failed"); }
    });
}

pub async fn publish_height_changed_versioned(height: u64, round: u64) {
    let nats_url = std::env::var("NATS_URL").unwrap_or_else(|_| "127.0.0.1:4222".into());
    let proto_version = std::env::var("PROTO_SCHEMA_VERSION").unwrap_or_else(|_| "v1".into());
    if let Ok(conn) = async_nats::connect(nats_url).await {
        let payload = serde_json::json!({"height": height, "round": round, "proto_schema_version": proto_version});
        let _ = conn.publish("consensus.v1.height.changed".into(), payload.to_string().into()).await;
        tracing::info!(height, round, proto_schema_version=?proto_version, "broadcast consensus.v1.height.changed");
    } else { tracing::debug!("NATS unavailable - skip broadcast"); }
}

pub async fn publish_round_changed(height: u64, round: u64) {
    let nats_url = std::env::var("NATS_URL").unwrap_or_else(|_| "127.0.0.1:4222".into());
    let proto_version = std::env::var("PROTO_SCHEMA_VERSION").unwrap_or_else(|_| "v1".into());
    if let Ok(conn) = async_nats::connect(nats_url).await {
        let payload = serde_json::json!({"height": height, "round": round, "proto_schema_version": proto_version});
        let _ = conn.publish("consensus.v1.round.changed".into(), payload.to_string().into()).await;
        tracing::info!(height, round, proto_schema_version=?proto_version, "broadcast consensus.v1.round.changed");
    } else { tracing::debug!("NATS unavailable - skip broadcast"); }
}

#[tokio::main]
async fn main() -> Result<()> {
    init_tracing("consensus-core")?;
    start_health_server(8081).await?; // separate health port
    let grpc_port: u16 = std::env::var("CONSENSUS_GRPC_PORT").ok().and_then(|v| v.parse().ok()).unwrap_or(50051);
    let addr = ([0,0,0,0], grpc_port).into();
    let svc = PbftService::new();
    spawn_metrics_server(9102);
    info!(?addr, "Starting consensus-core gRPC server");
    let server = Server::builder()
        .add_service(PbftServer::new(svc))
        .serve_with_shutdown(addr, async {
            use tokio::signal::unix::{signal, SignalKind};
            let mut sigint = signal(SignalKind::interrupt()).expect("sigint");
            let mut sigterm = signal(SignalKind::terminate()).expect("sigterm");
            tokio::select! { _ = sigint.recv() => {}, _ = sigterm.recv() => {} }
            tracing::info!("Shutdown signal received");
        });
    if let Err(e) = server.await { tracing::error!(error=?e, "gRPC server error"); }
    shutdown_tracer();
    Ok(())
}
