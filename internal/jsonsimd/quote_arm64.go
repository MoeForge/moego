//go:build arm64 && !purego

package jsonsimd

import "unsafe"

// LazyCopy is true: CopyPlainBlocks runs wherever QuoteCountBlocks reads
// blocks, so a long string found plain in its first blocks can be checked
// again as it is copied.
const LazyCopy = true

// quoteShuf[m] is the VTBL control that writes an 8-byte group, held in the
// low half of a register whose high half is backslashes, with a backslash
// before each byte whose bit is set in m.
var quoteShuf = func() (t [256][2]uint64) {
	for m := range t {
		var c [16]byte
		for p := range c {
			c[p] = 0x80
		}
		p := 0
		for b := 0; b < 8; b++ {
			if m>>b&1 != 0 {
				c[p] = 8
				p++
			}
			c[p] = byte(b)
			p++
		}
		for k := range c {
			t[m][k/8] |= uint64(c[k]) << (8 * (k % 8))
		}
	}
	return t
}()

//go:noescape
func quoteCountNEON(src *byte, n, i int, html bool) (int, int)

//go:noescape
func quoteNEON(dst *byte, dn int, src *byte, n, i, w int, html bool, shuf *[256][2]uint64) (int, int)

//go:noescape
func copyPlainNEON(dst, src *byte, n int, html bool) int

//go:noescape
func CopyShortNEON(dst, src *byte, n int, html bool) bool

// CopyShortFast is CopyShort for callers that checked what it checks.
func CopyShortFast(dst, src *byte, n int, html bool) bool {
	return CopyShortNEON(dst, src, n, html)
}

// QuoteCountBlocks counts for quotedLen the 64-byte blocks of s from i
// whose only escapes are quotes and backslashes. It returns the new i and
// the number of those escapes.
func QuoteCountBlocks(s string, i int, html bool) (int, int) {
	if len(s)-i < 64 {
		return i, 0
	}
	return quoteCountNEON(unsafe.StringData(s), len(s), i, html)
}

// QuoteBlocks writes for writeQuoted the 64-byte blocks of s from i whose
// only escapes are quotes and backslashes, to out[w:], while out has room
// for the worst case. It returns the new i and w.
func QuoteBlocks(out []byte, s string, i, w int, html bool) (int, int) {
	if len(s)-i < 64 {
		return i, w
	}
	return quoteNEON(unsafe.SliceData(out), len(out), unsafe.StringData(s), len(s), i, w, html, &quoteShuf)
}

// CopyPlainBlocks copies the 64-byte blocks of s to dst, which has room for
// s, while no byte of a block needs an escape in a JSON string: it copies
// ASCII without control bytes, quotes, backslashes and, with html, '<',
// '>' and '&'. It returns how many bytes it copied.
func CopyPlainBlocks(dst []byte, s string, html bool) int {
	if len(s) < 64 {
		return 0
	}
	_ = dst[len(s)-1]
	return copyPlainNEON(unsafe.SliceData(dst), unsafe.StringData(s), len(s), html)
}

// CopyShort copies s, shorter than 64 bytes, to dst when no byte of s
// needs an escape in a JSON string, and reports whether it did. Bytes of
// 0x80 and above, and with html '<', '>' and '&', count as needing one. It
// may also report false for such s, and it may write to dst either way.
// dst has room for max(len(s), 32) bytes.
func CopyShort(dst []byte, s string, html bool) bool {
	if len(s) == 0 {
		return false
	}
	p := unsafe.StringData(s)
	if len(s) < 32 && uintptr(unsafe.Pointer(p))&4095 > 4096-32 {
		// 32 bytes from p would reach into the next page.
		return false
	}
	_ = dst[max(len(s), 32)-1]
	return CopyShortNEON(unsafe.SliceData(dst), p, len(s), html)
}
