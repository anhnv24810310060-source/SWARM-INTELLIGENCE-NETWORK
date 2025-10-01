#[test]
fn init_tracing_ok() {
    swarm_core::init_tracing("test-core").unwrap();
}
