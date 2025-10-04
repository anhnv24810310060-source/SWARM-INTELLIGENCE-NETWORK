package mainpackage main


import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	logging "github.com/swarmguard/libs/go/core/logging"
	"github.com/swarmguard/libs/go/core/otelinit"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const (
	serviceName = "api-gateway-v2"
	version     = "2.0.0"
)

// Gateway manages all gateway components
type Gateway struct {
	rateLimiter    *PerKeyRateLimiter
	circuitBreakers *CircuitBreakerPool
	validator      *RequestValidator
	
	// Metrics
	reqCounter     metric.Int64Counter
	latencyHist    metric.Float64Histogram
	rlDenied       metric.Int64Counter
	authDenied     metric.Int64Counter
	validationFail metric.Int64Counter
	cbOpen         metric.Int64Counter
	
	// Service registry (for circuit breakers)
	services map[string]string
	mu       sync.RWMutex
}

func newGateway(meter metric.Meter) *Gateway {
	// Initialize rate limiter with hybrid strategy
	rateLimiter := NewPerKeyRateLimiter(RateLimitConfig{
		Capacity:     200,                // burst capacity
		Refill:       200,                // refill rate
		Interval:     1 * time.Minute,    // refill interval
		WindowSize:   1 * time.Minute,    // sliding window
		RequestLimit: 300,                // max requests in window
	})
	
	// Initialize circuit breaker pool
	cbPool := NewCircuitBreakerPool(CircuitBreakerConfig{
		MaxFailures: 5,
		Timeout:     2 * time.Second,
		Cooldown:    30 * time.Second,
	})
	
	// Initialize request validator
	validator := NewRequestValidator()
	
	// Setup metrics
	reqCounter, _ := meter.Int64Counter("swarm_api_requests_total")
	latencyHist, _ := meter.Float64Histogram("swarm_api_latency_ms")
	rlDenied, _ := meter.Int64Counter("swarm_api_rate_limited_total")
	authDenied, _ := meter.Int64Counter("swarm_api_auth_denied_total")
	validationFail, _ := meter.Int64Counter("swarm_api_validation_failed_total")
	cbOpen, _ := meter.Int64Counter("swarm_api_circuit_open_total")
	
	return &Gateway{
		rateLimiter:     rateLimiter,
		circuitBreakers: cbPool,
		validator:       validator,
		reqCounter:      reqCounter,
		latencyHist:     latencyHist,
		rlDenied:        rlDenied,
		authDenied:      authDenied,
		validationFail:  validationFail,
		cbOpen:          cbOpen,
		services: map[string]string{
			"threat-intel":     getEnv("THREAT_INTEL_URL", "http://threat-intel:8080"),
			"detection":        getEnv("DETECTION_URL", "http://detection-service:8080"),
			"policy":           getEnv("POLICY_URL", "http://policy-service:8080"),
			"orchestrator":     getEnv("ORCHESTRATOR_URL", "http://orchestrator:8080"),
		},
	}
}

// Middleware for authentication
func (g *Gateway) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract and validate JWT
		token := extractToken(r)
		if token == "" {
			g.authDenied.Add(r.Context(), 1)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing authorization"})
			return
		}
		
		// Simple token validation (in production, verify JWT signature)
		if !isValidToken(token) {
			g.authDenied.Add(r.Context(), 1)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}
		
		// Add user context
		ctx := context.WithValue(r.Context(), "user_id", extractUserID(token))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Middleware for rate limiting
func (g *Gateway) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := getRateLimitKey(r)
		
		if !g.rateLimiter.Allow(key) {
			g.rlDenied.Add(r.Context(), 1, metric.WithAttributes(
				attribute.String("key", maskKey(key)),
			))
			writeJSON(w, http.StatusTooManyRequests, map[string]string{
				"error": "rate limit exceeded",
				"retry_after": "60",
			})
			return
		}
		
		next.ServeHTTP(w, r)
	})
}

// Middleware for request logging and metrics
func (g *Gateway) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		
		// Create tracer span
		ctx, span := otel.Tracer(serviceName).Start(r.Context(), r.URL.Path)
		defer span.End()
		
		// Request ID
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = generateRequestID()
		}
		w.Header().Set("X-Request-ID", reqID)
		
		// Wrap response writer to capture status
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		
		// Add span attributes
		span.SetAttributes(
			attribute.String("http.method", r.Method),
			attribute.String("http.url", r.URL.Path),
			attribute.String("http.request_id", reqID),
		)
		
		next.ServeHTTP(rw, r.WithContext(ctx))
		
		// Record metrics
		duration := float64(time.Since(start).Milliseconds())
		
		g.reqCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("method", r.Method),
			attribute.String("path", r.URL.Path),
			attribute.Int("status", rw.status),
		))
		
		g.latencyHist.Record(ctx, duration, metric.WithAttributes(
			attribute.String("path", r.URL.Path),
		))
		
		span.SetAttributes(
			attribute.Int("http.status_code", rw.status),
			attribute.Float64("http.duration_ms", duration),
		)
		
		slog.InfoContext(ctx, "request completed",
			"request_id", reqID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", duration,
			"remote_addr", r.RemoteAddr,
		)
	})
}

// Handler for event ingestion with validation
func (g *Gateway) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	
	ctx, span := otel.Tracer(serviceName).Start(r.Context(), "ingest.validate")
	defer span.End()
	
	// Read body with size limit
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20)) // 2MB limit
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
		return
	}
	
	// Validate request
	if err := g.validator.ValidateJSON("ingest_event", body); err != nil {
		g.validationFail.Add(ctx, 1, metric.WithAttributes(
			attribute.String("schema", "ingest_event"),
		))
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	
	// Parse validated JSON
	var event map[string]interface{}
	if err := json.Unmarshal(body, &event); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	
	// Forward to detection service with circuit breaker
	cb := g.circuitBreakers.Get("detection")
	
	err = cb.Execute(ctx, func(ctx context.Context) error {
		return g.forwardToService(ctx, "detection", "/v1/ingest", body)
	})
	
	if err != nil {
		if err == ErrCircuitOpen {
			g.cbOpen.Add(ctx, 1, metric.WithAttributes(
				attribute.String("service", "detection"),
			))
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "service temporarily unavailable",
			})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to forward request"})
		return
	}
	
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status": "accepted",
		"id":     event["id"].(string),
	})
}

// Handler for threat reporting
func (g *Gateway) handleThreatReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	
	ctx := r.Context()
	
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
		return
	}
	
	// Validate
	if err := g.validator.ValidateJSON("threat_report", body); err != nil {
		g.validationFail.Add(ctx, 1)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	
	// Forward to threat-intel service
	cb := g.circuitBreakers.Get("threat-intel")
	err = cb.Execute(ctx, func(ctx context.Context) error {
		return g.forwardToService(ctx, "threat-intel", "/v1/threats", body)
	})
	
	if err != nil {
		if err == ErrCircuitOpen {
			g.cbOpen.Add(ctx, 1)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "service temporarily unavailable",
			})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to forward"})
		return
	}
	
	writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

// Handler for circuit breaker stats (internal)
func (g *Gateway) handleCircuitStats(w http.ResponseWriter, r *http.Request) {
	stats := g.circuitBreakers.GetAllStats()
	writeJSON(w, http.StatusOK, stats)
}

// Handler for rate limiter stats (internal)
func (g *Gateway) handleRateLimitStats(w http.ResponseWriter, r *http.Request) {
	stats := g.rateLimiter.GetAllStats()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tracked_keys": len(stats),
		"limiters":     stats,
	})
}

// forwardToService sends request to downstream service
func (g *Gateway) forwardToService(ctx context.Context, service, path string, body []byte) error {
	g.mu.RLock()
	baseURL, exists := g.services[service]
	g.mu.RUnlock()
	
	if !exists {
		return fmt.Errorf("service %s not registered", service)
	}
	
	url := baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-By", serviceName)
	
	// Propagate trace context
	otel.GetTextMapPropagator().Inject(ctx, &headerCarrier{req.Header})
	
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode >= 400 {
		return fmt.Errorf("downstream error: %d", resp.StatusCode)
	}
	
	return nil
}

func realMainV2() {
	logging.Init(serviceName)
	
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	
	shutdownTrace := otelinit.InitTracer(ctx, serviceName)
	shutdownMetrics, promHandler, _ := otelinit.InitMetrics(ctx, serviceName)
	
	meter := otel.GetMeterProvider().Meter(serviceName)
	gateway := newGateway(meter)
	
	mux := http.NewServeMux()
	
	// Public endpoints
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "healthy",
			"version": version,
			"service": serviceName,
		})
	})
	
	// Protected endpoints
	publicMux := http.NewServeMux()
	publicMux.HandleFunc("/v1/ingest", gateway.handleIngest)
	publicMux.HandleFunc("/v1/threats", gateway.handleThreatReport)
	
	// Apply middlewares (order matters: logging -> auth -> rate limit -> handler)
	protectedHandler := gateway.loggingMiddleware(
		gateway.authMiddleware(
			gateway.rateLimitMiddleware(publicMux),
		),
	)
	
	mux.Handle("/v1/", protectedHandler)
	
	// Internal endpoints (no auth/rate limit)
	mux.HandleFunc("/internal/circuit-breakers", gateway.handleCircuitStats)
	mux.HandleFunc("/internal/rate-limits", gateway.handleRateLimitStats)
	
	// Metrics endpoint
	if promHandler != nil {
		if h, ok := promHandler.(http.Handler); ok {
			mux.Handle("/metrics", h)
		}
	}
	
	srv := &http.Server{
		Addr:         ":" + getEnv("PORT", "8080"),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	
	go func() {
		slog.Info("starting gateway", "addr", srv.Addr, "version", version)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			cancel()
		}
	}()
	
	<-ctx.Done()
	slog.Info("shutdown initiated")
	
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
	
	otelinit.Flush(shutdownCtx, shutdownTrace)
	_ = shutdownMetrics(shutdownCtx)
	
	slog.Info("shutdown complete")
}

// Utility functions
func extractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func isValidToken(token string) bool {
	// Development mode
	if token == "dev" || token == "test" {
		return true
	}
	// JWT format check (3 parts separated by dots)
	return strings.Count(token, ".") == 2 && len(token) > 20
}

func extractUserID(token string) string {
	// Simplified - in production, decode JWT claims
	if token == "dev" {
		return "dev-user"
	}
	return "user-" + token[:8]
}

func getRateLimitKey(r *http.Request) string {
	// Priority: API key > User ID > IP
	if apiKey := r.Header.Get("X-API-Key"); apiKey != "" {
		return "key:" + apiKey
	}
	if userID := r.Context().Value("user_id"); userID != nil {
		return "user:" + userID.(string)
	}
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.RemoteAddr
	}
	return "ip:" + ip
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return key
	}
	return key[:8] + "***"
}

func generateRequestID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
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

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// headerCarrier adapts http.Header to propagate trace context
type headerCarrier struct {
	header http.Header
}

func (hc *headerCarrier) Get(key string) string {
	return hc.header.Get(key)
}

func (hc *headerCarrier) Set(key, value string) {
	hc.header.Set(key, value)
}

func (hc *headerCarrier) Keys() []string {
	keys := make([]string, 0, len(hc.header))
	for k := range hc.header {
		keys = append(keys, k)
	}
	return keys
}
