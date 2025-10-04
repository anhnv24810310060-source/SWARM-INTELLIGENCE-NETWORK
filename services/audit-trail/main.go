package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	sloglog "github.com/swarmguard/libs/go/core/logging"
	"github.com/swarmguard/libs/go/core/otelinit"

	"github.com/swarmguard/SWARM-INTELLIGENCE-NETWORK/services/audit-trail/internal"
	"go.opentelemetry.io/otel"
)

func main() {
	service := "audit-trail"
	sloglog.Init(service)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Tracing
	shutdownTrace := otelinit.InitTracer(ctx, service)
	// Metrics
	shutdownMetrics, promHandler, _ := otelinit.InitMetrics(ctx, service)

	logStore := internal.NewAppendLog()
	// metrics instruments
	meter := otel.Meter("swarm-go")
	appendCounter, _ := meter.Int64Counter("swarm_audit_events_total")
	verificationCounter, _ := meter.Int64Counter("swarm_audit_verifications_total")

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/append", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct{ Action, Actor, Resource, Metadata string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Action == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("missing action"))
			return
		}
		ent := logStore.Append(body.Action, body.Actor, body.Resource, body.Metadata)
		appendCounter.Add(r.Context(), 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ent)
	})
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		if ent, ok := logStore.Latest(); ok {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/verify", func(w http.ResponseWriter, r *http.Request) {
		verificationCounter.Add(r.Context(), 1)
		if logStore.Verify() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("valid"))
		} else {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("corrupt"))
		}
	})

	// optional import / export future (placeholder)
	_ = os.Getenv("AUDIT_EXPORT_ENABLED")
	if promHandler != nil { // Prometheus exporter exposes /metrics
		if h, ok := promHandler.(http.Handler); ok {
			mux.Handle("/metrics", h)
		}
	}

	srv := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		slog.Info("http server starting", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			cancel()
		}
	}()

	slog.Info("service started")
	<-ctx.Done()
	slog.Info("shutdown initiated")
	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	_ = srv.Shutdown(ctxShutdown)
	otelinit.Flush(ctxShutdown, shutdownTrace)
	_ = shutdownMetrics(ctxShutdown)
	slog.Info("shutdown complete")
	// TODO: Append-only log & Merkle root chain
}
