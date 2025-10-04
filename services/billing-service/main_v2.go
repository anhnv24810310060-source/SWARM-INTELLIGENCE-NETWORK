package main

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
	"sync"
	"syscall"
	"time"

	logging "github.com/swarmguard/libs/go/core/logging"
	"github.com/swarmguard/libs/go/core/otelinit"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const serviceName = "billing-service"

// PricingTier defines pricing model
type PricingTier string

const (
	TierStarter     PricingTier = "starter"
	TierProfessional PricingTier = "professional"
	TierEnterprise  PricingTier = "enterprise"
	TierGlobal      PricingTier = "global"
)

// TierLimits defines resource limits per tier
type TierLimits struct {
	MaxAPICallsPerDay       int
	MaxEventsPerDay         int
	MaxStorageGB            int
	MaxNodesAllowed         int
	PricePerNodePerMonth    float64
	PricePerAPICall         float64
	PricePerEvent           float64
	PricePerStorageGB       float64
	SupportLevel            string
	SLAPercentage           float64
}

var tierConfigs = map[PricingTier]TierLimits{
	TierStarter: {
		MaxAPICallsPerDay:    10000,
		MaxEventsPerDay:      50000,
		MaxStorageGB:         10,
		MaxNodesAllowed:      10,
		PricePerNodePerMonth: 50.0,
		PricePerAPICall:      0.0001, // $0.01 per 100 calls
		PricePerEvent:        0.00005,
		PricePerStorageGB:    5.0,
		SupportLevel:         "community",
		SLAPercentage:        99.9,
	},
	TierProfessional: {
		MaxAPICallsPerDay:    100000,
		MaxEventsPerDay:      500000,
		MaxStorageGB:         100,
		MaxNodesAllowed:      100,
		PricePerNodePerMonth: 200.0,
		PricePerAPICall:      0.00008,
		PricePerEvent:        0.00004,
		PricePerStorageGB:    4.0,
		SupportLevel:         "priority",
		SLAPercentage:        99.95,
	},
	TierEnterprise: {
		MaxAPICallsPerDay:    1000000,
		MaxEventsPerDay:      5000000,
		MaxStorageGB:         1000,
		MaxNodesAllowed:      1000,
		PricePerNodePerMonth: 500.0,
		PricePerAPICall:      0.00005,
		PricePerEvent:        0.00003,
		PricePerStorageGB:    3.0,
		SupportLevel:         "dedicated",
		SLAPercentage:        99.99,
	},
	TierGlobal: {
		MaxAPICallsPerDay:    -1, // unlimited
		MaxEventsPerDay:      -1,
		MaxStorageGB:         -1,
		MaxNodesAllowed:      -1,
		PricePerNodePerMonth: 0, // custom pricing
		PricePerAPICall:      0,
		PricePerEvent:        0,
		PricePerStorageGB:    0,
		SupportLevel:         "enterprise+",
		SLAPercentage:        99.999,
	},
}

// UsageRecord tracks resource consumption
type UsageRecord struct {
	CustomerID    string
	Tier          PricingTier
	Date          time.Time
	
	// Counters
	APICalls      uint64
	EventsIngested uint64
	StorageUsedGB  float64
	NodesActive    int
	
	// Unique counts (using HyperLogLog)
	UniqueUsers   *HyperLogLog
	UniqueIPs     *HyperLogLog
	
	// Top-K tracking
	TopEndpoints  *CountMinSketch
	
	mu sync.RWMutex
}

// NewUsageRecord creates new usage tracker
func NewUsageRecord(customerID string, tier PricingTier) *UsageRecord {
	return &UsageRecord{
		CustomerID:   customerID,
		Tier:         tier,
		Date:         time.Now().Truncate(24 * time.Hour),
		UniqueUsers:  NewHyperLogLog(),
		UniqueIPs:    NewHyperLogLog(),
		TopEndpoints: NewCountMinSketch(0.01, 0.01), // 1% error, 99% confidence
	}
}

// RecordAPICall tracks API usage
func (ur *UsageRecord) RecordAPICall(endpoint, userID, ip string) {
	ur.mu.Lock()
	defer ur.mu.Unlock()
	
	ur.APICalls++
	ur.UniqueUsers.Add([]byte(userID))
	ur.UniqueIPs.Add([]byte(ip))
	ur.TopEndpoints.Add([]byte(endpoint), 1)
}

// RecordEvent tracks event ingestion
func (ur *UsageRecord) RecordEvent(count uint64) {
	ur.mu.Lock()
	defer ur.mu.Unlock()
	
	ur.EventsIngested += count
}

// UpdateStorage updates storage usage
func (ur *UsageRecord) UpdateStorage(gb float64) {
	ur.mu.Lock()
	defer ur.mu.Unlock()
	
	ur.StorageUsedGB = gb
}

// UpdateActiveNodes updates node count
func (ur *UsageRecord) UpdateActiveNodes(count int) {
	ur.mu.Lock()
	defer ur.mu.Unlock()
	
	ur.NodesActive = count
}

// CalculateCost computes bill for usage period
func (ur *UsageRecord) CalculateCost() Invoice {
	ur.mu.RLock()
	defer ur.mu.RUnlock()
	
	limits := tierConfigs[ur.Tier]
	
	invoice := Invoice{
		CustomerID:  ur.CustomerID,
		Period:      ur.Date,
		Tier:        ur.Tier,
		LineItems:   make([]LineItem, 0),
	}
	
	// Node costs
	if limits.PricePerNodePerMonth > 0 {
		nodeCost := float64(ur.NodesActive) * limits.PricePerNodePerMonth
		invoice.LineItems = append(invoice.LineItems, LineItem{
			Description: fmt.Sprintf("Nodes (%d active)", ur.NodesActive),
			Quantity:    float64(ur.NodesActive),
			UnitPrice:   limits.PricePerNodePerMonth,
			Total:       nodeCost,
		})
		invoice.Subtotal += nodeCost
	}
	
	// API call costs (with overage)
	if limits.MaxAPICallsPerDay > 0 && ur.APICalls > uint64(limits.MaxAPICallsPerDay) {
		overage := ur.APICalls - uint64(limits.MaxAPICallsPerDay)
		overageCost := float64(overage) * limits.PricePerAPICall
		invoice.LineItems = append(invoice.LineItems, LineItem{
			Description: fmt.Sprintf("API Calls Overage (%d calls)", overage),
			Quantity:    float64(overage),
			UnitPrice:   limits.PricePerAPICall,
			Total:       overageCost,
		})
		invoice.Subtotal += overageCost
	}
	
	// Event processing costs (with overage)
	if limits.MaxEventsPerDay > 0 && ur.EventsIngested > uint64(limits.MaxEventsPerDay) {
		overage := ur.EventsIngested - uint64(limits.MaxEventsPerDay)
		overageCost := float64(overage) * limits.PricePerEvent
		invoice.LineItems = append(invoice.LineItems, LineItem{
			Description: fmt.Sprintf("Event Processing Overage (%d events)", overage),
			Quantity:    float64(overage),
			UnitPrice:   limits.PricePerEvent,
			Total:       overageCost,
		})
		invoice.Subtotal += overageCost
	}
	
	// Storage costs (with overage)
	if limits.MaxStorageGB > 0 && ur.StorageUsedGB > float64(limits.MaxStorageGB) {
		overage := ur.StorageUsedGB - float64(limits.MaxStorageGB)
		overageCost := overage * limits.PricePerStorageGB
		invoice.LineItems = append(invoice.LineItems, LineItem{
			Description: fmt.Sprintf("Storage Overage (%.2f GB)", overage),
			Quantity:    overage,
			UnitPrice:   limits.PricePerStorageGB,
			Total:       overageCost,
		})
		invoice.Subtotal += overageCost
	}
	
	// Apply discounts for high volume
	if ur.APICalls > 1000000 {
		discount := invoice.Subtotal * 0.1 // 10% discount
		invoice.Discount = discount
	}
	
	invoice.Tax = invoice.Subtotal * 0.08 // 8% tax (simplified)
	invoice.Total = invoice.Subtotal - invoice.Discount + invoice.Tax
	
	return invoice
}

// GetStats returns usage statistics
func (ur *UsageRecord) GetStats() UsageStats {
	ur.mu.RLock()
	defer ur.mu.RUnlock()
	
	return UsageStats{
		CustomerID:     ur.CustomerID,
		Tier:           ur.Tier,
		APICalls:       ur.APICalls,
		EventsIngested: ur.EventsIngested,
		StorageUsedGB:  ur.StorageUsedGB,
		NodesActive:    ur.NodesActive,
		UniqueUsers:    ur.UniqueUsers.Count(),
		UniqueIPs:      ur.UniqueIPs.Count(),
	}
}

// Invoice represents billing statement
type Invoice struct {
	CustomerID string
	Period     time.Time
	Tier       PricingTier
	LineItems  []LineItem
	Subtotal   float64
	Discount   float64
	Tax        float64
	Total      float64
	GeneratedAt time.Time
}

type LineItem struct {
	Description string
	Quantity    float64
	UnitPrice   float64
	Total       float64
}

type UsageStats struct {
	CustomerID     string
	Tier           PricingTier
	APICalls       uint64
	EventsIngested uint64
	StorageUsedGB  float64
	NodesActive    int
	UniqueUsers    uint64
	UniqueIPs      uint64
}

// BillingService manages usage tracking and billing
type BillingService struct {
	mu sync.RWMutex
	customers map[string]*UsageRecord
	
	// Metrics
	usageCounter   metric.Int64Counter
	revenueGauge   metric.Float64ObservableGauge
	
	totalRevenue   float64
}

func NewBillingService(meter metric.Meter) *BillingService {
	usageCounter, _ := meter.Int64Counter("swarm_billing_usage_total")
	revenueGauge, _ := meter.Float64ObservableGauge("swarm_billing_revenue_usd")
	
	bs := &BillingService{
		customers:    make(map[string]*UsageRecord),
		usageCounter: usageCounter,
		revenueGauge: revenueGauge,
	}
	
	meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		bs.mu.RLock()
		defer bs.mu.RUnlock()
		o.ObserveFloat64(revenueGauge, bs.totalRevenue)
		return nil
	}, revenueGauge)
	
	return bs
}

func (bs *BillingService) GetOrCreateUsage(customerID string, tier PricingTier) *UsageRecord {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	
	if usage, exists := bs.customers[customerID]; exists {
		return usage
	}
	
	usage := NewUsageRecord(customerID, tier)
	bs.customers[customerID] = usage
	return usage
}

func (bs *BillingService) GenerateInvoice(customerID string) (Invoice, error) {
	bs.mu.RLock()
	usage, exists := bs.customers[customerID]
	bs.mu.RUnlock()
	
	if !exists {
		return Invoice{}, fmt.Errorf("customer not found: %s", customerID)
	}
	
	invoice := usage.CalculateCost()
	invoice.GeneratedAt = time.Now()
	
	// Update total revenue
	bs.mu.Lock()
	bs.totalRevenue += invoice.Total
	bs.mu.Unlock()
	
	return invoice, nil
}

func main() {
	logging.Init(serviceName)
	
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	
	shutdownTrace := otelinit.InitTracer(ctx, serviceName)
	shutdownMetrics, promHandler, _ := otelinit.InitMetrics(ctx, serviceName)
	
	meter := otel.GetMeterProvider().Meter(serviceName)
	billingService := NewBillingService(meter)
	
	mux := http.NewServeMux()
	
	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
	})
	
	// Record usage endpoint
	mux.HandleFunc("/billing/usage", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		
		var req struct {
			CustomerID string      `json:"customer_id"`
			Tier       PricingTier `json:"tier"`
			Type       string      `json:"type"` // "api_call", "event", "storage", "nodes"
			Count      uint64      `json:"count"`
			Endpoint   string      `json:"endpoint,omitempty"`
			UserID     string      `json:"user_id,omitempty"`
			IP         string      `json:"ip,omitempty"`
		}
		
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		
		usage := billingService.GetOrCreateUsage(req.CustomerID, req.Tier)
		
		switch req.Type {
		case "api_call":
			usage.RecordAPICall(req.Endpoint, req.UserID, req.IP)
		case "event":
			usage.RecordEvent(req.Count)
		case "storage":
			usage.UpdateStorage(float64(req.Count))
		case "nodes":
			usage.UpdateActiveNodes(int(req.Count))
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown type"})
			return
		}
		
		billingService.usageCounter.Add(r.Context(), int64(req.Count), metric.WithAttributes(
			attribute.String("customer", req.CustomerID),
			attribute.String("type", req.Type),
		))
		
		writeJSON(w, http.StatusOK, map[string]string{"status": "recorded"})
	})
	
	// Get usage stats
	mux.HandleFunc("/billing/stats", func(w http.ResponseWriter, r *http.Request) {
		customerID := r.URL.Query().Get("customer_id")
		if customerID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "customer_id required"})
			return
		}
		
		billingService.mu.RLock()
		usage, exists := billingService.customers[customerID]
		billingService.mu.RUnlock()
		
		if !exists {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "customer not found"})
			return
		}
		
		stats := usage.GetStats()
		writeJSON(w, http.StatusOK, stats)
	})
	
	// Generate invoice
	mux.HandleFunc("/billing/invoice", func(w http.ResponseWriter, r *http.Request) {
		customerID := r.URL.Query().Get("customer_id")
		if customerID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "customer_id required"})
			return
		}
		
		invoice, err := billingService.GenerateInvoice(customerID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		
		writeJSON(w, http.StatusOK, invoice)
	})
	
	// Get pricing tiers
	mux.HandleFunc("/billing/tiers", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, tierConfigs)
	})
	
	// Metrics endpoint
	if promHandler != nil {
		if h, ok := promHandler.(http.Handler); ok {
			mux.Handle("/metrics", h)
		}
	}
	
	srv := &http.Server{
		Addr:    ":" + getEnv("PORT", "8080"),
		Handler: mux,
	}
	
	go func() {
		slog.Info("billing service started", "addr", srv.Addr)
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
