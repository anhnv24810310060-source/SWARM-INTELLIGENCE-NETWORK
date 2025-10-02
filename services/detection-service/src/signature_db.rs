use anyhow::Result;
use sled::Db;
use bloom::BloomFilter;
use std::path::PathBuf;
use std::hash::{Hash, Hasher};
use std::collections::hash_map::DefaultHasher;

#[derive(Clone)]
pub struct SignatureDb {
    db: Db,
    bloom: BloomFilter,
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
        Ok(Self { db, bloom })
    }

    pub fn put_signature(&self, sig: &str, meta: &str) -> Result<()> {
        self.db.insert(sig.as_bytes(), meta.as_bytes())?;
        self.db.flush()?;
        let mut h = DefaultHasher::new();
        sig.hash(&mut h);
        self.bloom.set(h.finish());
        Ok(())
    }

    pub fn match_event(&self, norm: &crate::pipeline::Normalized) -> Result<Option<String>> {
        // For now, derive a simple fingerprint from features length.
        let key = format!("sig:{}", norm.features.len());
        let mut h = DefaultHasher::new();
        key.hash(&mut h);
        let hash = h.finish();
        if !self.bloom.check(hash) { return Ok(None); }
        if let Some(v) = self.db.get(key.as_bytes())? { return Ok(Some(String::from_utf8_lossy(&v).to_string())); }
        Ok(None)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::pipeline::{Normalized};

    #[test]
    fn insert_and_match() {
        let db = SignatureDb::open(Default::default()).unwrap();
        db.put_signature("sig:2", "test-threat").unwrap();
        let norm = Normalized { id: "e".into(), features: vec![0.1, 0.2] };
        let m = db.match_event(&norm).unwrap();
        assert_eq!(m.unwrap(), "test-threat");
    }
}
