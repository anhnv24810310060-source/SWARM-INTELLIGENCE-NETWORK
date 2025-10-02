package internal

import (
	"sync"
	"time"
)

// MemoryIndicatorStore is a lock-striped map store with TTL purge.
// Optimized for high read concurrency.
type MemoryIndicatorStore struct {
	shards []shard
	mask   uint64
}

type shard struct {
	mu sync.RWMutex
	m  map[string]Indicator
}

func NewMemoryIndicatorStore(shardPow uint8) *MemoryIndicatorStore {
	if shardPow > 10 {
		shardPow = 10
	} // cap 1024 shards
	n := 1 << shardPow
	s := &MemoryIndicatorStore{mask: uint64(n - 1)}
	s.shards = make([]shard, n)
	for i := 0; i < n; i++ {
		s.shards[i].m = make(map[string]Indicator)
	}
	return s
}

func (s *MemoryIndicatorStore) shardFor(key string) *shard {
	h := fnv32(key)
	return &s.shards[uint64(h)&s.mask]
}

func (s *MemoryIndicatorStore) Upsert(ind Indicator) error {
	sh := s.shardFor(ind.Value)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	existing, ok := sh.m[ind.Value]
	if ok {
		ind.FirstSeen = existing.FirstSeen
	}
	ind.LastSeen = time.Now()
	sh.m[ind.Value] = ind
	return nil
}

func (s *MemoryIndicatorStore) Get(value string) (Indicator, bool) {
	sh := s.shardFor(value)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	ind, ok := sh.m[value]
	if !ok {
		return Indicator{}, false
	}
	if ind.TTL > 0 && time.Since(ind.LastSeen) > ind.TTL {
		return Indicator{}, false
	}
	return ind, true
}

func (s *MemoryIndicatorStore) Iter(fn func(Indicator) bool) {
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.RLock()
		for _, v := range sh.m {
			if !fn(v) {
				sh.mu.RUnlock()
				return
			}
		}
		sh.mu.RUnlock()
	}
}

func (s *MemoryIndicatorStore) PurgeExpired() {
	now := time.Now()
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.Lock()
		for k, v := range sh.m {
			if v.TTL > 0 && now.Sub(v.LastSeen) > v.TTL {
				delete(sh.m, k)
			}
		}
		sh.mu.Unlock()
	}
}

func fnv32(s string) uint32 {
	var h uint32 = 2166136261
	const prime = 16777619
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= prime
	}
	return h
}
