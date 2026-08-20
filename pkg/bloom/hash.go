// Package bloom provides Murmur3 hashing and Bloom filter data structures.
package bloom

import (
	"encoding/binary"
	"math/bits"
)

// Murmur3_128 returns two 64-bit hashes (h1, h2) for the given byte slice with the specified seed.
// This is an allocation-free pure-Go implementation of MurmurHash3 (128-bit variant for x64).
func Murmur3_128(data []byte, seed uint64) (h1, h2 uint64) {
	const (
		c1 = uint64(0x87c37b91114253d5)
		c2 = uint64(0x4cf5ad432745937f)
		c3 = uint64(0x52dce729304928b9)
		c4 = uint64(0x38495ab5b2f4fe55)
	)

	h1 = seed
	h2 = seed

	nblocks := len(data) / 16
	for i := range nblocks {
		k1 := binary.LittleEndian.Uint64(data[i*16 : i*16+8])
		k2 := binary.LittleEndian.Uint64(data[i*16+8 : i*16+16])

		k1 *= c1
		k1 = bits.RotateLeft64(k1, 31)
		k1 *= c2
		h1 ^= k1

		h1 = bits.RotateLeft64(h1, 27)
		h1 += h2
		h1 = h1*5 + c3

		k2 *= c2
		k2 = bits.RotateLeft64(k2, 33)
		k2 *= c1
		h2 ^= k2

		h2 = bits.RotateLeft64(h2, 31)
		h2 += h1
		h2 = h2*5 + c4
	}

	tail := data[nblocks*16:]
	var k1, k2 uint64

	switch len(tail) {
	case 15:
		k2 ^= uint64(tail[14]) << 48
		fallthrough
	case 14:
		k2 ^= uint64(tail[13]) << 40
		fallthrough
	case 13:
		k2 ^= uint64(tail[12]) << 32
		fallthrough
	case 12:
		k2 ^= uint64(tail[11]) << 24
		fallthrough
	case 11:
		k2 ^= uint64(tail[10]) << 16
		fallthrough
	case 10:
		k2 ^= uint64(tail[9]) << 8
		fallthrough
	case 9:
		k2 ^= uint64(tail[8])
		k2 *= c2
		k2 = bits.RotateLeft64(k2, 33)
		k2 *= c1
		h2 ^= k2
		fallthrough
	case 8:
		k1 ^= uint64(tail[7]) << 56
		fallthrough
	case 7:
		k1 ^= uint64(tail[6]) << 48
		fallthrough
	case 6:
		k1 ^= uint64(tail[5]) << 40
		fallthrough
	case 5:
		k1 ^= uint64(tail[4]) << 32
		fallthrough
	case 4:
		k1 ^= uint64(tail[3]) << 24
		fallthrough
	case 3:
		k1 ^= uint64(tail[2]) << 16
		fallthrough
	case 2:
		k1 ^= uint64(tail[1]) << 8
		fallthrough
	case 1:
		k1 ^= uint64(tail[0])
		k1 *= c1
		k1 = bits.RotateLeft64(k1, 31)
		k1 *= c2
		h1 ^= k1
	}

	length := uint64(len(data))
	h1 ^= length
	h2 ^= length

	h1 += h2
	h2 += h1

	h1 = fmix64(h1)
	h2 = fmix64(h2)

	h1 += h2
	h2 += h1

	return h1, h2
}

func fmix64(k uint64) uint64 {
	k ^= k >> 33
	k *= 0xff51afd7ed558ccd
	k ^= k >> 33
	k *= 0xc4ceb9fe1a85ec53
	k ^= k >> 33
	return k
}

// HashTokenToIndices computes k bit indices in the range [0, numBits) using
// Kirsch-Mitzenmacher double hashing technique: g_i(x) = (h1 + i * h2) % numBits.
func HashTokenToIndices(data []byte, k int, numBits uint64, indices []uint64) []uint64 {
	if len(indices) < k {
		indices = make([]uint64, k)
	} else {
		indices = indices[:k]
	}

	h1, h2 := Murmur3_128(data, 0x9747b28c)
	for i := range k {
		// Kirsch-Mitzenmacher: h1 + i*h2 mod numBits
		indices[i] = (h1 + uint64(i)*h2) % numBits
	}
	return indices
}
