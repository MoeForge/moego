//go:build arm64 && !purego

package jsonsimd

import "unsafe"

// plainMasks is false: Validate uses the NEON block paths.
const plainMasks = false

//go:noescape
func validMasksNEON(p *byte, nb int, out *vmask)

//go:noescape
func validMasksHiNEON(p *byte, nb int, out *vmask, hi *uint64)

//go:noescape
func countMasksNEON(p *byte, nb int, out *cmask)

//go:noescape
func strSkipNEON(p *byte, n, i int) (int, bool)

//go:noescape
func strSkipHTMLNEON(p *byte, n, i int) (int, bool)

//go:noescape
func htmlSkipNEON(p *byte, n, i int) int

//go:noescape
func strEndNEON(p *byte, n, i int) (int, bool)

// validMasks fills out with the masks of the 64-byte blocks of s from i, at
// most validBatch of them, and returns how many. A last block shorter than
// 64 bytes is read from tail, padded with spaces. When hi is not nil, it
// receives the masks of the bytes of 0x80 and up.
func validMasks(s string, i int, out *[validBatch]vmask, tail *[64]byte, hi *[validBatch]uint64) int {
	n := (len(s) - i) >> 6
	var p *byte
	if n == 0 {
		k := copy(tail[:], s[i:])
		for ; k < len(tail); k++ {
			tail[k] = ' '
		}
		p, n = &tail[0], 1
	} else {
		p, n = (*byte)(unsafe.Add(unsafe.Pointer(unsafe.StringData(s)), i)), min(n, validBatch)
	}
	if hi == nil {
		validMasksNEON(p, n, &out[0])
	} else {
		validMasksHiNEON(p, n, &out[0], &hi[0])
	}
	return n
}

// countMasks is validMasks for CountElements.
func countMasks(s string, i int, out *[validBatch]cmask, tail *[64]byte) int {
	n := (len(s) - i) >> 6
	if n == 0 {
		k := copy(tail[:], s[i:])
		for ; k < len(tail); k++ {
			tail[k] = ' '
		}
		countMasksNEON(&tail[0], 1, &out[0])
		return 1
	}
	n = min(n, validBatch)
	countMasksNEON((*byte)(unsafe.Add(unsafe.Pointer(unsafe.StringData(s)), i)), n, &out[0])
	return n
}

// strSkip returns the start of the first 64-byte block of s from i that
// holds a quote, a backslash or a control byte, or where fewer than 64
// bytes are left, and whether a byte before it is 0x80 or above.
func strSkip(s string, i int) (int, bool) {
	return strSkipNEON(unsafe.StringData(s), len(s), i)
}

// strSkipHTML is strSkip, but reports whether a byte before the block it
// returns is '<', '>', '&' or 0xE2, which HasHTML looks for.
func strSkipHTML(s string, i int) (int, bool) {
	return strSkipHTMLNEON(unsafe.StringData(s), len(s), i)
}

// strSkipBytes is strSkip for scans byte by byte, which go on from any
// index it returns: past the first block, it reads blocks aligned in
// memory.
func strSkipBytes(s string, i int) (int, bool) {
	p := unsafe.StringData(s)
	j, h := strSkipNEON(p, min(len(s), i+64), i)
	if j == i {
		return i, h
	}
	k := i + 64 - int((uintptr(unsafe.Pointer(p))+uintptr(i))&63)
	j, h2 := strSkipNEON(p, len(s), k)
	return j, h || h2
}

// strEnd returns the index of the first quote, backslash or control byte
// of s from i, or, when there is none, an index past which fewer than 32
// bytes are left, and whether a byte before that index is 0x80 or above.
func strEnd(s string, i int) (int, bool) {
	return strEndNEON(unsafe.StringData(s), len(s), i)
}

// htmlSkip returns the start of the first 64-byte block of s from i that
// holds a byte HasHTML looks for, or where fewer than 64 bytes are left.
func htmlSkip(s string, i int) int {
	return htmlSkipNEON(unsafe.StringData(s), len(s), i)
}
