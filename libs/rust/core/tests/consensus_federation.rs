use swarm_core::consensus::{PBFTConsensus, NodeId};
use swarm_core::federated_learning::{FederatedLearningCoordinator, AggregationMethod, ClientUpdate};

#[test]
fn pbft_basic_flow() {
    let node = PBFTConsensus::new(NodeId::generate(), 4);
    if let Some(msg) = node.propose([9u8;32]) { let follow = node.handle_message(msg); for m in follow { let _ = node.handle_message(m); } }
}

#[test]
fn federated_round_completion() {
    let fed = FederatedLearningCoordinator::new(3, AggregationMethod::FedAvg);
    let round = fed.current_round();
    for i in 0..3 { let upd = ClientUpdate { node_id: format!("n{i}"), round, weights: vec![i as f32 + 1.0, 2.0], sample_count: 10 }; let agg = fed.submit_update(upd); if i==2 { assert!(agg.is_some()); } }
    assert_eq!(fed.model_version(), 2);
}
