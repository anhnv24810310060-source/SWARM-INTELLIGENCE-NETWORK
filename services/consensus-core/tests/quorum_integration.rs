// Feature-gated E2E-ish integration test (requires feature=integration and running NATS optional)
// Simulates propose + votes reaching quorum using in-process service (not over network yet)

#[cfg(feature = "integration")]
mod tests {
    use consensus_core::PbftService;
    use swarm_proto::consensus::{Proposal, Vote};
    use tonic::Request;

    #[tokio::test]
    async fn quorum_reached_updates_leader() {
        std::env::set_var("VALIDATOR_SET_SIZE", "4");
        let svc = PbftService::new();
        let _ = svc.propose(Request::new(Proposal { id: "p1".into(), payload: vec![], height: 1, round: 0 })).await.unwrap();
        // Cast votes from 3 distinct validators (quorum = 3 for size 4)
        for n in ["node-0","node-1","node-2"] { let _ = svc.cast_vote(Request::new(Vote { proposal_id: "p1".into(), node_id: n.into(), height: 1, round: 0, vote_type: 0 })).await.unwrap(); }
        let snap = svc.snapshot();
        assert_eq!(snap.height, 1);
        assert_eq!(snap.round, 0);
        assert!(!snap.leader.is_empty());
    }
}
