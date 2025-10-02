package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

	sloglog "github.com/swarmguard/libs/go/core/logging"
	"github.com/swarmguard/libs/go/core/otelinit"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

func main() {
	service := "billing-service"
	sloglog.Init(service)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	shutdownTrace := otelinit.InitTracer(ctx, service)
	shutdownMetrics, promHandler, _ := otelinit.InitMetrics(ctx, service)
	meter := otel.GetMeterProvider().Meter("billing-service")
	usageCounter, _ := meter.Int64Counter("swarm_usage_api_calls_total")
	revenueGauge, _ := meter.Float64ObservableGauge("swarm_billing_revenue_usd")
	var revenueMu sync.RWMutex
	var totalRevenue float64
	_, _ = meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		revenueMu.RLock()
		defer revenueMu.RUnlock()
		o.ObserveFloat64(revenueGauge, totalRevenue, metric.WithAttributes(attribute.String("currency", "USD")))
		return nil
	}, revenueGauge)

	type usageRecord struct {
		Key   string `json:"key"`
		Count int64  `json:"count"`
	}
	var usageMu sync.Mutex
	usage := make(map[string]int64)

	// aggregation loop (simulate pricing 0.001 per call)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				usageMu.Lock()
				var cycleCalls int64
				for k, v := range usage {
					cycleCalls += v
					usage[k] = 0
				}
				usageMu.Unlock()
				if cycleCalls > 0 {
					revenueMu.Lock()
					totalRevenue += float64(cycleCalls) * 0.001
					revenueMu.Unlock()
					slog.Info("billing aggregation", "calls", cycleCalls, "total_revenue", totalRevenue)
				}
			}
		}
	}()

	increment := func(key string) {
		usageCounter.Add(context.Background(), 1, metric.WithAttributes(attribute.String("key", key)))
		usageMu.Lock()
		usage[key]++
		usageMu.Unlock()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/usage", func(w http.ResponseWriter, r *http.Request) {
		usageMu.Lock()
		defer usageMu.Unlock()
		var list []usageRecord
		for k, v := range usage {
			list = append(list, usageRecord{Key: k, Count: v})
		}
		_ = json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc("/v1/call", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		if key == "" {
			key = "default"
		}
		increment(key)
		w.WriteHeader(http.StatusAccepted)
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
	// TODO: Usage aggregation + pricing engine
}
