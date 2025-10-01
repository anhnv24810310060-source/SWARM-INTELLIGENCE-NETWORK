from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from libs.python.core.logging_util import init_logging  # type: ignore
import asyncio
import numpy as np
from typing import List, Dict, Optional
import logging

init_logging("federated-orchestrator")
logger = logging.getLogger(__name__)

app = FastAPI(title="Federated Orchestrator")

# Data models
class ModelUpdate(BaseModel):
    node_id: str
    round_id: int
    gradients: List[float]
    samples_count: int
    timestamp: int

class FederatedRound(BaseModel):
    round_id: int
    participants: List[str]
    aggregation_method: str = "fedavg"  # fedavg, fedprox, scaffold
    min_participants: int = 3
    timeout_seconds: int = 300

class RoundStatus(BaseModel):
    round_id: int
    status: str
    participants: List[str]
    updates_received: int
    started_at: Optional[int] = None
    completed_at: Optional[int] = None

# In-memory storage (should be replaced with distributed storage)
current_round: Optional[RoundStatus] = None
model_updates: Dict[int, List[ModelUpdate]] = {}
global_model_version: int = 0

@app.get("/healthz")
async def health():
    return {"status": "ok", "service": "federated-orchestrator"}

@app.post("/rounds/start")
async def start_round(round_config: FederatedRound):
    """Start a new federated learning round"""
    global current_round, model_updates
    
    if current_round and current_round.status == "in_progress":
        raise HTTPException(status_code=400, detail="Round already in progress")
    
    import time
    current_round = RoundStatus(
        round_id=round_config.round_id,
        status="in_progress",
        participants=round_config.participants,
        updates_received=0,
        started_at=int(time.time())
    )
    
    model_updates[round_config.round_id] = []
    
    logger.info(f"Started FL round {round_config.round_id} with {len(round_config.participants)} participants")
    
    # Schedule timeout
    asyncio.create_task(round_timeout(round_config.round_id, round_config.timeout_seconds))
    
    return current_round

@app.post("/rounds/{round_id}/update")
async def submit_update(round_id: int, update: ModelUpdate):
    """Submit model update from a node"""
    global current_round, model_updates
    
    if not current_round or current_round.round_id != round_id:
        raise HTTPException(status_code=404, detail="Round not found or inactive")
    
    if update.node_id not in current_round.participants:
        raise HTTPException(status_code=403, detail="Node not in participant list")
    
    # Check for duplicate submissions
    if any(u.node_id == update.node_id for u in model_updates.get(round_id, [])):
        raise HTTPException(status_code=400, detail="Update already submitted")
    
    model_updates[round_id].append(update)
    current_round.updates_received += 1
    
    logger.info(f"Received update from {update.node_id} for round {round_id}")
    
    # Check if we have enough updates to aggregate
    if current_round.updates_received >= len(current_round.participants):
        await aggregate_and_complete(round_id)
    
    return {"status": "accepted", "round_id": round_id}

@app.get("/rounds/{round_id}/status")
async def get_round_status(round_id: int):
    """Get status of a specific round"""
    if current_round and current_round.round_id == round_id:
        return current_round
    raise HTTPException(status_code=404, detail="Round not found")

@app.get("/model/version")
async def get_model_version():
    """Get current global model version"""
    return {"version": global_model_version, "timestamp": current_round.completed_at if current_round else None}

async def aggregate_and_complete(round_id: int):
    """Aggregate model updates using FedAvg or other methods"""
    global current_round, global_model_version
    
    updates = model_updates.get(round_id, [])
    if not updates:
        logger.warning(f"No updates to aggregate for round {round_id}")
        return
    
    logger.info(f"Aggregating {len(updates)} updates for round {round_id}")
    
    # FedAvg: Weighted average based on number of samples
    total_samples = sum(u.samples_count for u in updates)
    aggregated_gradients = []
    
    # Simple averaging (replace with actual tensor operations)
    gradient_length = len(updates[0].gradients)
    for i in range(gradient_length):
        weighted_sum = sum(
            u.gradients[i] * (u.samples_count / total_samples)
            for u in updates
        )
        aggregated_gradients.append(weighted_sum)
    
    # Update global model
    global_model_version += 1
    
    import time
    if current_round:
        current_round.status = "completed"
        current_round.completed_at = int(time.time())
    
    logger.info(f"Round {round_id} completed. Global model version: {global_model_version}")
    
    # TODO: Publish aggregated model to model-registry
    # TODO: Notify participants about new model

async def round_timeout(round_id: int, timeout_seconds: int):
    """Handle round timeout"""
    await asyncio.sleep(timeout_seconds)
    
    global current_round
    if current_round and current_round.round_id == round_id and current_round.status == "in_progress":
        logger.warning(f"Round {round_id} timed out")
        
        # Aggregate with whatever updates we have
        updates = model_updates.get(round_id, [])
        if len(updates) >= 3:  # Minimum threshold
            await aggregate_and_complete(round_id)
        else:
            current_round.status = "failed"
            logger.error(f"Round {round_id} failed: insufficient updates")

# Additional aggregation methods
class AggregationStrategy:
    @staticmethod
    def fedavg(updates: List[ModelUpdate]) -> List[float]:
        """Federated Averaging"""
        total_samples = sum(u.samples_count for u in updates)
        gradient_length = len(updates[0].gradients)
        return [
            sum(u.gradients[i] * (u.samples_count / total_samples) for u in updates)
            for i in range(gradient_length)
        ]
    
    @staticmethod
    def fedprox(updates: List[ModelUpdate], mu: float = 0.01) -> List[float]:
        """FedProx with proximal term"""
        # Simplified version - actual implementation needs previous model
        return AggregationStrategy.fedavg(updates)
    
    @staticmethod
    def krum(updates: List[ModelUpdate], f: int = 1) -> List[float]:
        """Krum aggregation - Byzantine-robust"""
        # Select update closest to others (Byzantine-robust)
        # Simplified: return median
        gradient_length = len(updates[0].gradients)
        return [
            float(np.median([u.gradients[i] for u in updates]))
            for i in range(gradient_length)
        ]

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8000)
