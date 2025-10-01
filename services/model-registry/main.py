from fastapi import FastAPI

app = FastAPI(title="Model Registry")

@app.get("/healthz")
async def health():
    return {"status": "ok"}

# TODO: Model upload, version list, signed retrieval
