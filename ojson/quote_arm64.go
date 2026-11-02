//go:build arm64 && !purego

package ojson

import "unsafe"

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

// quoteCountBlocks counts for quotedLen the 64-byte blocks of s from i
// whose only escapes are quotes and backslashes. It returns the new i and
// the number of those escapes.
func quoteCountBlocks(s string, i int, html bool) (int, int) {
	if len(s)-i < 64 {
		return i, 0
	}
	return quoteCountNEON(unsafe.StringData(s), len(s), i, html)
}

// quoteBlocks writes for writeQuoted the 64-byte blocks of s from i whose
// only escapes are quotes and backslashes, to out[w:], while out has room
// for the worst case. It returns the new i and w.
func quoteBlocks(out []byte, s string, i, w int, html bool) (int, int) {
	if len(s)-i < 64 {
		return i, w
	}
	return quoteNEON(unsafe.SliceData(out), len(out), unsafe.StringData(s), len(s), i, w, html, &quoteShuf)
}
