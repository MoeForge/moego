//go:build (!amd64 && !arm64) || purego

package regexp

const hasPairKernel = false

// indexPair returns the smallest i < len(s)-max(o1, o2) with
// s[i+o1]|m1 == r1 and s[i+o2]|m2 == r2, or -1.
func indexPair(s string, o1, o2 int, r1, r2, m1, m2 byte) int {
	return indexPairScalar(s, o1, o2, r1, r2, m1, m2)
}

const hasSpanKernel = false

// span returns the first i with s[i] outside the class (member = false: the
// first member), or len(s).
func span(s string, lo, hi *[16]byte, in *[256]bool, member bool) int {
	return spanScalar(s, in, member)
}

// classMasks sets out[k] to the membership bits of s[64k : 64k+64], bit j
// for byte j; len(s) is 64*len(out).
func classMasks(s string, lo, hi *[16]byte, in *[256]bool, out []uint64) {
	classMasksScalar(s, in, out)
}
