package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	logging "github.com/swarmguard/libs/go/core/logging"
	"github.com/swarmguard/libs/go/core/otelinit"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Task types
type TaskType string

const (
	TaskHTTP   TaskType = "http"
	TaskPython TaskType = "python"
	TaskPolicy TaskType = "policy"
)

// Task represents a single unit of work in a workflow
type Task struct {
	ID           string        `json:"id"`
	Type         TaskType      `json:"type"`
	DependsOn    []string      `json:"depends_on,omitempty"`
	Timeout      time.Duration `json:"timeout,omitempty"`
	AllowFailure bool          `json:"allow_failure,omitempty"`
	Cacheable    bool          `json:"cacheable,omitempty"`
	Condition    string        `json:"condition,omitempty"`

	// HTTP task fields
	URL     string            `json:"url,omitempty"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    interface{}       `json:"body,omitempty"`

	// Script task fields
	Script string `json:"script,omitempty"`

	// Policy task fields
	Policy string `json:"policy,omitempty"`
}

// Workflow represents a DAG of tasks
type Workflow struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Tasks       []Task `json:"tasks"`
	Trigger     struct {
		Type  string `json:"type"`  // "cron", "webhook", "kafka"
		Value string `json:"value"` // cron expression, URL path, topic name
	} `json:"trigger,omitempty"`
}

// Request types
type runRequest struct {
	Workflow   string                 `json:"workflow"`
	Parameters map[string]interface{} `json:"parameters,omitempty"`
}

func main() {
	service := "orchestrator"
	logging.Init(service)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	shutdownTrace := otelinit.InitTracer(ctx, service)
	shutdownMetrics, promHandler, _ := otelinit.InitMetrics(ctx, service)

	meter := otel.GetMeterProvider().Meter("orchestrator")

	// Initialize persistent store with RocksDB
	dbPath := getEnvDefault("ROCKSDB_PATH", "/data/orchestrator")
	store, err := NewWorkflowStore(dbPath, meter)
	if err != nil {
		slog.Error("failed to initialize workflow store", "error", err)
		return
	}
	defer store.Close()

	// Initialize DAG engine with 8 workers for concurrent task execution
	dagEngine := NewDAGEngine(meter, 8)

	// Initialize plugin registry
	pluginRegistry := NewPluginRegistry()

	// Initialize scheduler
	scheduler := NewScheduler(store, dagEngine, pluginRegistry, meter)
	scheduler.Start()
	defer scheduler.Stop(context.Background())

	// Restore persisted schedules
	if err := scheduler.RestoreSchedules(ctx); err != nil {
		slog.Error("failed to restore schedules", "error", err)
	}

	// Initialize cancellation manager
	cancellationMgr := NewCancellationManager(meter)

	// Start cleanup loop for old executions
	go cancellationMgr.StartCleanupLoop(ctx, 5*time.Minute, 1*time.Hour)

	// Seed sample workflow
	store.PutWorkflow(ctx, Workflow{
		Name:        "sample",
		Description: "Sample threat response workflow",
		Tasks: []Task{
			{
				ID:      "enrich",
				Type:    TaskHTTP,
				URL:     "http://threat-intel:8080/v1/enrich",
				Method:  http.MethodPost,
				Timeout: 5 * time.Second,
				Body: map[string]string{
					"indicator": "{{input.indicator}}",
				},
			},
			{
				ID:        "score",
				Type:      TaskPolicy,
				Policy:    "threat_scoring",
				DependsOn: []string{"enrich"},
				Timeout:   2 * time.Second,
			},
			{
				ID:           "block",
				Type:         TaskHTTP,
				URL:          "http://api-gateway:8080/v1/block",
				Method:       http.MethodPost,
				DependsOn:    []string{"score"},
				Condition:    "score.risk > 0.8",
				Timeout:      3 * time.Second,
				AllowFailure: true,
			},
		},
	})

	mux := http.NewServeMux()
	runCounter, _ := meter.Int64Counter("swarm_workflow_runs_total")
	runErrors, _ := meter.Int64Counter("swarm_workflow_run_errors_total")
	wfLatency, _ := meter.Float64Histogram("swarm_workflow_duration_seconds")

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "healthy",
			"service": service,
			"version": "2.0.0",
		})
	})
	// Workflow management endpoints
	mux.HandleFunc("/v1/workflows", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodPost:
			var wf Workflow
			if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if wf.Name == "" {
				http.Error(w, "name required", http.StatusBadRequest)
				return
			}

			// Set default timeouts
			for i := range wf.Tasks {
				if wf.Tasks[i].Timeout == 0 {
					wf.Tasks[i].Timeout = 30 * time.Second
				}
			}

			if err := store.PutWorkflow(r.Context(), wf); err != nil {
				http.Error(w, "failed to store workflow", http.StatusInternalServerError)
				return
			}
			slog.Info("workflow registered", "name", wf.Name, "tasks", len(wf.Tasks))

			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(wf)

		case http.MethodGet:
			name := r.URL.Query().Get("name")
			if name != "" {
				wf, ok, err := store.GetWorkflow(r.Context(), name)
				if err != nil {
					http.Error(w, "failed to get workflow", http.StatusInternalServerError)
					return
				}
				if !ok {
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(wf)
			} else {
				// List all workflows
				workflows, err := store.ListWorkflows(r.Context(), 1000, 0)
				if err != nil {
					http.Error(w, "failed to list workflows", http.StatusInternalServerError)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"workflows": workflows,
					"count":     len(workflows),
				})
			}

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	// Workflow execution endpoint
	mux.HandleFunc("/v1/run", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var req runRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		wf, ok, err := store.GetWorkflow(r.Context(), req.Workflow)
		if err != nil {
			http.Error(w, "failed to get workflow", http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "workflow not found", http.StatusNotFound)
			return
		}

		// Execute with timeout
		ctxExec, cancelExec := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancelExec()

		start := time.Now()
		slog.Info("workflow execution started", "workflow", wf.Name)

		// Register execution for cancellation support
		execCtxWithCancel, cancelFunc := context.WithCancel(ctxExec)

		exec, err := dagEngine.Execute(execCtxWithCancel, wf, pluginRegistry)

		// Register with cancellation manager if execution started
		if exec != nil {
			cancellationMgr.Register(exec.WorkflowID, exec, cancelFunc)
			defer cancellationMgr.Complete(exec.WorkflowID, ExecutionCompleted)
		}

		duration := time.Since(start)

		if err != nil {
			runErrors.Add(r.Context(), 1, metric.WithAttributes(attribute.String("workflow", wf.Name)))
			slog.Error("workflow execution failed", "workflow", wf.Name, "error", err, "duration_ms", duration.Milliseconds())

			if exec != nil {
				_ = store.PutExecution(r.Context(), exec)
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"status":       "failed",
					"workflow_id":  exec.WorkflowID,
					"error":        err.Error(),
					"task_results": exec.TaskResults,
				})
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}

		_ = store.PutExecution(r.Context(), exec)

		wfLatency.Record(r.Context(), duration.Seconds(), metric.WithAttributes(attribute.String("workflow", wf.Name)))
		runCounter.Add(r.Context(), 1, metric.WithAttributes(attribute.String("workflow", wf.Name), attribute.String("status", "success")))

		slog.Info("workflow execution completed", "workflow", wf.Name, "workflow_id", exec.WorkflowID, "duration_ms", duration.Milliseconds())

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":       "completed",
			"workflow_id":  exec.WorkflowID,
			"duration_ms":  duration.Milliseconds(),
			"task_results": exec.TaskResults,
		})
	})

	// Execution status endpoint
	mux.HandleFunc("/v1/executions/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		// Extract workflow ID from path
		execID := r.URL.Path[len("/v1/executions/"):]
		if execID == "" {
			http.Error(w, "execution ID required", http.StatusBadRequest)
			return
		}

		exec, ok, err := store.GetExecution(r.Context(), execID)
		if err != nil {
			http.Error(w, "failed to get execution", http.StatusInternalServerError)
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}

		_ = json.NewEncoder(w).Encode(exec)
	})

	// Workflow cancellation endpoint
	mux.HandleFunc("/v1/cancel/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		// Extract workflow ID from path
		workflowID := r.URL.Path[len("/v1/cancel/"):]
		if workflowID == "" {
			http.Error(w, "workflow ID required", http.StatusBadRequest)
			return
		}

		// Parse cancellation reason
		var req struct {
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			req.Reason = "user requested"
		}

		if err := cancellationMgr.Cancel(r.Context(), workflowID, req.Reason); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		slog.Info("workflow cancelled", "workflow_id", workflowID, "reason", req.Reason)

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "cancelled",
			"workflow_id": workflowID,
			"reason":      req.Reason,
		})
	})

	// List active executions endpoint
	mux.HandleFunc("/v1/executions/active", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		active := cancellationMgr.ListActive()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"active_executions": active,
			"count":             len(active),
		})
	})

	// Schedule management endpoints
	mux.HandleFunc("/v1/schedules", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodPost:
			var config ScheduleConfig
			if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}

			if err := scheduler.AddSchedule(r.Context(), &config); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(config)

		case http.MethodGet:
			schedules, err := scheduler.ListSchedules(r.Context())
			if err != nil {
				http.Error(w, "failed to list schedules", http.StatusInternalServerError)
				return
			}

			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"schedules": schedules,
				"count":     len(schedules),
				"stats":     scheduler.GetScheduleStats(),
			})

		case http.MethodDelete:
			workflowName := r.URL.Query().Get("workflow")
			if workflowName == "" {
				http.Error(w, "workflow name required", http.StatusBadRequest)
				return
			}

			if err := scheduler.RemoveSchedule(r.Context(), workflowName); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status": "removed",
			})

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// Event trigger endpoint
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			EventType string                 `json:"event_type"`
			EventData map[string]interface{} `json:"event_data"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if err := scheduler.TriggerEvent(r.Context(), req.EventType, req.EventData); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "triggered",
		})
	})

	// Database statistics endpoint
	mux.HandleFunc("/v1/stats/db", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		stats := store.GetStats()
		_ = json.NewEncoder(w).Encode(stats)
	})

	if promHandler != nil {
		if h, ok := promHandler.(http.Handler); ok {
			mux.Handle("/metrics", h)
		}
	}

	srv := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			cancel()
		}
	}()
	slog.Info("service started")
	<-ctx.Done()
	slog.Info("shutdown initiated")

	// Cancel all running workflows
	cancelled := cancellationMgr.CancelAll(context.Background(), "server shutdown")
	slog.Info("cancelled running workflows", "count", cancelled)

	ctxSd, c2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer c2()
	_ = srv.Shutdown(ctxSd)
	otelinit.Flush(ctxSd, shutdownTrace)
	_ = shutdownMetrics(ctxSd)
	slog.Info("shutdown complete")
}
