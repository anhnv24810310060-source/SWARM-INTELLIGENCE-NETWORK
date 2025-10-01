from fastapi import FastAPI

app = FastAPI(title="Federated Orchestrator")

@app.get("/healthz")
async def health():
    return {"status": "ok"}

# TODO: Manage FL rounds, aggregate updates, scheduling
