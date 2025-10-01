//! Core shared utilities for SwarmGuard services.

use anyhow::Result;
use tracing::info;
use once_cell::sync::OnceCell;
use opentelemetry::{global, sdk::{trace as sdktrace, Resource}, KeyValue};
use opentelemetry_otlp::WithExportConfig;
use axum::{routing::get, Router};
use std::net::SocketAddr;
use serde::Deserialize;
static OTEL_INIT: OnceCell<()> = OnceCell::new();

pub fn init_tracing(service: &str) -> Result<()> {
    OTEL_INIT.get_or_try_init(|| {
        let otlp_endpoint = std::env::var("OTEL_EXPORTER_OTLP_ENDPOINT").unwrap_or_else(|_| "http://localhost:4317".into());
        let tracer = opentelemetry_otlp::new_pipeline()
            .tracing()
            .with_endpoint(otlp_endpoint)
            .with_trace_config(sdktrace::config().with_resource(Resource::new(vec![
                KeyValue::new("service.name", service.to_string()),
            ])))
            .install_batch(opentelemetry::runtime::Tokio)?;

        let fmt_layer = tracing_subscriber::fmt::layer();
        let otel_layer = tracing_opentelemetry::layer().with_tracer(tracer);
        let env_filter = tracing_subscriber::EnvFilter::from_default_env();
        let registry = tracing_subscriber::registry().with(env_filter).with(fmt_layer).with(otel_layer);
        registry.try_init()?;
        Ok(())
    })?;
    info!(target: service, "Tracing + OTEL initialized");
    Ok(())
}

pub fn shutdown_tracer() {
    global::shutdown_tracer_provider();
}

pub async fn start_health_server(port: u16) -> Result<()> {
    let app = Router::new().route("/healthz", get(|| async { "ok" }));
    let addr = SocketAddr::from(([0,0,0,0], port));
    tracing::info!(?addr, "Health server listening");
    tokio::spawn(async move {
        if let Err(e) = axum::Server::bind(&addr).serve(app.into_make_service()).await {
            tracing::error!(error=?e, "Health server failed");
        }
    });
    Ok(())
}

#[derive(Debug, Deserialize, Clone)]
pub struct DynamicConfig {
    pub service_name: Option<String>,
    pub nats_url: Option<String>,
    pub log_level: Option<String>,
}

impl Default for DynamicConfig {
    fn default() -> Self { Self { service_name: None, nats_url: Some("127.0.0.1:4222".into()), log_level: Some("info".into()) } }
}

pub async fn load_config(service: &str) -> Result<DynamicConfig> {
    let mut builder = config::Config::builder()
        .set_default("service_name", service)?
        .set_default("nats_url", "127.0.0.1:4222")?
        .set_default("log_level", "info")?;

    if let Ok(file) = std::env::var("SWARM_CONFIG_FILE") { builder = builder.add_source(config::File::with_name(&file).required(false)); }
    if let Ok(http_url) = std::env::var("SWARM_CONFIG_HTTP") {
        if let Ok(resp) = reqwest::get(http_url.clone()).await { if let Ok(text) = resp.text().await { builder = builder.add_source(config::File::from_str(&text, config::FileFormat::Yaml)); } }
    }
    builder = builder.add_source(config::Environment::with_prefix("SWARM").separator("__"));
    let cfg = builder.build()?;
    let dyn_cfg: DynamicConfig = cfg.try_deserialize()?;
    Ok(dyn_cfg)
}
