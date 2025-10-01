use std::{fs, path::Path, time::{SystemTime, UNIX_EPOCH}};
use serde::Deserialize;
use regex::Regex;
use parking_lot::RwLock;
use std::sync::Arc;
use thiserror::Error;
use sha2::{Digest, Sha256};
use ed25519_dalek::{Verifier, PublicKey, Signature};

#[derive(Debug, Error)]
pub enum RuleError {
    #[error("io error: {0}")] Io(#[from] std::io::Error),
    #[error("serde error: {0}")] Serde(#[from] serde_yaml::Error),
    #[error("invalid signature")] InvalidSignature,
    #[error("regex compile failed: {0}")] Regex(String),
}

#[derive(Debug, Deserialize, Clone)]
pub struct DetectionRule {
    pub id: String,
    pub pattern: String,
    pub severity: Option<String>,
    #[serde(default)]
    pub action: Option<String>,
}

#[derive(Clone)]
pub struct CompiledRule {
    pub raw: DetectionRule,
    pub regex: Regex,
}

#[derive(Default, Clone)]
pub struct RuleSet {
    pub rules: Arc<RwLock<Vec<CompiledRule>>>,
    pub version_hash: Arc<RwLock<String>>, // sha256 of file
    pub loaded_ts: Arc<RwLock<u64>>,
}

impl RuleSet {
    pub fn new() -> Self { Self::default() }
    pub fn swap(&self, new_rules: Vec<CompiledRule>, hash: String) {
        *self.rules.write() = new_rules;
        *self.version_hash.write() = hash;
        *self.loaded_ts.write() = SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_secs();
    }
    pub fn list(&self) -> Vec<String> { self.rules.read().iter().map(|r| r.raw.id.clone()).collect() }
}

#[derive(Debug, Deserialize)]
struct FileBundle {
    rules: Vec<DetectionRule>,
    #[serde(default)]
    signature: Option<String>, // hex or base64 signature of sha256(rules_yaml_without_signature)
    #[serde(default)]
    public_key: Option<String>, // hex or base64 public key (ed25519) if distribution is self-contained
}

fn parse_key(pk: &str) -> Option<PublicKey> {
    let bytes = if let Ok(b) = hex::decode(pk) { b } else { base64::decode(pk).ok()? };
    PublicKey::from_bytes(&bytes).ok()
}

fn parse_sig(sig: &str) -> Option<Signature> {
    let bytes = if let Ok(b) = hex::decode(sig) { b } else { base64::decode(sig).ok()? };
    Signature::from_bytes(&bytes.try_into().ok()?).ok()
}

pub fn load_rules(path: &str, verify: bool, external_pubkey: Option<&str>) -> Result<Vec<CompiledRule>, RuleError> {
    let content = fs::read_to_string(path)?;
    let bundle: FileBundle = serde_yaml::from_str(&content)?;

    if verify {
        if let Some(sig_str) = &bundle.signature {
            // Compute hash of rules section only (remove signature/public_key lines). Simplified: use full file minus signature line.
            let mut hasher = Sha256::new();
            hasher.update(content.replace(sig_str, ""));
            let digest = hasher.finalize();
            // Determine public key
            let pk_str = external_pubkey.or(bundle.public_key.as_deref())
                .ok_or(RuleError::InvalidSignature)?;
            let pk = parse_key(pk_str).ok_or(RuleError::InvalidSignature)?;
            let sig = parse_sig(sig_str).ok_or(RuleError::InvalidSignature)?;
            pk.verify(&digest, &sig).map_err(|_| RuleError::InvalidSignature)?;
        }
    }

    let mut compiled = Vec::new();
    for r in bundle.rules.into_iter() {
        let regex = Regex::new(&r.pattern).map_err(|e| RuleError::Regex(e.to_string()))?;
        compiled.push(CompiledRule { raw: r, regex });
    }
    Ok(compiled)
}
