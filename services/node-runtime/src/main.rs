use anyhow::{Result, Context};
use tracing::{info, warn};

#[tokio::main]
async fn main() -> Result<()> {
    init_tracing();
    info!(target: "node-runtime", "Starting node-runtime service");
    #[cfg(feature = "wasm_plugins")]
    if let Err(e) = load_wasm_plugins().await { warn!(error=?e, "WASM plugin load failed"); }
    Ok(())
}

fn init_tracing() {
    let _ = tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .try_init();
}

#[cfg(feature = "wasm_plugins")]
async fn load_wasm_plugins() -> Result<()> {
    use wasmtime::{Engine, Module, Store, Linker};
    let dir = std::env::var("WASM_PLUGIN_DIR").unwrap_or_else(|_| "./wasm-plugins".into());
    let engine = Engine::default();
    let mut loaded = 0u32;
    if let Ok(read_dir) = std::fs::read_dir(&dir) {
        for entry in read_dir.flatten() {
            let path = entry.path();
            if path.extension().and_then(|s| s.to_str()) == Some("wasm") {
                let module = Module::from_file(&engine, &path).with_context(|| format!("compile wasm {:?}", path))?;
                let mut linker = Linker::new(&engine);
                let mut store = Store::new(&engine, ());
                let instance = linker.instantiate(&mut store, &module)?;
                // Expect optional exported function process_event(payload_ptr, payload_len)
                if let Some(func) = instance.get_func(&mut store, "process_event") {
                    info!(?path, "Loaded plugin with process_event export");
                    // For now just call with empty payload to validate
                    let _ = func.call(&mut store, &[], &mut []);
                } else {
                    warn!(?path, "Plugin missing process_event export");
                }
                loaded += 1;
            }
        }
    }
    info!(dir=%dir, loaded, "WASM plugin load complete");
    Ok(())
}
