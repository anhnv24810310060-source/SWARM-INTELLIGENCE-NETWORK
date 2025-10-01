from fastapi import FastAPI
from libs.python.core.logging_util import init_logging  # type: ignore

init_logging("federated-orchestrator")

app = FastAPI(title="Federated Orchestrator")

@app.get("/healthz")
async def health():
    return {"status": "ok"}

# TODO: Manage FL rounds, aggregate updates, scheduling
