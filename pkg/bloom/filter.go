// Package bloom provides high-performance, zero-allocation Bloom filters.
package bloom

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"sync"
	"unsafe"
)

const (
	// DefaultNumBits is 16384 bits = 2048 bytes (2 KB) per filter.
	// Provides < 0.1% FPR for up to ~700 items with k=4, and < 1.2% FPR for up to ~2000 items.
	DefaultNumBits = 16384

	// DefaultHashCount is the default number of hash probes (k=4).
	DefaultHashCount = 4
)

// Filter represents a memory-efficient, concurrency-safe Bloom filter backed by a 64-bit word bitset.
type Filter struct {
	mu       sync.RWMutex
	bits     []uint64
	numBits  uint64
	k        int
	elements uint64
}

// Config holds configuration parameters for creating a Bloom filter.
type Config struct {
	NumBits   uint64
	HashCount int
}

// New creates a new Bloom filter with the default bit size (16,384 bits = 2 KB) and k=4.
func New() *Filter {
	return NewWithConfig(Config{
		NumBits:   DefaultNumBits,
		HashCount: DefaultHashCount,
	})
}

// NewWithConfig creates a new Bloom filter with custom bit size and hash count.
// NumBits is rounded up to the nearest multiple of 64.
func NewWithConfig(cfg Config) *Filter {
	numBits := cfg.NumBits
	if numBits == 0 {
		numBits = DefaultNumBits
	}
	// Round up to multiple of 64
	numWords := (numBits + 63) / 64
	numBits = numWords * 64

	k := cfg.HashCount
	if k <= 0 {
		k = DefaultHashCount
	}

	return &Filter{
		bits:    make([]uint64, numWords),
		numBits: numBits,
		k:       k,
	}
}

// NewForCapacity computes the optimal bit size and hash count for a given expected item capacity
// and desired false positive probability (e.g., n=1000, fp=0.005).
func NewForCapacity(expectedItems uint64, falsePositiveRate float64) *Filter {
	if expectedItems == 0 {
		expectedItems = 100
	}
	if falsePositiveRate <= 0 || falsePositiveRate >= 1.0 {
		falsePositiveRate = 0.01 // 1% default
	}

	// Calculate optimal bit count m = -(n * ln(p)) / (ln(2)^2)
	n := float64(expectedItems)
	ln2 := math.Ln2
	m := -1.0 * (n * math.Log(falsePositiveRate)) / (ln2 * ln2)
	numBits := uint64(math.Ceil(m))
	if numBits < 64 {
		numBits = 64
	}

	// Calculate optimal hash count k = (m / n) * ln(2)
	k := int(math.Round((float64(numBits) / n) * ln2))
	if k < 1 {
		k = 1
	} else if k > 16 {
		k = 16
	}
	return NewWithConfig(Config{
		NumBits:   numBits,
		HashCount: k,
	})
}

// Add inserts a byte slice token into the Bloom filter.
func (f *Filter) Add(token []byte) {
	if len(token) == 0 {
		return
	}
	var indicesBuf [16]uint64
	indices := HashTokenToIndices(token, f.k, f.numBits, indicesBuf[:])

	f.mu.Lock()
	defer f.mu.Unlock()

	for _, idx := range indices {
		wordIdx := idx / 64
		bitIdx := idx % 64
		f.bits[wordIdx] |= (1 << bitIdx)
	}
	f.elements++
}

// AddString inserts a string token into the Bloom filter without heap allocation.
func (f *Filter) AddString(s string) {
	if s == "" {
		return
	}
	// Zero-allocation string-to-byte conversion (read-only)
	b := unsafe.Slice(unsafe.StringData(s), len(s))
	f.Add(b)
}

// Contains tests whether a byte slice token is possibly in the set.
// If it returns false, the token is DEFINITIVELY NOT in the set (zero false negatives).
// If it returns true, the token MIGHT be in the set (possible false positive).
func (f *Filter) Contains(token []byte) bool {
	if len(token) == 0 {
		return false
	}
	var indicesBuf [16]uint64
	indices := HashTokenToIndices(token, f.k, f.numBits, indicesBuf[:])

	f.mu.RLock()
	defer f.mu.RUnlock()

	for _, idx := range indices {
		wordIdx := idx / 64
		bitIdx := idx % 64
		if (f.bits[wordIdx] & (1 << bitIdx)) == 0 {
			return false
		}
	}
	return true
}

// ContainsString tests whether a string token is possibly in the set without heap allocation.
func (f *Filter) ContainsString(s string) bool {
	if s == "" {
		return false
	}
	b := unsafe.Slice(unsafe.StringData(s), len(s))
	return f.Contains(b)
}

// Union merges another Bloom filter into this one using bitwise OR.
// Both filters must have the same bit size and hash count.
func (f *Filter) Union(other *Filter) error {
	if other == nil {
		return nil
	}

	other.mu.RLock()
	defer other.mu.RUnlock()

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.numBits != other.numBits || f.k != other.k {
		return fmt.Errorf("cannot union bloom filters with different dimensions: (%d, %d) vs (%d, %d)",
			f.numBits, f.k, other.numBits, other.k)
	}

	for i := range f.bits {
		f.bits[i] |= other.bits[i]
	}
	f.elements += other.elements
	return nil
}

// Intersect merges another Bloom filter into this one using bitwise AND.
func (f *Filter) Intersect(other *Filter) error {
	if other == nil {
		return errors.New("cannot intersect with nil filter")
	}

	other.mu.RLock()
	defer other.mu.RUnlock()

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.numBits != other.numBits || f.k != other.k {
		return fmt.Errorf("cannot intersect bloom filters with different dimensions: (%d, %d) vs (%d, %d)",
			f.numBits, f.k, other.numBits, other.k)
	}

	for i := range f.bits {
		f.bits[i] &= other.bits[i]
	}
	return nil
}

// BitCount returns the number of bits set to 1 in the bitset.
func (f *Filter) BitCount() uint64 {
	f.mu.RLock()
	defer f.mu.RUnlock()

	var total uint64
	for _, word := range f.bits {
		total += uint64(bits.OnesCount64(word))
	}
	return total
}

// FillRatio returns the fraction of bits set to 1 (between 0.0 and 1.0).
func (f *Filter) FillRatio() float64 {
	return float64(f.BitCount()) / float64(f.numBits)
}

// EstimatedCount estimates the number of distinct items inserted into the filter:
// n* = - (m / k) * ln(1 - X / m), where X is the number of bits set.
func (f *Filter) EstimatedCount() uint64 {
	bitsSet := float64(f.BitCount())
	m := float64(f.numBits)
	k := float64(f.k)

	if bitsSet >= m {
		return math.MaxUint64
	}
	if bitsSet == 0 {
		return 0
	}

	n := -1.0 * (m / k) * math.Log(1.0-bitsSet/m)
	if n < 0 {
		return 0
	}
	return uint64(math.Round(n))
}

// CurrentFalsePositiveRate computes the empirical theoretical false positive rate
// given the current number of bits set: (X / m)^k.
func (f *Filter) CurrentFalsePositiveRate() float64 {
	ratio := f.FillRatio()
	return math.Pow(ratio, float64(f.k))
}

// NumBits returns the bit array length.
func (f *Filter) NumBits() uint64 {
	return f.numBits
}

// HashCount returns k.
func (f *Filter) HashCount() int {
	return f.k
}

// SizeBytes returns the in-memory size of the raw bitset array in bytes.
func (f *Filter) SizeBytes() int {
	return len(f.bits) * 8
}

// Clone returns an independent deep copy of the Bloom filter.
func (f *Filter) Clone() *Filter {
	f.mu.RLock()
	defer f.mu.RUnlock()

	newBits := make([]uint64, len(f.bits))
	copy(newBits, f.bits)

	return &Filter{
		bits:     newBits,
		numBits:  f.numBits,
		k:        f.k,
		elements: f.elements,
	}
}

// Reset clears all bits and elements in the filter.
func (f *Filter) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i := range f.bits {
		f.bits[i] = 0
	}
	f.elements = 0
}

// MarshalBinary serializes the Bloom filter into a compact binary format.
// Layout: [8 bytes numBits][4 bytes k][8 bytes elements][raw uint64 bitset...]
func (f *Filter) MarshalBinary() ([]byte, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	buf := make([]byte, 20+len(f.bits)*8)
	binary.LittleEndian.PutUint64(buf[0:8], f.numBits)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(f.k))
	binary.LittleEndian.PutUint64(buf[12:20], f.elements)

	offset := 20
	for _, word := range f.bits {
		binary.LittleEndian.PutUint64(buf[offset:offset+8], word)
		offset += 8
	}
	return buf, nil
}

// UnmarshalBinary deserializes binary data into the Bloom filter.
func (f *Filter) UnmarshalBinary(data []byte) error {
	if len(data) < 20 {
		return errors.New("data too short for bloom filter header")
	}

	numBits := binary.LittleEndian.Uint64(data[0:8])
	k := int(binary.LittleEndian.Uint32(data[8:12]))
	elements := binary.LittleEndian.Uint64(data[12:20])

	numWords := int((numBits + 63) / 64)
	expectedLen := 20 + numWords*8
	if len(data) < expectedLen {
		return fmt.Errorf("data length %d is less than expected %d", len(data), expectedLen)
	}

	bitsSlice := make([]uint64, numWords)
	offset := 20
	for i := range numWords {
		bitsSlice[i] = binary.LittleEndian.Uint64(data[offset : offset+8])
		offset += 8
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.numBits = numBits
	f.k = k
	f.elements = elements
	f.bits = bitsSlice
	return nil
}
