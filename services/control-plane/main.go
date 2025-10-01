package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"sync/atomic"
	"time"
	"google.golang.org/grpc"
	pb "github.com/swarmguard/proto/gen/go/consensus"
	nats "github.com/nats-io/nats.go"
)

func main() {
	log.Println("Starting control-plane")
	addr := getenv("CONSENSUS_GRPC_ADDR", "127.0.0.1:50051")
	conn, err := dialWithRetry(addr, 5, time.Second)
	if err != nil { log.Printf("[ERROR] failed to connect after retries: %v", err); return }
	defer conn.Close()
	client := pb.NewPbftClient(conn)
	var cachedHeight atomic.Uint64
	var cachedRound atomic.Uint64
	// NATS subscribe
	if nc, err := nats.Connect(getenv("NATS_URL", "127.0.0.1:4222")); err == nil {
		if _, err := nc.Subscribe("consensus.v1.height.changed", func(msg *nats.Msg) {
			var v struct { Height uint64 `json:"height"`; Round uint64 `json:"round"` }
			if json.Unmarshal(msg.Data, &v) == nil {
				cachedHeight.Store(v.Height)
				cachedRound.Store(v.Round)
			}
		}); err == nil { log.Printf("[NATS] subscribed consensus.v1.height.changed") } else { log.Printf("[WARN] subscribe failed: %v", err) }
	} else { log.Printf("[WARN] NATS connect failed: %v", err) }

	// Initial gRPC fetch fallback
	if st, err := client.GetState(context.Background(), &pb.ConsensusStateQuery{Height: 0}); err == nil {
		cachedHeight.Store(st.Height); cachedRound.Store(st.Round);
	}
	log.Printf("[CONSENSUS] cached height=%d round=%d", cachedHeight.Load(), cachedRound.Load())
}

func getenv(k, def string) string { if v := os.Getenv(k); v != "" { return v }; return def }

func dialWithRetry(addr string, maxAttempts int, baseDelay time.Duration) (*grpc.ClientConn, error) {
	var attempt int
	for {
		attempt++
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure(), grpc.WithBlock())
		cancel()
		if err == nil { if attempt > 1 { log.Printf("[INFO] connected to consensus after %d attempts", attempt) }; return conn, nil }
		if attempt >= maxAttempts { return nil, err }
		sleep := baseDelay * (1 << (attempt-1))
		if sleep > 8*baseDelay { sleep = 8 * baseDelay }
		log.Printf("[RETRY] dial attempt %d failed: %v; retrying in %s", attempt, err, sleep)
		time.Sleep(sleep)
	}
}
