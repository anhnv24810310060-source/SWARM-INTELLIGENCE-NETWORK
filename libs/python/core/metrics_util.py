import os, logging
from opentelemetry import metrics
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.resources import Resource
from opentelemetry.exporter.otlp.proto.grpc.metric_exporter import OTLPMetricExporter
from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader

_INIT = False

class MetricsBundle:
    def __init__(self, meter):
        self.retry_attempts = meter.create_counter("swarm_resilience_retry_attempts_total")
        self.circuit_open = meter.create_counter("swarm_resilience_circuit_open_total")

def init_metrics(service: str) -> MetricsBundle:
    global _INIT
    if not _INIT:
        endpoint = os.getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") or os.getenv("OTEL_EXPORTER_OTLP_ENDPOINT") or "http://localhost:4317"
        clean = endpoint.replace("http://","").replace("https://","")
        exporter = OTLPMetricExporter(endpoint=clean, insecure=True)
        reader = PeriodicExportingMetricReader(exporter)
        provider = MeterProvider(resource=Resource.create({"service.name": service}), metric_readers=[reader])
        metrics.set_meter_provider(provider)
        logging.getLogger(__name__).info("otel metrics initialized", extra={"endpoint": endpoint})
        _INIT = True
    meter = metrics.get_meter("swarm-python")
    return MetricsBundle(meter)

__all__ = ["init_metrics", "MetricsBundle"]
