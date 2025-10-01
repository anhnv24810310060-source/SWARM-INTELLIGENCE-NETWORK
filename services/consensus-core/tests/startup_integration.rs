// Feature-gated integration test ensuring consensus-core and swarm-gossip start.
// Run with: cargo test --features integration -- --nocapture
// Note: This test uses spawning processes; in constrained CI it can be ignored without the feature.

#[cfg(feature = "integration")]
mod tests {
    use std::process::{Command, Stdio, Child};
    use std::time::{Duration, Instant};
    use std::thread::sleep;
    use std::io::Read;

    fn wait_health(url: &str, timeout: Duration) -> bool {
        let start = Instant::now();
        while start.elapsed() < timeout {
            if let Ok(resp) = reqwest::blocking::get(url) { if resp.status().is_success() { return true; } }
            sleep(Duration::from_millis(200));
        }
        false
    }

    fn spawn_bin(name: &str, extra_env: &[(&str,&str)]) -> Child {
        let mut cmd = Command::new("cargo");
        cmd.args(["run","--quiet","--bin", name])
            .env("RUST_LOG","info")
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());
        for (k,v) in extra_env { cmd.env(k, v); }
        cmd.spawn().expect("failed to spawn binary")
    }

    #[test]
    fn consensus_and_gossip_start() {
        // Ports: gossip health 8080 (existing), consensus health 8081
        let mut consensus = spawn_bin("consensus-core", &[ ("CONSENSUS_GRPC_PORT","55051") ]);
        let mut gossip = spawn_bin("swarm-gossip", &[]);

        assert!(wait_health("http://127.0.0.1:8081/healthz", Duration::from_secs(15)), "consensus health not ready");
        assert!(wait_health("http://127.0.0.1:8080/healthz", Duration::from_secs(15)), "gossip health not ready");

        // Log sample output for debugging
        if let Some(mut so) = consensus.stdout.take() { let mut b = String::new(); let _ = so.read_to_string(&mut b); println!("consensus logs: {}", b); }
        if let Some(mut so) = gossip.stdout.take() { let mut b = String::new(); let _ = so.read_to_string(&mut b); println!("gossip logs: {}", b); }

        let _ = consensus.kill();
        let _ = gossip.kill();
    }
}
