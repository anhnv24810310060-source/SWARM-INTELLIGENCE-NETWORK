fn main() {
    let proto_dir = "../../proto"; // relative from crate dir
    let mut protos = Vec::new();
    for entry in walkdir::WalkDir::new(proto_dir) {
        let e = entry.unwrap();
        if e.path().extension().and_then(|s| s.to_str()) == Some("proto") {
            protos.push(e.path().to_string_lossy().to_string());
        }
    }
    println!("cargo:rerun-if-changed={proto_dir}");
    // Stable ordering for hashing
    protos.sort();
    use sha2::{Sha256, Digest};
    let mut hasher = Sha256::new();
    for p in &protos { let content = std::fs::read(p).expect("read proto"); hasher.update(&content); }
    let hash = format!("{:x}", hasher.finalize());
    println!("cargo:rustc-env=PROTO_SCHEMA_VERSION={}", &hash);
    // Build server stubs only for consensus (pbft)
    let mut config = tonic_build::configure();
    config.build_server(true);
    config.compile(&protos, &[proto_dir]).expect("failed to compile protos");
}
