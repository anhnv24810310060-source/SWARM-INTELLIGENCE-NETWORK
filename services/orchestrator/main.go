package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

	logging "github.com/swarmguard/libs/go/core/logging"
	"github.com/swarmguard/libs/go/core/otelinit"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// DAG workflow minimal structs

type TaskType string

const (
	TaskHTTP   TaskType = "http"
	TaskPython TaskType = "python" // placeholder
)

type Task struct {
	ID        string   `json:"id"`
	Type      TaskType `json:"type"`
	DependsOn []string `json:"depends_on"`
	URL       string   `json:"url,omitempty"`
}

type Workflow struct {
	Name  string `json:"name"`
	Tasks []Task `json:"tasks"`
}

type runRequest struct {
	Workflow string `json:"workflow"`
}

type workflowStore struct {
	mu sync.RWMutex
	wf map[string]Workflow
}

func newStore() *workflowStore          { return &workflowStore{wf: make(map[string]Workflow)} }
func (s *workflowStore) put(w Workflow) { s.mu.Lock(); defer s.mu.Unlock(); s.wf[w.Name] = w }
func (s *workflowStore) get(name string) (Workflow, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w, ok := s.wf[name]
	return w, ok
}

// simple executor: run tasks whose deps satisfied sequentially (no parallel for now)
func execute(ctx context.Context, w Workflow, taskDur metric.Float64Histogram) error {
	indeg := map[string]int{}
	adj := map[string][]string{}
	tasksByID := map[string]Task{}
	for _, t := range w.Tasks {
		tasksByID[t.ID] = t
		if _, ok := indeg[t.ID]; !ok {
			indeg[t.ID] = 0
		}
		for _, d := range t.DependsOn {
			indeg[t.ID]++
			adj[d] = append(adj[d], t.ID)
		}
	}
	// ready queue
	ready := make(chan string, len(w.Tasks))
	for id, v := range indeg {
		if v == 0 {
			ready <- id
		}
	}
	completed := 0
	total := len(w.Tasks)
	var mu sync.Mutex
	workers := 4
	ctxExec, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctxExec.Done():
					return
				case id := <-ready:
					t, ok := tasksByID[id]
					if !ok {
						continue
					}
					start := time.Now()
					slog.Info("task start", "workflow", w.Name, "task", t.ID, "type", t.Type)
					// simulate execution cost
					select {
					case <-time.After(10 * time.Millisecond):
					case <-ctxExec.Done():
						return
					}
					taskDur.Record(ctxExec, float64(time.Since(start).Milliseconds()), metric.WithAttributes(attribute.String("workflow", w.Name), attribute.String("task_type", string(t.Type))))
					slog.Info("task done", "workflow", w.Name, "task", t.ID)
					mu.Lock()
					completed++
					for _, nxt := range adj[id] {
						indeg[nxt]--
						if indeg[nxt] == 0 {
							ready <- nxt
						}
					}
					doneAll := completed == total
					mu.Unlock()
					if doneAll {
						cancel()
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	if completed != total {
		return errors.New("deadlock or unmet dependencies")
	}
	return nil
}

func main() {
	service := "orchestrator"
	logging.Init(service)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	shutdownTrace := otelinit.InitTracer(ctx, service)
	shutdownMetrics, promHandler, _ := otelinit.InitMetrics(ctx, service)

	st := newStore()
	// seed sample workflow
	st.put(Workflow{Name: "sample", Tasks: []Task{{ID: "a", Type: TaskHTTP}, {ID: "b", Type: TaskHTTP, DependsOn: []string{"a"}}}})

	mux := http.NewServeMux()
	meter := otel.GetMeterProvider().Meter("orchestrator")
	runCounter, _ := meter.Int64Counter("swarm_workflow_runs_total")
	runErrors, _ := meter.Int64Counter("swarm_workflow_run_errors_total")
	wfLatency, _ := meter.Float64Histogram("swarm_workflow_duration_ms")
	taskLatency, _ := meter.Float64Histogram("swarm_workflow_task_duration_ms")
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/workflows", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var wf Workflow
			if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if wf.Name == "" {
				http.Error(w, "name required", http.StatusBadRequest)
				return
			}
			st.put(wf)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(wf)
			return
		}
		if r.Method == http.MethodGet {
			name := r.URL.Query().Get("name")
			wf, ok := st.get(name)
			if !ok {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(wf)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("/v1/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req runRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		wf, ok := st.get(req.Workflow)
		if !ok {
			http.Error(w, "workflow not found", http.StatusNotFound)
			return
		}
		ctxExec, cancelExec := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancelExec()
		start := time.Now()
		if err := execute(ctxExec, wf, taskLatency); err != nil {
			runErrors.Add(r.Context(), 1, metric.WithAttributes(attribute.String("workflow", wf.Name)))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		wfLatency.Record(r.Context(), float64(time.Since(start).Milliseconds()), metric.WithAttributes(attribute.String("workflow", wf.Name)))
		runCounter.Add(r.Context(), 1, metric.WithAttributes(attribute.String("workflow", wf.Name)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("completed"))
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
	ctxSd, c2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer c2()
	_ = srv.Shutdown(ctxSd)
	otelinit.Flush(ctxSd, shutdownTrace)
	_ = shutdownMetrics(ctxSd)
	slog.Info("shutdown complete")
}
