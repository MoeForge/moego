//go:build amd64 && !purego

package regexp

import "unsafe"

var haveAVX2 = supportsAVX2()

// hasPairKernel reports whether indexPair runs a vector kernel; otherwise
// prefilters scan with strings.IndexByte.
var hasPairKernel = haveAVX2

//go:noescape
func supportsAVX2() bool

//go:noescape
func pairAVX2(a, b *byte, n int, r1, r2, m1, m2 byte) int

// indexPair returns the smallest i < len(s)-max(o1, o2) with
// s[i+o1]|m1 == r1 and s[i+o2]|m2 == r2, or -1. An OR mask of 0x20 on a
// lowercase letter matches both of its ASCII cases. r1 should be the rarer
// byte: it is tested first.
func indexPair(s string, o1, o2 int, r1, r2, m1, m2 byte) int {
	n := len(s) - max(o1, o2)
	if n >= 32 && haveAVX2 {
		p := unsafe.StringData(s)
		return pairAVX2((*byte)(unsafe.Add(unsafe.Pointer(p), o1)), (*byte)(unsafe.Add(unsafe.Pointer(p), o2)), n, r1, r2, m1, m2)
	}
	return indexPairScalar(s, o1, o2, r1, r2, m1, m2)
}

// hasSpanKernel reports whether span runs a vector kernel.
var hasSpanKernel = haveAVX2

//go:noescape
func spanAVX2(p *byte, n int, lo, hi *[16]byte, inv uint32) int

// span returns the first i with s[i] outside the class (member = false: the
// first member), or len(s). The class is ASCII-only, given as nibble
// tables: b is a member if lo[b&15]&hi[b>>4] != 0.
func span(s string, lo, hi *[16]byte, in *[256]bool, member bool) int {
	if len(s) >= 32 && haveAVX2 {
		var inv uint32
		if !member {
			inv = ^uint32(0)
		}
		return spanAVX2(unsafe.StringData(s), len(s), lo, hi, inv)
	}
	return spanScalar(s, in, member)
}

//go:noescape
func classMasksAVX2(p *byte, n int, lo, hi *[16]byte, out *uint64)

// classMasks sets out[k] to the membership bits of s[64k : 64k+64], bit j
// for byte j; len(s) is 64*len(out). The class is given as for span.
func classMasks(s string, lo, hi *[16]byte, in *[256]bool, out []uint64) {
	if len(out) > 0 && haveAVX2 {
		classMasksAVX2(unsafe.StringData(s), len(s), lo, hi, &out[0])
		return
	}
	classMasksScalar(s, in, out)
}
