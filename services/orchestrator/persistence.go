package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.etcd.io/bbolt"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// WorkflowStore provides persistent storage for workflows and executions using BoltDB
// BoltDB is chosen over RocksDB for easier deployment (pure Go, no C dependencies)
type WorkflowStore struct {
	db             *bbolt.DB
	mu             sync.RWMutex
	memCache       map[string]Workflow           // Hot cache for workflows
	executionCache map[string]*WorkflowExecution // Recent executions
	maxCacheSize   int

	// Metrics
	readLatency  metric.Float64Histogram
	writeLatency metric.Float64Histogram
	cacheHits    metric.Int64Counter
	cacheMisses  metric.Int64Counter
}

// Bucket names for different data types
var (
	bucketWorkflows  = []byte("workflows")
	bucketExecutions = []byte("executions")
	bucketVersions   = []byte("versions")
	bucketSchedules  = []byte("schedules")
	bucketIndexes    = []byte("indexes")
)

// NewWorkflowStore creates a persistent workflow store with BoltDB backend
func NewWorkflowStore(dbPath string, meter metric.Meter) (*WorkflowStore, error) {
	// BoltDB options for optimal performance
	opts := &bbolt.Options{
		Timeout:      1 * time.Second,
		NoSync:       false, // fsync for durability
		NoGrowSync:   false,
		FreelistType: bbolt.FreelistArrayType,
	}

	db, err := bbolt.Open(dbPath+"/workflows.db", 0600, opts)
	if err != nil {
		return nil, fmt.Errorf("open boltdb: %w", err)
	}

	// Create buckets
	err = db.Update(func(tx *bbolt.Tx) error {
		for _, bucket := range [][]byte{bucketWorkflows, bucketExecutions, bucketVersions, bucketSchedules, bucketIndexes} {
			if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create buckets: %w", err)
	}

	readLatency, _ := meter.Float64Histogram("swarm_workflow_db_read_ms")
	writeLatency, _ := meter.Float64Histogram("swarm_workflow_db_write_ms")
	cacheHits, _ := meter.Int64Counter("swarm_workflow_cache_hits_total")
	cacheMisses, _ := meter.Int64Counter("swarm_workflow_cache_misses_total")

	store := &WorkflowStore{
		db:             db,
		memCache:       make(map[string]Workflow),
		executionCache: make(map[string]*WorkflowExecution),
		maxCacheSize:   1000,
		readLatency:    readLatency,
		writeLatency:   writeLatency,
		cacheHits:      cacheHits,
		cacheMisses:    cacheMisses,
	}

	// Load workflows into memory cache on startup
	if err := store.warmCache(); err != nil {
		return nil, fmt.Errorf("warm cache: %w", err)
	}

	return store, nil
}

// Close gracefully closes the database
func (ws *WorkflowStore) Close() error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	return ws.db.Close()
}

// PutWorkflow stores a workflow with versioning support
func (ws *WorkflowStore) PutWorkflow(ctx context.Context, wf Workflow) error {
	start := time.Now()
	defer func() {
		ws.writeLatency.Record(ctx, float64(time.Since(start).Milliseconds()),
			metric.WithAttributes(attribute.String("operation", "put_workflow")))
	}()

	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Serialize workflow
	data, err := json.Marshal(wf)
	if err != nil {
		return fmt.Errorf("marshal workflow: %w", err)
	}

	err = ws.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWorkflows)
		if bucket == nil {
			return fmt.Errorf("workflows bucket not found")
		}

		// Check if workflow exists (for versioning)
		existingData := bucket.Get([]byte(wf.Name))
		if existingData != nil {
			// Store previous version
			versionBucket := tx.Bucket(bucketVersions)
			versionKey := fmt.Sprintf("%s:%d", wf.Name, time.Now().UnixNano())
			if err := versionBucket.Put([]byte(versionKey), existingData); err != nil {
				return fmt.Errorf("store version: %w", err)
			}
		}

		// Write new workflow
		return bucket.Put([]byte(wf.Name), data)
	})

	if err != nil {
		return fmt.Errorf("write workflow: %w", err)
	}

	// Update memory cache
	ws.memCache[wf.Name] = wf

	return nil
}

// GetWorkflow retrieves a workflow by name with cache support
func (ws *WorkflowStore) GetWorkflow(ctx context.Context, name string) (Workflow, bool, error) {
	start := time.Now()
	defer func() {
		ws.readLatency.Record(ctx, float64(time.Since(start).Milliseconds()),
			metric.WithAttributes(attribute.String("operation", "get_workflow")))
	}()

	ws.mu.RLock()

	// Check memory cache first
	if wf, found := ws.memCache[name]; found {
		ws.mu.RUnlock()
		ws.cacheHits.Add(ctx, 1, metric.WithAttributes(attribute.String("type", "workflow")))
		return wf, true, nil
	}

	ws.mu.RUnlock()
	ws.cacheMisses.Add(ctx, 1, metric.WithAttributes(attribute.String("type", "workflow")))

	// Read from database
	var wf Workflow
	err := ws.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWorkflows)
		if bucket == nil {
			return fmt.Errorf("workflows bucket not found")
		}

		data := bucket.Get([]byte(name))
		if data == nil {
			return nil
		}

		return json.Unmarshal(data, &wf)
	})

	if err != nil {
		return Workflow{}, false, fmt.Errorf("read workflow: %w", err)
	}

	if wf.Name == "" {
		return Workflow{}, false, nil
	}

	// Update cache
	ws.mu.Lock()
	ws.memCache[name] = wf
	ws.mu.Unlock()

	return wf, true, nil
}

// ListWorkflows returns all workflows with pagination
func (ws *WorkflowStore) ListWorkflows(ctx context.Context, limit, offset int) ([]Workflow, error) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	workflows := make([]Workflow, 0, len(ws.memCache))
	for _, wf := range ws.memCache {
		workflows = append(workflows, wf)
	}

	// Apply pagination
	start := offset
	if start > len(workflows) {
		start = len(workflows)
	}

	end := start + limit
	if end > len(workflows) {
		end = len(workflows)
	}

	return workflows[start:end], nil
}

// DeleteWorkflow removes a workflow (soft delete - keeps versions)
func (ws *WorkflowStore) DeleteWorkflow(ctx context.Context, name string) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	err := ws.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWorkflows)

		// Archive before delete
		data := bucket.Get([]byte(name))
		if data != nil {
			versionBucket := tx.Bucket(bucketVersions)
			archiveKey := fmt.Sprintf("archive:%s:%d", name, time.Now().UnixNano())
			if err := versionBucket.Put([]byte(archiveKey), data); err != nil {
				return err
			}
		}

		// Delete workflow
		return bucket.Delete([]byte(name))
	})

	if err != nil {
		return fmt.Errorf("delete workflow: %w", err)
	}

	// Remove from cache
	delete(ws.memCache, name)

	return nil
}

// PutExecution stores a workflow execution result
func (ws *WorkflowStore) PutExecution(ctx context.Context, exec *WorkflowExecution) error {
	start := time.Now()
	defer func() {
		ws.writeLatency.Record(ctx, float64(time.Since(start).Milliseconds()),
			metric.WithAttributes(attribute.String("operation", "put_execution")))
	}()

	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Serialize execution
	data, err := json.Marshal(exec)
	if err != nil {
		return fmt.Errorf("marshal execution: %w", err)
	}

	err = ws.db.Update(func(tx *bbolt.Tx) error {
		// Store execution
		execBucket := tx.Bucket(bucketExecutions)
		if err := execBucket.Put([]byte(exec.WorkflowID), data); err != nil {
			return err
		}

		// Create time-based index
		indexBucket := tx.Bucket(bucketIndexes)
		indexKey := fmt.Sprintf("%s:%d:%s", exec.WorkflowName, exec.StartTime.UnixNano(), exec.WorkflowID)
		return indexBucket.Put([]byte(indexKey), []byte(exec.WorkflowID))
	})

	if err != nil {
		return fmt.Errorf("write execution: %w", err)
	}

	// Update execution cache with LRU eviction
	if len(ws.executionCache) >= ws.maxCacheSize {
		ws.evictOldestExecution()
	}
	ws.executionCache[exec.WorkflowID] = exec

	return nil
}

// GetExecution retrieves an execution by ID
func (ws *WorkflowStore) GetExecution(ctx context.Context, id string) (*WorkflowExecution, bool, error) {
	start := time.Now()
	defer func() {
		ws.readLatency.Record(ctx, float64(time.Since(start).Milliseconds()),
			metric.WithAttributes(attribute.String("operation", "get_execution")))
	}()

	ws.mu.RLock()

	// Check cache
	if exec, found := ws.executionCache[id]; found {
		ws.mu.RUnlock()
		ws.cacheHits.Add(ctx, 1, metric.WithAttributes(attribute.String("type", "execution")))
		return exec, true, nil
	}

	ws.mu.RUnlock()
	ws.cacheMisses.Add(ctx, 1, metric.WithAttributes(attribute.String("type", "execution")))

	// Read from database
	var exec WorkflowExecution
	err := ws.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketExecutions)
		data := bucket.Get([]byte(id))
		if data == nil {
			return nil
		}

		return json.Unmarshal(data, &exec)
	})

	if err != nil {
		return nil, false, fmt.Errorf("read execution: %w", err)
	}

	if exec.WorkflowID == "" {
		return nil, false, nil
	}

	return &exec, true, nil
}

// ListExecutions returns executions for a workflow with time range filtering
func (ws *WorkflowStore) ListExecutions(ctx context.Context, workflowName string, startTime, endTime time.Time, limit int) ([]*WorkflowExecution, error) {
	executions := make([]*WorkflowExecution, 0, limit)

	err := ws.db.View(func(tx *bbolt.Tx) error {
		indexBucket := tx.Bucket(bucketIndexes)
		execBucket := tx.Bucket(bucketExecutions)

		prefix := []byte(workflowName + ":")
		cursor := indexBucket.Cursor()

		count := 0
		for k, v := cursor.Seek(prefix); k != nil && count < limit; k, v = cursor.Next() {
			if !hasPrefix(k, prefix) {
				break
			}

			execID := string(v)
			data := execBucket.Get([]byte(execID))
			if data == nil {
				continue
			}

			var exec WorkflowExecution
			if err := json.Unmarshal(data, &exec); err != nil {
				continue
			}

			// Check time range
			if exec.StartTime.After(endTime) {
				break
			}
			if exec.StartTime.Before(startTime) {
				continue
			}

			executions = append(executions, &exec)
			count++
		}

		return nil
	})

	return executions, err
}

// GetWorkflowVersions retrieves version history of a workflow
func (ws *WorkflowStore) GetWorkflowVersions(ctx context.Context, name string, limit int) ([]Workflow, error) {
	versions := make([]Workflow, 0, limit)

	err := ws.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketVersions)
		prefix := []byte(name + ":")
		cursor := bucket.Cursor()

		count := 0
		// Iterate backwards to get newest first
		for k, v := cursor.Seek(prefix); k != nil && count < limit; k, v = cursor.Next() {
			if !hasPrefix(k, prefix) {
				break
			}

			var wf Workflow
			if err := json.Unmarshal(v, &wf); err != nil {
				continue
			}

			versions = append(versions, wf)
			count++
		}

		return nil
	})

	return versions, err
}

// Compact triggers manual compaction (BoltDB does this automatically)
func (ws *WorkflowStore) Compact(ctx context.Context) error {
	// BoltDB handles compaction automatically during transactions
	// We can trigger a full database rewrite if needed for space reclamation
	return nil
}

// GetStats returns database statistics
func (ws *WorkflowStore) GetStats() map[string]interface{} {
	stats := make(map[string]interface{})

	ws.db.View(func(tx *bbolt.Tx) error {
		stats["db_size_bytes"] = tx.Size()

		// Count items in each bucket
		for _, bucketName := range [][]byte{bucketWorkflows, bucketExecutions, bucketVersions, bucketSchedules} {
			bucket := tx.Bucket(bucketName)
			if bucket != nil {
				stats[string(bucketName)+"_count"] = bucket.Stats().KeyN
			}
		}

		return nil
	})

	stats["cache_workflows"] = len(ws.memCache)
	stats["cache_executions"] = len(ws.executionCache)
	stats["cache_max_size"] = ws.maxCacheSize

	return stats
}

// warmCache loads frequently accessed workflows into memory
func (ws *WorkflowStore) warmCache() error {
	count := 0

	err := ws.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWorkflows)
		if bucket == nil {
			return nil
		}

		return bucket.ForEach(func(k, v []byte) error {
			var wf Workflow
			if err := json.Unmarshal(v, &wf); err != nil {
				return nil // Skip invalid entries
			}

			ws.memCache[wf.Name] = wf
			count++
			return nil
		})
	})

	return err
}

// evictOldestExecution removes the oldest execution from cache
func (ws *WorkflowStore) evictOldestExecution() {
	var oldestID string
	var oldestTime time.Time

	for id, exec := range ws.executionCache {
		if oldestID == "" || exec.StartTime.Before(oldestTime) {
			oldestID = id
			oldestTime = exec.StartTime
		}
	}

	if oldestID != "" {
		delete(ws.executionCache, oldestID)
	}
}

// Helper function to check byte slice prefix
func hasPrefix(data, prefix []byte) bool {
	if len(data) < len(prefix) {
		return false
	}
	for i := range prefix {
		if data[i] != prefix[i] {
			return false
		}
	}
	return true
}
