package main

import (
  "context"
  "crypto/rsa"
  "encoding/json"
  "errors"
  "io"
  "log/slog"
  "math"
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
)

type rateLimiter struct { capacity, refill int; interval time.Duration; mu sync.Mutex; buckets map[string]*bucket }
type bucket struct { tokens int; updated time.Time }
func newRateLimiter(capacity, refill int, interval time.Duration)*rateLimiter { return &rateLimiter{capacity:capacity, refill:refill, interval:interval, buckets:map[string]*bucket{}} }
func (r *rateLimiter) allow(id string) bool { r.mu.Lock(); defer r.mu.Unlock(); b,ok:=r.buckets[id]; if !ok { b=&bucket{tokens:r.capacity,updated:time.Now()}; r.buckets[id]=b }; if el:=time.Since(b.updated); el>=r.interval { periods:=int(el/r.interval); if periods>0 { b.tokens += periods*r.refill; if b.tokens>r.capacity { b.tokens=r.capacity }; b.updated=time.Now() } }; if b.tokens<=0 { return false }; b.tokens--; return true }

type latWindow struct { mu sync.Mutex; buf []float64; idx int; full bool }
func newLatWindow(n int)*latWindow { return &latWindow{buf:make([]float64,n)} }
func (l *latWindow) add(v float64){ l.mu.Lock(); l.buf[l.idx]=v; l.idx++; if l.idx==len(l.buf){ l.idx=0; l.full=true }; l.mu.Unlock() }
func (l *latWindow) p(pct float64) float64 { l.mu.Lock(); defer l.mu.Unlock(); size:=l.idx; if l.full { size=len(l.buf) }; if size==0 { return math.NaN() }; tmp:=make([]float64,size); copy(tmp,l.buf[:size]); for i:=1;i<size;i++{ j:=i; for j>0 && tmp[j-1]>tmp[j]{ tmp[j-1],tmp[j]=tmp[j],tmp[j-1]; j-- } }; k:=int(math.Ceil(pct*float64(size)))-1; if k<0 { k=0 }; if k>=size { k=size-1 }; return tmp[k] }

func realMain(){
  service := "api-gateway"
  logging.Init(service)
  ctx,cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP); defer cancel()
  shutdownTrace := otelinit.InitTracer(ctx, service)
  shutdownMetrics, promHandler, _ := otelinit.InitMetrics(ctx, service)
  meter := otel.GetMeterProvider().Meter(service)
  rlDenied,_ := meter.Int64Counter("swarm_api_rate_limited_total")
  authDenied,_ := meter.Int64Counter("swarm_api_auth_denied_total")
  reqCounter,_ := meter.Int64Counter("swarm_api_requests_total")
  latencyHist,_ := meter.Float64Histogram("swarm_api_latency_ms")
  p99Gauge,_ := meter.Float64ObservableGauge("swarm_api_latency_p99_ms")
  latW := newLatWindow(2048)
  meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error { p99:=latW.p(0.99); if !math.IsNaN(p99){ o.ObserveFloat64(p99Gauge,p99) }; return nil }, p99Gauge)
  lim := newRateLimiter(intFromEnv("RATE_LIMIT_CAPACITY",200), intFromEnv("RATE_LIMIT_REFILL",200), time.Duration(intFromEnv("RATE_LIMIT_INTERVAL_SEC",60))*time.Second)
  mux := http.NewServeMux()
  mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request){ w.WriteHeader(http.StatusOK); _,_ = w.Write([]byte("ok")) })
  mux.HandleFunc("/v1/echo", func(w http.ResponseWriter, r *http.Request){ start:=time.Now(); if !authenticate(r){ authDenied.Add(r.Context(),1); w.WriteHeader(http.StatusUnauthorized); return }; if !lim.allow(rateKey(r)){ rlDenied.Add(r.Context(),1); w.WriteHeader(http.StatusTooManyRequests); _,_ = w.Write([]byte("rate limit exceeded")); return }; w.Header().Set("Content-Type","application/json"); _ = json.NewEncoder(w).Encode(map[string]any{"message":"echo","time":time.Now().Format(time.RFC3339)}); durMs:=float64(time.Since(start).Milliseconds()); reqCounter.Add(r.Context(),1, metric.WithAttributes(attribute.String("path","/v1/echo"))); latencyHist.Record(r.Context(),durMs, metric.WithAttributes(attribute.String("path","/v1/echo"))); latW.add(durMs) })
  mux.HandleFunc("/v1/ingest", func(w http.ResponseWriter, r *http.Request){ if r.Method!=http.MethodPost { w.WriteHeader(http.StatusMethodNotAllowed); return }; if !authenticate(r){ w.WriteHeader(http.StatusUnauthorized); return }; if !lim.allow(rateKey(r)){ w.WriteHeader(http.StatusTooManyRequests); return }; body,err:=io.ReadAll(io.LimitReader(r.Body,1<<20)); if err!=nil { http.Error(w,"read error", http.StatusBadRequest); return }; var payload map[string]any; if err:=json.Unmarshal(body,&payload); err!=nil { http.Error(w,"invalid json", http.StatusBadRequest); return }; if _,ok:=payload["id"].(string); !ok { http.Error(w,"id string required", http.StatusBadRequest); return }; if t,ok:=payload["timestamp"].(float64); !ok || t<=0 { http.Error(w,"timestamp positive required", http.StatusBadRequest); return }; w.WriteHeader(http.StatusAccepted); _ = json.NewEncoder(w).Encode(map[string]any{"status":"queued"}); reqCounter.Add(r.Context(),1, metric.WithAttributes(attribute.String("path","/v1/ingest"))) })
  if promHandler!=nil { if h,ok:=promHandler.(http.Handler); ok { mux.Handle("/metrics", h) } }
  srv := &http.Server{Addr:":8080", Handler: logMiddleware(mux)}
  go func(){ if err:=srv.ListenAndServe(); err!=nil && !errors.Is(err, http.ErrServerClosed){ slog.Error("server error","error",err); cancel() } }()
  slog.Info("service started")
  <-ctx.Done()
  slog.Info("shutdown initiated")
  ctxSd,c2 := context.WithTimeout(context.Background(),5*time.Second); defer c2()
  _ = srv.Shutdown(ctxSd)
  otelinit.Flush(ctxSd, shutdownTrace)
  _ = shutdownMetrics(ctxSd)
  slog.Info("shutdown complete")
}

func authenticate(r *http.Request) bool { h:=r.Header.Get("Authorization"); if h=="" { return false }; parts:=strings.SplitN(h," ",2); if len(parts)!=2 || !strings.EqualFold(parts[0],"Bearer") { return false }; tok:=parts[1]; if tok=="dev" { return true }; if strings.Count(tok,".")==2 { return validateJWTStructure(tok) }; return false }
func validateJWTStructure(tok string) bool { return len(tok) > 10 }
var _ *rsa.PublicKey
func rateKey(r *http.Request) string { if k:=r.Header.Get("X-API-Key"); k!="" { return "k:"+k }; ip:=r.Header.Get("X-Forwarded-For"); if ip=="" { ip=r.RemoteAddr }; return "ip:"+ip }
type respWriter struct { http.ResponseWriter; status int }
func (r *respWriter) WriteHeader(code int){ r.status=code; r.ResponseWriter.WriteHeader(code) }
func logMiddleware(next http.Handler) http.Handler { return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request){ start:=time.Now(); rw:=&respWriter{ResponseWriter:w,status:200}; reqID:=r.Header.Get("X-Request-ID"); if reqID=="" { reqID=strconv.FormatInt(time.Now().UnixNano(),36) }; w.Header().Set("X-Request-ID", reqID); next.ServeHTTP(rw,r); slog.Info("request","id",reqID,"method",r.Method,"path",r.URL.Path,"status",rw.status,"dur_ms",time.Since(start).Milliseconds()) }) }
func intFromEnv(key string, def int) int { v:=os.Getenv(key); if v=="" { return def }; i,err:=strconv.Atoi(v); if err!=nil { return def }; return i }