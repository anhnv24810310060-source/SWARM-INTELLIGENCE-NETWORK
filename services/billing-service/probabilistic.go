package billingservice
package main

import (
	"encoding/binary"
	"hash"
	"math"
	"math/bits"
	"sync"
)

// HyperLogLog implements cardinality estimation algorithm
// Memory efficient: ~1.5KB for 0.81% standard error
// Perfect for tracking unique users, IPs, events, etc.

const (
	// HLL precision (p): number of bits for register index
	// Higher p = more accuracy but more memory
	// p=14 → 16KB memory, 0.81% error
	hllPrecision = 14
	hllM         = 1 << hllPrecision // 16384 registers
	hllAlpha     = 0.7213 / (1 + 1.079/float64(hllM))
)

// HyperLogLog probabilistic cardinality counter
type HyperLogLog struct {
	mu        sync.RWMutex
	registers []uint8    // Max value seen for each bucket
	hasher    hash.Hash64 // Reusable hasher
}

// NewHyperLogLog creates new HLL counter
func NewHyperLogLog() *HyperLogLog {
	return &HyperLogLog{
		registers: make([]uint8, hllM),
	}
}

// Add adds element to set
func (h *HyperLogLog) Add(data []byte) {
	// Hash the data (using FNV-1a for speed)
	hashValue := fnv1a64(data)
	
	// Split hash: first p bits for register index, remaining for run count
	idx := hashValue & (hllM - 1)                     // Lower p bits
	w := hashValue >> hllPrecision                     // Upper bits
	leadingZeros := uint8(bits.LeadingZeros64(w)) + 1 // Position of first 1-bit
	
	h.mu.Lock()
	if leadingZeros > h.registers[idx] {
		h.registers[idx] = leadingZeros
	}
	h.mu.Unlock()
}

// Count estimates cardinality
func (h *HyperLogLog) Count() uint64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	// Raw HLL estimate
	sum := 0.0
	for _, val := range h.registers {
		sum += 1.0 / float64(uint64(1)<<val)
	}
	
	estimate := hllAlpha * float64(hllM) * float64(hllM) / sum
	
	// Corrections for small/large cardinalities
	if estimate <= 2.5*float64(hllM) {
		// Small range correction
		zeros := 0
		for _, val := range h.registers {
			if val == 0 {
				zeros++
			}
		}
		if zeros != 0 {
			estimate = float64(hllM) * math.Log(float64(hllM)/float64(zeros))
		}
	} else if estimate > (1.0/30.0)*float64(uint64(1)<<32) {
		// Large range correction
		estimate = -float64(uint64(1)<<32) * math.Log(1.0-estimate/float64(uint64(1)<<32))
	}
	
	return uint64(estimate)
}

// Merge combines another HLL (union operation)
func (h *HyperLogLog) Merge(other *HyperLogLog) {
	h.mu.Lock()
	other.mu.RLock()
	defer h.mu.Unlock()
	defer other.mu.RUnlock()
	
	for i := range h.registers {
		if other.registers[i] > h.registers[i] {
			h.registers[i] = other.registers[i]
		}
	}
}

// Reset clears all registers
func (h *HyperLogLog) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	for i := range h.registers {
		h.registers[i] = 0
	}
}

// Serialize exports state for storage/transmission
func (h *HyperLogLog) Serialize() []byte {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	// Simple encoding: register values only
	data := make([]byte, len(h.registers))
	copy(data, h.registers)
	return data
}

// Deserialize imports state
func (h *HyperLogLog) Deserialize(data []byte) error {
	if len(data) != hllM {
		return ErrInvalidHLLData
	}
	
	h.mu.Lock()
	defer h.mu.Unlock()
	
	copy(h.registers, data)
	return nil
}

var ErrInvalidHLLData = error(nil)

// FNV-1a hash (fast, good distribution)
func fnv1a64(data []byte) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	
	hash := uint64(offset64)
	for _, b := range data {
		hash ^= uint64(b)
		hash *= prime64
	}
	return hash
}

// CountMinSketch for frequency estimation (top-K queries)
// Useful for tracking most active users, popular resources, etc.
type CountMinSketch struct {
	mu    sync.RWMutex
	depth int       // Number of hash functions
	width int       // Counters per hash
	table [][]uint32 // Count table
	seeds []uint64   // Hash seeds
}

// NewCountMinSketch creates new sketch
// epsilon: error bound (0.01 = 1% error)
// delta: failure probability (0.01 = 99% confidence)
func NewCountMinSketch(epsilon, delta float64) *CountMinSketch {
	width := int(math.Ceil(math.E / epsilon))
	depth := int(math.Ceil(math.Log(1.0 / delta)))
	
	table := make([][]uint32, depth)
	for i := range table {
		table[i] = make([]uint32, width)
	}
	
	// Generate random seeds for hash functions
	seeds := make([]uint64, depth)
	for i := range seeds {
		seeds[i] = uint64(i * 0x9E3779B9) // Golden ratio
	}
	
	return &CountMinSketch{
		depth: depth,
		width: width,
		table: table,
		seeds: seeds,
	}
}

// Add increments count for item
func (cm *CountMinSketch) Add(data []byte, count uint32) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	for i := 0; i < cm.depth; i++ {
		hash := fnv1a64Seed(data, cm.seeds[i])
		idx := int(hash % uint64(cm.width))
		cm.table[i][idx] += count
	}
}

// Count estimates frequency of item
func (cm *CountMinSketch) Count(data []byte) uint32 {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	
	minCount := uint32(math.MaxUint32)
	for i := 0; i < cm.depth; i++ {
		hash := fnv1a64Seed(data, cm.seeds[i])
		idx := int(hash % uint64(cm.width))
		if cm.table[i][idx] < minCount {
			minCount = cm.table[i][idx]
		}
	}
	
	return minCount
}

// Merge combines another sketch (element-wise max)
func (cm *CountMinSketch) Merge(other *CountMinSketch) error {
	if cm.depth != other.depth || cm.width != other.width {
		return ErrSketchDimensionMismatch
	}
	
	cm.mu.Lock()
	other.mu.RLock()
	defer cm.mu.Unlock()
	defer other.mu.RUnlock()
	
	for i := 0; i < cm.depth; i++ {
		for j := 0; j < cm.width; j++ {
			if other.table[i][j] > cm.table[i][j] {
				cm.table[i][j] = other.table[i][j]
			}
		}
	}
	
	return nil
}

var ErrSketchDimensionMismatch = error(nil)

func fnv1a64Seed(data []byte, seed uint64) uint64 {
	const prime64 = 1099511628211
	
	hash := seed
	for _, b := range data {
		hash ^= uint64(b)
		hash *= prime64
	}
	return hash
}

// BloomFilter for membership testing (has user been seen?)
// Space-efficient probabilistic data structure
type BloomFilter struct {
	mu      sync.RWMutex
	bits    []uint64 // Bit array (packed into uint64s)
	size    uint64   // Number of bits
	hashNum int      // Number of hash functions
}

// NewBloomFilter creates filter for expected items with false positive rate
// n: expected number of items
// fp: false positive rate (0.01 = 1%)
func NewBloomFilter(n int, fp float64) *BloomFilter {
	// Optimal bit array size: m = -n*ln(p) / (ln(2)^2)
	m := uint64(math.Ceil(-float64(n) * math.Log(fp) / (math.Log(2) * math.Log(2))))
	
	// Optimal number of hash functions: k = m/n * ln(2)
	k := int(math.Ceil(float64(m) / float64(n) * math.Log(2)))
	
	return &BloomFilter{
		bits:    make([]uint64, (m+63)/64), // Round up to uint64 blocks
		size:    m,
		hashNum: k,
	}
}

// Add adds item to filter
func (bf *BloomFilter) Add(data []byte) {
	bf.mu.Lock()
	defer bf.mu.Unlock()
	
	h1, h2 := bf.hash(data)
	for i := 0; i < bf.hashNum; i++ {
		// Double hashing: h(i) = h1 + i*h2
		pos := (h1 + uint64(i)*h2) % bf.size
		bf.setBit(pos)
	}
}

// Contains checks if item might be in set
func (bf *BloomFilter) Contains(data []byte) bool {
	bf.mu.RLock()
	defer bf.mu.RUnlock()
	
	h1, h2 := bf.hash(data)
	for i := 0; i < bf.hashNum; i++ {
		pos := (h1 + uint64(i)*h2) % bf.size
		if !bf.getBit(pos) {
			return false // Definitely not in set
		}
	}
	return true // Probably in set
}

func (bf *BloomFilter) hash(data []byte) (uint64, uint64) {
	h1 := fnv1a64(data)
	h2 := fnv1a64Seed(data, 0xDEADBEEF)
	return h1, h2
}

func (bf *BloomFilter) setBit(pos uint64) {
	bf.bits[pos/64] |= 1 << (pos % 64)
}

func (bf *BloomFilter) getBit(pos uint64) bool {
	return (bf.bits[pos/64] & (1 << (pos % 64))) != 0
}

// EstimatedCount estimates number of items added (approximation)
func (bf *BloomFilter) EstimatedCount() uint64 {
	bf.mu.RLock()
	defer bf.mu.RUnlock()
	
	// Count set bits
	setBits := uint64(0)
	for _, word := range bf.bits {
		setBits += uint64(bits.OnesCount64(word))
	}
	
	// Estimate: n ≈ -m/k * ln(1 - X/m), where X = number of set bits
	if setBits == bf.size {
		return 0 // Saturated
	}
	
	ratio := float64(setBits) / float64(bf.size)
	estimate := -float64(bf.size) / float64(bf.hashNum) * math.Log(1.0-ratio)
	
	return uint64(estimate)
}
