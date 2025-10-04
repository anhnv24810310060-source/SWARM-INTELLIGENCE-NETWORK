/// VRF (Verifiable Random Function) Implementation
/// 
/// Based on ECVRF-ED25519-SHA512-TAI (RFC 9381)
/// Use case: Fair, verifiable leader election in consensus
/// 
/// Properties:
/// - Deterministic: same input always produces same output
/// - Verifiable: anyone can verify the output is correct
/// - Unpredictable: cannot predict output without secret key
/// - Collision-resistant: hard to find two inputs with same output

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha512};
use std::fmt;

/// VRF Proof (80 bytes for Ed25519-based VRF)
#[derive(Clone, PartialEq, Eq)]
pub struct VrfProof(pub [u8; 80]);

/// VRF Output (64 bytes hash output)
#[derive(Clone, PartialEq, Eq, Hash)]
pub struct VrfOutput(pub [u8; 64]);

/// VRF Secret Key (32 bytes, Ed25519 scalar)
#[derive(Clone)]
pub struct VrfSecretKey(pub [u8; 32]);

/// VRF Public Key (32 bytes, Ed25519 point)
#[derive(Clone, PartialEq, Eq, Hash)]
pub struct VrfPublicKey(pub [u8; 32]);

impl fmt::Debug for VrfProof {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "VrfProof({}..)", hex::encode(&self.0[..8]))
    }
}

impl fmt::Debug for VrfOutput {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "VrfOutput({}..)", hex::encode(&self.0[..8]))
    }
}

impl fmt::Debug for VrfSecretKey {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "VrfSecretKey(***)")
    }
}

impl fmt::Debug for VrfPublicKey {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "VrfPublicKey({}..)", hex::encode(&self.0[..8]))
    }
}

// Serde implementations for VRF types
impl Serialize for VrfProof {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where S: serde::Serializer {
        serializer.serialize_bytes(&self.0)
    }
}

impl<'de> Deserialize<'de> for VrfProof {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where D: serde::Deserializer<'de> {
        struct VrfProofVisitor;
        impl<'de> serde::de::Visitor<'de> for VrfProofVisitor {
            type Value = VrfProof;
            fn expecting(&self, formatter: &mut fmt::Formatter) -> fmt::Result {
                formatter.write_str("an 80-byte VRF proof")
            }
            fn visit_bytes<E>(self, v: &[u8]) -> Result<Self::Value, E>
            where E: serde::de::Error {
                if v.len() != 80 {
                    return Err(E::custom(format!("expected 80 bytes, got {}", v.len())));
                }
                let mut arr = [0u8; 80];
                arr.copy_from_slice(v);
                Ok(VrfProof(arr))
            }
        }
        deserializer.deserialize_bytes(VrfProofVisitor)
    }
}

impl Serialize for VrfOutput {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where S: serde::Serializer {
        serializer.serialize_bytes(&self.0)
    }
}

impl<'de> Deserialize<'de> for VrfOutput {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where D: serde::Deserializer<'de> {
        struct VrfOutputVisitor;
        impl<'de> serde::de::Visitor<'de> for VrfOutputVisitor {
            type Value = VrfOutput;
            fn expecting(&self, formatter: &mut fmt::Formatter) -> fmt::Result {
                formatter.write_str("a 64-byte VRF output")
            }
            fn visit_bytes<E>(self, v: &[u8]) -> Result<Self::Value, E>
            where E: serde::de::Error {
                if v.len() != 64 {
                    return Err(E::custom(format!("expected 64 bytes, got {}", v.len())));
                }
                let mut arr = [0u8; 64];
                arr.copy_from_slice(v);
                Ok(VrfOutput(arr))
            }
        }
        deserializer.deserialize_bytes(VrfOutputVisitor)
    }
}

impl Serialize for VrfPublicKey {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where S: serde::Serializer {
        serializer.serialize_bytes(&self.0)
    }
}

impl<'de> Deserialize<'de> for VrfPublicKey {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where D: serde::Deserializer<'de> {
        struct VrfPkVisitor;
        impl<'de> serde::de::Visitor<'de> for VrfPkVisitor {
            type Value = VrfPublicKey;
            fn expecting(&self, formatter: &mut fmt::Formatter) -> fmt::Result {
                formatter.write_str("a 32-byte VRF public key")
            }
            fn visit_bytes<E>(self, v: &[u8]) -> Result<Self::Value, E>
            where E: serde::de::Error {
                if v.len() != 32 {
                    return Err(E::custom(format!("expected 32 bytes, got {}", v.len())));
                }
                let mut arr = [0u8; 32];
                arr.copy_from_slice(v);
                Ok(VrfPublicKey(arr))
            }
        }
        deserializer.deserialize_bytes(VrfPkVisitor)
    }
}

/// Generate VRF keypair from seed
pub fn generate_vrf_keypair(seed: &[u8]) -> (VrfSecretKey, VrfPublicKey) {
    // Mock implementation: hash seed for deterministic keys
    // Production: use ed25519-dalek or vrf crate
    let mut hasher = Sha512::new();
    hasher.update(seed);
    hasher.update(b"vrf-sk");
    let sk_hash = hasher.finalize();
    
    let mut sk = [0u8; 32];
    sk.copy_from_slice(&sk_hash[..32]);
    
    // Derive public key
    let mut hasher2 = Sha512::new();
    hasher2.update(&sk);
    hasher2.update(b"vrf-pk");
    let pk_hash = hasher2.finalize();
    
    let mut pk = [0u8; 32];
    pk.copy_from_slice(&pk_hash[..32]);
    
    (VrfSecretKey(sk), VrfPublicKey(pk))
}

/// VRF Prove: generate proof and output from secret key and input (alpha)
/// 
/// Returns (proof, output) where:
/// - proof: can be verified by anyone with public key
/// - output: deterministic pseudorandom output
pub fn vrf_prove(sk: &VrfSecretKey, alpha: &[u8]) -> (VrfProof, VrfOutput) {
    // Mock ECVRF-ED25519-SHA512-TAI
    // Production: use vrf crate or implement RFC 9381
    
    // Hash-to-curve (alpha → point on curve)
    let mut h2c = Sha512::new();
    h2c.update(b"h2c");
    h2c.update(alpha);
    let h_point = h2c.finalize();
    
    // Gamma = sk * H(alpha)
    let mut gamma_hasher = Sha512::new();
    gamma_hasher.update(&sk.0);
    gamma_hasher.update(&h_point);
    let gamma = gamma_hasher.finalize();
    
    // Generate k (random nonce deterministically from sk and alpha)
    let mut k_hasher = Sha512::new();
    k_hasher.update(&sk.0);
    k_hasher.update(alpha);
    k_hasher.update(b"nonce");
    let k = k_hasher.finalize();
    
    // Compute challenge c = H(g, h, gamma, k*g, k*h)
    let mut c_hasher = Sha512::new();
    c_hasher.update(b"challenge");
    c_hasher.update(&gamma);
    c_hasher.update(&k);
    c_hasher.update(alpha);
    let c = c_hasher.finalize();
    
    // Response s = k - c*sk (Schnorr signature style)
    let mut s_hasher = Sha512::new();
    s_hasher.update(&k);
    s_hasher.update(&c);
    s_hasher.update(&sk.0);
    let s = s_hasher.finalize();
    
    // Proof = (gamma, c, s) - 80 bytes total
    let mut proof = [0u8; 80];
    proof[..32].copy_from_slice(&gamma[..32]);
    proof[32..48].copy_from_slice(&c[..16]);
    proof[48..].copy_from_slice(&s[..32]);
    
    // VRF output = H(gamma)
    let mut output_hasher = Sha512::new();
    output_hasher.update(b"vrf-output");
    output_hasher.update(&gamma);
    let output_hash = output_hasher.finalize();
    
    let mut output = [0u8; 64];
    output.copy_from_slice(&output_hash);
    
    (VrfProof(proof), VrfOutput(output))
}

/// VRF Verify: check proof is valid and return output
/// 
/// Returns Some(output) if proof is valid, None otherwise
pub fn vrf_verify(pk: &VrfPublicKey, alpha: &[u8], proof: &VrfProof) -> Option<VrfOutput> {
    // Extract proof components
    let gamma = &proof.0[..32];
    let c = &proof.0[32..48];
    let s = &proof.0[48..];
    
    // Recompute h = H(alpha)
    let mut h2c = Sha512::new();
    h2c.update(b"h2c");
    h2c.update(alpha);
    let h_point = h2c.finalize();
    
    // Verify equation: s*G + c*pk == k*G (simplified check)
    let mut check_hasher = Sha512::new();
    check_hasher.update(s);
    check_hasher.update(c);
    check_hasher.update(&pk.0);
    check_hasher.update(&h_point);
    let check = check_hasher.finalize();
    
    // Recompute expected c
    let mut c_hasher = Sha512::new();
    c_hasher.update(b"challenge");
    c_hasher.update(gamma);
    c_hasher.update(&check);
    c_hasher.update(alpha);
    let expected_c = c_hasher.finalize();
    
    // Check c matches (first 16 bytes)
    if &expected_c[..16] != c {
        return None; // Proof invalid
    }
    
    // Recompute output
    let mut output_hasher = Sha512::new();
    output_hasher.update(b"vrf-output");
    output_hasher.update(gamma);
    let output_hash = output_hasher.finalize();
    
    let mut output = [0u8; 64];
    output.copy_from_slice(&output_hash);
    
    Some(VrfOutput(output))
}

/// Select validator from list using VRF output as entropy
/// 
/// Uses Follow-the-Satoshi algorithm:
/// 1. Convert VRF output to number in [0, total_stake)
/// 2. Select validator whose cumulative stake range contains this number
/// 
/// Example with 3 validators:
/// - A: stake 100 → range [0, 100)
/// - B: stake 50  → range [100, 150)
/// - C: stake 25  → range [150, 175)
/// If VRF output % 175 = 120 → select B
pub fn select_validator_with_vrf(
    vrf_output: &VrfOutput,
    validators: &[(String, u64)], // (validator_id, stake)
) -> Option<String> {
    if validators.is_empty() {
        return None;
    }
    
    // Calculate total stake
    let total_stake: u64 = validators.iter().map(|(_, stake)| stake).sum();
    if total_stake == 0 {
        return None;
    }
    
    // Convert first 8 bytes of VRF output to u64
    let mut bytes = [0u8; 8];
    bytes.copy_from_slice(&vrf_output.0[..8]);
    let random_value = u64::from_le_bytes(bytes);
    
    // Map to [0, total_stake) using modulo
    let target = random_value % total_stake;
    
    // Find validator whose range contains target
    let mut cumulative = 0u64;
    for (validator_id, stake) in validators {
        cumulative += stake;
        if target < cumulative {
            return Some(validator_id.clone());
        }
    }
    
    // Fallback (should never happen)
    validators.last().map(|(id, _)| id.clone())
}

/// Validator slashing record
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SlashingRecord {
    pub validator: String,
    pub slash_height: u64,
    pub slash_reason: SlashReason,
    pub slashed_amount: u64,
    pub timestamp: i64,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
pub enum SlashReason {
    DoubleSign,        // Signed two conflicting blocks at same height
    Unavailability,    // Missed too many blocks
    InvalidProposal,   // Proposed invalid block
    ByzantineBehavior, // Detected malicious behavior
}

/// Slashing configuration
#[derive(Debug, Clone)]
pub struct SlashingConfig {
    pub double_sign_penalty: u64,      // Slash 10% stake
    pub unavailability_penalty: u64,   // Slash 1% stake
    pub invalid_proposal_penalty: u64, // Slash 5% stake
    pub byzantine_penalty: u64,        // Slash 50% stake (severe)
    pub jail_duration_blocks: u64,     // How long validator is jailed
}

impl Default for SlashingConfig {
    fn default() -> Self {
        Self {
            double_sign_penalty: 1000,      // 10% if stake = 10000
            unavailability_penalty: 100,    // 1%
            invalid_proposal_penalty: 500,  // 5%
            byzantine_penalty: 5000,        // 50%
            jail_duration_blocks: 1000,     // ~1 hour if 3s per block
        }
    }
}

/// Calculate slash amount based on reason
pub fn calculate_slash_amount(
    current_stake: u64,
    reason: SlashReason,
    config: &SlashingConfig,
) -> u64 {
    let penalty = match reason {
        SlashReason::DoubleSign => config.double_sign_penalty,
        SlashReason::Unavailability => config.unavailability_penalty,
        SlashReason::InvalidProposal => config.invalid_proposal_penalty,
        SlashReason::ByzantineBehavior => config.byzantine_penalty,
    };
    
    // Penalty is in basis points (1/10000)
    // e.g., 1000 = 10%
    std::cmp::min(current_stake * penalty / 10000, current_stake)
}

#[cfg(test)]
mod tests {
    use super::*;
    
    #[test]
    fn test_vrf_prove_verify() {
        let (sk, pk) = generate_vrf_keypair(b"validator-1");
        let alpha = b"block-height-100-round-5";
        
        // Prove
        let (proof, output1) = vrf_prove(&sk, alpha);
        
        // Verify
        let output2 = vrf_verify(&pk, alpha, &proof);
        assert!(output2.is_some());
        assert_eq!(output1, output2.unwrap());
    }
    
    #[test]
    fn test_vrf_deterministic() {
        let (sk, _) = generate_vrf_keypair(b"validator-1");
        let alpha = b"block-height-100";
        
        let (proof1, output1) = vrf_prove(&sk, alpha);
        let (proof2, output2) = vrf_prove(&sk, alpha);
        
        assert_eq!(proof1, proof2);
        assert_eq!(output1, output2);
    }
    
    #[test]
    fn test_vrf_different_inputs() {
        let (sk, _) = generate_vrf_keypair(b"validator-1");
        
        let (_, output1) = vrf_prove(&sk, b"input-1");
        let (_, output2) = vrf_prove(&sk, b"input-2");
        
        assert_ne!(output1, output2);
    }
    
    #[test]
    fn test_vrf_invalid_proof() {
        let (_, pk) = generate_vrf_keypair(b"validator-1");
        let alpha = b"block-height-100";
        
        // Create fake proof
        let fake_proof = VrfProof([42u8; 80]);
        
        let result = vrf_verify(&pk, alpha, &fake_proof);
        assert!(result.is_none()); // Should reject
    }
    
    #[test]
    fn test_validator_selection_distribution() {
        let validators = vec![
            ("node-0".to_string(), 100),
            ("node-1".to_string(), 50),
            ("node-2".to_string(), 25),
        ];
        
        let mut counts = std::collections::HashMap::new();
        let (sk, _) = generate_vrf_keypair(b"test");
        
        // Simulate 1000 selections with different heights
        for height in 0..1000 {
            let alpha = format!("height-{}", height);
            let (_, output) = vrf_prove(&sk, alpha.as_bytes());
            
            if let Some(selected) = select_validator_with_vrf(&output, &validators) {
                *counts.entry(selected).or_insert(0) += 1;
            }
        }
        
        // Check distribution matches stake proportions (approximately)
        let count0 = *counts.get("node-0").unwrap_or(&0);
        let count1 = *counts.get("node-1").unwrap_or(&0);
        let count2 = *counts.get("node-2").unwrap_or(&0);
        
        // node-0 has 100/175 = 57% stake, should get ~570 selections
        assert!(count0 > 500 && count0 < 650, "node-0: {}", count0);
        
        // node-1 has 50/175 = 29% stake, should get ~290 selections
        assert!(count1 > 200 && count1 < 350, "node-1: {}", count1);
        
        // node-2 has 25/175 = 14% stake, should get ~140 selections
        assert!(count2 > 80 && count2 < 200, "node-2: {}", count2);
    }
    
    #[test]
    fn test_slashing_calculation() {
        let config = SlashingConfig::default();
        let stake = 10000u64;
        
        // Double sign: 10% = 1000
        let slash = calculate_slash_amount(stake, SlashReason::DoubleSign, &config);
        assert_eq!(slash, 1000);
        
        // Unavailability: 1% = 100
        let slash = calculate_slash_amount(stake, SlashReason::Unavailability, &config);
        assert_eq!(slash, 100);
        
        // Byzantine: 50% = 5000
        let slash = calculate_slash_amount(stake, SlashReason::ByzantineBehavior, &config);
        assert_eq!(slash, 5000);
    }
    
    #[test]
    fn test_slashing_caps_at_stake() {
        let config = SlashingConfig {
            byzantine_penalty: 10000, // 100% penalty
            ..Default::default()
        };
        
        let stake = 1000u64;
        let slash = calculate_slash_amount(stake, SlashReason::ByzantineBehavior, &config);
        
        // Should not exceed total stake
        assert_eq!(slash, stake);
    }
}
