package state

import (
	"crypto/sha256"
	"errors"
)

// Incremental Merkle tree optimized for append (sparse frontier technique)
// Inspired by Ethereum's binary Merkle accumulation (using running frontier nodes).
// This allows O(log N) memory and O(log N) update per append.

type Hash [32]byte

type Tree struct {
	count    uint64
	frontier []Hash // frontier[i] stores hash of subtree size 2^i if present
}

func New() *Tree { return &Tree{frontier: make([]Hash, 0, 32)} }

func (t *Tree) Append(leaf []byte) Hash {
	var h Hash = sha256.Sum256(leaf)
	idx := 0
	for {
		if idx >= len(t.frontier) { // extend frontier
			t.frontier = append(t.frontier, h)
			break
		}
		if isSlotEmpty(t.frontier[idx]) { // fill empty slot
			t.frontier[idx] = h
			break
		}
		// combine and carry
		combined := combine(t.frontier[idx], h)
		var empty Hash
		t.frontier[idx] = empty
		h = combined
		idx++
	}
	// update count
	if t.count++; t.count == 0 { /* overflow wrap rarely */
	}
	return t.Root()
}

func (t *Tree) Root() Hash {
	var acc Hash
	for i := range t.frontier {
		if !isSlotEmpty(t.frontier[i]) {
			acc = combine(t.frontier[i], acc)
		}
	}
	return acc
}

func isSlotEmpty(h Hash) bool { var zero Hash; return h == zero }

func combine(left, right Hash) Hash {
	var out Hash
	buf := make([]byte, 64)
	copy(buf[:32], left[:])
	copy(buf[32:], right[:])
	out = sha256.Sum256(buf)
	return out
}

// GenerateProof returns Merkle proof indices for a given leaf index (0-based).
// Current implementation recomputes from scratch; can be optimized later.
func (t *Tree) GenerateProof(_ uint64) ([][]byte, error) {
	return nil, errors.New("not implemented yet")
}
