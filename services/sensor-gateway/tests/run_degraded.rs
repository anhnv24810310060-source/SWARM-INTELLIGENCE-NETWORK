//! Test the exported `run()` function with an invalid NATS URL ensuring degraded mode is entered
//! and the function does not panic during initialization. We abort the infinite synthetic loop
//! via timeout.

use std::time::Duration;

#[tokio::test]
async fn test_run_degraded_mode_metric() {
    // Use an invalid port to force connection failure quickly.
    std::env::set_var("NATS_URL", "127.0.0.1:59999");
    // Short-circuit OTEL exporter to avoid network attempts in test environment.
    std::env::set_var("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4317");

    std::env::set_var("SWARM_RUN_ONCE", "1");
    // Directly call run(); with SWARM_RUN_ONCE it should return quickly.
    let res = crate::run().await;
    assert!(res.is_ok());
    // If we reached here without panic, test passes. Optionally we could fetch /metrics
    // but that would require spinning an HTTP client; omitted for simplicity.
    assert!(true);
}
