package deflate

import (
	"math/bits"
	"slices"
)

// huffman builds length-limited prefix codes. Its scratch is reused across
// blocks, so building a code allocates nothing.
type huffman struct {
	// sorted holds freq<<16 | symbol of the used symbols, by frequency.
	sorted [numLitLen]uint64
	work   [numLitLen]uint32

	// Package-merge scratch: the weights of the list being built and of
	// the one after it, and per list a bit per item set for leaves.
	pmWeight [2][2 * numLitLen]uint32
	pmLeaf   [maxCodeBits][(2*numLitLen + 63) / 64]uint64
	pmLen    [maxCodeBits]int
}

// build sets lens[i] to the code length of symbol i under an optimal prefix
// code for freq with lengths at most limit. Symbols with zero frequency get
// length 0. When fewer than two symbols are used, the code still gets two
// symbols of length 1 (symbol 0 or 1 is added), so every code is complete:
// decoders differ in how they treat a lone one-bit code. Frequencies must
// sum to less than 1<<24.
func (h *huffman) build(freq []uint32, lens []uint8, limit int) {
	n := 0
	for i, f := range freq {
		lens[i] = 0
		if f != 0 {
			h.sorted[n] = uint64(f)<<16 | uint64(i)
			n++
		}
	}
	if n < 2 {
		extra := 0
		if n == 1 && h.sorted[0]&0xffff == 0 {
			extra = 1
		}
		lens[extra] = 1
		if n == 1 {
			lens[h.sorted[0]&0xffff] = 1
		} else {
			lens[1-extra] = 1
		}
		return
	}
	s := h.sorted[:n]
	slices.Sort(s)

	// Minimum-redundancy lengths in place (Moffat and Katajainen). If
	// they fit the limit they are optimal under it too.
	a := h.work[:n]
	for i, v := range s {
		a[i] = uint32(v >> 16)
	}
	leaf, root := 0, 0
	for next := 0; next < n-1; next++ {
		// Each internal node takes the two lightest of the remaining
		// leaves and finished internal nodes, preferring leaves on ties.
		for c := 0; c < 2; c++ {
			var w uint32
			if leaf < n && (root >= next || a[leaf] <= a[root]) {
				w = a[leaf]
				leaf++
			} else {
				w = a[root]
				a[root] = uint32(next)
				root++
			}
			if c == 0 {
				a[next] = w
			} else {
				a[next] += w
			}
		}
	}
	a[n-2] = 0
	for next := n - 3; next >= 0; next-- {
		a[next] = a[a[next]] + 1
	}
	avail, used, depth := 1, 0, uint32(0)
	root, next := n-2, n-1
	for avail > 0 {
		for root >= 0 && a[root] == depth {
			used++
			root--
		}
		for avail > used {
			a[next] = depth
			next--
			avail--
		}
		avail, used = 2*used, 0
		depth++
	}
	if a[0] <= uint32(limit) {
		for i, v := range s {
			lens[v&0xffff] = uint8(a[i])
		}
		return
	}
	h.packageMerge(s, lens, limit)
}

// packageMerge finds optimal lengths at most limit for the sorted used
// symbols s (Larmore and Hirschberg). List j holds the leaves merged with
// pairs from list j+1; the 2n-2 lightest items of list 0 form the code,
// and a symbol's length is the number of lists in whose taken prefix its
// leaf appears.
func (h *huffman) packageMerge(s []uint64, lens []uint8, limit int) {
	n := len(s)
	for j := limit - 1; j >= 0; j-- {
		w, leaf := &h.pmWeight[j&1], &h.pmLeaf[j]
		*leaf = [len(h.pmLeaf[0])]uint64{}
		var pw []uint32
		np := 0
		if j < limit-1 {
			pw = h.pmWeight[(j+1)&1][:h.pmLen[j+1]]
			np = len(pw) / 2
		}
		li, pi, k := 0, 0, 0
		for li < n || pi < np {
			if pi >= np || (li < n && uint32(s[li]>>16) <= pw[2*pi]+pw[2*pi+1]) {
				w[k] = uint32(s[li] >> 16)
				leaf[k>>6] |= 1 << (k & 63)
				li++
			} else {
				w[k] = pw[2*pi] + pw[2*pi+1]
				pi++
			}
			k++
		}
		h.pmLen[j] = k
	}
	for i := range s {
		lens[s[i]&0xffff] = 0
	}
	take := 2*n - 2
	for j := 0; j < limit && take > 0; j++ {
		// The leaves among the first take items of list j.
		leaves := 0
		for i, b := range h.pmLeaf[j][:(take+63)/64] {
			if r := take - 64*i; r < 64 {
				b &= 1<<r - 1
			}
			leaves += bits.OnesCount64(b)
		}
		for i := 0; i < leaves; i++ {
			lens[s[i]&0xffff]++
		}
		take = 2 * (take - leaves)
	}
}
