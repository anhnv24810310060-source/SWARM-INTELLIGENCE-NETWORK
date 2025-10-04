package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// CancellationManager handles workflow execution cancellation
type CancellationManager struct {
	mu               sync.RWMutex
	activeExecutions map[string]*CancellableExecution

	// Metrics
	cancellations metric.Int64Counter
	tracer        trace.Tracer
}

// CancellableExecution wraps WorkflowExecution with cancellation support
type CancellableExecution struct {
	Exec         *WorkflowExecution
	CancelFunc   context.CancelFunc
	CancelReason string
	CancelledAt  time.Time
	Status       ExecutionStatus
}

type ExecutionStatus string

const (
	ExecutionRunning   ExecutionStatus = "running"
	ExecutionCompleted ExecutionStatus = "completed"
	ExecutionFailed    ExecutionStatus = "failed"
	ExecutionCancelled ExecutionStatus = "cancelled"
)

func NewCancellationManager(meter metric.Meter) *CancellationManager {
	cancellations, _ := meter.Int64Counter("swarm_workflow_cancellations_total")

	return &CancellationManager{
		activeExecutions: make(map[string]*CancellableExecution),
		cancellations:    cancellations,
		tracer:           otel.Tracer("orchestrator-cancellation"),
	}
}

// Register adds an active execution for tracking
func (cm *CancellationManager) Register(workflowID string, exec *WorkflowExecution, cancelFunc context.CancelFunc) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.activeExecutions[workflowID] = &CancellableExecution{
		Exec:       exec,
		CancelFunc: cancelFunc,
		Status:     ExecutionRunning,
	}
}

// Cancel stops a running workflow execution
func (cm *CancellationManager) Cancel(ctx context.Context, workflowID, reason string) error {
	ctx, span := cm.tracer.Start(ctx, "cancellation.cancel",
		trace.WithAttributes(
			attribute.String("workflow_id", workflowID),
			attribute.String("reason", reason),
		),
	)
	defer span.End()

	cm.mu.Lock()
	defer cm.mu.Unlock()

	cancellable, exists := cm.activeExecutions[workflowID]
	if !exists {
		return fmt.Errorf("workflow execution not found or already completed: %s", workflowID)
	}

	if cancellable.Status != ExecutionRunning {
		return fmt.Errorf("workflow execution is not running: %s (status: %s)", workflowID, cancellable.Status)
	}

	// Trigger cancellation
	cancellable.CancelFunc()
	cancellable.CancelReason = reason
	cancellable.CancelledAt = time.Now()
	cancellable.Status = ExecutionCancelled

	// Update execution status
	cancellable.Exec.mu.Lock()
	cancellable.Exec.Status = TaskFailed // Mark as failed with cancellation reason
	cancellable.Exec.EndTime = time.Now()
	cancellable.Exec.mu.Unlock()

	cm.cancellations.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("workflow", cancellable.Exec.WorkflowName),
			attribute.String("reason", reason),
		),
	)

	span.AddEvent("workflow_cancelled")

	return nil
}

// Complete marks an execution as completed and removes from active tracking
func (cm *CancellationManager) Complete(workflowID string, status ExecutionStatus) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cancellable, exists := cm.activeExecutions[workflowID]; exists {
		cancellable.Status = status
		// Keep in map for a short time for status queries
		// Cleanup will be done by periodic cleanup goroutine
	}
}

// GetStatus returns the status of a workflow execution
func (cm *CancellationManager) GetStatus(workflowID string) (ExecutionStatus, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	cancellable, exists := cm.activeExecutions[workflowID]
	if !exists {
		return "", false
	}

	return cancellable.Status, true
}

// ListActive returns all currently running executions
func (cm *CancellationManager) ListActive() []*CancellableExecution {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	active := make([]*CancellableExecution, 0)
	for _, cancellable := range cm.activeExecutions {
		if cancellable.Status == ExecutionRunning {
			active = append(active, cancellable)
		}
	}

	return active
}

// Cleanup removes completed executions older than retention period
func (cm *CancellationManager) Cleanup(retentionPeriod time.Duration) int {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	now := time.Now()
	cleaned := 0

	for workflowID, cancellable := range cm.activeExecutions {
		// Keep running executions
		if cancellable.Status == ExecutionRunning {
			continue
		}

		// Check if completed execution is old enough to clean
		var completionTime time.Time
		if cancellable.Status == ExecutionCancelled {
			completionTime = cancellable.CancelledAt
		} else {
			completionTime = cancellable.Exec.EndTime
		}

		if !completionTime.IsZero() && now.Sub(completionTime) > retentionPeriod {
			delete(cm.activeExecutions, workflowID)
			cleaned++
		}
	}

	return cleaned
}

// StartCleanupLoop runs periodic cleanup of old executions
func (cm *CancellationManager) StartCleanupLoop(ctx context.Context, interval time.Duration, retentionPeriod time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleaned := cm.Cleanup(retentionPeriod)
			if cleaned > 0 {
				// Log cleanup activity (using slog if available)
			}
		}
	}
}

// CancelAll cancels all running executions (for shutdown)
func (cm *CancellationManager) CancelAll(ctx context.Context, reason string) int {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cancelled := 0
	for workflowID, cancellable := range cm.activeExecutions {
		if cancellable.Status == ExecutionRunning {
			cancellable.CancelFunc()
			cancellable.CancelReason = reason
			cancellable.CancelledAt = time.Now()
			cancellable.Status = ExecutionCancelled

			cm.cancellations.Add(ctx, 1,
				metric.WithAttributes(
					attribute.String("workflow", cancellable.Exec.WorkflowName),
					attribute.String("reason", reason),
				),
			)

			cancelled++
		}

		// Remove from tracking
		delete(cm.activeExecutions, workflowID)
	}

	return cancelled
}

// GetMetrics returns current metrics snapshot
func (cm *CancellationManager) GetMetrics() map[string]int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	metrics := map[string]int{
		"total":     len(cm.activeExecutions),
		"running":   0,
		"completed": 0,
		"failed":    0,
		"cancelled": 0,
	}

	for _, cancellable := range cm.activeExecutions {
		switch cancellable.Status {
		case ExecutionRunning:
			metrics["running"]++
		case ExecutionCompleted:
			metrics["completed"]++
		case ExecutionFailed:
			metrics["failed"]++
		case ExecutionCancelled:
			metrics["cancelled"]++
		}
	}

	return metrics
}
