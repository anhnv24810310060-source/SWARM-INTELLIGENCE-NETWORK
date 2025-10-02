package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	logging "github.com/swarmguard/libs/go/core/logging"
	"github.com/swarmguard/libs/go/core/otelinit"
	"google.golang.org/grpc"
)

// Placeholder federation gRPC service registration would go here.

func main() {
	service := "federation"
	logging.Init(service)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	shutdownTrace := otelinit.InitTracer(ctx, service)
	shutdownMetrics, promHandler, _ := otelinit.InitMetrics(ctx, service)

	// gRPC server
	grpcServer := grpc.NewServer()
	lis, err := net.Listen("tcp", ":9090")
	if err != nil {
		slog.Error("listen failed", "error", err)
		return
	}
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			slog.Error("grpc serve", "error", err)
			cancel()
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	if promHandler != nil {
		if h, ok := promHandler.(http.Handler); ok {
			mux.Handle("/metrics", h)
		}
	}
	httpSrv := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server", "error", err)
			cancel()
		}
	}()

	slog.Info("service started")
	<-ctx.Done()
	slog.Info("shutdown initiated")
	grpcServer.GracefulStop()
	ctxSd, c2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer c2()
	_ = httpSrv.Shutdown(ctxSd)
	otelinit.Flush(ctxSd, shutdownTrace)
	_ = shutdownMetrics(ctxSd)
	slog.Info("shutdown complete")
}
