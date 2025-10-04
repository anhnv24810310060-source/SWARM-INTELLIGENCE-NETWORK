package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	sloglog "github.com/swarmguard/libs/go/core/logging"
	"github.com/swarmguard/libs/go/core/otelinit"
	"github.com/swarmguard/threat-intel/internal"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

func main() {
	service := "threat-intel"
	sloglog.Init(service)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	shutdownTrace := otelinit.InitTracer(ctx, service)
	shutdownMetrics, promHandler, _ := otelinit.InitMetrics(ctx, service)
	store := internal.NewMemoryIndicatorStore(5) // 32 shards
	corr := internal.NewSimpleCorrelator(store)
	meter := otel.Meter("swarm-go")
	ingestCounter, _ := meter.Int64Counter("swarm_threat_indicators_total")
	threatCounter, _ := meter.Int64Counter("swarm_threats_detected_total")
	purgeDur, _ := meter.Float64Histogram("swarm_threat_store_purge_duration_seconds")
	feedLag, _ := meter.Float64Histogram("swarm_threat_feeds_sync_lag_seconds") // placeholder usage
	// Launch TTL purge loop
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				t0 := time.Now()
				store.PurgeExpired()
				purgeDur.Record(context.Background(), time.Since(t0).Seconds())
			}
		}
	}()
	_ = feedLag // will be used when collectors implemented

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/indicators", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var payload any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			now := time.Now()
			toProcess := []internal.Indicator{}
			switch v := payload.(type) {
			case map[string]any:
				if id, ok := decodeIndicator(v, now); ok {
					toProcess = append(toProcess, id)
				}
			case []any:
				for _, it := range v {
					if m, ok := it.(map[string]any); ok {
						if id, ok2 := decodeIndicator(m, now); ok2 {
							toProcess = append(toProcess, id)
						}
					}
				}
			default:
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			threats := []internal.Threat{}
			for _, ind := range toProcess {
				if err := store.Upsert(ind); err == nil {
					ingestCounter.Add(r.Context(), 1, attribute.String("type", string(ind.Type)))
					ts, _ := corr.Correlate(ind)
					threats = append(threats, ts...)
				}
			}
			for range threats {
				threatCounter.Add(r.Context(), 1)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ingested": len(toProcess), "threats": threats})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/v1/indicator/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		val := r.URL.Path[len("/v1/indicator/"):]
		ind, ok := store.Get(val)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ind)
	})
	mux.HandleFunc("/v1/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// derive simple stats
		total := 0
		store.Iter(func(in internal.Indicator) bool { total++; return true })
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"indicators": total})
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
	// TODO: implement external feed collectors (OTX, VirusTotal, etc.) updating feedLag metric
}

// decodeIndicator converts dynamic map to Indicator with basic validation
func decodeIndicator(m map[string]any, now time.Time) (internal.Indicator, bool) {
	val, _ := m["value"].(string)
	typ, _ := m["type"].(string)
	src, _ := m["source"].(string)
	if val == "" || typ == "" {
		return internal.Indicator{}, false
	}
	scoreF := parseFloatAny(m["score"], 5.0)
	ttlSec := parseFloatAny(m["ttl_seconds"], 3600)
	ind := internal.Indicator{Value: val, Type: internal.IndicatorType(typ), Source: src, FirstSeen: now, LastSeen: now, Score: scoreF, TTL: time.Duration(ttlSec) * time.Second}
	return ind, true
}

func parseFloatAny(v any, def float64) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case string:
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return f
		}
	}
	return def
}
