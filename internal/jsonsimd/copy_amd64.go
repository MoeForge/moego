//go:build amd64 && !purego

package jsonsimd

import "unsafe"

//go:noescape
func copyNTAVX2(dst, src *byte, n int)

//go:noescape
func zeroAVX2(p *byte, n int) bool

// ZeroAVX2 reports whether the n bytes at p are all zero. It needs AVX2 and
// n of at least 32, and reads only those bytes.
func ZeroAVX2(p unsafe.Pointer, n uintptr) bool { return zeroAVX2((*byte)(p), int(n)) }

// copyNTChunk is the most CopyLong copies in one call of the kernel, which
// can not be preempted.
const copyNTChunk = 128 << 10

// CopyLong copies s to dst, which has room for it, with stores that bypass
// the caches: for long copies to memory that is not in them, whose lines
// such stores need not read before writing them.
func CopyLong(dst []byte, s string) {
	dst = dst[:len(s)]
	if !HaveAVX2 || len(s) < 4096 {
		copy(dst, s)
		return
	}
	head := int(-uintptr(unsafe.Pointer(unsafe.SliceData(dst))) & 31)
	copy(dst[:head], s[:head])
	i := head
	for len(s)-i >= 128 {
		n := min(len(s)-i, copyNTChunk) &^ 127
		copyNTAVX2(&dst[i], unsafe.StringData(s[i:]), n)
		i += n
	}
	copy(dst[i:], s[i:])
}
