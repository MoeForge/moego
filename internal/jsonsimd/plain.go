package jsonsimd

import "math/bits"

// plainBlocks is leanBlocks for validatePlain, which needs none of its
// options.
type plainBlocks struct {
	s    string
	next int // the start of the next block
	k, n int // the next of the n blocks in masks
	bad  bool
	// As in vblocks.
	inStr, escNext, otherLast uint64
	masks                     [validBatch]vmask
	tail                      [64]byte
}

// block is leanBlocks.block without its options.
func (v *plainBlocks) block() (uint64, int) {
	s := v.s
	for {
		if v.k == v.n {
			if v.next >= len(s) {
				return 0, 0
			}
			if v.inStr != 0 && v.n >= 2 && v.masks[v.n-1].quote|v.masks[v.n-1].bslash|v.masks[v.n-2].quote|v.masks[v.n-2].bslash == 0 && len(s)-v.next >= 64 {
				// Inside a long string: skip the blocks with no quote,
				// backslash or control byte.
				if v.next, _ = strSkip(s, v.next); v.next >= len(s) {
					return 0, 0
				}
			}
			v.k, v.n = 0, validMasks(s, v.next, &v.masks, &v.tail, nil)
		}
		m := &v.masks[v.k]
		base := v.next
		v.k, v.next = v.k+1, v.next+64
		q, r := m.quote, m.r0
		if m.bslash|v.escNext != 0 {
			carried := v.escNext != 0
			var e uint64
			e, v.escNext = escapes(m.bslash, v.escNext)
			q &^= e
			r = prefixXor(q) ^ v.inStr
			if !validEscapes(s, base, e&r, m, carried) {
				v.bad = true
				return 0, 0
			}
		} else {
			r ^= v.inStr
		}
		v.inStr = uint64(int64(r) >> 63)
		if m.ctrl&r != 0 {
			v.bad = true
			return 0, 0
		}
		out := ^(r | q)
		other := out &^ (m.ws | m.op)
		st := m.op&out | q&r | other&^(other<<1|v.otherLast)
		v.otherLast = other >> 63
		if st != 0 {
			return st, base
		}
	}
}

// validatePlain is leanLoop for Validate without tables: the same grammar,
// without the options that would keep its variables out of registers. It
// returns leanInvalid, leanDeep or leanValid.
func validatePlain(s string) int {
	v := plainBlocks{s: s}
	// stack holds one bit per open container: 1 for object, 0 for array,
	// 64 to a word. cur is the word of the innermost one, which is not
	// stored while it is open, and top is its bit.
	var stack [leanMaxDepth / 64]uint64
	// st holds the structural bytes not yet read of the block at base.
	var st, cur, top, x uint64
	var depth, base, i int
	var c byte

	// Each label first reads the next structural byte into c at s[i].
value:
	for st == 0 {
		if st, base = v.block(); st == 0 {
			return leanInvalid
		}
	}
	i = base + bits.TrailingZeros64(st)
	c, st = s[i], st&(st-1)
valueAt:
	switch c {
	case '{', '[':
		if depth == leanMaxDepth {
			return leanDeep
		}
		top = uint64(c>>5) & 1 // 1 for '{'
		b := uint(depth & 63)
		if b == 0 && depth > 0 {
			stack[depth>>6-1] = cur
		}
		cur = cur&^(1<<b) | top<<b
		depth++
		if top != 0 {
			goto keyOrEnd
		}
		goto valueOrEnd
	case '"':
		goto after
	case ':', ',', '}', ']':
		return leanInvalid
	}
	// A number or a literal, read eight bytes at a time: x holds the bytes
	// from s[j] on, with spaces past the end of s.
	x = vload(s, i)
	switch {
	case c-'0' < 10 || c == '-':
		j := i
		if c == '-' {
			j++
			x = vload(s, j)
		}
		// The integer part: one digit, or digits that do not start with 0.
		t := nonDigits(x)
		if t&0x80 != 0 || byte(x) == '0' && t&0x8000 == 0 {
			return leanInvalid
		}
		for t == 0 {
			j += 8
			x = vload(s, j)
			t = nonDigits(x)
		}
		// The lowest bit of t is bit 7 of the first byte after the digits.
		n := bits.TrailingZeros64(t)
		j, x = j+n>>3, x>>(uint(n)&56)
		if byte(x) == '.' {
			j++
			x = vload(s, j)
			if t = nonDigits(x); t&0x80 != 0 {
				return leanInvalid
			}
			for t == 0 {
				j += 8
				x = vload(s, j)
				t = nonDigits(x)
			}
			n = bits.TrailingZeros64(t)
			j, x = j+n>>3, x>>(uint(n)&56)
		}
		if byte(x)|0x20 == 'e' {
			j++
			if x = vload(s, j); byte(x) == '+' || byte(x) == '-' {
				j++
				x = vload(s, j)
			}
			if t = nonDigits(x); t&0x80 != 0 {
				return leanInvalid
			}
			for t == 0 {
				j += 8
				x = vload(s, j)
				t = nonDigits(x)
			}
			x >>= uint(bits.TrailingZeros64(t)) & 56
		}
	case c == 't':
		if uint32(x) != 't'|'r'<<8|'u'<<16|'e'<<24 {
			return leanInvalid
		}
		x >>= 32
	case c == 'f':
		if x&(1<<40-1) != 'f'|'a'<<8|'l'<<16|'s'<<24|'e'<<32 {
			return leanInvalid
		}
		x >>= 40
	case c == 'n':
		if uint32(x) != 'n'|'u'<<8|'l'<<16|'l'<<24 {
			return leanInvalid
		}
		x >>= 32
	default:
		return leanInvalid
	}
	if byte(x) == ',' {
		// The comma is the next structural byte: take it here.
		for st == 0 {
			if st, base = v.block(); st == 0 {
				return leanInvalid
			}
		}
		st &= st - 1
		goto comma
	}
	if !validDelim[byte(x)] {
		return leanInvalid
	}
	goto after

valueOrEnd:
	for st == 0 {
		if st, base = v.block(); st == 0 {
			return leanInvalid
		}
	}
	i = base + bits.TrailingZeros64(st)
	c, st = s[i], st&(st-1)
	if c != ']' {
		goto valueAt
	}
	goto pop

keyOrEnd:
	for st == 0 {
		if st, base = v.block(); st == 0 {
			return leanInvalid
		}
	}
	i = base + bits.TrailingZeros64(st)
	c, st = s[i], st&(st-1)
	if c == '}' {
		goto pop
	}
	goto keyAt

key:
	for st == 0 {
		if st, base = v.block(); st == 0 {
			return leanInvalid
		}
	}
	i = base + bits.TrailingZeros64(st)
	c, st = s[i], st&(st-1)
keyAt:
	if c != '"' {
		return leanInvalid
	}
	for st == 0 {
		if st, base = v.block(); st == 0 {
			return leanInvalid
		}
	}
	i = base + bits.TrailingZeros64(st)
	c, st = s[i], st&(st-1)
	if c != ':' {
		return leanInvalid
	}
	goto value

after:
	for st == 0 {
		if st, base = v.block(); st == 0 {
			if v.bad || depth != 0 || v.inStr != 0 {
				return leanInvalid
			}
			return leanValid
		}
	}
	i = base + bits.TrailingZeros64(st)
	c, st = s[i], st&(st-1)
	if c != ',' {
		if depth == 0 || c != byte(']'+top<<5) { // '}' in an object
			return leanInvalid
		}
		goto pop
	}
comma:
	if depth == 0 {
		return leanInvalid
	}
	if top != 0 {
		goto key
	}
	goto value
pop:
	depth--
	top = 0
	if d := depth - 1; d >= 0 {
		if d&63 == 63 {
			cur = stack[d>>6]
		}
		top = cur >> uint(d&63) & 1
	}
	goto after
}
