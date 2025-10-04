package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// DAGEngine executes workflows as directed acyclic graphs with optimal concurrency
type DAGEngine struct {
	// Metrics
	taskDuration     metric.Float64Histogram
	taskRetries      metric.Int64Counter
	taskFailures     metric.Int64Counter
	parallelismGauge metric.Int64Gauge
	
	// Configuration
	maxWorkers    int
	defaultRetry  RetryPolicy
	resultCache   *ResultCache
	tracer        trace.Tracer
}

// RetryPolicy defines exponential backoff retry strategy
type RetryPolicy struct {
	MaxAttempts int           `json:"max_attempts"`
	InitialWait time.Duration `json:"initial_wait"`
	MaxWait     time.Duration `json:"max_wait"`
	Multiplier  float64       `json:"multiplier"` // default 2.0
}

// TaskResult stores execution outcome
type TaskResult struct {
	TaskID    string                 `json:"task_id"`
	Status    TaskStatus             `json:"status"`
	StartTime time.Time              `json:"start_time"`
	EndTime   time.Time              `json:"end_time"`
	Duration  time.Duration          `json:"duration"`
	Output    map[string]interface{} `json:"output,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Attempts  int                    `json:"attempts"`
}

type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskSkipped   TaskStatus = "skipped"
)

// WorkflowExecution tracks DAG execution state
type WorkflowExecution struct {
	WorkflowID   string                  `json:"workflow_id"`
	WorkflowName string                  `json:"workflow_name"`
	StartTime    time.Time               `json:"start_time"`
	EndTime      time.Time               `json:"end_time"`
	Status       TaskStatus              `json:"status"`
	TaskResults  map[string]*TaskResult  `json:"task_results"`
	Context      map[string]interface{}  `json:"context"` // shared context
	mu           sync.RWMutex
}

// ResultCache implements LRU cache with TTL for task results
type ResultCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	maxSize int
	ttl     time.Duration
}

type cacheEntry struct {
	key       string
	result    *TaskResult
	expiresAt time.Time
	lastUsed  time.Time
}

func NewResultCache(maxSize int, ttl time.Duration) *ResultCache {
	rc := &ResultCache{
		entries: make(map[string]*cacheEntry),
		maxSize: maxSize,
		ttl:     ttl,
	}
	
	// Background cleanup goroutine
	go rc.cleanup()
	
	return rc
}

func (rc *ResultCache) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		rc.mu.Lock()
		now := time.Now()
		for key, entry := range rc.entries {
			if now.After(entry.expiresAt) {
				delete(rc.entries, key)
			}
		}
		rc.mu.Unlock()
	}
}

func (rc *ResultCache) Get(key string) (*TaskResult, bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	
	entry, exists := rc.entries[key]
	if !exists || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	
	entry.lastUsed = time.Now()
	return entry.result, true
}

func (rc *ResultCache) Put(key string, result *TaskResult) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	
	// Evict oldest if at capacity
	if len(rc.entries) >= rc.maxSize {
		rc.evictOldest()
	}
	
	rc.entries[key] = &cacheEntry{
		key:       key,
		result:    result,
		expiresAt: time.Now().Add(rc.ttl),
		lastUsed:  time.Now(),
	}
}

func (rc *ResultCache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	
	for key, entry := range rc.entries {
		if oldestKey == "" || entry.lastUsed.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.lastUsed
		}
	}
	
	if oldestKey != "" {
		delete(rc.entries, oldestKey)
	}
}

func NewDAGEngine(meter metric.Meter, maxWorkers int) *DAGEngine {
	taskDuration, _ := meter.Float64Histogram("swarm_workflow_task_duration_ms")
	taskRetries, _ := meter.Int64Counter("swarm_workflow_task_retries_total")
	taskFailures, _ := meter.Int64Counter("swarm_workflow_task_failures_total")
	parallelism, _ := meter.Int64Gauge("swarm_workflow_parallelism")
	
	return &DAGEngine{
		taskDuration:     taskDuration,
		taskRetries:      taskRetries,
		taskFailures:     taskFailures,
		parallelismGauge: parallelism,
		maxWorkers:       maxWorkers,
		defaultRetry: RetryPolicy{
			MaxAttempts: 3,
			InitialWait: 100 * time.Millisecond,
			MaxWait:     5 * time.Second,
			Multiplier:  2.0,
		},
		resultCache: NewResultCache(1000, 30*time.Minute),
		tracer:      otel.Tracer("orchestrator-dag"),
	}
}

// Execute runs workflow using optimized topological sort with concurrent execution
func (de *DAGEngine) Execute(ctx context.Context, wf Workflow, executor TaskExecutor) (*WorkflowExecution, error) {
	ctx, span := de.tracer.Start(ctx, "dag.execute",
		trace.WithAttributes(attribute.String("workflow", wf.Name)),
	)
	defer span.End()
	
	// Build DAG representation
	dag, err := de.buildDAG(wf)
	if err != nil {
		return nil, fmt.Errorf("invalid workflow: %w", err)
	}
	
	// Initialize execution state
	exec := &WorkflowExecution{
		WorkflowID:   generateWorkflowID(wf.Name),
		WorkflowName: wf.Name,
		StartTime:    time.Now(),
		Status:       TaskRunning,
		TaskResults:  make(map[string]*TaskResult),
		Context:      make(map[string]interface{}),
	}
	
	// Execute DAG with Kahn's algorithm + worker pool
	if err := de.executeDAG(ctx, dag, exec, executor); err != nil {
		exec.Status = TaskFailed
		return exec, err
	}
	
	exec.EndTime = time.Now()
	exec.Status = TaskCompleted
	return exec, nil
}

// dagNode represents a task with dependency tracking
type dagNode struct {
	Task       Task
	InDegree   int
	Children   []*dagNode
	Retry      RetryPolicy
	Condition  string // expression to evaluate (e.g., "prev_task.score > 0.8")
	CacheKey   string
}

type dag struct {
	Nodes     map[string]*dagNode
	RootNodes []*dagNode
	TaskCount int
}

func (de *DAGEngine) buildDAG(wf Workflow) (*dag, error) {
	nodes := make(map[string]*dagNode)
	
	// Create nodes
	for _, task := range wf.Tasks {
		node := &dagNode{
			Task:     task,
			InDegree: len(task.DependsOn),
			Retry:    de.defaultRetry,
		}
		
		// Generate cache key if task is cacheable
		if task.Cacheable {
			node.CacheKey = de.generateCacheKey(task)
		}
		
		nodes[task.ID] = node
	}
	
	// Build edges and validate
	for _, node := range nodes {
		for _, depID := range node.Task.DependsOn {
			parent, exists := nodes[depID]
			if !exists {
				return nil, fmt.Errorf("task %s depends on non-existent task %s", node.Task.ID, depID)
			}
			parent.Children = append(parent.Children, node)
		}
	}
	
	// Find root nodes (no dependencies)
	var roots []*dagNode
	for _, node := range nodes {
		if node.InDegree == 0 {
			roots = append(roots, node)
		}
	}
	
	if len(roots) == 0 {
		return nil, errors.New("workflow has circular dependencies")
	}
	
	return &dag{
		Nodes:     nodes,
		RootNodes: roots,
		TaskCount: len(nodes),
	}, nil
}

// executeDAG runs tasks in topological order with max concurrency
func (de *DAGEngine) executeDAG(ctx context.Context, dag *dag, exec *WorkflowExecution, executor TaskExecutor) error {
	// Track remaining in-degrees for Kahn's algorithm
	inDegree := make(map[string]int)
	for id, node := range dag.Nodes {
		inDegree[id] = node.InDegree
	}
	
	// Ready queue for tasks with satisfied dependencies
	ready := make(chan *dagNode, dag.TaskCount)
	for _, root := range dag.RootNodes {
		ready <- root
	}
	
	// Results channel
	results := make(chan *taskExecResult, dag.TaskCount)
	
	// Worker pool
	var wg sync.WaitGroup
	for i := 0; i < de.maxWorkers; i++ {
		wg.Add(1)
		go de.worker(ctx, ready, results, exec, executor, &wg)
	}
	
	// Coordinator: collect results and schedule children
	completed := 0
	failed := 0
	
	coordinatorDone := make(chan error, 1)
	go func() {
		defer close(coordinatorDone)
		
		for completed+failed < dag.TaskCount {
			select {
			case <-ctx.Done():
				coordinatorDone <- ctx.Err()
				return
			case res := <-results:
				if res.err != nil || res.result.Status == TaskFailed {
					failed++
					
					// Check if task is critical (default: all tasks are critical)
					if !res.node.Task.AllowFailure {
						coordinatorDone <- fmt.Errorf("task %s failed: %v", res.node.Task.ID, res.err)
						return
					}
				} else {
					completed++
				}
				
				// Schedule children whose dependencies are now satisfied
				for _, child := range res.node.Children {
					inDegree[child.Task.ID]--
					
					if inDegree[child.Task.ID] == 0 {
						// Check condition if present
						if child.Condition != "" {
							if !de.evaluateCondition(child.Condition, exec) {
								// Mark as skipped
								exec.mu.Lock()
								exec.TaskResults[child.Task.ID] = &TaskResult{
									TaskID:    child.Task.ID,
									Status:    TaskSkipped,
									StartTime: time.Now(),
									EndTime:   time.Now(),
								}
								exec.mu.Unlock()
								
								completed++
								
								// Recursively skip children
								de.skipChildren(child, exec, &completed)
								continue
							}
						}
						
						ready <- child
					}
				}
			}
		}
		
		coordinatorDone <- nil
	}()
	
	// Wait for coordinator
	err := <-coordinatorDone
	
	// Stop workers
	close(ready)
	wg.Wait()
	close(results)
	
	return err
}

type taskExecResult struct {
	node   *dagNode
	result *TaskResult
	err    error
}

// worker executes tasks from ready queue
func (de *DAGEngine) worker(
	ctx context.Context,
	ready <-chan *dagNode,
	results chan<- *taskExecResult,
	exec *WorkflowExecution,
	executor TaskExecutor,
	wg *sync.WaitGroup,
) {
	defer wg.Done()
	
	for {
		select {
		case <-ctx.Done():
			return
		case node, ok := <-ready:
			if !ok {
				return
			}
			
			// Update parallelism metric
			de.parallelismGauge.Record(ctx, 1)
			
			result, err := de.executeTask(ctx, node, exec, executor)
			
			de.parallelismGauge.Record(ctx, -1)
			
			results <- &taskExecResult{
				node:   node,
				result: result,
				err:    err,
			}
		}
	}
}

// executeTask runs a single task with retry logic and caching
func (de *DAGEngine) executeTask(
	ctx context.Context,
	node *dagNode,
	exec *WorkflowExecution,
	executor TaskExecutor,
) (*TaskResult, error) {
	ctx, span := de.tracer.Start(ctx, "task.execute",
		trace.WithAttributes(
			attribute.String("task_id", node.Task.ID),
			attribute.String("task_type", string(node.Task.Type)),
		),
	)
	defer span.End()
	
	// Check cache
	if node.CacheKey != "" {
		if cached, found := de.resultCache.Get(node.CacheKey); found {
			span.AddEvent("cache_hit")
			return cached, nil
		}
	}
	
	result := &TaskResult{
		TaskID:    node.Task.ID,
		Status:    TaskRunning,
		StartTime: time.Now(),
	}
	
	var lastErr error
	wait := node.Retry.InitialWait
	
	for attempt := 1; attempt <= node.Retry.MaxAttempts; attempt++ {
		result.Attempts = attempt
		
		execCtx, cancel := context.WithTimeout(ctx, node.Task.Timeout)
		
		output, err := executor.Execute(execCtx, node.Task, exec)
		cancel()
		
		if err == nil {
			// Success
			result.Status = TaskCompleted
			result.Output = output
			result.EndTime = time.Now()
			result.Duration = result.EndTime.Sub(result.StartTime)
			
			// Update execution state
			exec.mu.Lock()
			exec.TaskResults[node.Task.ID] = result
			// Store output in shared context
			exec.Context[node.Task.ID] = output
			exec.mu.Unlock()
			
			// Cache result
			if node.CacheKey != "" {
				de.resultCache.Put(node.CacheKey, result)
			}
			
			// Record metrics
			de.taskDuration.Record(ctx, float64(result.Duration.Milliseconds()),
				metric.WithAttributes(
					attribute.String("workflow", exec.WorkflowName),
					attribute.String("task", node.Task.ID),
					attribute.String("type", string(node.Task.Type)),
				),
			)
			
			return result, nil
		}
		
		lastErr = err
		
		if attempt < node.Retry.MaxAttempts {
			de.taskRetries.Add(ctx, 1,
				metric.WithAttributes(
					attribute.String("task", node.Task.ID),
					attribute.Int("attempt", attempt),
				),
			)
			
			// Exponential backoff with jitter
			jitter := time.Duration(float64(wait) * 0.1 * (2*float64(time.Now().UnixNano()%100)/100 - 1))
			time.Sleep(wait + jitter)
			
			wait = time.Duration(float64(wait) * node.Retry.Multiplier)
			if wait > node.Retry.MaxWait {
				wait = node.Retry.MaxWait
			}
		}
	}
	
	// All retries exhausted
	result.Status = TaskFailed
	result.Error = lastErr.Error()
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)
	
	exec.mu.Lock()
	exec.TaskResults[node.Task.ID] = result
	exec.mu.Unlock()
	
	de.taskFailures.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("task", node.Task.ID),
		),
	)
	
	return result, lastErr
}

// evaluateCondition checks if task should run based on previous results
func (de *DAGEngine) evaluateCondition(condition string, exec *WorkflowExecution) bool {
	// Simple condition evaluation (in production, use expr library)
	// Format: "task_id.output.field > value"
	// For now, just return true (always run)
	// TODO: Implement full expression evaluation
	return true
}

// skipChildren recursively marks all children as skipped
func (de *DAGEngine) skipChildren(node *dagNode, exec *WorkflowExecution, completed *int) {
	for _, child := range node.Children {
		exec.mu.Lock()
		if _, exists := exec.TaskResults[child.Task.ID]; !exists {
			exec.TaskResults[child.Task.ID] = &TaskResult{
				TaskID:    child.Task.ID,
				Status:    TaskSkipped,
				StartTime: time.Now(),
				EndTime:   time.Now(),
			}
			*completed++
		}
		exec.mu.Unlock()
		
		de.skipChildren(child, exec, completed)
	}
}

// generateCacheKey creates deterministic key for task result caching
func (de *DAGEngine) generateCacheKey(task Task) string {
	// Hash task definition
	data, _ := json.Marshal(task)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func generateWorkflowID(name string) string {
	return fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
}
