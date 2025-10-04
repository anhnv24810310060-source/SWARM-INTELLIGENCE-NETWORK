// Standalone VRF test without other dependencies
#![allow(dead_code)]

use std::collections::HashMap;

// Manual SHA-512 placeholder (for demo purposes, not cryptographically secure)
fn simple_hash(data: &[u8]) -> [u8; 64] {
    let mut output = [0u8; 64];
    let mut state = 0x6a09e667f3bcc908u64;
    
    for (i, &byte) in data.iter().enumerate() {
        state = state.wrapping_mul(31).wrapping_add(byte as u64);
        state = state.rotate_left(13) ^ state.rotate_right(7);
        let idx = (i * 8) % 64;
        output[idx] ^= ((state >> (i % 8)) & 0xFF) as u8;
    }
    
    // Mix output
    for i in 0..64 {
        let j = (i * 17 + 13) % 64;
        output[i] ^= output[j];
    }
    
    output
}

// Minimal VRF types
struct VrfOutput([u8; 64]);
struct VrfProof([u8; 80]);

fn generate_vrf_keypair(seed: &[u8]) -> ([u8; 32], [u8; 32]) {
    let mut input = Vec::new();
    input.extend_from_slice(seed);
    input.extend_from_slice(b"vrf-sk");
    let sk_hash = simple_hash(&input);
    
    let mut sk = [0u8; 32];
    sk.copy_from_slice(&sk_hash[..32]);
    
    let mut input2 = Vec::new();
    input2.extend_from_slice(&sk);
    input2.extend_from_slice(b"vrf-pk");
    let pk_hash = simple_hash(&input2);
    
    let mut pk = [0u8; 32];
    pk.copy_from_slice(&pk_hash[..32]);
    
    (sk, pk)
}

fn vrf_prove(sk: &[u8; 32], alpha: &[u8]) -> (VrfProof, VrfOutput) {
    let mut h2c_input = Vec::new();
    h2c_input.extend_from_slice(b"h2c");
    h2c_input.extend_from_slice(alpha);
    let h_point = simple_hash(&h2c_input);
    
    let mut gamma_input = Vec::new();
    gamma_input.extend_from_slice(sk);
    gamma_input.extend_from_slice(&h_point);
    let gamma = simple_hash(&gamma_input);
    
    let mut k_input = Vec::new();
    k_input.extend_from_slice(sk);
    k_input.extend_from_slice(alpha);
    k_input.extend_from_slice(b"nonce");
    let k = simple_hash(&k_input);
    
    let mut c_input = Vec::new();
    c_input.extend_from_slice(b"challenge");
    c_input.extend_from_slice(&gamma);
    c_input.extend_from_slice(&k);
    c_input.extend_from_slice(alpha);
    let c = simple_hash(&c_input);
    
    let mut s_input = Vec::new();
    s_input.extend_from_slice(&k);
    s_input.extend_from_slice(&c);
    s_input.extend_from_slice(sk);
    let s = simple_hash(&s_input);
    
    let mut proof = [0u8; 80];
    proof[..32].copy_from_slice(&gamma[..32]);
    proof[32..48].copy_from_slice(&c[..16]);
    proof[48..].copy_from_slice(&s[..32]);
    
    let mut output_input = Vec::new();
    output_input.extend_from_slice(b"vrf-output");
    output_input.extend_from_slice(&gamma);
    let output_hash = simple_hash(&output_input);
    
    let mut output = [0u8; 64];
    output.copy_from_slice(&output_hash);
    
    (VrfProof(proof), VrfOutput(output))
}

fn select_validator(vrf_output: &VrfOutput, validators: &[(String, u64)]) -> Option<String> {
    if validators.is_empty() { return None; }
    
    let total_stake: u64 = validators.iter().map(|(_, stake)| stake).sum();
    if total_stake == 0 { return None; }
    
    let mut bytes = [0u8; 8];
    bytes.copy_from_slice(&vrf_output.0[..8]);
    let random_value = u64::from_le_bytes(bytes);
    let target = random_value % total_stake;
    
    let mut cumulative = 0u64;
    for (validator_id, stake) in validators {
        cumulative += stake;
        if target < cumulative {
            return Some(validator_id.clone());
        }
    }
    
    validators.last().map(|(id, _)| id.clone())
}

fn main() {
    println!("=== VRF Standalone Tests ===\n");
    
    // Test 1: Determinism
    println!("Test 1: VRF Determinism");
    let (sk, pk) = generate_vrf_keypair(b"validator-1");
    let alpha = b"block-100";
    
    let (proof1, output1) = vrf_prove(&sk, alpha);
    let (proof2, output2) = vrf_prove(&sk, alpha);
    
    assert_eq!(proof1.0, proof2.0, "Proofs should be identical");
    assert_eq!(output1.0, output2.0, "Outputs should be identical");
    println!("✓ VRF is deterministic\n");
    
    // Test 2: Different inputs produce different outputs
    println!("Test 2: Different Inputs");
    let (_, output_a) = vrf_prove(&sk, b"input-A");
    let (_, output_b) = vrf_prove(&sk, b"input-B");
    
    assert_ne!(output_a.0, output_b.0, "Different inputs should produce different outputs");
    println!("✓ Different inputs produce different outputs\n");
    
    // Test 3: Validator selection distribution
    println!("Test 3: Validator Selection Distribution");
    let validators = vec![
        ("node-0".to_string(), 100),
        ("node-1".to_string(), 50),
        ("node-2".to_string(), 25),
    ];
    
    let mut counts = HashMap::new();
    for height in 0u64..1000u64 {
        let mut alpha = Vec::new();
        alpha.extend_from_slice(&height.to_le_bytes());
        
        let (_, output) = vrf_prove(&sk, &alpha);
        
        if let Some(selected) = select_validator(&output, &validators) {
            *counts.entry(selected).or_insert(0) += 1;
        }
    }
    
    let count0 = *counts.get("node-0").unwrap_or(&0);
    let count1 = *counts.get("node-1").unwrap_or(&0);
    let count2 = *counts.get("node-2").unwrap_or(&0);
    
    println!("  node-0 (stake 100/175 = 57%): selected {} times ({}%)", count0, count0 as f64 / 10.0);
    println!("  node-1 (stake  50/175 = 29%): selected {} times ({}%)", count1, count1 as f64 / 10.0);
    println!("  node-2 (stake  25/175 = 14%): selected {} times ({}%)", count2, count2 as f64 / 10.0);
    
    // Check distribution matches stake proportions
    assert!(count0 > 500 && count0 < 650, "node-0 selection out of expected range: {}", count0);
    assert!(count1 > 200 && count1 < 350, "node-1 selection out of expected range: {}", count1);
    assert!(count2 > 80 && count2 < 200, "node-2 selection out of expected range: {}", count2);
    
    println!("✓ Selection distribution matches stake weights\n");
    
    println!("=== All VRF Tests Passed! ===");
}
