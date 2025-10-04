package scanner

import (
	"hash/fnv"
	"math"
)

// BloomFilter provides fast negative lookups (if absent => guaranteed no match)
// Reduces false positives in Aho-Corasick by pre-filtering byte chunks.
type BloomFilter struct {
	bits []uint64
	k    int // number of hash functions
	m    int // bit array size
}

// NewBloomFilter creates filter with desired false positive rate (e.g., 0.01 = 1%)
func NewBloomFilter(expectedElements int, fpRate float64) *BloomFilter {
	m := optimalM(expectedElements, fpRate)
	k := optimalK(m, expectedElements)
	bits := make([]uint64, (m+63)/64) // ceil(m/64) uint64s
	return &BloomFilter{bits: bits, k: k, m: m}
}

func optimalM(n int, p float64) int {
	return int(math.Ceil(-float64(n) * math.Log(p) / (math.Log(2) * math.Log(2))))
}

func optimalK(m, n int) int {
	k := int(math.Ceil(float64(m) / float64(n) * math.Log(2)))
	if k < 1 {
		k = 1
	}
	if k > 10 {
		k = 10 // practical limit
	}
	return k
}

// Add inserts pattern bytes into bloom filter
func (bf *BloomFilter) Add(data []byte) {
	for i := 0; i < bf.k; i++ {
		idx := bf.hash(data, i) % bf.m
		bf.bits[idx/64] |= 1 << (idx % 64)
	}
}

// MayContain checks if data possibly exists (false positive possible, no false negative)
func (bf *BloomFilter) MayContain(data []byte) bool {
	for i := 0; i < bf.k; i++ {
		idx := bf.hash(data, i) % bf.m
		if (bf.bits[idx/64] & (1 << (idx % 64))) == 0 {
			return false
		}
	}
	return true
}

// hash combines FNV-1a with seed for k different hash functions
func (bf *BloomFilter) hash(data []byte, seed int) int {
	h := fnv.New64a()
	h.Write(data)
	if seed > 0 {
		h.Write([]byte{byte(seed)})
	}
	return int(h.Sum64())
}

// Stats returns bloom filter statistics
func (bf *BloomFilter) Stats() map[string]interface{} {
	setBits := 0
	for _, word := range bf.bits {
		setBits += popcount(word)
	}
	fillRatio := float64(setBits) / float64(bf.m)
	return map[string]interface{}{
		"size_bits":  bf.m,
		"hash_funcs": bf.k,
		"set_bits":   setBits,
		"fill_ratio": fillRatio,
		"capacity":   len(bf.bits) * 64,
	}
}

func popcount(x uint64) int {
	count := 0
	for x != 0 {
		x &= x - 1
		count++
	}
	return count
}
