package jsonsimd

import (
	"math/bits"
	"unsafe"
)

// leanMaxDepth is the deepest nesting validateLean holds on its stack.
// Deeper input goes to validateScalar.
const leanMaxDepth = 64 * 64

// leanScratch is the memory of leanLoop that it need not find zeroed: its
// block reader and its stack of open containers.
type leanScratch struct {
	v     leanBlocks
	run   leanState
	ents  [2 * validBatch]leanEnt
	stack [leanMaxDepth / 64]uint64
	// ends holds, for ValidateRestEnds, the ends of the first nends
	// values that close at its depth.
	ends  [restEnds]int32
	nends int
}

// restEnds is the number of value ends ValidateRestEnds records.
const restEnds = 16

// leanEnt is a block with structural bytes: their mask and its start.
type leanEnt struct {
	st   uint64
	base int
}

// leanState is where leanRun stopped, for the next blocks.
type leanState struct {
	at    int    // the label to go on at, one of the lean* labels below
	depth int    // the containers open
	cur   uint64 // as in leanRun
	one   bool   // as in leanLoop
	// mark is the depth at which leanRun stops, with one, or records the
	// end of each value it closes, or -1.
	mark int
}

// leanBlocks reads blocks for validateLean, without the records of vblocks.
type leanBlocks struct {
	s     string
	next  int // the start of the next block
	start int // the start of the n blocks in masks
	n     int
	bad   bool
	// As in vblocks.
	inStr, escNext, otherLast uint64
	// When spaced is set, wsOut has a bit set when a block had whitespace
	// outside strings. The ws mask of a block read holds only those bytes.
	spaced bool
	wsOut  uint64
	// skipped counts the bytes of long strings skipped with strSkip.
	skipped int
	// When grow is not 0, the next read is of at most grow blocks, and
	// reads double in size up to validBatch blocks.
	grow  int
	masks [validBatch]vmask
	tail  [64]byte
}

// fill reads the next blocks of s and puts those that have structural
// bytes in ents, in order, followed by an entry with no structural bytes,
// and returns how many. It returns 0 at the end of s. It stops before a
// block where a string is not valid, with v.bad set, and returns 0 from
// then on. When html is not nil, it looks for the characters HasHTML
// looks for.
func (v *leanBlocks) fill(ents *[2 * validBatch]leanEnt, html *htmlScan) int {
	s := v.s
	for {
		if v.spaced {
			v.addSpaces()
		}
		if v.next >= len(s) || v.bad {
			return 0
		}
		if v.inStr != 0 && v.n >= 2 && v.masks[v.n-1].quote|v.masks[v.n-1].bslash|v.masks[v.n-2].quote|v.masks[v.n-2].bslash == 0 && len(s)-v.next >= 64 {
			// The last two blocks were all inside a string, which is
			// long. Only a quote, a backslash or a control byte
			// matters: skip the blocks that hold none.
			from := v.next
			if html != nil {
				v.next = html.skip(s, v.next)
			} else {
				v.next, _ = strSkip(s, v.next)
			}
			v.skipped += v.next - from
			v.n = 0
			if v.next >= len(s) {
				return 0
			}
		}
		lim := len(s)
		if v.grow != 0 {
			lim = min(lim, v.next+64*v.grow)
			if v.grow *= 2; v.grow >= validBatch {
				v.grow = 0
			}
		}
		v.start = v.next
		v.n = validMasks(s[:lim], v.next, &v.masks, &v.tail, nil)
		k := 0
		for b := 0; b < v.n; b++ {
			m := &v.masks[b]
			base := v.next
			q, r := m.quote, m.r0
			if m.bslash|v.escNext != 0 {
				carried := v.escNext != 0
				var e uint64
				e, v.escNext = escapes(m.bslash, v.escNext)
				q &^= e
				r = prefixXor(q) ^ v.inStr
				if !validEscapes(s, base, e&r, m, carried) {
					v.bad = true
					ents[k].st = 0
					return k
				}
			} else {
				r ^= v.inStr
			}
			v.inStr = uint64(int64(r) >> 63)
			if m.ctrl&r != 0 {
				v.bad = true
				ents[k].st = 0
				return k
			}
			v.next += 64
			out := ^(r | q)
			ws := m.ws & out
			m.ws = ws
			other := out &^ (ws | m.op)
			st := m.op&out | q&r | other&^(other<<1|v.otherLast)
			v.otherLast = other >> 63
			if st != 0 {
				ents[k] = leanEnt{st, base}
				k++
			}
		}
		if k != 0 {
			ents[k].st = 0
			return k
		}
	}
}

// addSpaces adds the whitespace outside strings of the blocks read to
// v.wsOut.
func (v *leanBlocks) addSpaces() {
	if v.n == 0 {
		return
	}
	var ws uint64
	for i := 0; i < v.n-1; i++ {
		ws |= v.masks[i].ws
	}
	last := v.masks[v.n-1].ws
	if k := len(v.s) - (v.next - 64); k < 64 {
		// The tail block is padded with spaces.
		last &= 1<<uint(k) - 1
	}
	v.wsOut |= ws | last
}

// spacedTo reports whether the blocks read hold whitespace outside strings
// before end, which is after the start of the last blocks read.
func (v *leanBlocks) spacedTo(end int) bool {
	ws := v.wsOut
	kk := (end - 1 - v.start) >> 6
	for k := 0; k < kk; k++ {
		ws |= v.masks[k].ws
	}
	last := v.masks[kk].ws
	if n := end - (v.start + 64*kk); n < 64 {
		last &= 1<<uint(n) - 1
	}
	return ws|last != 0
}

// validateLean is Validate without tables on 64-byte blocks, which feeds
// html when it is not nil. The structural bytes that
// leanBlocks gives drive the grammar of validateScalar, with one label for
// each thing the next of them may be, and the other values are checked as
// numbers or literals that end where a delimiter follows. The loop calls
// out only to read a block, so that its variables stay in registers.
func validateLean(s string, maxDepth int, spaced *bool, html *htmlScan) bool {
	if maxDepth > 0 && maxDepth < leanMaxDepth {
		return validateScalar(s, maxDepth, nil, nil, spaced)
	}
	var sc leanScratch
	r := leanLoop(s, spaced != nil, false, nil, html, nil, &sc)
	if r == leanDeep {
		return validateScalar(s, maxDepth, nil, nil, spaced)
	}
	if spaced != nil {
		*spaced = r == leanSpaced
	}
	return r >= leanValid
}

// Results of leanLoop.
const (
	leanInvalid = iota
	leanDeep    // nested deeper than its stack
	leanValid
	leanSpaced // valid, with whitespace outside strings
	leanEnd    // with one, plus the end of the value
)

// leanInfo receives from leanLoop, with one, whether the value holds
// whitespace outside its strings and the bytes of long strings skipped.
type leanInfo struct {
	spaced bool
	long   int
}

// leanOpen describes the containers open before s, for ValidateRest.
type leanOpen struct {
	kinds []uint64 // bit k set when the container at depth k is an object
	depth int      // how many are open, at most leanMaxDepth-1
	ends  bool     // record the ends of the values that close at depth
}

// leanLoop is validateLean without its depth limit. It tells leanValid from
// leanSpaced only when spaced is set. With one, s starts with an array or
// object, and leanLoop stops at its end, reading blocks a few at a time at
// first. With open, s continues inside the containers it lists, where a
// value must follow. When html is not nil, leanLoop feeds it, and when info
// is not nil, with one and spaced, leanLoop fills it. It works in sc. It
// reads blocks a batch at a time and leaves the grammar to leanRun, which
// makes no calls, so that its variables stay in registers.
func leanLoop(s string, spaced, one bool, open *leanOpen, html *htmlScan, info *leanInfo, sc *leanScratch) int {
	v := &sc.v
	v.s, v.next, v.start, v.n, v.bad = s, 0, 0, 0, false
	v.inStr, v.escNext, v.otherLast = 0, 0, 0
	v.spaced, v.wsOut, v.skipped, v.grow = spaced, 0, 0, 0
	if one {
		v.grow = 1
	}
	r := &sc.run
	r.at, r.depth, r.cur, r.one, r.mark = leanValue, 0, 0, one, -1
	if one {
		r.mark = 0
	}
	if open != nil {
		r.depth = open.depth
		inner := (r.depth - 1) >> 6
		copy(sc.stack[:inner], open.kinds)
		r.cur = open.kinds[inner]
		if open.ends {
			r.mark, sc.nends = open.depth, 0
		}
	}
	for {
		n := v.fill(&sc.ents, html)
		if n == 0 {
			// The end of s, or a string that is not valid.
			if r.at != leanAfter || v.bad || r.depth != 0 || v.inStr != 0 {
				return leanInvalid
			}
			if v.wsOut != 0 {
				return leanSpaced
			}
			return leanValid
		}
		if res := leanRun(sc); res != leanMore {
			if res >= leanEnd && info != nil {
				info.spaced, info.long = v.spacedTo(res-leanEnd), v.skipped
			}
			return res
		}
	}
}

// leanMore is the result of leanRun when it needs the next blocks.
const leanMore = -1

// The labels of leanRun to go on at.
const (
	leanAfter = iota
	leanValue
	leanValueOrEnd
	leanKeyOrEnd
	leanKey
	leanColon
	leanComma
)

// leanRun runs the grammar of leanLoop on the blocks of sc.ents, from
// where sc.run says, and returns a result of leanLoop, or leanMore, with
// sc.run updated, when it reaches the end of the blocks. It keeps few
// variables, so that they fit in registers: c is read from s[i] without a
// bounds check, as i is always the index of a structural byte of s.
func leanRun(sc *leanScratch) int {
	p := unsafe.Pointer(unsafe.StringData(sc.v.s))
	// stack holds one bit per open container: 1 for object, 0 for array,
	// 64 to a word. cur is the word of the innermost one, which is not
	// stored while it is open, and top is its bit. A word is read only
	// after it is stored.
	depth, cur := sc.run.depth, sc.run.cur
	var top uint64
	if depth > 0 {
		top = cur >> uint((depth-1)&63) & 1
	}
	// st holds the structural bytes not yet read of the block at base,
	// which is sc.ents[k].
	k := 0
	st, base := sc.ents[0].st, sc.ents[0].base
	var s string
	var x uint64
	var i int
	var c byte
	switch sc.run.at {
	case leanValue:
		goto value
	case leanValueOrEnd:
		goto valueOrEnd
	case leanKeyOrEnd:
		goto keyOrEnd
	case leanKey:
		goto key
	case leanColon:
		goto colon
	case leanComma:
		goto comma
	}
	goto after

	// Each label first reads the next structural byte into c at s[i]. st
	// is not 0 there; its high bit set tells the compiler so.
value:
	if st == 0 {
		k++
		if st, base = sc.ents[k&(2*validBatch-1)].st, sc.ents[k&(2*validBatch-1)].base; st == 0 {
			sc.run.at = leanValue
			goto more
		}
	}
	i = base + bits.TrailingZeros64(st|1<<63)
	c, st = *(*byte)(unsafe.Add(p, i)), st&(st-1)
valueAt:
	switch c {
	case '{', '[':
		if depth == leanMaxDepth {
			return leanDeep
		}
		top = uint64(c>>5) & 1 // 1 for '{'
		b := uint(depth & 63)
		if b == 0 && depth > 0 {
			sc.stack[depth>>6-1] = cur
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
	s = sc.v.s
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
	if byte(x) != ',' {
		if !validDelim[byte(x)] {
			return leanInvalid
		}
		goto after
	}
	// The comma is the next structural byte: take it here.
	if st == 0 {
		k++
		if st, base = sc.ents[k&(2*validBatch-1)].st, sc.ents[k&(2*validBatch-1)].base; st == 0 {
			sc.run.at = leanComma
			goto more
		}
	}
comma:
	st &= st - 1
	goto afterComma

valueOrEnd:
	if st == 0 {
		k++
		if st, base = sc.ents[k&(2*validBatch-1)].st, sc.ents[k&(2*validBatch-1)].base; st == 0 {
			sc.run.at = leanValueOrEnd
			goto more
		}
	}
	i = base + bits.TrailingZeros64(st|1<<63)
	c, st = *(*byte)(unsafe.Add(p, i)), st&(st-1)
	if c != ']' {
		goto valueAt
	}
	goto pop

keyOrEnd:
	if st == 0 {
		k++
		if st, base = sc.ents[k&(2*validBatch-1)].st, sc.ents[k&(2*validBatch-1)].base; st == 0 {
			sc.run.at = leanKeyOrEnd
			goto more
		}
	}
	i = base + bits.TrailingZeros64(st|1<<63)
	c, st = *(*byte)(unsafe.Add(p, i)), st&(st-1)
	if c == '"' {
		// Most often the colon is next in this block: take it here.
		if st != 0 && *(*byte)(unsafe.Add(p, base+bits.TrailingZeros64(st|1<<63))) == ':' {
			st &= st - 1
			goto value
		}
		goto colon
	}
	if c == '}' {
		goto pop
	}
	goto keyAt

key:
	if st == 0 {
		k++
		if st, base = sc.ents[k&(2*validBatch-1)].st, sc.ents[k&(2*validBatch-1)].base; st == 0 {
			sc.run.at = leanKey
			goto more
		}
	}
	i = base + bits.TrailingZeros64(st|1<<63)
	c, st = *(*byte)(unsafe.Add(p, i)), st&(st-1)
keyAt:
	if c != '"' {
		return leanInvalid
	}
colon:
	if st == 0 {
		k++
		if st, base = sc.ents[k&(2*validBatch-1)].st, sc.ents[k&(2*validBatch-1)].base; st == 0 {
			sc.run.at = leanColon
			goto more
		}
	}
	i = base + bits.TrailingZeros64(st|1<<63)
	c, st = *(*byte)(unsafe.Add(p, i)), st&(st-1)
	if c != ':' {
		return leanInvalid
	}
	goto value

after:
	if st == 0 {
		k++
		if st, base = sc.ents[k&(2*validBatch-1)].st, sc.ents[k&(2*validBatch-1)].base; st == 0 {
			sc.run.at = leanAfter
			goto more
		}
	}
	i = base + bits.TrailingZeros64(st|1<<63)
	c, st = *(*byte)(unsafe.Add(p, i)), st&(st-1)
	if c != ',' {
		if depth == 0 || c != byte(']'+top<<5) { // '}' in an object
			return leanInvalid
		}
		goto pop
	}
afterComma:
	if top != 0 {
		// Most often the name and the colon of the member are next in
		// this block: take them here.
		if st2 := st & (st - 1); st2 != 0 &&
			(uint(*(*byte)(unsafe.Add(p, base+bits.TrailingZeros64(st|1<<63))))^'"')|
				(uint(*(*byte)(unsafe.Add(p, base+bits.TrailingZeros64(st2|1<<63))))^':') == 0 {
			st = st2 & (st2 - 1)
			goto value
		}
		goto key
	}
	// top is 1 in an object only.
	if depth == 0 {
		return leanInvalid
	}
	goto value
pop:
	depth--
	if depth == sc.run.mark {
		if sc.run.one {
			return leanEnd + i + 1
		}
		if n := sc.nends; n < restEnds {
			sc.ends[n] = int32(i + 1)
			sc.nends = n + 1
		}
	}
	top = 0
	if d := depth - 1; d >= 0 {
		if d&63 == 63 {
			cur = sc.stack[d>>6]
		}
		top = cur >> uint(d&63) & 1
	}
	goto after
more:
	sc.run.depth, sc.run.cur = depth, cur
	return leanMore
}
