package mainpackage main


import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// CRDT (Conflict-free Replicated Data Type) implementations
// Using LWW (Last-Write-Wins) strategy with vector clocks

// VectorClock tracks causality between events
type VectorClock map[string]uint64

// NewVectorClock creates empty vector clock
func NewVectorClock() VectorClock {
	return make(VectorClock)
}

// Increment increments clock for given node
func (vc VectorClock) Increment(nodeID string) {
	vc[nodeID]++
}

// Merge combines two vector clocks (taking maximum for each node)
func (vc VectorClock) Merge(other VectorClock) {
	for nodeID, timestamp := range other {
		if vc[nodeID] < timestamp {
			vc[nodeID] = timestamp
		}
	}
}

// HappensBefore returns true if this clock happened before other
func (vc VectorClock) HappensBefore(other VectorClock) bool {
	atLeastOne := false
	for nodeID, timestamp := range vc {
		otherTimestamp := other[nodeID]
		if timestamp > otherTimestamp {
			return false
		}
		if timestamp < otherTimestamp {
			atLeastOne = true
		}
	}
	return atLeastOne
}

// Concurrent returns true if clocks are concurrent (no happens-before relation)
func (vc VectorClock) Concurrent(other VectorClock) bool {
	return !vc.HappensBefore(other) && !other.HappensBefore(vc)
}

// Copy creates deep copy
func (vc VectorClock) Copy() VectorClock {
	copy := make(VectorClock, len(vc))
	for k, v := range vc {
		copy[k] = v
	}
	return copy
}

// LWWRegister implements Last-Write-Wins register with vector clock
type LWWRegister struct {
	mu        sync.RWMutex
	value     interface{}
	timestamp time.Time
	clock     VectorClock
	nodeID    string
}

// NewLWWRegister creates new LWW register
func NewLWWRegister(nodeID string) *LWWRegister {
	return &LWWRegister{
		clock:     NewVectorClock(),
		nodeID:    nodeID,
		timestamp: time.Now(),
	}
}

// Set updates value with new timestamp and clock
func (r *LWWRegister) Set(value interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	r.value = value
	r.timestamp = time.Now()
	r.clock.Increment(r.nodeID)
}

// Get returns current value
func (r *LWWRegister) Get() interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.value
}

// Merge merges another register's value (LWW semantics)
func (r *LWWRegister) Merge(other *LWWRegister) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	// Compare vector clocks first
	if other.clock.HappensBefore(r.clock) {
		// Other is older, keep current
		return
	}
	
	if r.clock.HappensBefore(other.clock) {
		// Other is newer, take it
		r.value = other.value
		r.timestamp = other.timestamp
		r.clock.Merge(other.clock)
		return
	}
	
	// Concurrent - use timestamp as tiebreaker
	if other.timestamp.After(r.timestamp) {
		r.value = other.value
		r.timestamp = other.timestamp
	}
	
	r.clock.Merge(other.clock)
}

// GCounter implements Grow-only Counter CRDT
type GCounter struct {
	mu      sync.RWMutex
	counts  map[string]uint64
	nodeID  string
}

// NewGCounter creates new grow-only counter
func NewGCounter(nodeID string) *GCounter {
	return &GCounter{
		counts: make(map[string]uint64),
		nodeID: nodeID,
	}
}

// Increment increments counter for this node
func (c *GCounter) Increment(delta uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[c.nodeID] += delta
}

// Value returns total count across all nodes
func (c *GCounter) Value() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	var total uint64
	for _, count := range c.counts {
		total += count
	}
	return total
}

// Merge merges another counter (taking max for each node)
func (c *GCounter) Merge(other *GCounter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	for nodeID, count := range other.counts {
		if c.counts[nodeID] < count {
			c.counts[nodeID] = count
		}
	}
}

// PNCounter implements Positive-Negative Counter (supports decrement)
type PNCounter struct {
	positive *GCounter
	negative *GCounter
}

// NewPNCounter creates new PN counter
func NewPNCounter(nodeID string) *PNCounter {
	return &PNCounter{
		positive: NewGCounter(nodeID),
		negative: NewGCounter(nodeID),
	}
}

// Increment adds to counter
func (c *PNCounter) Increment(delta uint64) {
	c.positive.Increment(delta)
}

// Decrement subtracts from counter
func (c *PNCounter) Decrement(delta uint64) {
	c.negative.Increment(delta)
}

// Value returns net count
func (c *PNCounter) Value() int64 {
	return int64(c.positive.Value()) - int64(c.negative.Value())
}

// Merge merges another PN counter
func (c *PNCounter) Merge(other *PNCounter) {
	c.positive.Merge(other.positive)
	c.negative.Merge(other.negative)
}

// GSet implements Grow-only Set CRDT
type GSet struct {
	mu    sync.RWMutex
	items map[string]struct{}
}

// NewGSet creates new grow-only set
func NewGSet() *GSet {
	return &GSet{
		items: make(map[string]struct{}),
	}
}

// Add adds item to set
func (s *GSet) Add(item string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[item] = struct{}{}
}

// Contains checks if item exists
func (s *GSet) Contains(item string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.items[item]
	return exists
}

// Items returns all items
func (s *GSet) Items() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	items := make([]string, 0, len(s.items))
	for item := range s.items {
		items = append(items, item)
	}
	return items
}

// Merge merges another set (union)
func (s *GSet) Merge(other *GSet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	for item := range other.items {
		s.items[item] = struct{}{}
	}
}

// Size returns number of items
func (s *GSet) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}

// ORSet implements Observed-Remove Set with tombstones
type ORSet struct {
	mu        sync.RWMutex
	additions map[string]map[string]struct{} // item -> set of unique tags
	removals  map[string]map[string]struct{} // item -> set of removed tags
}

// NewORSet creates new OR-Set
func NewORSet() *ORSet {
	return &ORSet{
		additions: make(map[string]map[string]struct{}),
		removals:  make(map[string]map[string]struct{}),
	}
}

// Add adds item with unique tag
func (s *ORSet) Add(item, tag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if s.additions[item] == nil {
		s.additions[item] = make(map[string]struct{})
	}
	s.additions[item][tag] = struct{}{}
}

// Remove removes item by marking all its tags as removed
func (s *ORSet) Remove(item string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	// Copy all addition tags to removals
	if tags, exists := s.additions[item]; exists {
		if s.removals[item] == nil {
			s.removals[item] = make(map[string]struct{})
		}
		for tag := range tags {
			s.removals[item][tag] = struct{}{}
		}
	}
}

// Contains checks if item exists (has additions not in removals)
func (s *ORSet) Contains(item string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	additions := s.additions[item]
	removals := s.removals[item]
	
	for tag := range additions {
		if _, removed := removals[tag]; !removed {
			return true // At least one addition not removed
		}
	}
	return false
}

// Items returns all items currently in set
func (s *ORSet) Items() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	var items []string
	for item := range s.additions {
		if s.hasActiveAdditions(item) {
			items = append(items, item)
		}
	}
	return items
}

func (s *ORSet) hasActiveAdditions(item string) bool {
	additions := s.additions[item]
	removals := s.removals[item]
	
	for tag := range additions {
		if _, removed := removals[tag]; !removed {
			return true
		}
	}
	return false
}

// Merge merges another OR-Set
func (s *ORSet) Merge(other *ORSet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	// Merge additions
	for item, tags := range other.additions {
		if s.additions[item] == nil {
			s.additions[item] = make(map[string]struct{})
		}
		for tag := range tags {
			s.additions[item][tag] = struct{}{}
		}
	}
	
	// Merge removals
	for item, tags := range other.removals {
		if s.removals[item] == nil {
			s.removals[item] = make(map[string]struct{})
		}
		for tag := range tags {
			s.removals[item][tag] = struct{}{}
		}
	}
}

// LWWMap implements Last-Write-Wins Map CRDT
type LWWMap struct {
	mu      sync.RWMutex
	entries map[string]*LWWRegister
	nodeID  string
}

// NewLWWMap creates new LWW map
func NewLWWMap(nodeID string) *LWWMap {
	return &LWWMap{
		entries: make(map[string]*LWWRegister),
		nodeID:  nodeID,
	}
}

// Set updates key with value
func (m *LWWMap) Set(key string, value interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if m.entries[key] == nil {
		m.entries[key] = NewLWWRegister(m.nodeID)
	}
	m.entries[key].Set(value)
}

// Get retrieves value for key
func (m *LWWMap) Get(key string) (interface{}, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	if reg, exists := m.entries[key]; exists {
		return reg.Get(), true
	}
	return nil, false
}

// Delete removes key (sets to nil with new timestamp)
func (m *LWWMap) Delete(key string) {
	m.Set(key, nil)
}

// Keys returns all non-nil keys
func (m *LWWMap) Keys() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	var keys []string
	for key, reg := range m.entries {
		if reg.Get() != nil {
			keys = append(keys, key)
		}
	}
	return keys
}

// Merge merges another LWW map
func (m *LWWMap) Merge(other *LWWMap) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	for key, otherReg := range other.entries {
		if m.entries[key] == nil {
			m.entries[key] = NewLWWRegister(m.nodeID)
		}
		m.entries[key].Merge(otherReg)
	}
}

// MerkleTree for efficient sync (detect differences quickly)
type MerkleTree struct {
	root *merkleNode
}

type merkleNode struct {
	hash  [32]byte
	left  *merkleNode
	right *merkleNode
	data  []byte
}

// NewMerkleTree creates tree from data blocks
func NewMerkleTree(blocks [][]byte) *MerkleTree {
	if len(blocks) == 0 {
		return &MerkleTree{}
	}
	
	// Create leaf nodes
	nodes := make([]*merkleNode, len(blocks))
	for i, block := range blocks {
		nodes[i] = &merkleNode{
			hash: sha256.Sum256(block),
			data: block,
		}
	}
	
	// Build tree bottom-up
	for len(nodes) > 1 {
		var parentLevel []*merkleNode
		for i := 0; i < len(nodes); i += 2 {
			left := nodes[i]
			var right *merkleNode
			if i+1 < len(nodes) {
				right = nodes[i+1]
			} else {
				right = left // Duplicate if odd number
			}
			
			parent := &merkleNode{
				left:  left,
				right: right,
			}
			
			// Hash = H(left.hash || right.hash)
			combined := append(left.hash[:], right.hash[:]...)
			parent.hash = sha256.Sum256(combined)
			
			parentLevel = append(parentLevel, parent)
		}
		nodes = parentLevel
	}
	
	return &MerkleTree{root: nodes[0]}
}

// RootHash returns root hash
func (t *MerkleTree) RootHash() [32]byte {
	if t.root == nil {
		return [32]byte{}
	}
	return t.root.hash
}

// Serialize CRDT state for network transmission (efficient encoding)
func SerializeLWWMap(m *LWWMap) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	type entry struct {
		Key       string
		Value     interface{}
		Timestamp int64
		Clock     VectorClock
	}
	
	entries := make([]entry, 0, len(m.entries))
	for key, reg := range m.entries {
		reg.mu.RLock()
		entries = append(entries, entry{
			Key:       key,
			Value:     reg.value,
			Timestamp: reg.timestamp.Unix(),
			Clock:     reg.clock.Copy(),
		})
		reg.mu.RUnlock()
	}
	
	return json.Marshal(entries)
}

// DeserializeLWWMap reconstructs LWW map from bytes
func DeserializeLWWMap(data []byte, nodeID string) (*LWWMap, error) {
	type entry struct {
		Key       string
		Value     interface{}
		Timestamp int64
		Clock     VectorClock
	}
	
	var entries []entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	
	m := NewLWWMap(nodeID)
	for _, e := range entries {
		reg := NewLWWRegister(nodeID)
		reg.value = e.Value
		reg.timestamp = time.Unix(e.Timestamp, 0)
		reg.clock = e.Clock
		m.entries[e.Key] = reg
	}
	
	return m, nil
}

// Delta-based sync optimization
type Delta struct {
	NodeID    string
	Timestamp time.Time
	Changes   map[string]interface{}
}

// ComputeDelta computes minimal delta since given version
func (m *LWWMap) ComputeDelta(sinceVersion VectorClock) *Delta {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	delta := &Delta{
		NodeID:    m.nodeID,
		Timestamp: time.Now(),
		Changes:   make(map[string]interface{}),
	}
	
	for key, reg := range m.entries {
		reg.mu.RLock()
		// Include only changes after sinceVersion
		if !reg.clock.HappensBefore(sinceVersion) && reg.clock != sinceVersion {
			delta.Changes[key] = reg.value
		}
		reg.mu.RUnlock()
	}
	
	return delta
}

// ApplyDelta applies delta to map
func (m *LWWMap) ApplyDelta(delta *Delta) {
	for key, value := range delta.Changes {
		m.Set(key, value)
	}
}
