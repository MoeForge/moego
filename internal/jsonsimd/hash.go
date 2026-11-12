package jsonsimd

import (
	"hash/maphash"
	"math/rand/v2"
	"unsafe"
)

// A HashKey keys Sum, a hash that tells whether bytes changed, but for a
// random match: package json stamps bytes with it.
type HashKey struct {
	k    [24]uint64
	seed maphash.Seed
}

// NewHashKey returns a random key.
func NewHashKey() *HashKey {
	h := &HashKey{seed: maphash.MakeSeed()}
	for i := range h.k {
		h.k[i] = rand.Uint64()
	}
	return h
}

// Sum returns the hash of b under the key. From 128 bytes on, with AES
// instructions, eight lanes of 16 bytes take b 128 bytes at a time, each
// with an AES round whose round key is its 16 bytes, and last the rest of
// b padded with zeros, XOR key bytes; the lanes are folded into one by AES
// rounds, with the length and key bytes. Each byte is taken once: one
// taken twice could undo a change to itself through the S-box. Otherwise
// Sum is maphash.Bytes.
func (h *HashKey) Sum(b []byte) uint64 {
	if !haveAES || len(b) < 128 {
		return maphash.Bytes(h.seed, b)
	}
	n := len(b) &^ 127
	var tail [128]byte
	copy(tail[:], b[n:])
	return hashAES(&h.k, unsafe.SliceData(b), n, &tail, len(b))
}

// FastSum reports whether Sum reads long bytes with its AES kernel, which
// reads them faster than a check of JSON does.
func FastSum() bool { return haveAES }
