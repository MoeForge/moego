package deflate

import "math/bits"

const fastHashBits = 15

// fastMatcher finds matches for the fast engine: per hash of four bytes,
// the two latest positions with that hash, newest first.
type fastMatcher struct {
	table [1 << fastHashBits][2]int32
}

func (m *fastMatcher) rebase(delta int32) {
	for i := range m.table {
		for k, v := range m.table[i] {
			m.table[i][k] = max(v-delta, 0)
		}
	}
}

// fastTail is how many positions at the end of each match the fast
// engine adds to the table (tuning knob): runs and repeats right after a
// match then find near copies instead of far ones.
var fastTail = 8

// fastHash hashes the low four bytes of u.
func fastHash(u uint64) uint32 { return uint32(u) * 0x9e3779b1 >> (32 - fastHashBits) }

// extend returns the length of the match at c and i, at most limit, given
// x, the XOR of their first eight bytes.
func extend(win []byte, c, i int, x uint64, limit int) int {
	if x != 0 {
		return min(bits.TrailingZeros64(x)>>3, limit)
	}
	if limit <= 8 {
		return limit
	}
	return 8 + matchLen(win, c+8, i+8, limit-8)
}

// matchLen returns the length of the common prefix of win[a:] and win[b:],
// at most limit. win has eight readable bytes past a+limit and b+limit.
func matchLen(win []byte, a, b, limit int) int {
	n := 0
	for n < limit {
		if x := load64(win, a+n) ^ load64(win, b+n); x != 0 {
			return min(n+bits.TrailingZeros64(x)>>3, limit)
		}
		n += 8
	}
	return limit
}

// compressFast codes win[pos:end] with greedy parsing: at each position
// the longer match of the two latest positions with the same hash of four
// bytes, or a literal.
func (w *Writer) compressFast(final bool) {
	win := w.win[:len(w.win)+winSlack]
	m := &w.fast.table
	b := &w.blk
	base, end := w.base, w.end
	i, start := w.pos, w.pend
	tail := fastTail
	// Tokens are stored by index; the buffer holds a token per input byte
	// or bufTokens.
	toks := b.tokens[:cap(b.tokens)]
	nt := len(b.tokens)
	for i+4 <= end {
		if nt >= bufTokens-1 {
			b.tokens = toks[:nt]
			start = w.flushTokens(start, i, false, maxPend)
			nt = len(b.tokens)
		}
		u := load64(win, i)
		e := &m[fastHash(u)]
		c0, c1 := int(e[0])-base, int(e[1])-base
		e[1], e[0] = e[0], int32(i+base)
		limit := min(maxMatch, end-i)
		l, d := 0, 0
		// Positions of earlier streams and those past the window are
		// more than windowSize back, so the loads stay inside win.
		if i-c0 <= windowSize {
			if x := load64(win, c0) ^ u; uint32(x) == 0 {
				l, d = extend(win, c0, i, x, limit), i-c0
			}
		}
		if i-c1 <= windowSize && l < limit {
			if x := load64(win, c1) ^ u; uint32(x) == 0 {
				if l1 := extend(win, c1, i, x, limit); l1 > l {
					l, d = l1, i-c1
				}
			}
		}
		if l == 0 {
			toks[nt] = token(uint8(u))
			nt++
			i++
			continue
		}
		toks[nt] = matchToken(l, d)
		nt++
		i += l
		for k := max(i-tail, i-l+1); k < i; k++ {
			e := &m[fastHash(load64(win, k))]
			e[1], e[0] = e[0], int32(k+base)
		}
	}
	for ; i < end; i++ {
		if nt >= bufTokens-1 {
			b.tokens = toks[:nt]
			start = w.flushTokens(start, i, false, maxPend)
			nt = len(b.tokens)
		}
		toks[nt] = token(win[i])
		nt++
	}
	b.tokens = toks[:nt]
	w.endChunk(start, final)
}
