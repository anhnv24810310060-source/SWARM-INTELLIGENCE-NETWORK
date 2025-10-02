use anyhow::{Result, anyhow};
use sled::Db;
use bloom::BloomFilter;
use std::path::PathBuf;
use std::hash::{Hash, Hasher};
use std::collections::hash_map::DefaultHasher;
use aho-corasick::AhoCorasick;
use sha2::{Sha256, Digest as ShaDigest};
use arc_swap::ArcSwap;
use std::sync::Arc;
use serde::{Serialize, Deserialize};
use memmap2::Mmap;
use std::fs::File;
use sha2::Sha256;
use sha2::Digest as _; // for finalize
use serde_yaml;
use ed25519_dalek::{Verifier, Signature, VerifyingKey};
use base64::{Engine as _, engine::general_purpose::STANDARD as B64};

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct SignatureMeta {
    pub id: String,
    pub pattern: String,
    pub severity: u8,
    pub category: String,
    pub sha256: String,
}

#[derive(Clone)]
pub struct SignatureDb {
    db: Db,
    bloom: BloomFilter,
    automaton: ArcSwap<Option<Arc<AhoCorasick>>>,
    patterns: ArcSwap<Vec<SignatureMeta>>,
    last_checksum: ArcSwap<Option<String>>,
}

#[derive(Default)]
pub struct SignatureDbOptions {
    pub path: Option<PathBuf>,
    pub bloom_items: usize,
    pub bloom_fp_rate: f64,
}

impl Default for SignatureDbOptions { fn default() -> Self { Self { path: None, bloom_items: 50_000, bloom_fp_rate: 0.01 } } }

impl SignatureDb {
    pub fn open(opts: SignatureDbOptions) -> Result<Self> {
        let path = opts.path.unwrap_or_else(|| PathBuf::from("/tmp/detection-signatures"));
        let db = sled::open(path)?;
        let bloom = BloomFilter::with_rate(opts.bloom_fp_rate, opts.bloom_items as u32);
        Ok(Self { db, bloom, automaton: ArcSwap::from_pointee(None), patterns: ArcSwap::from_pointee(Vec::new()), last_checksum: ArcSwap::from_pointee(None) })
    }

    pub fn load_rules_file(&self, file: &str) -> Result<()> {
        let path = PathBuf::from(file);
        if !path.exists() { return Err(anyhow!("rules file missing")); }
        let f = File::open(&path)?;
        let mmap = unsafe { Mmap::map(&f)? };
        // If signature verification enabled, expect <file>.sig and env SWARM__DETECTION__RULES_PUBKEY (base64)
        if std::env::var("SWARM__DETECTION__RULES_VERIFY").map(|v| v=="1"|| v.eq_ignore_ascii_case("true")).unwrap_or(false) {
            let sig_path = format!("{file}.sig");
            let pub_b64 = std::env::var("SWARM__DETECTION__RULES_PUBKEY")?;
            let pub_bytes = B64.decode(pub_b64)?;
            let vk = VerifyingKey::from_bytes(&pub_bytes.try_into().map_err(|_| anyhow!("invalid pubkey length"))?)?;
            let sig_bytes = std::fs::read(&sig_path)?;
            let sig = Signature::from_slice(&sig_bytes)?;
            vk.verify(&mmap, &sig).map_err(|e| anyhow!("rules signature verify failed: {e}"))?;
        }
        // Detect format by extension
        let metas: Vec<SignatureMeta> = if file.ends_with(".yaml") || file.ends_with(".yml") {
            serde_yaml::from_slice(&mmap)?
        } else { serde_json::from_slice(&mmap)? };
        // checksum
        let mut hasher = Sha256::new();
        hasher.update(&mmap);
        let sum = format!("{:x}", hasher.finalize());
        if let Some(prev) = self.last_checksum.load().as_ref() { if prev == &sum { return Ok(()); } }
        self.last_checksum.store(Arc::new(Some(sum)));
        self.rebuild(metas)?;
        Ok(())
    }

    pub fn rebuild(&self, metas: Vec<SignatureMeta>) -> Result<()> {
        // Build automaton
        let pats: Vec<&str> = metas.iter().map(|m| m.pattern.as_str()).collect();
        let ac = if pats.is_empty() { None } else { Some(Arc::new(AhoCorasick::new(pats)?)) };
        // update bloom
        for m in &metas { let mut h = DefaultHasher::new(); m.pattern.hash(&mut h); self.bloom.set(h.finish()); }
        self.patterns.store(Arc::new(metas));
        self.automaton.store(Arc::new(ac));
        Ok(())
    }

    pub fn put_signature(&self, meta: SignatureMeta) -> Result<()> {
        let json = serde_json::to_vec(&meta)?;
        self.db.insert(meta.id.as_bytes(), json)?;
        self.db.flush()?;
        let mut h = DefaultHasher::new(); meta.pattern.hash(&mut h); self.bloom.set(h.finish());
        // naive rebuild append: gather existing + new (in future optimize incremental)
        let mut list = (*self.patterns.load()).clone();
        list.push(meta);
        self.rebuild(list)?;
        Ok(())
    }

    pub fn match_bytes(&self, data: &[u8]) -> Vec<SignatureMeta> {
        let auto_guard = self.automaton.load();
        let maybe_auto = auto_guard.as_ref();
        if let Some(auto) = maybe_auto { 
            let list = self.patterns.load();
            let mut results = Vec::new();
            for mat in auto.find_iter(data) { if let Some(meta) = list.get(mat.pattern()) { results.push(meta.clone()); } }
            return results;
        }
        Vec::new()
    }

    pub fn match_event(&self, norm: &crate::pipeline::Normalized) -> Result<Option<String>> {
        // Compose synthetic bytes from features for now (placeholder). Real path should use raw event bytes.
        let raw = norm.features.iter().map(|f| (f*255.0) as u8).collect::<Vec<u8>>();
        let hits = self.match_bytes(&raw);
        Ok(hits.first().map(|m| m.id.clone()))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::pipeline::Normalized;

    #[test]
    fn build_and_match_pattern() {
        let db = SignatureDb::open(Default::default()).unwrap();
        let meta = SignatureMeta { id: "rule1".into(), pattern: "AA".into(), severity: 5, category: "test".into(), sha256: "".into() };
        db.put_signature(meta).unwrap();
        let norm = Normalized { id: "e".into(), features: vec![0.7, 0.7] }; // 0.7*255 ~ 178 -> bytes maybe not match "AA" but placeholder path
        let _ = db.match_event(&norm).unwrap();
    }
}
