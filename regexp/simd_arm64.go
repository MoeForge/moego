//go:build arm64 && !purego

package regexp

import "unsafe"

// Advanced SIMD is part of the arm64 baseline.
const hasPairKernel = true
const hasSpanKernel = true

//go:noescape
func pairNEON(a, b *byte, n int, r1, r2, m1, m2 byte) int

// indexPair returns the first position whose two offset bytes match after
// applying the OR masks, or -1. The first byte should be the rarer one.
func indexPair(s string, o1, o2 int, r1, r2, m1, m2 byte) int {
	n := len(s) - max(o1, o2)
	if n >= 16 {
		p := unsafe.Pointer(unsafe.StringData(s))
		return pairNEON((*byte)(unsafe.Add(p, o1)), (*byte)(unsafe.Add(p, o2)), n, r1, r2, m1, m2)
	}
	return indexPairScalar(s, o1, o2, r1, r2, m1, m2)
}

//go:noescape
func spanNEON(p *byte, n int, lo, hi *[16]byte, inv byte) int

// span returns the first non-member (member is true) or member (member is
// false), or len(s). The nibble tables describe an ASCII-only class.
func span(s string, lo, hi *[16]byte, in *[256]bool, member bool) int {
	if len(s) >= 16 {
		var inv byte
		if !member {
			inv = 255
		}
		return spanNEON(unsafe.StringData(s), len(s), lo, hi, inv)
	}
	return spanScalar(s, in, member)
}

//go:noescape
func classMasksNEON(p *byte, n int, lo, hi *[16]byte, out *uint64)

// classMasks writes one membership bit per byte in each complete 64-byte
// block. len(s) is 64*len(out); bytes above ASCII are never members.
func classMasks(s string, lo, hi *[16]byte, in *[256]bool, out []uint64) {
	if len(out) > 0 {
		classMasksNEON(unsafe.StringData(s), len(s), lo, hi, &out[0])
	}
}
