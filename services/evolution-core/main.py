from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from libs.python.core.logging_util import init_logging  # type: ignore
import random
import logging
from typing import List, Dict, Optional
import numpy as np

init_logging("evolution-core")
logger = logging.getLogger(__name__)

app = FastAPI(title="Evolution Core")

# Genetic Algorithm for Detection Rules
class DetectionRule(BaseModel):
    id: str
    pattern: str
    severity: str
    confidence: float
    fitness: float = 0.0
    generation: int = 0

class GAConfig(BaseModel):
    population_size: int = 50
    generations: int = 100
    mutation_rate: float = 0.1
    crossover_rate: float = 0.7
    elite_size: int = 5

class PSOConfig(BaseModel):
    n_particles: int = 30
    n_iterations: int = 100
    w: float = 0.7  # inertia weight
    c1: float = 1.5  # cognitive parameter
    c2: float = 1.5  # social parameter

class OptimizationResult(BaseModel):
    best_solution: List[float]
    best_fitness: float
    iterations: int
    convergence_history: List[float]

# Global state
population: List[DetectionRule] = []
pso_particles: List[Dict] = []

@app.get("/healthz")
async def health():
    return {"status": "ok", "service": "evolution-core"}

@app.post("/ga/initialize")
async def initialize_population(config: GAConfig):
    """Initialize GA population with random detection rules"""
    global population
    population = []
    
    severity_levels = ["critical", "high", "medium", "low"]
    patterns = ["*.exe", "*.dll", "*.ps1", "*.sh", "*.bat", "cmd", "powershell"]
    
    for i in range(config.population_size):
        rule = DetectionRule(
            id=f"rule-{i}",
            pattern=random.choice(patterns),
            severity=random.choice(severity_levels),
            confidence=random.uniform(0.5, 1.0),
            fitness=0.0,
            generation=0
        )
        population.append(rule)
    
    logger.info(f"Initialized GA population with {len(population)} rules")
    return {"population_size": len(population), "config": config}

@app.post("/ga/evolve")
async def evolve_population(fitness_scores: Dict[str, float]):
    """Evolve population based on fitness scores"""
    global population
    
    if not population:
        raise HTTPException(status_code=400, detail="Population not initialized")
    
    # Update fitness scores
    for rule in population:
        if rule.id in fitness_scores:
            rule.fitness = fitness_scores[rule.id]
    
    # Sort by fitness (descending)
    population.sort(key=lambda x: x.fitness, reverse=True)
    
    # Selection: Keep elite
    elite = population[:5]
    
    # Crossover and mutation
    new_population = elite.copy()
    
    while len(new_population) < len(population):
        # Tournament selection
        parent1 = tournament_select(population)
        parent2 = tournament_select(population)
        
        # Crossover
        if random.random() < 0.7:
            child = crossover(parent1, parent2)
        else:
            child = parent1
        
        # Mutation
        if random.random() < 0.1:
            child = mutate(child)
        
        child.generation += 1
        new_population.append(child)
    
    population = new_population
    
    best_fitness = max(r.fitness for r in population)
    avg_fitness = sum(r.fitness for r in population) / len(population)
    
    logger.info(f"Evolution complete. Best: {best_fitness:.4f}, Avg: {avg_fitness:.4f}")
    
    return {
        "best_fitness": best_fitness,
        "avg_fitness": avg_fitness,
        "population_size": len(population),
        "best_rules": [r.dict() for r in population[:5]]
    }

def tournament_select(population: List[DetectionRule], k: int = 3) -> DetectionRule:
    """Tournament selection"""
    tournament = random.sample(population, k)
    return max(tournament, key=lambda x: x.fitness)

def crossover(parent1: DetectionRule, parent2: DetectionRule) -> DetectionRule:
    """Single-point crossover"""
    child = DetectionRule(
        id=f"rule-{random.randint(1000, 9999)}",
        pattern=random.choice([parent1.pattern, parent2.pattern]),
        severity=random.choice([parent1.severity, parent2.severity]),
        confidence=(parent1.confidence + parent2.confidence) / 2,
        fitness=0.0,
        generation=max(parent1.generation, parent2.generation)
    )
    return child

def mutate(rule: DetectionRule) -> DetectionRule:
    """Mutation operator"""
    patterns = ["*.exe", "*.dll", "*.ps1", "*.sh", "*.bat", "cmd", "powershell"]
    severities = ["critical", "high", "medium", "low"]
    
    if random.random() < 0.5:
        rule.pattern = random.choice(patterns)
    if random.random() < 0.3:
        rule.severity = random.choice(severities)
    if random.random() < 0.3:
        rule.confidence = min(1.0, max(0.0, rule.confidence + random.uniform(-0.1, 0.1)))
    
    return rule

# Particle Swarm Optimization
@app.post("/pso/optimize")
async def optimize_pso(config: PSOConfig, objective: str = "detection_rate"):
    """Optimize hyperparameters using PSO"""
    logger.info(f"Starting PSO optimization with {config.n_particles} particles")
    
    # Initialize particles (representing hyperparameters)
    particles = []
    for _ in range(config.n_particles):
        position = [random.uniform(0, 1) for _ in range(5)]  # 5 hyperparameters
        velocity = [random.uniform(-0.1, 0.1) for _ in range(5)]
        particles.append({
            "position": position,
            "velocity": velocity,
            "best_position": position.copy(),
            "best_fitness": -float('inf')
        })
    
    global_best_position = particles[0]["position"].copy()
    global_best_fitness = -float('inf')
    
    convergence_history = []
    
    # PSO iterations
    for iteration in range(config.n_iterations):
        for particle in particles:
            # Evaluate fitness
            fitness = evaluate_fitness(particle["position"], objective)
            
            # Update personal best
            if fitness > particle["best_fitness"]:
                particle["best_fitness"] = fitness
                particle["best_position"] = particle["position"].copy()
            
            # Update global best
            if fitness > global_best_fitness:
                global_best_fitness = fitness
                global_best_position = particle["position"].copy()
        
        # Update velocities and positions
        for particle in particles:
            for i in range(len(particle["position"])):
                r1, r2 = random.random(), random.random()
                
                cognitive = config.c1 * r1 * (particle["best_position"][i] - particle["position"][i])
                social = config.c2 * r2 * (global_best_position[i] - particle["position"][i])
                
                particle["velocity"][i] = (
                    config.w * particle["velocity"][i] + cognitive + social
                )
                
                # Update position
                particle["position"][i] += particle["velocity"][i]
                particle["position"][i] = max(0.0, min(1.0, particle["position"][i]))
        
        convergence_history.append(global_best_fitness)
        
        if iteration % 10 == 0:
            logger.info(f"PSO Iteration {iteration}: Best fitness = {global_best_fitness:.4f}")
    
    return OptimizationResult(
        best_solution=global_best_position,
        best_fitness=global_best_fitness,
        iterations=config.n_iterations,
        convergence_history=convergence_history
    )

def evaluate_fitness(hyperparams: List[float], objective: str) -> float:
    """Evaluate fitness of hyperparameters (mock implementation)"""
    # In real implementation, this would train/test model with these hyperparameters
    # For now, return synthetic fitness based on distance from optimal
    optimal = [0.5, 0.5, 0.5, 0.5, 0.5]
    distance = sum((h - o) ** 2 for h, o in zip(hyperparams, optimal))
    return 1.0 / (1.0 + distance)

# Ant Colony Optimization for routing/paths
@app.post("/aco/optimize_routing")
async def optimize_routing(n_nodes: int, n_ants: int = 20, n_iterations: int = 100):
    """Optimize network routing paths using ACO"""
    logger.info(f"Starting ACO for routing optimization: {n_nodes} nodes")
    
    # Initialize pheromone matrix
    pheromones = np.ones((n_nodes, n_nodes)) * 0.1
    distances = np.random.rand(n_nodes, n_nodes) + 0.1  # Random distance matrix
    np.fill_diagonal(distances, 0)
    
    best_path = None
    best_length = float('inf')
    
    alpha = 1.0  # Pheromone importance
    beta = 2.0   # Distance importance
    rho = 0.5    # Evaporation rate
    
    for iteration in range(n_iterations):
        paths = []
        lengths = []
        
        # Each ant constructs a path
        for ant in range(n_ants):
            path = construct_path(n_nodes, pheromones, distances, alpha, beta)
            length = calculate_path_length(path, distances)
            
            paths.append(path)
            lengths.append(length)
            
            if length < best_length:
                best_length = length
                best_path = path
        
        # Update pheromones
        pheromones *= (1 - rho)  # Evaporation
        
        for path, length in zip(paths, lengths):
            deposit = 1.0 / length
            for i in range(len(path) - 1):
                pheromones[path[i]][path[i + 1]] += deposit
        
        if iteration % 10 == 0:
            logger.info(f"ACO Iteration {iteration}: Best path length = {best_length:.4f}")
    
    return {
        "best_path": best_path,
        "best_length": float(best_length),
        "iterations": n_iterations
    }

def construct_path(n_nodes: int, pheromones: np.ndarray, distances: np.ndarray, 
                   alpha: float, beta: float) -> List[int]:
    """Construct path using ACO probability rules"""
    path = [0]  # Start from node 0
    unvisited = set(range(1, n_nodes))
    
    while unvisited:
        current = path[-1]
        
        # Calculate probabilities
        probabilities = []
        for next_node in unvisited:
            pheromone = pheromones[current][next_node] ** alpha
            heuristic = (1.0 / distances[current][next_node]) ** beta
            probabilities.append(pheromone * heuristic)
        
        # Normalize
        total = sum(probabilities)
        if total > 0:
            probabilities = [p / total for p in probabilities]
        else:
            probabilities = [1.0 / len(unvisited)] * len(unvisited)
        
        # Choose next node
        next_node = random.choices(list(unvisited), weights=probabilities)[0]
        path.append(next_node)
        unvisited.remove(next_node)
    
    return path

def calculate_path_length(path: List[int], distances: np.ndarray) -> float:
    """Calculate total path length"""
    length = 0.0
    for i in range(len(path) - 1):
        length += distances[path[i]][path[i + 1]]
    return length

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8001)
