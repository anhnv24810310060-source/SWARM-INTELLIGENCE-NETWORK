from fastapi import FastAPI
from libs.python.core.logging_util import init_logging  # type: ignore
from libs.python.core.tracing_util import init_tracing, get_tracer  # type: ignore

init_logging("model-registry")
init_tracing("model-registry")
tracer = get_tracer()

app = FastAPI(title="Model Registry")

@app.get("/healthz")
async def health():
    with tracer.start_as_current_span("healthz"):
        return {"status": "ok"}

# TODO: Model upload, version list, signed retrieval
