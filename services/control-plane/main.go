package main

import (
	"context"
	"encoding/json"
	"os"
	"sync/atomic"
	"time"

	"log/slog"

	nats "github.com/nats-io/nats.go"
	sloglog "github.com/swarmguard/libs/go/core/logging"
	otelinit "github.com/swarmguard/libs/go/core/otelinit"
	natsctx "github.com/swarmguard/libs/go/core/natsctx"
	resilience "github.com/swarmguard/libs/go/core/resilience"
	pb "github.com/swarmguard/proto/gen/go/consensus"
	"google.golang.org/grpc"
)

func main() {
	sloglog.Init("control-plane")
	ctx := context.Background()
	shutdown := otelinit.InitTracer(ctx, "control-plane")
	defer otelinit.Flush(ctx, shutdown)
	slog.Info("starting service")
	addr := getenv("CONSENSUS_GRPC_ADDR", "127.0.0.1:50051")
	conn, err := dialWithRetry(addr, 5, time.Second)
	if err != nil {
		slog.Error("connect failed after retries", "error", err)
		return
	}
	defer conn.Close()
	client := pb.NewPbftClient(conn)
	var cachedHeight atomic.Uint64
	var cachedRound atomic.Uint64
	// NATS subscribe
	if nc, err := nats.Connect(getenv("NATS_URL", "127.0.0.1:4222")); err == nil {
		if _, err := natsctx.Subscribe(nc, "consensus.v1.height.changed", func(msgCtx context.Context, msg *nats.Msg) {
			var v struct { Height uint64 `json:"height"`; Round uint64 `json:"round"` }
			if json.Unmarshal(msg.Data, &v) == nil {
				cachedHeight.Store(v.Height)
				cachedRound.Store(v.Round)
			}
		}); err == nil {
			slog.Info("nats subscribed", "subject", "consensus.v1.height.changed")
		} else { slog.Warn("subscribe failed", "error", err) }
	} else { slog.Warn("nats connect failed", "error", err) }

	// Initial gRPC fetch fallback
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Use Retry wrapper for GetState
	_, _ = resilience.Retry(ctx, 3, 150*time.Millisecond, func() (struct{}, error) {
		if st, err := client.GetState(context.Background(), &pb.ConsensusStateQuery{Height: 0}); err == nil {
			cachedHeight.Store(st.Height)
			cachedRound.Store(st.Round)
			return struct{}{}, nil
		} else {
			return struct{}{}, err
		}
	})
	slog.Info("consensus cached state", "height", cachedHeight.Load(), "round", cachedRound.Load())
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func dialWithRetry(addr string, maxAttempts int, baseDelay time.Duration) (*grpc.ClientConn, error) {
	var attempt int
	for {
		attempt++
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure(), grpc.WithBlock())
		cancel()
		if err == nil {
			if attempt > 1 {
				slog.Info("connected to consensus", "attempts", attempt)
			}
			return conn, nil
		}
		if attempt >= maxAttempts {
			return nil, err
		}
		sleep := baseDelay * (1 << (attempt - 1))
		if sleep > 8*baseDelay {
			sleep = 8 * baseDelay
		}
		slog.Warn("dial failed", "attempt", attempt, "error", err, "sleep", sleep.String())
		time.Sleep(sleep)
	}
}
