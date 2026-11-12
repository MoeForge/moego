package regexp

import (
	"encoding/binary"
	"strings"
)

func indexPairScalar(s string, o1, o2 int, r1, r2, m1, m2 byte) int {
	n := len(s) - max(o1, o2)
	for i := 0; i < n; i++ {
		if s[i+o1]|m1 == r1 && s[i+o2]|m2 == r2 {
			return i
		}
	}
	return -1
}

// wordLit compares a short literal with 8-byte loads instead of a call to
// the runtime's memequal.
type wordLit struct {
	lit  string
	kind uint8
	a, b uint64 // wlWords: first and last 8 bytes; wlShort: the zero-padded literal
	mask uint64
}

const (
	wlEmpty uint8 = iota
	wlShort       // 1..7 bytes
	wlWords       // 8..16 bytes
	wlLong
)

func newWordLit(lit string) wordLit {
	w := wordLit{lit: lit}
	n := len(lit)
	switch {
	case n > 16:
		w.kind = wlLong
	case n >= 8:
		w.kind = wlWords
		w.a, w.b = load64(lit, 0), load64(lit, n-8)
	case n > 0:
		w.kind = wlShort
		var buf [8]byte
		copy(buf[:], lit)
		w.a = binary.LittleEndian.Uint64(buf[:])
		w.mask = 1<<(8*n) - 1
	}
	return w
}

// at reports whether s[o:] starts with the literal; o+len(lit) <= len(s).
func (w *wordLit) at(s string, o int) bool {
	if w.kind == wlWords {
		return load64(s, o) == w.a && load64(s, o+len(w.lit)-8) == w.b
	}
	if w.kind == wlShort && o+8 <= len(s) {
		return load64(s, o)&w.mask == w.a
	}
	return w.atSlow(s, o)
}

//go:noinline
func (w *wordLit) atSlow(s string, o int) bool {
	n := len(w.lit)
	switch {
	case w.kind == wlEmpty:
		return true
	case w.kind == wlShort && o+n >= 8:
		// Near the end: the literal is the top n bytes of the word ending
		// with it.
		return load64(s, o+n-8)>>(64-8*n) == w.a
	}
	return s[o:o+n] == w.lit
}

// noNewline reports whether s, len(s) >= 8, holds no newline byte.
func noNewline(s string) bool {
	if len(s) > 32 {
		return strings.IndexByte(s, '\n') < 0
	}
	const lsb, nl = 0x0101010101010101, 0x0A0A0A0A0A0A0A0A
	n := len(s)
	v1 := load64(s, 0) ^ nl
	v2 := load64(s, n-8) ^ nl
	v3 := load64(s, min(8, n-8)) ^ nl
	v4 := load64(s, max(n-16, 0)) ^ nl
	z := (v1-lsb)&^v1 | (v2-lsb)&^v2 | (v3-lsb)&^v3 | (v4-lsb)&^v4
	return z&(lsb<<7) == 0
}

func spanScalar(s string, in *[256]bool, member bool) int {
	i := 0
	if member {
		for i < len(s) && in[s[i]] {
			i++
		}
	} else {
		for i < len(s) && !in[s[i]] {
			i++
		}
	}
	return i
}

// classMasksScalar is classMasks without a vector kernel.
func classMasksScalar(s string, in *[256]bool, out []uint64) {
	for k := range out {
		out[k] = blockMask(s[64*k:64*k+64], in)
	}
}

// blockMask returns the membership bits of the bytes of s, len(s) <= 64.
func blockMask(s string, in *[256]bool) uint64 {
	var m uint64
	for j := 0; j < len(s); j++ {
		var bit uint64
		if in[s[j]] {
			bit = 1
		}
		m |= bit << j
	}
	return m
}

// nibbleTables returns the nibble tables of an ASCII class: bit h of lo[l]
// is set when byte h<<4|l is a member, and hi[h] = 1<<h for h < 8.
func nibbleTables(in *[256]bool) (lo, hi [16]byte) {
	for b := 0; b < 128; b++ {
		if in[b] {
			lo[b&15] |= 1 << (b >> 4)
		}
	}
	for h := 0; h < 8; h++ {
		hi[h] = 1 << h
	}
	return
}
