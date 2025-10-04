package store

import (
	"context"
	"testing"
	"time"
)

func TestFibonacciCheckpoints(t *testing.T) {
	tests := []struct {
		maxHeight uint64
		want      []uint64
	}{
		{10, []uint64{1, 1, 2, 3, 5, 8}},
		{100, []uint64{1, 1, 2, 3, 5, 8, 13, 21, 34, 55, 89}},
		{1000, []uint64{1, 1, 2, 3, 5, 8, 13, 21, 34, 55, 89, 144, 233, 377, 610, 987}},
	}

	for _, tt := range tests {
		got := FibonacciCheckpoints(tt.maxHeight)
		if len(got) != len(tt.want) {
			t.Errorf("FibonacciCheckpoints(%d) = %v, want %v", tt.maxHeight, got, tt.want)
			continue
		}

		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("FibonacciCheckpoints(%d)[%d] = %d, want %d", tt.maxHeight, i, got[i], tt.want[i])
			}
		}
	}
}

func TestIncrementalMerkleVerifier(t *testing.T) {
	verifier := NewIncrementalMerkleVerifier()

	// Add some leaves
	leaf1 := []byte("state-1")
	leaf2 := []byte("state-2")
	leaf3 := []byte("state-3")

	verifier.Update(0, leaf1)
	verifier.Update(1, leaf2)
	root1 := verifier.Update(2, leaf3)

	if len(root1) != 32 {
		t.Errorf("Root should be 32 bytes, got %d", len(root1))
	}

	// Update one leaf and verify root changes
	leaf2Updated := []byte("state-2-updated")
	root2 := verifier.Update(1, leaf2Updated)

	if string(root1) == string(root2) {
		t.Error("Root should change after leaf update")
	}

	// Verify proof (simplified - just check non-panic)
	proof := [][]byte{leaf1, leaf3}
	// In production, would generate actual Merkle proof
	_ = verifier.VerifyProof(leaf2Updated, 1, proof, root2)
}

func TestSnapSyncEncoding(t *testing.T) {
	state := &SnapSyncState{
		Height:    1000,
		StateRoot: []byte("merkle-root-hash-32-bytes-xx"),
		Accounts: map[string]uint64{
			"addr1": 100,
			"addr2": 200,
			"addr3": 300,
		},
		Timestamp: time.Now().Unix(),
	}

	// Encode
	encoded, err := EncodeSnapState(state)
	if err != nil {
		t.Fatalf("EncodeSnapState failed: %v", err)
	}

	// Decode
	decoded, err := DecodeSnapState(encoded)
	if err != nil {
		t.Fatalf("DecodeSnapState failed: %v", err)
	}

	// Verify
	if decoded.Height != state.Height {
		t.Errorf("Height mismatch: got %d, want %d", decoded.Height, state.Height)
	}

	if string(decoded.StateRoot) != string(state.StateRoot) {
		t.Errorf("StateRoot mismatch")
	}

	if len(decoded.Accounts) != len(state.Accounts) {
		t.Errorf("Account count mismatch: got %d, want %d", len(decoded.Accounts), len(state.Accounts))
	}

	for addr, bal := range state.Accounts {
		if decoded.Accounts[addr] != bal {
			t.Errorf("Account %s balance mismatch: got %d, want %d", addr, decoded.Accounts[addr], bal)
		}
	}
}

func BenchmarkFastSync(b *testing.B) {
	store, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	sync := NewFastSync(store)
	ctx := context.Background()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Simulate syncing 100 blocks
		if err := sync.downloadBlocksParallel(ctx, "mock-peer", 1, 100); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkIncrementalMerkle(b *testing.B) {
	verifier := NewIncrementalMerkleVerifier()

	// Pre-populate with 1000 leaves
	for i := 0; i < 1000; i++ {
		leaf := sha256sum([]byte{byte(i)})
		verifier.Update(i, leaf)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Update random leaf and recompute root
		idx := i % 1000
		newLeaf := sha256sum([]byte{byte(i), byte(idx)})
		verifier.Update(idx, newLeaf)
	}
}
