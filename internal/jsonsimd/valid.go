package jsonsimd

import (
	"math"
	"math/bits"
	"unsafe"
)

// LongString is the shortest string content, in bytes between the quotes,
// that Validate records in Tables.Spans.
const LongString = 256

// TrackDepth is the number of nesting levels whose containers Validate
// records. Containers nested deeper are validated but not recorded.
const TrackDepth = 64

// A Container is the position of an array or object in the input.
type Container struct {
	Off uint32 // index of the opening bracket
	End uint32 // index of the closing bracket
	N   uint32 // number of elements or members
	// Next is the index in Tables.Containers of the first container
	// after this one's descendants.
	Next uint32
}

// A Span is the position of a string of at least LongString bytes.
type Span struct {
	Start uint32 // index of the opening quote
	End   uint32 // index of the closing quote
	Flags uint32 // SpanEscaped, SpanHigh
}

// Span flags.
const (
	SpanEscaped = 1 << iota // the string holds a backslash
	SpanHigh                // the string holds a byte of 0x80 or above
)

// Tables are the positions a validation records, in the order of their
// first byte, so a reader moving forward through the input finds each one
// with a forward cursor. They are complete only up to the limits described
// at Container and Span; a reader must handle values that have no entry.
type Tables struct {
	Containers []Container
	Spans      []Span

	// Scratch space of the validators, kept here so that it is not zeroed
	// on each call.
	frames [TrackDepth + 1]frame
	vb     vblocks
	lean   leanScratch
}

// maxContainers bounds the container entries recorded for n input bytes.
func maxContainers(n int) int { return n/4 + 64 }

// Validate reports whether s is one JSON value (RFC 8259) surrounded only
// by whitespace, with at most maxDepth nested arrays and objects, or any
// depth when maxDepth <= 0. Bytes of 0x80 and above are accepted in strings
// as they are. With t not nil, Validate also fills t; t is complete only
// when Validate returns true.
func Validate(s string, maxDepth int, t *Tables) bool {
	t = resetTables(t, len(s))
	if HaveAVX2 && len(s) >= 64 {
		if t == nil {
			if maxDepth <= 0 || maxDepth >= leanMaxDepth {
				if r := validatePlain(s); r != leanDeep {
					return r == leanValid
				}
			}
			return validateScalar(s, maxDepth, nil, nil, nil)
		}
		return validateBlocks(s, maxDepth, t, nil, nil)
	}
	if plainMasks && t == nil && len(s) >= 64 && (maxDepth <= 0 || maxDepth >= leanMaxDepth) {
		if r := validatePlain(s); r != leanDeep {
			return r == leanValid
		}
	}
	return validateScalar(s, maxDepth, t, nil, nil)
}

// ValidateSpaced is Validate without tables that also reports whether s
// holds whitespace outside its strings, which compacting would remove,
// and, with html, whether valid s holds a character HasHTML looks for.
// Its long strings are read once for both.
func ValidateSpaced(s string, maxDepth int, html bool) (ok, spaced, hasHTML bool) {
	if !HaveAVX2 || len(s) < 64 {
		ok = validateScalar(s, maxDepth, nil, nil, &spaced)
		return ok, spaced, ok && html && HasHTML(s)
	}
	if !html {
		ok = validateLean(s, maxDepth, &spaced, nil)
		return ok, spaced, false
	}
	var h htmlScan
	ok = validateLean(s, maxDepth, &spaced, &h)
	return ok, spaced, ok && h.in(s)
}

// ValidateRest reports whether s validly continues a document after a
// prefix that leaves depth containers open and ends where a value must
// follow: a value, the ends of the open containers, with their members or
// elements, and then only whitespace. Bit k of kinds, 64 to a word, is set
// when the container open at depth k, 0 the outermost, is an object. The
// nesting, the open containers included, may not exceed maxDepth when it
// is positive. ValidateRest uses the scratch space of t when t is not nil,
// and leaves its tables alone.
func ValidateRest(s string, kinds []uint64, depth, maxDepth int, t *Tables) bool {
	if depth == 0 {
		return Validate(s, maxDepth, nil)
	}
	if HaveAVX2 && len(s) >= 64 && depth < leanMaxDepth && (maxDepth <= 0 || maxDepth >= leanMaxDepth) {
		if r := leanLoopIn(t, s, false, false, &leanOpen{kinds: kinds, depth: depth}, nil); r != leanDeep {
			return r >= leanValid
		}
	}
	// Validate s after a prefix that opens the same containers.
	b := make([]byte, 0, 4*depth+len(s))
	for k := 0; k < depth; k++ {
		if kinds[k>>6]>>(k&63)&1 != 0 {
			b = append(b, `{"":`...)
		} else {
			b = append(b, "[0,"...)
		}
	}
	b = append(b, s...)
	return Validate(unsafe.String(unsafe.SliceData(b), len(b)), maxDepth, nil)
}

// ValidateRestEnds is ValidateRest that also returns the ends in s of the
// first values that close at depth, the arrays and objects that are
// members or elements of the innermost open container and of those that
// open later at the same depth, in order, and whether those are all of
// them. It uses t, which must not be nil, and leaves its tables alone. The
// ends are valid until t is used again.
func ValidateRestEnds(s string, kinds []uint64, depth, maxDepth int, t *Tables) (ok bool, ends []int32, all bool) {
	if depth > 0 && HaveAVX2 && len(s) >= 64 && depth < leanMaxDepth && (maxDepth <= 0 || maxDepth >= leanMaxDepth) {
		if r := leanLoopIn(t, s, false, false, &leanOpen{kinds, depth, true}, nil); r != leanDeep {
			sc := &t.lean
			return r >= leanValid, sc.ends[:sc.nends], sc.nends < restEnds
		}
	}
	return ValidateRest(s, kinds, depth, maxDepth, t), nil, false
}

// RestEnds returns the ends that the last ValidateRestEnds with t
// returned, while they are valid.
func (t *Tables) RestEnds() []int32 { return t.lean.ends[:t.lean.nends] }

// Release drops what t refers to of the last input it read, so that
// keeping t for later calls does not keep that input alive. The tables
// and the scratch space stay for the next call.
func (t *Tables) Release() {
	t.vb.s, t.vb.spans = "", nil
	t.lean.v.s = ""
}

// ValidateValue validates the array or object at the start of s and
// returns its end, or PrefixInvalid when it is not valid or does not end in
// s. It may also return PrefixInvalid for a valid value when a string that
// starts after it in the same 64 bytes is not valid. It uses the scratch
// space of t when t is not nil, and leaves its tables alone.
func ValidateValue(s string, maxDepth int, t *Tables) int {
	if HaveAVX2 && len(s) >= 64 && (maxDepth <= 0 || maxDepth >= leanMaxDepth) {
		r := leanLoopIn(t, s, false, true, nil, nil)
		if r >= leanEnd {
			return r - leanEnd
		}
		if r != leanDeep {
			return PrefixInvalid
		}
	}
	end := PrefixInvalid
	validateScalar(s, maxDepth, nil, &end, nil)
	return max(end, PrefixInvalid)
}

// ValidateValueInfo is ValidateValue that also reports whether the value
// may hold whitespace outside its strings, which compacting would remove,
// and how many bytes of its long strings it passed over without reading
// them byte by byte.
func ValidateValueInfo(s string, maxDepth int, t *Tables) (end int, spaced bool, long int) {
	if HaveAVX2 && len(s) >= 64 && (maxDepth <= 0 || maxDepth >= leanMaxDepth) {
		var info leanInfo
		r := leanLoopIn(t, s, true, true, nil, &info)
		if r >= leanEnd {
			return r - leanEnd, info.spaced, info.long
		}
		if r != leanDeep {
			return PrefixInvalid, false, 0
		}
	}
	return ValidateValue(s, maxDepth, t), true, 0
}

// leanLoopIn is leanLoop in the scratch space of t, or in new scratch
// space when t is nil.
func leanLoopIn(t *Tables, s string, spaced, one bool, open *leanOpen, info *leanInfo) int {
	if t == nil {
		var sc leanScratch
		return leanLoop(s, spaced, one, open, nil, info, &sc)
	}
	return leanLoop(s, spaced, one, open, nil, info, &t.lean)
}

// Results of ValidatePrefix besides an end.
const (
	PrefixInvalid    = -1 // s does not start with a valid value
	PrefixIncomplete = -2 // s is valid so far but ends inside the value
)

// ValidatePrefix validates the array or object at the start of s, after
// optional whitespace, and returns its end when it is complete and valid,
// with t filled as Validate fills it for s[:end]. Otherwise it returns
// PrefixIncomplete when s ends inside a value that is valid so far, or
// PrefixInvalid. What follows the value does not matter, except that a
// malformed string after it in the same 512 bytes gives PrefixInvalid,
// and a number, literal or escape cut by the end of s may too.
func ValidatePrefix(s string, maxDepth int, t *Tables) int {
	t = resetTables(t, len(s))
	end := PrefixInvalid
	if i := skipSpace(s, 0); i == len(s) || s[i] != '{' && s[i] != '[' {
		return PrefixInvalid
	}
	if HaveAVX2 && len(s) >= 64 {
		validateBlocks(s, maxDepth, t, &end, nil)
	} else {
		validateScalar(s, maxDepth, t, &end, nil)
	}
	return end
}

// resetTables empties t, and returns nil when s is too long for it.
func resetTables(t *Tables, n int) *Tables {
	if t != nil {
		t.Containers = t.Containers[:0]
		t.Spans = t.Spans[:0]
		if uint64(n) >= math.MaxUint32 {
			t = nil
		}
	}
	return t
}

// validBatch is the number of blocks whose masks validateBlocks reads at a
// time.
const validBatch = 8

// vmask holds bit masks of a 64-byte block, bit i for byte i: quotes,
// backslashes, whitespace, the bytes of {}[]:, , control bytes, and, only
// when the block has a backslash, the bytes that may follow one (one of
// "\/bfnrt) and 'u'. r0 is the prefix XOR of quote, which marks the bytes
// inside strings when no quote is escaped.
type vmask struct {
	quote, bslash, ws, op, ctrl, esc, u, r0 uint64
}

// vblocks is the first stage of validateBlocks. It reads the masks of s a
// batch of blocks at a time and gives, block by block, the structural
// bytes outside strings: brackets, colons, commas, opening quotes and the
// first bytes of other values. The bytes inside strings must hold no
// control byte and only valid escapes.
type vblocks struct {
	s    string
	next int // the start of the next block
	k, n int // the next of the n blocks in masks
	bad  bool
	// At the end of the last block read: all ones inside a string, 1 when
	// the next byte is escaped, and 1 when the last byte was part of a value
	// other than a string or container.
	inStr, escNext, otherLast uint64
	// wsOut has a bit set when a block had whitespace outside strings.
	wsOut uint64
	// spans, when not nil, receives the long strings; strStart and strFlags
	// describe the string open at the end of the last block.
	spans    *[]Span
	strStart int
	strFlags uint32
	masks    [validBatch]vmask
	his      [validBatch]uint64 // the bytes of 0x80 and up in each block
	tail     [64]byte
}

// reset prepares v to read s, leaving its buffers as they are.
func (v *vblocks) reset(s string) {
	v.s, v.next, v.k, v.n, v.bad = s, 0, 0, 0, false
	v.inStr, v.escNext, v.otherLast, v.wsOut = 0, 0, 0, 0
	v.spans, v.strStart, v.strFlags = nil, 0, 0
}

// block returns the structural bytes of the next block that has any, as a
// mask, and the start of the block. At the end of s, or when a string is
// not valid and v.bad is set, the mask is 0.
func (v *vblocks) block() (uint64, int) {
	s := v.s
	for {
		if v.k == v.n {
			if v.next >= len(s) {
				return 0, 0
			}
			if v.inStr != 0 && v.escNext == 0 && len(s)-v.next >= 64 {
				// Inside a string, only a quote, a backslash or a control
				// byte matters: skip the blocks that hold none.
				p, hi := strSkip(s, v.next)
				if hi {
					v.strFlags |= SpanHigh
				}
				v.next = p
				if p >= len(s) {
					return 0, 0
				}
			}
			v.k, v.n = 0, validMasks(s, v.next, &v.masks, &v.tail, &v.his)
		}
		m := &v.masks[v.k]
		base := v.next
		v.k, v.next = v.k+1, v.next+64
		inPrev := v.inStr
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
		if v.spans != nil && inPrev|v.inStr != 0 {
			v.track(base, q, r, inPrev, m.bslash, v.his[v.k-1])
		}
		out := ^(r | q)
		ws := m.ws & out
		if v.next > len(s) {
			// The tail block is padded with spaces.
			ws &= 1<<uint(len(s)-base) - 1
		}
		v.wsOut |= ws
		other := out &^ (m.ws | m.op)
		st := m.op&out | q&r | other&^(other<<1|v.otherLast)
		v.otherLast = other >> 63
		if st != 0 {
			return st, base
		}
	}
}

// track records the long strings of the block at base, which starts inside
// a string when inPrev is set or ends inside one when v.inStr is. q holds
// its unescaped quotes, r its bytes inside strings, opening quotes
// included, bslash its backslashes and hi its bytes of 0x80 and up.
func (v *vblocks) track(base int, q, r, inPrev, bslash, hi uint64) {
	if inPrev != 0 {
		if c := q &^ r; c == 0 {
			v.strFlags |= spanFlags(bslash, hi)
		} else {
			e := uint(bits.TrailingZeros64(c))
			low := uint64(1)<<e - 1
			f := v.strFlags | spanFlags(bslash&low, hi&low)
			if end := base + int(e); end-v.strStart > LongString {
				*v.spans = append(*v.spans, Span{uint32(v.strStart), uint32(end), f})
			}
		}
	}
	if v.inStr != 0 {
		if o := q & r; o != 0 {
			b := uint(63 - bits.LeadingZeros64(o))
			v.strStart = base + int(b)
			high := ^uint64(0) << b
			v.strFlags = spanFlags(bslash&high, hi&high)
		}
	}
}

func spanFlags(bslash, hi uint64) uint32 {
	var f uint32
	if bslash != 0 {
		f = SpanEscaped
	}
	if hi != 0 {
		f |= SpanHigh
	}
	return f
}

// blockMaxDepth is the deepest nesting validateBlocks holds on its stack.
// Deeper input, allowed only without a depth limit, goes to validateScalar.
const blockMaxDepth = 160 * 64

// frame saves the container entry and element count of an open container
// while a nested one is read.
type frame struct {
	entry int32
	n     uint32
}

// validateBlocks is Validate on 64-byte blocks. The structural bytes that
// vblocks gives drive the grammar, with one label for each thing the next
// of them may be, and the other values are checked as numbers or literals
// that end where a delimiter follows. The loop calls out only to read a
// block, so that its variables stay in registers.
//
// With end not nil, it stops at the end of the first top-level value,
// which must be an array or object, and sets *end. With spaced not nil, it
// reports there whether s holds whitespace outside strings.
func validateBlocks(s string, maxDepth int, t *Tables, end *int, spaced *bool) bool {
	var v *vblocks
	var frames *[TrackDepth + 1]frame
	if t != nil {
		v, frames = &t.vb, &t.frames
		v.reset(s)
	} else {
		v = &vblocks{s: s}
	}
	limit := maxDepth
	if limit <= 0 || limit > blockMaxDepth {
		limit = blockMaxDepth
	}
	// stack holds one bit per open container: 1 for object, 0 for array,
	// 64 to a word. cur is the word of the innermost one, which is not
	// stored while it is open, and top is its bit.
	var small [4]uint64
	stack := small[:]
	// st holds the structural bytes not yet read of the block at base.
	var st, cur, top, x uint64
	var depth, base, i int
	var c byte
	ok := false
	// With t: the container entries, the entry of the innermost open
	// container (-1 when not recorded) and its count of commas.
	var cont []Container
	entry, maxCont := -1, 0
	var n uint32
	if t != nil {
		cont, maxCont = t.Containers, maxContainers(len(s))
		v.spans = &t.Spans
	}

	// Each label first reads the next structural byte into c at s[i].
value:
	for st == 0 {
		if st, base = v.block(); st == 0 {
			goto eof
		}
	}
	i = base + bits.TrailingZeros64(st)
	c, st = s[i], st&(st-1)
valueAt:
	switch c {
	case '{', '[':
		if depth == limit {
			if limit == maxDepth {
				goto done
			}
			if t != nil {
				t.Containers = cont
			}
			return validateScalar(s, maxDepth, t, end, spaced)
		}
		top = uint64(c>>5) & 1 // 1 for '{'
		b := uint(depth & 63)
		if b == 0 && depth > 0 {
			if depth>>6 > len(stack) {
				stack = append(stack, make([]uint64, len(stack))...)
			}
			stack[depth>>6-1] = cur
		}
		cur = cur&^(1<<b) | top<<b
		if t != nil {
			if depth <= TrackDepth {
				frames[depth] = frame{int32(entry), n}
			}
			entry = -1
			if depth < TrackDepth && len(cont) < maxCont {
				entry = len(cont)
				cont = append(cont, Container{Off: uint32(i)})
			}
			n = 0
		}
		depth++
		if top != 0 {
			goto keyOrEnd
		}
		goto valueOrEnd
	case '"':
		goto after
	case ':', ',', '}', ']':
		goto done
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
		nd := nonDigits(x)
		if nd&0x80 != 0 || byte(x) == '0' && nd&0x8000 == 0 {
			goto done
		}
		for nd == 0 {
			j += 8
			x = vload(s, j)
			nd = nonDigits(x)
		}
		// The lowest bit of nd is bit 7 of the first byte after the digits.
		k := bits.TrailingZeros64(nd)
		j, x = j+k>>3, x>>(uint(k)&56)
		if byte(x) == '.' {
			j++
			x = vload(s, j)
			if nd = nonDigits(x); nd&0x80 != 0 {
				goto done
			}
			for nd == 0 {
				j += 8
				x = vload(s, j)
				nd = nonDigits(x)
			}
			k = bits.TrailingZeros64(nd)
			j, x = j+k>>3, x>>(uint(k)&56)
		}
		if byte(x)|0x20 == 'e' {
			j++
			if x = vload(s, j); byte(x) == '+' || byte(x) == '-' {
				j++
				x = vload(s, j)
			}
			if nd = nonDigits(x); nd&0x80 != 0 {
				goto done
			}
			for nd == 0 {
				j += 8
				x = vload(s, j)
				nd = nonDigits(x)
			}
			x >>= uint(bits.TrailingZeros64(nd)) & 56
		}
	case c == 't':
		if uint32(x) != 't'|'r'<<8|'u'<<16|'e'<<24 {
			goto done
		}
		x >>= 32
	case c == 'f':
		if x&(1<<40-1) != 'f'|'a'<<8|'l'<<16|'s'<<24|'e'<<32 {
			goto done
		}
		x >>= 40
	case c == 'n':
		if uint32(x) != 'n'|'u'<<8|'l'<<16|'l'<<24 {
			goto done
		}
		x >>= 32
	default:
		goto done
	}
	if byte(x) == ',' {
		// The comma is the next structural byte: take it here.
		for st == 0 {
			if st, base = v.block(); st == 0 {
				goto done
			}
		}
		st &= st - 1
		goto comma
	}
	if !validDelim[byte(x)] {
		goto done
	}
	goto after

valueOrEnd:
	for st == 0 {
		if st, base = v.block(); st == 0 {
			goto eof
		}
	}
	i = base + bits.TrailingZeros64(st)
	c, st = s[i], st&(st-1)
	if c != ']' {
		goto valueAt
	}
	n = ^uint32(0)
	goto pop

keyOrEnd:
	for st == 0 {
		if st, base = v.block(); st == 0 {
			goto eof
		}
	}
	i = base + bits.TrailingZeros64(st)
	c, st = s[i], st&(st-1)
	if c == '}' {
		n = ^uint32(0)
		goto pop
	}
	goto keyAt

key:
	for st == 0 {
		if st, base = v.block(); st == 0 {
			goto eof
		}
	}
	i = base + bits.TrailingZeros64(st)
	c, st = s[i], st&(st-1)
keyAt:
	if c != '"' {
		goto done
	}
	for st == 0 {
		if st, base = v.block(); st == 0 {
			goto eof
		}
	}
	i = base + bits.TrailingZeros64(st)
	c, st = s[i], st&(st-1)
	if c != ':' {
		goto done
	}
	goto value

after:
	for st == 0 {
		if st, base = v.block(); st == 0 {
			if end != nil {
				goto eof
			}
			ok = !v.bad && depth == 0 && v.inStr == 0
			goto done
		}
	}
	i = base + bits.TrailingZeros64(st)
	c, st = s[i], st&(st-1)
	if c != ',' {
		if depth == 0 || c != byte(']'+top<<5) { // '}' in an object
			goto done
		}
		goto pop
	}
comma:
	if depth == 0 {
		goto done
	}
	n++
	if top != 0 {
		goto key
	}
	goto value
pop:
	depth--
	if t != nil {
		if entry >= 0 {
			cont[entry].End, cont[entry].N, cont[entry].Next = uint32(i), n+1, uint32(len(cont))
		}
		entry = -1
		if depth <= TrackDepth {
			entry, n = int(frames[depth].entry), frames[depth].n
		}
	}
	top = 0
	if d := depth - 1; d >= 0 {
		if d&63 == 63 {
			cur = stack[d>>6]
		}
		top = cur >> uint(d&63) & 1
	} else if end != nil {
		*end, ok = i+1, true
		goto done
	}
	goto after

eof:
	// The input ended, or a string in the last block read is not valid.
	if end != nil && !v.bad {
		*end = PrefixIncomplete
	}
done:
	if t != nil {
		t.Containers = cont
	}
	if spaced != nil {
		*spaced = v.wsOut != 0
	}
	return ok
}

// validScalar reports whether the number or literal at s[i] is valid and
// followed by a delimiter or the end of s.
func validScalar(s string, i int) bool {
	c := s[i]
	x := vload(s, i)
	switch {
	case c-'0' < 10 || c == '-':
		j := i
		if c == '-' {
			j++
			x = vload(s, j)
		}
		// The integer part: one digit, or digits that do not start with 0.
		nd := nonDigits(x)
		if nd&0x80 != 0 || byte(x) == '0' && nd&0x8000 == 0 {
			return false
		}
		for nd == 0 {
			j += 8
			x = vload(s, j)
			nd = nonDigits(x)
		}
		k := bits.TrailingZeros64(nd)
		j, x = j+k>>3, x>>(uint(k)&56)
		if byte(x) == '.' {
			j++
			x = vload(s, j)
			if nd = nonDigits(x); nd&0x80 != 0 {
				return false
			}
			for nd == 0 {
				j += 8
				x = vload(s, j)
				nd = nonDigits(x)
			}
			k = bits.TrailingZeros64(nd)
			j, x = j+k>>3, x>>(uint(k)&56)
		}
		if byte(x)|0x20 == 'e' {
			j++
			if x = vload(s, j); byte(x) == '+' || byte(x) == '-' {
				j++
				x = vload(s, j)
			}
			if nd = nonDigits(x); nd&0x80 != 0 {
				return false
			}
			for nd == 0 {
				j += 8
				x = vload(s, j)
				nd = nonDigits(x)
			}
			x >>= uint(bits.TrailingZeros64(nd)) & 56
		}
	case c == 't':
		if uint32(x) != 't'|'r'<<8|'u'<<16|'e'<<24 {
			return false
		}
		x >>= 32
	case c == 'f':
		if x&(1<<40-1) != 'f'|'a'<<8|'l'<<16|'s'<<24|'e'<<32 {
			return false
		}
		x >>= 40
	case c == 'n':
		if uint32(x) != 'n'|'u'<<8|'l'<<16|'l'<<24 {
			return false
		}
		x >>= 32
	default:
		return false
	}
	return validDelim[byte(x)]
}

// vload is Load64 for any i <= len(s), with spaces for the bytes past the
// end of s.
func vload(s string, i int) uint64 {
	if i+8 <= len(s) {
		return Load64(s, i)
	}
	return vloadTail(s, i)
}

func vloadTail(s string, i int) uint64 {
	x := uint64(0x2020202020202020)
	for k := len(s) - 1; k >= i; k-- {
		x = x<<8 | uint64(s[k])
	}
	return x
}

// nonDigits returns the high bits of the bytes of x that are not digits,
// surely for the lowest such byte: below it, the subtraction does not
// borrow and the addition does not carry. The bits above it may be wrong.
func nonDigits(x uint64) uint64 {
	return ((x - 0x3030303030303030) | (x + 0x4646464646464646)) & 0x8080808080808080
}

// escapes returns the bytes of a block that a backslash escapes, from its
// backslashes b and whether its first byte is escaped (first is 0 or 1),
// and whether the first byte of the next block is. Of a run of
// backslashes, every other one escapes the byte after it.
func escapes(b, first uint64) (escaped, next uint64) {
	const odd = 0xaaaaaaaaaaaaaaaa
	may := b &^ first
	// 1 at each escaping backslash and each escaped byte: subtraction
	// marks the runs that start on an even byte, the XORs with odd fix
	// those that start on an odd one.
	code := (may<<1 | odd) - may ^ odd
	return code ^ (b | first), (code & b) >> 63
}

// prefixXor sets bit i of the result to the parity of bits 0 to i of x.
func prefixXor(x uint64) uint64 {
	x ^= x << 1
	x ^= x << 2
	x ^= x << 4
	x ^= x << 8
	x ^= x << 16
	return x ^ x<<32
}

// validEscapes checks the escaped bytes ev, inside strings, of the block at
// s[base:]. When carried, the first byte is escaped by the last backslash
// of the block before, and m may not mark it.
func validEscapes(s string, base int, ev uint64, m *vmask, carried bool) bool {
	if carried && ev&1 != 0 {
		if !validEscape(s, base) {
			return false
		}
		ev &^= 1
	}
	for bad := ev &^ m.esc; bad != 0; bad &= bad - 1 {
		p := bits.TrailingZeros64(bad)
		if m.u>>uint(p)&1 == 0 {
			return false
		}
		if !hex4(s, base+p+1) {
			return false
		}
	}
	return true
}

// validEscape reports whether s[i] may follow a backslash.
func validEscape(s string, i int) bool {
	if i >= len(s) {
		return false
	}
	switch s[i] {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return true
	case 'u':
		return hex4(s, i+1)
	}
	return false
}

// hex4 reports whether s[i:i+4] are four hexadecimal digits.
func hex4(s string, i int) bool {
	if i+4 > len(s) {
		return false
	}
	for _, c := range []byte(s[i : i+4]) {
		if hexVal[c] < 0 {
			return false
		}
	}
	return true
}

// hexVal maps hexadecimal digits to their value and other bytes to -1.
var hexVal = func() (t [256]int8) {
	for i := range t {
		t[i] = -1
	}
	for c := '0'; c <= '9'; c++ {
		t[c] = int8(c - '0')
	}
	for c := 'a'; c <= 'f'; c++ {
		t[c] = int8(c - 'a' + 10)
		t[c-'a'+'A'] = int8(c - 'a' + 10)
	}
	return t
}()

// validDelim marks the bytes that may follow a number or literal.
var validDelim = [256]bool{' ': true, '\t': true, '\n': true, '\r': true, ',': true, ':': true,
	'{': true, '}': true, '[': true, ']': true, '"': true}

// isSpace marks JSON whitespace.
var isSpace = [256]bool{' ': true, '\t': true, '\n': true, '\r': true}

func skipSpace(s string, i int) int {
	for i < len(s) && isSpace[s[i]] {
		i++
	}
	return i
}

// spaceTo is skipSpace that sets *sp when it skips a byte.
func spaceTo(s string, i int, sp *bool) int {
	j := skipSpace(s, i)
	if j != i {
		*sp = true
	}
	return j
}

// validateScalar is Validate one byte at a time, eight within strings.
func validateScalar(s string, maxDepth int, t *Tables, end *int, spaced *bool) bool {
	// stack holds one bit per open container: 1 for object, 0 for array.
	var small [8]uint64
	stack := small[:0]
	var frames *[TrackDepth + 1]frame
	if t != nil {
		frames = &t.frames
	}
	var cont []Container
	var spans []Span
	depth, entry, maxCont := 0, -1, 0
	var n uint32
	if t != nil {
		cont, spans, maxCont = t.Containers[:0], t.Spans[:0], maxContainers(len(s))
	}
	ok, obj, sp := false, false, false
	i := skipSpace(s, 0)
	sp = i != 0
value:
	if i >= len(s) {
		goto done
	}
	switch c := s[i]; {
	case c == '{' || c == '[':
		if maxDepth > 0 && depth == maxDepth {
			goto done
		}
		obj = c == '{'
		w, b := depth>>6, uint(depth&63)
		if w == len(stack) {
			stack = append(stack, 0)
		}
		if obj {
			stack[w] |= 1 << b
		} else {
			stack[w] &^= 1 << b
		}
		if t != nil {
			if depth <= TrackDepth {
				frames[depth] = frame{int32(entry), n}
			}
			entry = -1
			if depth < TrackDepth && len(cont) < maxCont {
				entry = len(cont)
				cont = append(cont, Container{Off: uint32(i)})
			}
			n = 0
		}
		depth++
		i = spaceTo(s, i+1, &sp)
		if i < len(s) && s[i] == c+2 { // '}' or ']'
			n = ^uint32(0)
			goto pop
		}
		if obj {
			goto key
		}
		goto value
	case c == '"':
		j := stringEnd(s, i+1)
		if j < 0 {
			goto done
		}
		if t != nil && j-i > LongString {
			spans = append(spans, Span{uint32(i), uint32(j), stringFlags(s[i+1 : j])})
		}
		i = j + 1
	case c == '-' || c-'0' < 10:
		if i = numberEnd(s, i); i < 0 {
			goto done
		}
	case c == 't':
		if !hasPrefixAt(s, i, "true") {
			goto done
		}
		i += 4
	case c == 'f':
		if !hasPrefixAt(s, i, "false") {
			goto done
		}
		i += 5
	case c == 'n':
		if !hasPrefixAt(s, i, "null") {
			goto done
		}
		i += 4
	default:
		goto done
	}
after:
	if depth == 0 && end != nil {
		*end, ok = i, true
		goto done
	}
	i = spaceTo(s, i, &sp)
	if depth == 0 {
		ok = i == len(s)
		goto done
	}
	if i >= len(s) {
		goto done
	}
	{
		d := depth - 1
		obj = stack[d>>6]>>uint(d&63)&1 == 1
	}
	switch s[i] {
	case ',':
		n++
		i = spaceTo(s, i+1, &sp)
		if obj {
			goto key
		}
		goto value
	case '}':
		if !obj {
			goto done
		}
	case ']':
		if obj {
			goto done
		}
	default:
		goto done
	}
pop:
	depth--
	if t != nil {
		if entry >= 0 {
			cont[entry].End, cont[entry].N, cont[entry].Next = uint32(i), n+1, uint32(len(cont))
		}
		entry = -1
		if depth <= TrackDepth {
			entry, n = int(frames[depth].entry), frames[depth].n
		}
	}
	i++
	goto after
key:
	if i >= len(s) || s[i] != '"' {
		goto done
	}
	{
		j := stringEnd(s, i+1)
		if j < 0 {
			goto done
		}
		if t != nil && j-i > LongString {
			spans = append(spans, Span{uint32(i), uint32(j), stringFlags(s[i+1 : j])})
		}
		i = spaceTo(s, j+1, &sp)
	}
	if i >= len(s) || s[i] != ':' {
		goto done
	}
	i = spaceTo(s, i+1, &sp)
	goto value
done:
	if t != nil {
		t.Containers, t.Spans = cont, spans
	}
	if spaced != nil {
		*spaced = sp
	}
	return ok
}

func hasPrefixAt(s string, i int, lit string) bool {
	return len(s)-i >= len(lit) && s[i:i+len(lit)] == lit
}

const (
	lsb = 0x0101010101010101
	msb = 0x8080808080808080
)

// stopBytes returns the high bits of the bytes of w that end a run of plain
// string bytes: quotes, backslashes and control bytes, surely for the
// lowest one; the bits above it may be wrong.
func stopBytes(w uint64) uint64 {
	q := w ^ ('"' * lsb)
	b := w ^ ('\\' * lsb)
	return (((q - lsb) &^ q) | ((b - lsb) &^ b) | ((w - 0x20*lsb) &^ w)) & msb
}

// PlainEnd returns the index of the first byte of s from i that is a quote,
// a backslash or a control byte, or len(s) when there is none.
func PlainEnd(s string, i int) int {
	for n := 0; i+8 <= len(s); {
		if m := stopBytes(Load64(s, i)); m != 0 {
			return i + bits.TrailingZeros64(m)>>3
		}
		i += 8
		if n++; n == 8 && HaveAVX2 && len(s)-i >= 64 {
			i, _ = strSkipBytes(s, i)
			n = 0
		}
	}
	for i < len(s) && s[i] != '"' && s[i] != '\\' && s[i] >= 0x20 {
		i++
	}
	return i
}

// StringEnd returns the index of the quote that closes the string whose
// contents start at s[i], or -1 when the string is not valid: it holds a
// control byte or a bad escape, or does not end.
func StringEnd(s string, i int) int { return stringEnd(s, i) }

// PlainRun returns the index of the first byte of s from i that is a
// quote, a backslash or a control byte, or len(s) when there is none, and
// hi with the bytes before it ORed in, so that a byte of 0x80 or above sets
// a high bit of hi.
func PlainRun(s string, i int, hi uint64) (int, uint64) {
	for n := 0; i+8 <= len(s); {
		w := Load64(s, i)
		if m := stopBytes(w); m != 0 {
			k := uint(bits.TrailingZeros64(m))
			return i + int(k>>3), hi | w&(uint64(1)<<(k&^7)-1)
		}
		hi |= w
		i += 8
		// Past 16 bytes, a longer run: find its end with AVX2.
		if n++; n == 2 && HaveAVX2 && len(s)-i >= 32 {
			var h bool
			if i, h = strEnd(s, i); h {
				hi |= msb
			}
		}
	}
	for i < len(s) {
		c := s[i]
		if c == '"' || c == '\\' || c < 0x20 {
			break
		}
		hi |= uint64(c)
		i++
	}
	return i, hi
}

// ScanString is StringEnd that also returns the Span flags of the string.
func ScanString(s string, i int) (int, uint32) {
	var flags uint32
	var hi uint64
	for {
		for n := 0; i+8 <= len(s); {
			w := Load64(s, i)
			if m := stopBytes(w); m != 0 {
				k := uint(bits.TrailingZeros64(m))
				hi |= w & (uint64(1)<<(k&^7) - 1)
				i += int(k >> 3)
				break
			}
			hi |= w
			i += 8
			// Past 16 bytes, a longer run: find its end with AVX2.
			if n++; n == 2 && HaveAVX2 && len(s)-i >= 32 {
				var h bool
				if i, h = strEnd(s, i); h {
					flags |= SpanHigh
				}
			}
		}
		if i >= len(s) {
			return -1, 0
		}
		switch c := s[i]; {
		case c == '"':
			if hi&msb != 0 {
				flags |= SpanHigh
			}
			return i, flags
		case c == '\\':
			flags |= SpanEscaped
			if i+1 >= len(s) {
				return -1, 0
			}
			switch s[i+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				i += 2
			case 'u':
				if !hex4(s, i+2) {
					return -1, 0
				}
				i += 6
			default:
				return -1, 0
			}
		case c < 0x20:
			return -1, 0
		default:
			hi |= uint64(c)
			i++
		}
	}
}

func stringEnd(s string, i int) int {
	for {
		for n := 0; i+8 <= len(s); {
			if m := stopBytes(Load64(s, i)); m != 0 {
				i += bits.TrailingZeros64(m) >> 3
				break
			}
			i += 8
			// Past 64 bytes, a long string: skip blocks with AVX2.
			if n++; n == 8 && HaveAVX2 && len(s)-i >= 64 {
				i, _ = strSkipBytes(s, i)
				n = 0
			}
		}
		if i >= len(s) {
			return -1
		}
		switch c := s[i]; {
		case c == '"':
			return i
		case c == '\\':
			if i+1 >= len(s) {
				return -1
			}
			switch s[i+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				i += 2
			case 'u':
				if !hex4(s, i+2) {
					return -1
				}
				i += 6
			default:
				return -1
			}
		case c < 0x20:
			return -1
		default:
			i++
		}
	}
}

// stringFlags returns the Span flags of the string contents s.
func stringFlags(s string) uint32 {
	var esc bool
	var hi uint64
	i := 0
	for ; i+8 <= len(s); i += 8 {
		w := Load64(s, i)
		hi |= w
		b := w ^ ('\\' * lsb)
		if (b-lsb)&^b&msb != 0 {
			esc = true
		}
	}
	for ; i < len(s); i++ {
		hi |= uint64(s[i])
		esc = esc || s[i] == '\\'
	}
	var f uint32
	if esc {
		f = SpanEscaped
	}
	if hi&msb != 0 {
		f |= SpanHigh
	}
	return f
}

// numberEnd checks a JSON number starting at s[i] and returns the index
// after it, or -1.
func numberEnd(s string, i int) int {
	if s[i] == '-' {
		i++
	}
	if i >= len(s) {
		return -1
	}
	switch {
	case s[i] == '0':
		i++
	case s[i] >= '1' && s[i] <= '9':
		i = digitsEnd(s, i+1)
	default:
		return -1
	}
	if i < len(s) && s[i] == '.' {
		j := digitsEnd(s, i+1)
		if j == i+1 {
			return -1
		}
		i = j
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		j := digitsEnd(s, i)
		if j == i {
			return -1
		}
		i = j
	}
	return i
}

// digitsEnd returns the index of the first byte from s[i] on that is not a
// digit, or len(s).
func digitsEnd(s string, i int) int {
	for i+8 <= len(s) {
		if t := nonDigits(Load64(s, i)); t != 0 {
			return i + bits.TrailingZeros64(t)>>3
		}
		i += 8
	}
	for i < len(s) && s[i]-'0' < 10 {
		i++
	}
	return i
}
