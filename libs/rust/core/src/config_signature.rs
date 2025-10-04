//! Configuration signature verification (Ed25519 baseline)
//! Future roadmap: hybrid Ed25519 + Dilithium, canonicalization stable ordering.

use sha2::{Sha256, Digest};
use ed25519_dalek::{Signature, VerifyingKey, Verifier};

// Attempt to canonicalize by trimming trailing whitespace. Full canonical YAML planned.
fn canonicalize(input: &str) -> Vec<u8> { input.replace("\r\n", "\n").trim_end().as_bytes().to_vec() }

pub fn verify_config_signature(raw: &str, provided_sig: &str) -> bool {
    let pk_b64 = match std::env::var("SWARM_CONFIG_PUBKEY") { Ok(v) => v, Err(_) => return false };
    let pk_bytes = match base64::prelude::BASE64_STANDARD.decode(pk_b64) { Ok(b) => b, Err(_) => return false };
    let vk = match VerifyingKey::from_bytes(&pk_bytes.try_into().ok()?) { Ok(v) => v, Err(_) => return false };
    // signature may be base64 or hex
    let sig_bytes = if let Ok(b) = base64::prelude::BASE64_STANDARD.decode(provided_sig) { b } else if let Ok(b) = hex::decode(provided_sig) { b } else { return false };
    let sig = match Signature::from_slice(&sig_bytes) { Ok(s) => s, Err(_) => return false };
    let canon = canonicalize(raw);
    // hash message then sign/verify raw hash (domain separation tag)
    let mut h = Sha256::new();
    h.update(b"SWARM-CONFIG:v1:");
    h.update(&canon);
    let digest = h.finalize();
    vk.verify(&digest, &sig).is_ok()
}
