package jsonsimd

import "math/bits"

// CountElements reads s, which continues an array or object from the start
// of one of its elements or members, or from just after its opening
// bracket, or from inside depth containers open there, up to the bracket
// that closes it. It returns the number of commas at its level plus one,
// which counts its elements from s[0], and the index of the closing
// bracket, or -1 and len(s) when s does not hold it. It does not check s:
// on malformed input the count may be wrong, but the index is that of a
// closing bracket in s. When longest > 0, it gives up at a string longer
// than about longest bytes, and returns -1 and an index inside the string.
// It reads 64-byte blocks, so it needs AVX2 and is for s of at least 64
// bytes.
func CountElements(s string, depth, longest int) (n, end int) {
	var masks [validBatch]cmask
	var tail [64]byte
	var inStr, escNext uint64
	grow := 1
	n = 1
	for next := 0; next < len(s); {
		// Reads start small, as most arrays and objects end soon.
		lim := min(len(s), next+64*grow)
		grow = min(2*grow, validBatch)
		nb := countMasks(s[:lim], next, &masks, &tail)
		for k := range nb {
			m := &masks[k]
			r := m.r0
			if m.bslash|escNext != 0 {
				var e uint64
				e, escNext = escapes(m.bslash, escNext)
				r = prefixXor(m.quote&^e) ^ inStr
			} else {
				r ^= inStr
			}
			inStr = uint64(int64(r) >> 63)
			open, close := m.open&^r, m.close&^r
			if c := bits.OnesCount64(close); c < depth {
				// The block stays inside a nested container.
				depth += bits.OnesCount64(open) - c
				next += 64
				continue
			}
			// Between brackets the depth holds: count the commas at
			// depth 0 a run at a time.
			comma := m.comma &^ r
			for br := open | close; br != 0; br &= br - 1 {
				b := br & -br
				if depth == 0 {
					n += bits.OnesCount64(comma & (b - 1))
				}
				comma &^= b - 1
				if open&b != 0 {
					depth++
				} else if depth == 0 {
					return n, next + bits.TrailingZeros64(b)
				} else {
					depth--
				}
			}
			if depth == 0 {
				n += bits.OnesCount64(comma)
			}
			next += 64
		}
		if inStr != 0 && nb >= 2 && masks[nb-1].quote|masks[nb-1].bslash|masks[nb-2].quote|masks[nb-2].bslash == 0 && len(s)-next >= 64 {
			// Inside a long string: skip the blocks with no quote or
			// backslash.
			lim := len(s)
			if longest > 0 {
				lim = min(lim, next+longest)
			}
			j, _ := strSkip(s[:lim], next)
			if lim < len(s) && j+64 > lim {
				return -1, j
			}
			next = j
		}
	}
	return -1, len(s)
}

// cmask holds bit masks of a 64-byte block for CountElements, bit i for
// byte i: quotes, backslashes, '{' and '[', '}' and ']', commas, and the
// prefix XOR of the quotes.
type cmask struct {
	quote, bslash, open, close, comma, r0 uint64
}
