#[cfg(feature = "integration")]
#[tokio::test]
async fn nats_connection_attempt() {
    // This test assumes NATS running locally (dev-up). If not available, it should not fail the whole suite.
    match async_nats::connect("127.0.0.1:4222").await {
        Ok(client) => {
            client.publish("swarm.test.integration".into(), "ping".into()).await.unwrap();
        }
        Err(_) => {
            // Degrade silently to allow CI without NATS
            eprintln!("NATS not available, skipping integration test");
        }
    }
}
