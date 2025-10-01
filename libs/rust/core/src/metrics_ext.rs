//! Extended metrics registration for consensus, federated learning, autoscaling, resilience.
//!
//! Provides structured groups so services can selectively opt-in.

use once_cell::sync::Lazy;
use opentelemetry::metrics::{Meter, Counter, Histogram, Unit};

pub struct ConsensusMetrics {
    pub proposals_total: Counter<u64>,
    pub prepares_total: Counter<u64>,
    pub commits_total: Counter<u64>,
    pub view_changes_total: Counter<u64>,
    pub phase_latency_ms: Histogram<f64>,
}

pub struct FederatedLearningMetrics {
    pub rounds_total: Counter<u64>,
    pub participants_total: Counter<u64>,
    pub rejected_gradients_total: Counter<u64>,
    pub aggregation_latency_ms: Histogram<f64>,
}

pub struct AutoscaleMetrics {
    pub decisions_total: Counter<u64>,
    pub scale_out_total: Counter<u64>,
    pub scale_in_total: Counter<u64>,
    pub evaluation_latency_ms: Histogram<f64>,
}

pub struct ResilienceAdvMetrics {
    pub retries_total: Counter<u64>,
    pub breaker_open_total: Counter<u64>,
    pub breaker_half_open_total: Counter<u64>,
    pub breaker_close_total: Counter<u64>,
    pub retry_delay_ms: Histogram<f64>,
}

pub struct ExtendedMetrics {
    pub consensus: ConsensusMetrics,
    pub fl: FederatedLearningMetrics,
    pub autoscale: AutoscaleMetrics,
    pub resilience_adv: ResilienceAdvMetrics,
}

static EXT_METER: Lazy<Meter> = Lazy::new(|| opentelemetry::global::meter("swarm_ext"));

pub static EXTENDED_METRICS: Lazy<ExtendedMetrics> = Lazy::new(|| {
    ExtendedMetrics {
        consensus: ConsensusMetrics {
            proposals_total: EXT_METER.u64_counter("swarm_consensus_proposals_total").with_description("Total consensus proposals initiated").init(),
            prepares_total: EXT_METER.u64_counter("swarm_consensus_prepares_total").with_description("Total prepare messages handled").init(),
            commits_total: EXT_METER.u64_counter("swarm_consensus_commits_total").with_description("Total commit messages handled").init(),
            view_changes_total: EXT_METER.u64_counter("swarm_consensus_view_changes_total").with_description("Total view changes (future)").init(),
            phase_latency_ms: EXT_METER.f64_histogram("swarm_consensus_phase_latency_ms").with_description("Latency per consensus phase ms").with_unit(Unit::new("ms")).init(),
        },
        fl: FederatedLearningMetrics {
            rounds_total: EXT_METER.u64_counter("swarm_fl_rounds_total").with_description("Total federated learning rounds").init(),
            participants_total: EXT_METER.u64_counter("swarm_fl_participants_total").with_description("Total participants aggregated").init(),
            rejected_gradients_total: EXT_METER.u64_counter("swarm_fl_rejected_gradients_total").with_description("Gradients rejected (validation/security)").init(),
            aggregation_latency_ms: EXT_METER.f64_histogram("swarm_fl_aggregation_latency_ms").with_description("Aggregation latency ms").with_unit(Unit::new("ms")).init(),
        },
        autoscale: AutoscaleMetrics {
            decisions_total: EXT_METER.u64_counter("swarm_autoscale_decisions_total").with_description("Total autoscale evaluations resulting in action or no-op").init(),
            scale_out_total: EXT_METER.u64_counter("swarm_autoscale_scale_out_total").with_description("Scale-out decisions").init(),
            scale_in_total: EXT_METER.u64_counter("swarm_autoscale_scale_in_total").with_description("Scale-in decisions").init(),
            evaluation_latency_ms: EXT_METER.f64_histogram("swarm_autoscale_evaluation_latency_ms").with_description("Autoscaler evaluation latency ms").with_unit(Unit::new("ms")).init(),
        },
        resilience_adv: ResilienceAdvMetrics {
            retries_total: EXT_METER.u64_counter("swarm_resilience_retries_total").with_description("Total retry attempts (successful + failed)").init(),
            breaker_open_total: EXT_METER.u64_counter("swarm_resilience_breaker_open_total").with_description("Circuit breaker transitions to open").init(),
            breaker_half_open_total: EXT_METER.u64_counter("swarm_resilience_breaker_half_open_total").with_description("Circuit breaker transitions to half-open").init(),
            breaker_close_total: EXT_METER.u64_counter("swarm_resilience_breaker_close_total").with_description("Circuit breaker transitions to closed").init(),
            retry_delay_ms: EXT_METER.f64_histogram("swarm_resilience_retry_delay_ms").with_description("Observed retry delays ms").with_unit(Unit::new("ms")).init(),
        }
    }
});
