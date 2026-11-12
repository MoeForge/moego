package jsonsimd

import "math/bits"

// AppendCompact appends s, which is valid JSON, to dst without the
// whitespace outside its strings. It reads 64-byte blocks, so it needs
// AVX2.
func AppendCompact(dst []byte, s string) []byte {
	var masks [validBatch]vmask
	var tail [64]byte
	var inStr, escNext uint64
	// The bytes from from on are kept and not yet appended.
	from := 0
	for next := 0; next < len(s); {
		nb := validMasks(s, next, &masks, &tail, nil)
		for k := range nb {
			m := &masks[k]
			q, r := m.quote, m.r0
			if m.bslash|escNext != 0 {
				var e uint64
				e, escNext = escapes(m.bslash, escNext)
				q &^= e
				r = prefixXor(q) ^ inStr
			} else {
				r ^= inStr
			}
			inStr = uint64(int64(r) >> 63)
			ws := m.ws &^ r
			if n := len(s) - next; n < 64 {
				// The tail block is padded with spaces.
				ws &= 1<<uint(n) - 1
			}
			for ws != 0 {
				a := uint(bits.TrailingZeros64(ws))
				run := uint(bits.TrailingZeros64(^(ws >> a)))
				dst = append(dst, s[from:next+int(a)]...)
				from = next + int(a+run)
				ws &^= (uint64(1)<<run - 1) << a
			}
			next += 64
		}
		if inStr != 0 && nb >= 2 && masks[nb-1].quote|masks[nb-1].bslash|masks[nb-2].quote|masks[nb-2].bslash == 0 && len(s)-next >= 64 {
			// Inside a long string, which is kept: skip the blocks with
			// no quote or backslash.
			next, _ = strSkip(s, next)
		}
	}
	return append(dst, s[from:]...)
}
