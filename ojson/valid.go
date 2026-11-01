package ojson

import "math/bits"

// Valid reports whether json is one well-formed JSON value surrounded only by
// whitespace. It uses an explicit stack, so deep nesting cannot overflow the
// goroutine stack.
func Valid(json string) bool { return validate(json) }

// ValidBytes is Valid for a byte slice.
func ValidBytes(json []byte) bool { return validate(bytesView(json)) }

// validate reads 64-byte blocks with AVX2, else one byte at a time.
func validate(s string) bool {
	if haveAVX2 && len(s) >= validBlockMin {
		return validateBlocks(s)
	}
	return validateScalar(s)
}

// validBlockMin is the shortest input validateBlocks takes.
const validBlockMin = 64

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
	masks                     [validBatch]vmask
	tail                      [64]byte
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
			v.k, v.n = 0, validMasks(s, v.next, &v.masks, &v.tail)
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

// validMaxDepth is the deepest nesting validateBlocks holds on its stack.
// Deeper input goes to validateScalar.
const validMaxDepth = 64 * 64

// validateBlocks is validate on 64-byte blocks. The structural bytes that
// vblocks gives drive the grammar of validateScalar, with one label for
// each thing the next of them may be, and the other values are checked as
// numbers or literals that end where a delimiter follows. The loop calls
// out only to read a block, so that its variables stay in registers.
func validateBlocks(s string) bool {
	v := vblocks{s: s}
	// stack holds one bit per open container: 1 for object, 0 for array,
	// 64 to a word. cur is the word of the innermost one, which is not
	// stored while it is open, and top is its bit.
	var stack [validMaxDepth / 64]uint64
	// st holds the structural bytes not yet read of the block at base.
	var st, cur, top, x uint64
	var depth, base, i int
	var c byte

	// Each label first reads the next structural byte into c at s[i].
value:
	for st == 0 {
		if st, base = v.block(); st == 0 {
			return false
		}
	}
	i = base + bits.TrailingZeros64(st)
	c, st = s[i], st&(st-1)
valueAt:
	switch c {
	case '{', '[':
		if depth == validMaxDepth {
			return validateScalar(s)
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
		return false
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
			return false
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
				return false
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
				return false
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
	if byte(x) == ',' {
		// The comma is the next structural byte: take it here.
		for st == 0 {
			if st, base = v.block(); st == 0 {
				return false
			}
		}
		st &= st - 1
		goto comma
	}
	if !validDelim[byte(x)] {
		return false
	}
	goto after

valueOrEnd:
	for st == 0 {
		if st, base = v.block(); st == 0 {
			return false
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
			return false
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
			return false
		}
	}
	i = base + bits.TrailingZeros64(st)
	c, st = s[i], st&(st-1)
keyAt:
	if c != '"' {
		return false
	}
	for st == 0 {
		if st, base = v.block(); st == 0 {
			return false
		}
	}
	i = base + bits.TrailingZeros64(st)
	c, st = s[i], st&(st-1)
	if c != ':' {
		return false
	}
	goto value

after:
	for st == 0 {
		if st, base = v.block(); st == 0 {
			return !v.bad && depth == 0 && v.inStr == 0
		}
	}
	i = base + bits.TrailingZeros64(st)
	c, st = s[i], st&(st-1)
	if c != ',' {
		if depth == 0 || c != byte(']'+top<<5) { // '}' in an object
			return false
		}
		goto pop
	}
comma:
	if depth == 0 {
		return false
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

// vload is load64 for any i <= len(s), with spaces for the bytes past the
// end of s.
func vload(s string, i int) uint64 {
	if i+8 <= len(s) {
		return load64(s, i)
	}
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
		if _, ok := hex4(s, base+p+1); !ok {
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
		_, ok := hex4(s, i+1)
		return ok
	}
	return false
}

// validDelim marks the bytes that may follow a number or literal.
var validDelim = [256]bool{' ': true, '\t': true, '\n': true, '\r': true, ',': true, ':': true,
	'{': true, '}': true, '[': true, ']': true, '"': true}

// validateScalar is validate one byte at a time.
func validateScalar(s string) bool {
	// stack holds one bit per open container: 1 for object, 0 for array.
	var small [8]uint64
	stack := small[:0]
	depth := 0
	push := func(obj bool) {
		w, b := depth>>6, uint(depth&63)
		if w == len(stack) {
			stack = append(stack, 0)
		}
		if obj {
			stack[w] |= 1 << b
		} else {
			stack[w] &^= 1 << b
		}
		depth++
	}
	top := func() bool {
		d := depth - 1
		return stack[d>>6]>>(uint(d&63))&1 == 1
	}

	i := skipSpace(s, 0)
value:
	if i >= len(s) {
		return false
	}
	switch c := s[i]; {
	case c == '{':
		i = skipSpace(s, i+1)
		if i < len(s) && s[i] == '}' {
			i++
			goto after
		}
		push(true)
		goto key
	case c == '[':
		i = skipSpace(s, i+1)
		if i < len(s) && s[i] == ']' {
			i++
			goto after
		}
		push(false)
		goto value
	case c == '"':
		if i = validString(s, i+1); i < 0 {
			return false
		}
	case c == '-' || (c >= '0' && c <= '9'):
		if i = validNumber(s, i); i < 0 {
			return false
		}
	case c == 't':
		if !hasPrefixAt(s, i, "true") {
			return false
		}
		i += 4
	case c == 'f':
		if !hasPrefixAt(s, i, "false") {
			return false
		}
		i += 5
	case c == 'n':
		if !hasPrefixAt(s, i, "null") {
			return false
		}
		i += 4
	default:
		return false
	}
after:
	i = skipSpace(s, i)
	if depth == 0 {
		return i == len(s)
	}
	if i >= len(s) {
		return false
	}
	switch s[i] {
	case ',':
		i = skipSpace(s, i+1)
		if top() {
			goto key
		}
		goto value
	case '}':
		if !top() {
			return false
		}
	case ']':
		if top() {
			return false
		}
	default:
		return false
	}
	depth--
	i++
	goto after
key:
	if i >= len(s) || s[i] != '"' {
		return false
	}
	if i = validString(s, i+1); i < 0 {
		return false
	}
	i = skipSpace(s, i)
	if i >= len(s) || s[i] != ':' {
		return false
	}
	i = skipSpace(s, i+1)
	goto value
}

func hasPrefixAt(s string, i int, lit string) bool {
	return len(s)-i >= len(lit) && s[i:i+len(lit)] == lit
}

// validString checks the string contents starting at s[i] and returns the
// index after the closing quote, or -1. Bytes >= 0x80 are accepted as is.
func validString(s string, i int) int {
	for i < len(s) {
		c := s[i]
		switch {
		case c == '"':
			return i + 1
		case c < 0x20:
			return -1
		case c == '\\':
			if i+1 >= len(s) {
				return -1
			}
			switch s[i+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				i += 2
			case 'u':
				if _, ok := hex4(s, i+2); !ok {
					return -1
				}
				i += 6
			default:
				return -1
			}
		default:
			i++
		}
	}
	return -1
}

// validNumber checks a JSON number starting at s[i] and returns the index
// after it, or -1.
func validNumber(s string, i int) int {
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
		if t := nonDigits(load64(s, i)); t != 0 {
			return i + bits.TrailingZeros64(t)>>3
		}
		i += 8
	}
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i
}
