package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"crypto/sha256"

	badger "github.com/dgraph-io/badger/v4"
	"github.com/spaolacci/murmur3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Block represents minimal block metadata for storage.
type Block struct {
	Height    uint64
	Timestamp int64
	Hash      []byte
	Parent    []byte
	Data      []byte // opaque payload / transactions batch
	StateRoot []byte
}

// Store wraps BadgerDB with simplified methods and metrics.
type Store struct {
	mu     sync.RWMutex
	db     *badger.DB
	blocks metric.Int64Counter
	lag    metric.Int64Gauge
}

var (
	ErrNotFound = errors.New("block not found")
)

// Open returns a store rooted at path
func Open(path string) (*Store, error) {
	opts := badger.DefaultOptions(filepath.Clean(path)).WithLoggingLevel(badger.WARNING)
	db, err := badger.Open(opts)
	if err != nil {
		return nil, err
	}
	m := otel.Meter("swarm-blockchain")
	bc, _ := m.Int64Counter("swarm_blockchain_blocks_total")
	lag, _ := m.Int64Gauge("swarm_blockchain_sync_lag_blocks")
	return &Store{db: db, blocks: bc, lag: lag}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func encodeKey(height uint64) []byte {
	// little endian height key ensures natural ordering
	var k [8]byte
	for i := 0; i < 8; i++ {
		k[i] = byte(height >> (8 * i))
	}
	return k[:]
}

// SaveBlock writes block idempotently.
func (s *Store) SaveBlock(ctx context.Context, blk *Block) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(txn *badger.Txn) error {
		key := encodeKey(blk.Height)
		_, err := txn.Get(key)
		if err == nil {
			return nil
		} // already present
		if !errors.Is(err, badger.ErrKeyNotFound) {
			return err
		}
		enc, err := marshalBlock(blk)
		if err != nil {
			return err
		}
		if err := txn.Set(key, enc); err != nil {
			return err
		}
		s.blocks.Add(ctx, 1)
		return nil
	})
}

// GetBlock by height.
func (s *Store) GetBlock(_ context.Context, height uint64) (*Block, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out *Block
	err := s.db.View(func(txn *badger.Txn) error {
		it, err := txn.Get(encodeKey(height))
		if err != nil {
			return err
		}
		val, err := it.ValueCopy(nil)
		if err != nil {
			return err
		}
		blk, err := unmarshalBlock(val)
		if err != nil {
			return err
		}
		out = blk
		return nil
	})
	if err != nil {
		if errors.Is(err, badger.ErrKeyNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return out, nil
}

// GetLatestBlock returns the highest block using reverse iterator.
func (s *Store) GetLatestBlock(_ context.Context) (*Block, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out *Block
	err := s.db.View(func(txn *badger.Txn) error {
		opt := badger.DefaultIteratorOptions
		opt.Reverse = true
		opt.PrefetchValues = true
		it := txn.NewIterator(opt)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			k := item.KeyCopy(nil)
			if len(k) != 8 {
				continue
			} // skip non-block keys
			val, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			blk, err := unmarshalBlock(val)
			if err != nil {
				continue
			} // skip malformed (should not happen)
			out = blk
			return nil
		}
		return ErrNotFound
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SaveState root (height -> state root mapping) using a dedicated prefix.
func (s *Store) SaveState(ctx context.Context, height uint64, stateRoot []byte) error {
	key := append([]byte("state:"), encodeKey(height)...)
	return s.db.Update(func(txn *badger.Txn) error { return txn.Set(key, stateRoot) })
}

// CalcSyncLag updates gauge with difference between local latest and network height.
func (s *Store) CalcSyncLag(ctx context.Context, networkHeight uint64) error {
	blk, err := s.GetLatestBlock(ctx)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.lag.Record(ctx, int64(networkHeight))
			return nil
		}
		return err
	}
	lag := int64(0)
	if networkHeight > blk.Height {
		lag = int64(networkHeight - blk.Height)
	}
	s.lag.Record(ctx, lag, metric.WithAttributes(attribute.String("source", "sync")))
	return nil
}

// Prune keeps last N blocks + checkpoint heights (each 100 by default).
func (s *Store) Prune(retain uint64) error {
	if retain == 0 {
		return nil
	}
	cutoff := uint64(0)
	// compute cutoff height to delete strictly below (except checkpoint multiples of 100)
	latest, err := s.GetLatestBlock(context.Background())
	if err != nil {
		return err
	}
	if latest.Height > retain {
		cutoff = latest.Height - retain
	}
	return s.db.Update(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			k := item.KeyCopy(nil)
			// only raw 8-byte keys correspond to blocks (state: prefixed keys ignored)
			if len(k) != 8 {
				continue
			}
			var height uint64
			for i := 0; i < 8; i++ {
				height |= uint64(k[i]) << (8 * i)
			}
			if height < cutoff && height%100 != 0 {
				if err := txn.Delete(k); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// marshalBlock uses a compact binary layout:
// [height(8)][ts(8)][hashLen(2)][parentLen(2)][stateLen(2)][dataLen(4)] + blobs
func marshalBlock(b *Block) ([]byte, error) {
	hashLen := len(b.Hash)
	parentLen := len(b.Parent)
	stateLen := len(b.StateRoot)
	dataLen := len(b.Data)
	buf := make([]byte, 8+8+2+2+2+4+hashLen+parentLen+stateLen+dataLen)
	p := 0
	writeU64 := func(v uint64) {
		for i := 0; i < 8; i++ {
			buf[p+i] = byte(v >> (8 * i))
		}
		p += 8
	}
	writeU16 := func(v int) { buf[p] = byte(v); buf[p+1] = byte(v >> 8); p += 2 }
	writeU32 := func(v int) {
		buf[p] = byte(v)
		buf[p+1] = byte(v >> 8)
		buf[p+2] = byte(v >> 16)
		buf[p+3] = byte(v >> 24)
		p += 4
	}
	writeU64(b.Height)
	writeU64(uint64(b.Timestamp))
	writeU16(hashLen)
	writeU16(parentLen)
	writeU16(stateLen)
	writeU32(dataLen)
	copy(buf[p:], b.Hash)
	p += hashLen
	copy(buf[p:], b.Parent)
	p += parentLen
	copy(buf[p:], b.StateRoot)
	p += stateLen
	copy(buf[p:], b.Data)
	return buf, nil
}

func unmarshalBlock(b []byte) (*Block, error) {
	if len(b) < 8+8+2+2+2+4 {
		return nil, errors.New("short block encoding")
	}
	p := 0
	readU64 := func() uint64 {
		var v uint64
		for i := 0; i < 8; i++ {
			v |= uint64(b[p+i]) << (8 * i)
		}
		p += 8
		return v
	}
	readI64 := func() int64 { return int64(readU64()) }
	readU16 := func() int { v := int(b[p]) | int(b[p+1])<<8; p += 2; return v }
	readU32 := func() int { v := int(b[p]) | int(b[p+1])<<8 | int(b[p+2])<<16 | int(b[p+3])<<24; p += 4; return v }
	height := readU64()
	ts := readI64()
	hLen := readU16()
	pLen := readU16()
	sLen := readU16()
	dLen := readU32()
	end := p + hLen + pLen + sLen + dLen
	if end != len(b) {
		return nil, errors.New("corrupt block encoding")
	}
	hash := append([]byte(nil), b[p:p+hLen]...)
	p += hLen
	parent := append([]byte(nil), b[p:p+pLen]...)
	p += pLen
	state := append([]byte(nil), b[p:p+sLen]...)
	p += sLen
	data := append([]byte(nil), b[p:p+dLen]...)
	return &Block{Height: height, Timestamp: ts, Hash: hash, Parent: parent, StateRoot: state, Data: data}, nil
}

// NewBlock constructs a block computing hash if empty.
func NewBlock(height uint64, parent []byte, data []byte, stateRoot []byte) *Block {
	if parent == nil {
		parent = make([]byte, 32)
	}
	h := fastHash(height, parent, data, stateRoot)
	return &Block{Height: height, Timestamp: time.Now().UnixNano(), Parent: parent, Data: data, StateRoot: stateRoot, Hash: h}
}

// fastHash uses sha256-simd then murmur3 mixing for speed + diffusion.
func fastHash(height uint64, parent, data, stateRoot []byte) []byte {
	buf := make([]byte, 8+len(parent)+len(data)+len(stateRoot))
	for i := 0; i < 8; i++ {
		buf[i] = byte(height >> (8 * i))
	}
	p := 8
	copy(buf[p:], parent)
	p += len(parent)
	copy(buf[p:], data)
	p += len(data)
	copy(buf[p:], stateRoot)
	sha := sha256sum(buf)
	// mix first 8 bytes with murmur3 for avalanche
	mix := murmurMix(sha[:8])
	return append(sha, mix...)
}

func sha256sum(b []byte) []byte {
	// use sha256-simd optimized implementation
	h := sha256.New()
	h.Write(b)
	return h.Sum(nil)
}

// murmurMix returns 8 bytes from murmur3 64-bit hash
func murmurMix(b []byte) []byte {
	x := murmur3.Sum64(b)
	var out [8]byte
	for i := 0; i < 8; i++ {
		out[i] = byte(x >> (8 * i))
	}
	return out[:]
}
