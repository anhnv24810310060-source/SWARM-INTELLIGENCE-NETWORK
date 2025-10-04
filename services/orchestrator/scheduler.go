package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"go.etcd.io/bbolt"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Scheduler manages cron schedules and event-driven triggers
type Scheduler struct {
	cron          *cron.Cron
	store         *WorkflowStore
	dagEngine     *DAGEngine
	pluginReg     *PluginRegistry
	eventHandlers map[string]*EventHandler // event type -> handler
	mu            sync.RWMutex

	// Metrics
	scheduleRuns  metric.Int64Counter
	scheduleFails metric.Int64Counter
	eventTriggers metric.Int64Counter
	tracer        trace.Tracer
}

// ScheduleConfig defines when and how to execute a workflow
type ScheduleConfig struct {
	WorkflowName  string                 `json:"workflow_name"`
	CronExpr      string                 `json:"cron_expr,omitempty"`    // "0 */5 * * * *" = every 5 minutes
	EventType     string                 `json:"event_type,omitempty"`   // "kafka.message", "webhook.received"
	EventFilter   map[string]interface{} `json:"event_filter,omitempty"` // Filter conditions
	Enabled       bool                   `json:"enabled"`
	MaxConcurrent int                    `json:"max_concurrent,omitempty"` // Max concurrent executions (0 = unlimited)
	Timeout       time.Duration          `json:"timeout,omitempty"`
	Metadata      map[string]string      `json:"metadata,omitempty"`
}

// EventHandler processes events and triggers workflows
type EventHandler struct {
	schedules   []*ScheduleConfig
	running     int
	mu          sync.Mutex
	lastTrigger time.Time
}

func NewScheduler(store *WorkflowStore, dagEngine *DAGEngine, pluginReg *PluginRegistry, meter metric.Meter) *Scheduler {
	// Create cron with seconds precision
	cronScheduler := cron.New(cron.WithSeconds())

	scheduleRuns, _ := meter.Int64Counter("swarm_workflow_schedule_runs_total")
	scheduleFails, _ := meter.Int64Counter("swarm_workflow_schedule_failures_total")
	eventTriggers, _ := meter.Int64Counter("swarm_workflow_event_triggers_total")

	return &Scheduler{
		cron:          cronScheduler,
		store:         store,
		dagEngine:     dagEngine,
		pluginReg:     pluginReg,
		eventHandlers: make(map[string]*EventHandler),
		scheduleRuns:  scheduleRuns,
		scheduleFails: scheduleFails,
		eventTriggers: eventTriggers,
		tracer:        otel.Tracer("orchestrator-scheduler"),
	}
}

// Start begins the scheduler
func (s *Scheduler) Start() {
	s.cron.Start()
	slog.Info("scheduler started")
}

// Stop gracefully stops the scheduler
func (s *Scheduler) Stop(ctx context.Context) error {
	stopCtx := s.cron.Stop()

	select {
	case <-stopCtx.Done():
		slog.Info("scheduler stopped gracefully")
		return nil
	case <-ctx.Done():
		slog.Warn("scheduler stop timeout")
		return ctx.Err()
	}
}

// AddSchedule registers a new scheduled workflow
func (s *Scheduler) AddSchedule(ctx context.Context, config *ScheduleConfig) error {
	ctx, span := s.tracer.Start(ctx, "scheduler.add_schedule",
		trace.WithAttributes(
			attribute.String("workflow", config.WorkflowName),
			attribute.String("cron", config.CronExpr),
		),
	)
	defer span.End()

	if config.CronExpr != "" {
		// Cron-based schedule
		entryID, err := s.cron.AddFunc(config.CronExpr, func() {
			s.executeScheduledWorkflow(context.Background(), config)
		})

		if err != nil {
			return fmt.Errorf("add cron schedule: %w", err)
		}

		slog.Info("cron schedule added",
			"workflow", config.WorkflowName,
			"cron", config.CronExpr,
			"entry_id", entryID,
		)

		// Store schedule metadata in BoltDB
		data, _ := json.Marshal(config)
		err = s.store.db.Update(func(tx *bbolt.Tx) error {
			bucket := tx.Bucket(bucketSchedules)
			return bucket.Put([]byte(config.WorkflowName), data)
		})
		if err != nil {
			return fmt.Errorf("persist schedule: %w", err)
		}

	} else if config.EventType != "" {
		// Event-driven trigger
		s.registerEventHandler(config)

		slog.Info("event trigger added",
			"workflow", config.WorkflowName,
			"event_type", config.EventType,
		)
	} else {
		return fmt.Errorf("either cron_expr or event_type must be specified")
	}

	return nil
}

// RemoveSchedule unregisters a scheduled workflow
func (s *Scheduler) RemoveSchedule(ctx context.Context, workflowName string) error {
	// Remove from cron
	// Note: cron library doesn't provide remove by name, only by ID
	// In production, maintain a map of workflow -> entryID

	// Remove event handlers
	s.mu.Lock()
	for eventType, handler := range s.eventHandlers {
		newSchedules := make([]*ScheduleConfig, 0)
		for _, sched := range handler.schedules {
			if sched.WorkflowName != workflowName {
				newSchedules = append(newSchedules, sched)
			}
		}
		handler.schedules = newSchedules

		// Clean up empty handlers
		if len(handler.schedules) == 0 {
			delete(s.eventHandlers, eventType)
		}
	}
	s.mu.Unlock()

	// Remove from BoltDB
	err := s.store.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketSchedules)
		return bucket.Delete([]byte(workflowName))
	})
	if err != nil {
		return fmt.Errorf("delete schedule: %w", err)
	}

	slog.Info("schedule removed", "workflow", workflowName)

	return nil
}

// ListSchedules returns all registered schedules
func (s *Scheduler) ListSchedules(ctx context.Context) ([]*ScheduleConfig, error) {
	schedules := make([]*ScheduleConfig, 0)

	err := s.store.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketSchedules)
		if bucket == nil {
			return nil
		}

		return bucket.ForEach(func(k, v []byte) error {
			var config ScheduleConfig
			if err := json.Unmarshal(v, &config); err != nil {
				return nil // Skip invalid entries
			}
			schedules = append(schedules, &config)
			return nil
		})
	})

	return schedules, err
}

// TriggerEvent processes an incoming event and triggers matching workflows
func (s *Scheduler) TriggerEvent(ctx context.Context, eventType string, eventData map[string]interface{}) error {
	ctx, span := s.tracer.Start(ctx, "scheduler.trigger_event",
		trace.WithAttributes(attribute.String("event_type", eventType)),
	)
	defer span.End()

	s.mu.RLock()
	handler, exists := s.eventHandlers[eventType]
	s.mu.RUnlock()

	if !exists {
		span.AddEvent("no_handlers")
		return nil
	}

	s.eventTriggers.Add(ctx, 1, metric.WithAttributes(attribute.String("event_type", eventType)))

	// Process each schedule that matches this event
	for _, schedule := range handler.schedules {
		if !schedule.Enabled {
			continue
		}

		// Check event filter
		if !s.matchesFilter(eventData, schedule.EventFilter) {
			continue
		}

		// Check concurrency limit
		handler.mu.Lock()
		if schedule.MaxConcurrent > 0 && handler.running >= schedule.MaxConcurrent {
			handler.mu.Unlock()
			slog.Warn("max concurrent executions reached",
				"workflow", schedule.WorkflowName,
				"max", schedule.MaxConcurrent,
			)
			continue
		}
		handler.running++
		handler.lastTrigger = time.Now()
		handler.mu.Unlock()

		// Execute asynchronously
		go func(cfg *ScheduleConfig) {
			defer func() {
				handler.mu.Lock()
				handler.running--
				handler.mu.Unlock()
			}()

			execCtx := context.Background()
			if cfg.Timeout > 0 {
				var cancel context.CancelFunc
				execCtx, cancel = context.WithTimeout(execCtx, cfg.Timeout)
				defer cancel()
			}

			s.executeScheduledWorkflow(execCtx, cfg)
		}(schedule)
	}

	return nil
}

// executeScheduledWorkflow executes a workflow from a schedule
func (s *Scheduler) executeScheduledWorkflow(ctx context.Context, config *ScheduleConfig) {
	ctx, span := s.tracer.Start(ctx, "scheduler.execute_workflow",
		trace.WithAttributes(
			attribute.String("workflow", config.WorkflowName),
		),
	)
	defer span.End()

	start := time.Now()

	// Load workflow
	workflow, found, err := s.store.GetWorkflow(ctx, config.WorkflowName)
	if err != nil {
		slog.Error("failed to load workflow", "workflow", config.WorkflowName, "error", err)
		s.scheduleFails.Add(ctx, 1, metric.WithAttributes(attribute.String("workflow", config.WorkflowName)))
		return
	}

	if !found {
		slog.Error("workflow not found", "workflow", config.WorkflowName)
		s.scheduleFails.Add(ctx, 1, metric.WithAttributes(attribute.String("workflow", config.WorkflowName)))
		return
	}

	// Execute workflow
	exec, err := s.dagEngine.Execute(ctx, workflow, s.pluginReg)
	if err != nil {
		slog.Error("scheduled workflow execution failed",
			"workflow", config.WorkflowName,
			"error", err,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		s.scheduleFails.Add(ctx, 1, metric.WithAttributes(attribute.String("workflow", config.WorkflowName)))
		return
	}

	// Store execution result
	if err := s.store.PutExecution(ctx, exec); err != nil {
		slog.Error("failed to store execution", "error", err)
	}

	s.scheduleRuns.Add(ctx, 1, metric.WithAttributes(
		attribute.String("workflow", config.WorkflowName),
		attribute.String("status", "success"),
	))

	slog.Info("scheduled workflow completed",
		"workflow", config.WorkflowName,
		"workflow_id", exec.WorkflowID,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

// registerEventHandler adds an event handler
func (s *Scheduler) registerEventHandler(config *ScheduleConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	handler, exists := s.eventHandlers[config.EventType]
	if !exists {
		handler = &EventHandler{
			schedules: make([]*ScheduleConfig, 0),
		}
		s.eventHandlers[config.EventType] = handler
	}

	handler.schedules = append(handler.schedules, config)
}

// matchesFilter checks if event data matches filter conditions
func (s *Scheduler) matchesFilter(eventData, filter map[string]interface{}) bool {
	if len(filter) == 0 {
		return true // No filter = match all
	}

	for key, expectedValue := range filter {
		actualValue, exists := eventData[key]
		if !exists {
			return false
		}

		// Simple equality check (can be extended to support operators)
		if fmt.Sprintf("%v", actualValue) != fmt.Sprintf("%v", expectedValue) {
			return false
		}
	}

	return true
}

// GetScheduleStats returns statistics about scheduled workflows
func (s *Scheduler) GetScheduleStats() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := map[string]interface{}{
		"cron_entries":    len(s.cron.Entries()),
		"event_handlers":  len(s.eventHandlers),
		"total_schedules": 0,
	}

	totalSchedules := 0
	eventHandlerStats := make(map[string]interface{})

	for eventType, handler := range s.eventHandlers {
		handler.mu.Lock()
		eventHandlerStats[eventType] = map[string]interface{}{
			"schedules":    len(handler.schedules),
			"running":      handler.running,
			"last_trigger": handler.lastTrigger.Format(time.RFC3339),
		}
		totalSchedules += len(handler.schedules)
		handler.mu.Unlock()
	}

	stats["total_schedules"] = totalSchedules + len(s.cron.Entries())
	stats["event_handler_stats"] = eventHandlerStats

	return stats
}

// RestoreSchedules loads persisted schedules on startup
func (s *Scheduler) RestoreSchedules(ctx context.Context) error {
	schedules, err := s.ListSchedules(ctx)
	if err != nil {
		return fmt.Errorf("list schedules: %w", err)
	}

	restored := 0
	failed := 0

	for _, schedule := range schedules {
		if !schedule.Enabled {
			continue
		}

		if err := s.AddSchedule(ctx, schedule); err != nil {
			slog.Error("failed to restore schedule",
				"workflow", schedule.WorkflowName,
				"error", err,
			)
			failed++
		} else {
			restored++
		}
	}

	slog.Info("schedules restored", "restored", restored, "failed", failed)

	return nil
}
