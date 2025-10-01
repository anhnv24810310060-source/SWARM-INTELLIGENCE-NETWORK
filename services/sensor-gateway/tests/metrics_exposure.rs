//! Ensures /metrics endpoint exposes newly added histograms & counters.
use reqwest::Client;

#[tokio::test]
async fn test_metrics_endpoint_contains_histograms() {
    // Run service in run-once mode so it exits synthetic loop quickly after one event.
    std::env::set_var("SWARM_RUN_ONCE", "1");
    std::env::set_var("NATS_URL", "127.0.0.1:59999"); // force degraded
    // Launch in background
    let handle = tokio::spawn(async move { let _ = crate::run().await; });
    // Wait a moment for server startup
    tokio::time::sleep(std::time::Duration::from_millis(300)).await;
    let body = Client::new().get("http://127.0.0.1:8080/metrics").send().await.expect("metrics request").text().await.expect("text");
    assert!(body.contains("swarm_ingest_encode_latency_ms"), "missing encode latency histogram");
    assert!(body.contains("swarm_ingest_payload_bytes"), "missing payload bytes histogram");
    assert!(body.contains("swarm_ingest_events_total"), "missing events counter");
    handle.abort();
}
