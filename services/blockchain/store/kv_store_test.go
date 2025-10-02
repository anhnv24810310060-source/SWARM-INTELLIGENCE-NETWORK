package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreLifecycle(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	for i := 1; i <= 25; i++ { // seed multiple blocks
		b := NewBlock(uint64(i), nil, []byte("data"), []byte("state"))
		if err := st.SaveBlock(context.Background(), b); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	got, err := st.GetBlock(context.Background(), 10)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got.Data) != "data" {
		t.Fatalf("unexpected data")
	}
	latest, err := st.GetLatestBlock(context.Background())
	if err != nil || latest.Height != 25 {
		t.Fatalf("latest mismatch: %+v err=%v", latest, err)
	}
	if err := st.SaveState(context.Background(), latest.Height, []byte("root")); err != nil {
		t.Fatalf("state: %v", err)
	}
	if err := st.Prune(10); err != nil {
		t.Fatalf("prune: %v", err)
	}
	// after prune earliest height should be >= 15 (except checkpoints every 100 which none yet)
	if _, err := st.GetBlock(context.Background(), 5); err == nil {
		t.Fatalf("expected old block pruned")
	}
}

func BenchmarkNewBlock(b *testing.B) {
	tmp := b.TempDir()
	st, _ := Open(filepath.Join(tmp, "db"))
	defer st.Close()
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		blk := NewBlock(uint64(i+1), nil, []byte("x"), []byte("s"))
		_ = st.SaveBlock(ctx, blk)
	}
	_ = os.RemoveAll(tmp)
}
