import os, logging
from opentelemetry import trace
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter

_INITIALIZED = False

def init_tracing(service: str):
    global _INITIALIZED
    if _INITIALIZED:
        return
    endpoint = os.getenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317")
    # Strip http:// for grpc OTLP exporter if present
    clean = endpoint.replace("http://","" ).replace("https://","")
    resource = Resource.create({"service.name": service})
    provider = TracerProvider(resource=resource)
    exporter = OTLPSpanExporter(endpoint=clean, insecure=True)
    provider.add_span_processor(BatchSpanProcessor(exporter))
    trace.set_tracer_provider(provider)
    logging.getLogger(__name__).info("otel tracing initialized", extra={"endpoint": endpoint})
    _INITIALIZED = True

def get_tracer():
    return trace.get_tracer("swarm-python")
