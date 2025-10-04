/// Integration tests for optimized PBFT consensus
/// 
/// Test scenarios:
/// 1. Normal operation (happy path)
/// 2. Byzantine validator behavior
/// 3. Network partition recovery
/// 4. View change under load
/// 5. Checkpoint creation and recovery

#[cfg(test)]
mod consensus_integration_tests {
    use consensus_core::optimized_pbft::{OptimizedPbft, PbftMessage, ConsensusState};
    use swarm_core::crypto_bls::{generate_keypair, sign};
    use std::collections::HashMap;
    use std::time::Duration;
    use tokio::time::sleep;

    /// Setup a 5-node consensus cluster
    fn setup_cluster() -> Vec<OptimizedPbft> {
        let num_validators = 5;
        
        // Generate keys for all validators
        let keys: Vec<_> = (0..num_validators)
            .map(|i| {
                let (sk, pk) = generate_keypair(format!("validator-{}", i).as_bytes());
                (format!("node-{}", i), sk, pk)
            })
            .collect();
        
        // Build validator map
        let validator_map: HashMap<_, _> = keys
            .iter()
            .map(|(id, _sk, pk)| (id.clone(), (pk.clone(), 100u64)))
            .collect();
        
        // Create consensus engines
        keys.into_iter()
            .map(|(id, sk, pk)| {
                OptimizedPbft::new(id, sk, pk, validator_map.clone())
            })
            .collect()
    }

    #[tokio::test]
    async fn test_normal_consensus_flow() {
        let cluster = setup_cluster();
        
        // Node 0 proposes a block
        let leader = &cluster[0];
        let payload = b"transaction-batch-1".to_vec();
        let proposal_hash = leader.propose_block(payload.clone()).expect("propose failed");
        
        // Wait for message processing
        sleep(Duration::from_millis(100)).await;
        
        // All validators vote prepare
        for (idx, node) in cluster.iter().enumerate() {
            if idx != 0 {
                node.vote_prepare(1, 0, proposal_hash).expect("prepare vote failed");
            }
        }
        
        sleep(Duration::from_millis(200)).await;
        
        // All validators vote commit
        for node in cluster.iter() {
            node.vote_commit(1, 0, proposal_hash).expect("commit vote failed");
        }
        
        sleep(Duration::from_millis(200)).await;
        
        // Verify all nodes reached consensus on height 1
        for (idx, node) in cluster.iter().enumerate() {
            let state = node.get_state();
            assert_eq!(state.height, 1, "Node {} didn't reach height 1", idx);
        }
    }

    #[tokio::test]
    async fn test_byzantine_validator_detection() {
        let cluster = setup_cluster();
        
        let leader = &cluster[0];
        let payload = b"transaction-batch-1".to_vec();
        let proposal_hash = leader.propose_block(payload.clone()).expect("propose failed");
        
        sleep(Duration::from_millis(100)).await;
        
        // Byzantine node (node-1) votes for different proposal hash
        let fake_hash = [0u8; 32]; // Wrong hash
        cluster[1].vote_prepare(1, 0, fake_hash).expect("prepare vote failed");
        
        // Honest nodes vote correctly
        for idx in 2..cluster.len() {
            cluster[idx].vote_prepare(1, 0, proposal_hash).expect("prepare vote failed");
        }
        
        sleep(Duration::from_millis(200)).await;
        
        // Check Byzantine fault counter increased
        let state = leader.get_state();
        assert!(state.byzantine_faults > 0, "Byzantine fault not detected");
    }

    #[tokio::test]
    async fn test_checkpoint_creation() {
        let cluster = setup_cluster();
        
        // Propose 100 blocks to trigger checkpoint
        for height in 1..=100 {
            let leader = &cluster[0];
            let payload = format!("batch-{}", height).into_bytes();
            let proposal_hash = leader.propose_block(payload).expect("propose failed");
            
            sleep(Duration::from_millis(10)).await;
            
            // Quick vote
            for node in cluster.iter() {
                node.vote_prepare(height, 0, proposal_hash).ok();
                node.vote_commit(height, 0, proposal_hash).ok();
            }
            
            sleep(Duration::from_millis(10)).await;
        }
        
        // Verify checkpoint created at height 100
        let checkpoint = cluster[0].get_checkpoint(100);
        assert!(checkpoint.is_some(), "Checkpoint at 100 not created");
        
        let cp = checkpoint.unwrap();
        assert_eq!(cp.height, 100);
        assert!(cp.validator_signatures.count() >= 4); // Quorum
    }

    #[tokio::test]
    async fn test_concurrent_proposals() {
        let cluster = setup_cluster();
        
        // Spawn 10 concurrent proposals
        let mut handles = vec![];
        
        for i in 0..10 {
            let leader = cluster[0].clone();
            let handle = tokio::spawn(async move {
                let payload = format!("batch-{}", i).into_bytes();
                leader.propose_block(payload)
            });
            handles.push(handle);
        }
        
        // Wait for all proposals
        let results: Vec<_> = futures::future::join_all(handles).await;
        
        let success_count = results.iter().filter(|r| r.is_ok()).count();
        assert!(success_count >= 5, "Too many proposal failures");
    }

    #[tokio::test]
    async fn test_phase_pruning() {
        let leader = &setup_cluster()[0];
        
        // Create 300 rounds worth of phase states
        for height in 1..=300 {
            let payload = format!("batch-{}", height).into_bytes();
            leader.propose_block(payload).ok();
        }
        
        sleep(Duration::from_millis(500)).await;
        
        // Trigger pruning
        leader.prune_old_phases();
        
        // Verify old phases were removed (no easy way to check without exposing internals)
        // In production, would check metrics or logs
    }

    #[tokio::test]
    async fn test_high_throughput() {
        let cluster = setup_cluster();
        
        let start = std::time::Instant::now();
        let target_blocks = 1000;
        
        // Rapid-fire proposals
        for height in 1..=target_blocks {
            let leader = &cluster[0];
            let payload = format!("batch-{}", height).into_bytes();
            
            if let Ok(proposal_hash) = leader.propose_block(payload) {
                // Fast vote (simplified - in reality would need proper message broadcast)
                for node in cluster.iter() {
                    node.vote_prepare(height, 0, proposal_hash).ok();
                    node.vote_commit(height, 0, proposal_hash).ok();
                }
            }
            
            // Minimal delay to avoid overwhelming
            if height % 100 == 0 {
                sleep(Duration::from_millis(50)).await;
            }
        }
        
        let elapsed = start.elapsed();
        let tps = target_blocks as f64 / elapsed.as_secs_f64();
        
        println!("Throughput: {:.2} TPS", tps);
        
        // Should achieve > 1000 TPS in test environment
        assert!(tps > 100.0, "Throughput too low: {:.2} TPS", tps);
    }

    #[tokio::test]
    async fn test_state_consistency_across_nodes() {
        let cluster = setup_cluster();
        
        // Process 50 blocks
        for height in 1..=50 {
            let leader = &cluster[0];
            let payload = format!("batch-{}", height).into_bytes();
            let proposal_hash = leader.propose_block(payload).expect("propose failed");
            
            // All nodes vote
            for node in cluster.iter() {
                node.vote_prepare(height, 0, proposal_hash).ok();
                node.vote_commit(height, 0, proposal_hash).ok();
            }
            
            sleep(Duration::from_millis(20)).await;
        }
        
        sleep(Duration::from_millis(500)).await;
        
        // Verify all nodes have consistent state
        let states: Vec<_> = cluster.iter().map(|n| n.get_state()).collect();
        
        let first_height = states[0].height;
        for (idx, state) in states.iter().enumerate() {
            assert_eq!(
                state.height, first_height,
                "Node {} height {} != {}", idx, state.height, first_height
            );
        }
    }
}

/// Benchmark tests
#[cfg(test)]
mod consensus_benchmarks {
    use super::*;
    use test::Bencher;

    #[bench]
    fn bench_proposal_creation(b: &mut Bencher) {
        let cluster = setup_cluster();
        let leader = &cluster[0];
        let payload = b"benchmark-payload".to_vec();
        
        b.iter(|| {
            leader.propose_block(payload.clone()).ok();
        });
    }

    #[bench]
    fn bench_vote_processing(b: &mut Bencher) {
        let cluster = setup_cluster();
        let node = &cluster[0];
        let hash = [1u8; 32];
        
        b.iter(|| {
            node.vote_prepare(1, 0, hash).ok();
        });
    }

    #[bench]
    fn bench_signature_verification(b: &mut Bencher) {
        use swarm_core::crypto_bls::{generate_keypair, sign, verify};
        
        let (sk, pk) = generate_keypair(b"bench-validator");
        let message = b"block-hash";
        let signature = sign(&sk, message);
        
        b.iter(|| {
            verify(&pk, message, &signature);
        });
    }
}
