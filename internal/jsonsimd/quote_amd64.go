//go:build amd64 && !purego

package jsonsimd

import "unsafe"

// LazyCopy is true: CopyPlainBlocks runs wherever QuoteCountBlocks reads
// blocks, so a long string found plain in its first blocks can be checked
// again as it is copied.
const LazyCopy = true

//go:noescape
func quoteCountAVX2(src *byte, n, i int, html bool) (int, int)

//go:noescape
func copyPlainAVX2(dst, src *byte, n int, html bool) int

// CopyShortAVX2 is CopyShort for callers that checked what it checks: it
// needs AVX2, n from 1 to 63, room for max(n, 32) bytes at dst, and, when n
// is below 32, the 32 bytes from src in src's page.
//
//go:noescape
func CopyShortAVX2(dst, src *byte, n int, html bool) bool

//go:noescape
func quoteAVX2(dst *byte, dn int, src *byte, n, i, w int, html bool, shuf *[256][16]byte) (int, int)

// QuoteCountBlocks counts the 64-byte blocks of s from i that are ASCII and
// whose only bytes to escape are quotes and backslashes (and, with html,
// no '<', '>' or '&'). It returns the index of the first block it did not
// count and the number of quotes and backslashes in the counted blocks.
func QuoteCountBlocks(s string, i int, html bool) (int, int) {
	if !HaveAVX2 || len(s)-i < 64 {
		return i, 0
	}
	return quoteCountAVX2(unsafe.StringData(s), len(s), i, html)
}

// QuoteBlocks writes the blocks QuoteCountBlocks counts, from s[i:], to
// out[w:] as JSON string contents, with a backslash before each quote and
// backslash, while out has room for the worst case of a block (136
// bytes). It returns the new i and w.
func QuoteBlocks(out []byte, s string, i, w int, html bool) (int, int) {
	if !HaveAVX2 || len(s)-i < 64 {
		return i, w
	}
	return quoteAVX2(unsafe.SliceData(out), len(out), unsafe.StringData(s), len(s), i, w, html, &escapeExp)
}

// CopyPlainBlocks copies the 64-byte blocks of s to dst, which has room for
// s, while no byte of a block needs an escape in a JSON string: it copies
// ASCII without control bytes, quotes, backslashes and, with html, '<',
// '>' and '&'. It returns how many bytes it copied.
func CopyPlainBlocks(dst []byte, s string, html bool) int {
	if !HaveAVX2 || len(s) < 64 {
		return 0
	}
	_ = dst[len(s)-1]
	return copyPlainAVX2(unsafe.SliceData(dst), unsafe.StringData(s), len(s), html)
}

// CopyShort copies s, shorter than 64 bytes, to dst when no byte of s
// needs an escape in a JSON string, and reports whether it did. Bytes of
// 0x80 and above, and with html '<', '>' and '&', count as needing one. It
// may also report false for such s, and it may write to dst either way.
// dst has room for max(len(s), 32) bytes.
func CopyShort(dst []byte, s string, html bool) bool {
	if !HaveAVX2 || len(s) == 0 {
		return false
	}
	p := unsafe.StringData(s)
	if len(s) < 32 && uintptr(unsafe.Pointer(p))&4095 > 4096-32 {
		// 32 bytes from p would reach into the next page.
		return false
	}
	_ = dst[max(len(s), 32)-1]
	return CopyShortAVX2(unsafe.SliceData(dst), p, len(s), html)
}
