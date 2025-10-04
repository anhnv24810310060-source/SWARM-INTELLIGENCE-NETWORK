package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	logging "github.com/swarmguard/libs/go/core/logging"
	"github.com/swarmguard/libs/go/core/otelinit"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc"
)

const serviceName = "federation"

func main() {
	logging.Init(serviceName)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	shutdownTrace := otelinit.InitTracer(ctx, serviceName)
	shutdownMetrics, promHandler, _ := otelinit.InitMetrics(ctx, serviceName)

	// Initialize federated state with CRDT
	nodeID := getEnv("NODE_ID", fmt.Sprintf("node-%d", os.Getpid()))
	federatedState := NewFederatedState(nodeID)

	// Setup metrics
	meter := otel.GetMeterProvider().Meter(serviceName)
	syncCounter, _ := meter.Int64Counter("swarm_federation_syncs_total")
	syncErrors, _ := meter.Int64Counter("swarm_federation_sync_errors_total")
	peerGauge, _ := meter.Int64ObservableGauge("swarm_federation_peers_total")
	activePeerGauge, _ := meter.Int64ObservableGauge("swarm_federation_active_peers")
	threatIntelGauge, _ := meter.Int64ObservableGauge("swarm_federation_threat_intel_count")

	// Register metrics callback
	meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		stats := federatedState.GetStats()
		o.ObserveInt64(peerGauge, int64(stats.TotalPeers))
		o.ObserveInt64(activePeerGauge, int64(stats.ActivePeers))
		o.ObserveInt64(threatIntelGauge, int64(stats.ThreatIntelCount))
		return nil
	}, peerGauge, activePeerGauge, threatIntelGauge)

	// Start anti-entropy background process
	go federatedState.StartAntiEntropy(ctx)

	// gRPC server for high-performance sync
	grpcServer := grpc.NewServer()
	// TODO: Register federation gRPC service

	lis, err := net.Listen("tcp", ":9090")
	if err != nil {
		slog.Error("grpc listen failed", "error", err)
		return
	}

	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			slog.Error("grpc serve error", "error", err)
			cancel()
		}
	}()

	// HTTP server for REST API
	mux := http.NewServeMux()

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "healthy",
			"node_id": nodeID,
		})
	})

	// Peer management endpoints
	mux.HandleFunc("/federation/peers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// Add new peer
			var node FederationNode
			if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
				return
			}

			if err := federatedState.AddPeer(&node); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}

			writeJSON(w, http.StatusCreated, map[string]string{"status": "peer added"})
			return
		}

		if r.Method == http.MethodGet {
			// List peers
			stats := federatedState.GetStats()
			writeJSON(w, http.StatusOK, stats)
			return
		}

		w.WriteHeader(http.StatusMethodNotAllowed)
	})

	// Sync endpoint (receives sync messages from peers)
	mux.HandleFunc("/federation/sync", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10MB limit
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
			return
		}

		var msg SyncMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid sync message"})
			return
		}

		// Handle sync message
		if err := federatedState.HandleSyncMessage(msg); err != nil {
			syncErrors.Add(r.Context(), 1, metric.WithAttributes(
				attribute.String("peer", msg.FromNode),
			))
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		syncCounter.Add(r.Context(), 1, metric.WithAttributes(
			attribute.String("type", string(msg.Type)),
			attribute.String("peer", msg.FromNode),
		))

		writeJSON(w, http.StatusOK, map[string]string{"status": "synced"})
	})

	// Threat intelligence endpoints
	mux.HandleFunc("/federation/threats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// Add threat intel
			var intel map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&intel); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
				return
			}

			key, ok := intel["id"].(string)
			if !ok {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
				return
			}

			federatedState.UpdateThreatIntel(key, intel)
			writeJSON(w, http.StatusCreated, map[string]string{"status": "threat intel added"})
			return
		}

		if r.Method == http.MethodGet {
			// Get threat intel
			key := r.URL.Query().Get("id")
			if key == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
				return
			}

			intel, exists := federatedState.GetThreatIntel(key)
			if !exists {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
				return
			}

			writeJSON(w, http.StatusOK, intel)
			return
		}

		w.WriteHeader(http.StatusMethodNotAllowed)
	})

	// Detection rules endpoints
	mux.HandleFunc("/federation/rules", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// Add rule
			var req struct {
				RuleID string `json:"rule_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
				return
			}

			federatedState.AddDetectionRule(req.RuleID)
			writeJSON(w, http.StatusCreated, map[string]string{"status": "rule added"})
			return
		}

		if r.Method == http.MethodGet {
			// List active rules
			rules := federatedState.GetActiveRules()
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"rules": rules,
				"count": len(rules),
			})
			return
		}

		w.WriteHeader(http.StatusMethodNotAllowed)
	})

	// Metrics endpoint
	if promHandler != nil {
		if h, ok := promHandler.(http.Handler); ok {
			mux.Handle("/metrics", h)
		}
	}

	httpSrv := &http.Server{
		Addr:         ":8080",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("federation service started", "node_id", nodeID, "grpc_port", 9090, "http_port", 8080)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server error", "error", err)
			cancel()
		}
	}()

	<-ctx.Done()
	slog.Info("shutdown initiated")

	// Graceful shutdown
	grpcServer.GracefulStop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown error", "error", err)
	}

	otelinit.Flush(shutdownCtx, shutdownTrace)
	_ = shutdownMetrics(shutdownCtx)

	slog.Info("shutdown complete")
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
