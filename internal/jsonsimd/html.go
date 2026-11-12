package jsonsimd

// HasHTML reports whether s holds '<', '>', '&', U+2028 or U+2029, the
// characters that HTML-safe JSON escapes.
func HasHTML(s string) bool { return hasHTMLIn(s, 0, len(s)) }

// hasHTMLIn reports whether one of the characters HasHTML looks for starts
// in s[i:j].
func hasHTMLIn(s string, i, j int) bool {
	for HaveAVX2 && j-i >= 64 {
		if i = htmlSkip(s[:j], i); j-i < 64 {
			break
		}
		// The block holds a candidate byte, maybe 0xE2 of another
		// character.
		if htmlIn(s, i, i+64) {
			return true
		}
		i += 64
	}
	for ; i+8 <= j; i += 8 {
		w := Load64(s, i)
		if (hasByte(w, '<') || hasByte(w, '>') || hasByte(w, '&') || hasByte(w, 0xE2)) && htmlIn(s, i, i+8) {
			return true
		}
	}
	return htmlIn(s, i, j)
}

// htmlScan looks for the characters HasHTML looks for while leanLoop reads
// s, so that the long strings it skips are read once: within them, with
// strSkipHTML, and between them, with hasHTMLIn.
type htmlScan struct {
	from  int // the characters starting before from were looked for
	found bool
}

// skip skips the blocks of a long string from i, as strSkip does.
func (h *htmlScan) skip(s string, i int) int {
	j, cand := strSkipHTML(s, i)
	if !cand && !h.found {
		// s[i:j] holds no candidate byte.
		h.found = hasHTMLIn(s, h.from, i)
		h.from = j
	}
	return j
}

// in reports whether s, which leanLoop read, holds one of the characters.
func (h *htmlScan) in(s string) bool { return h.found || hasHTMLIn(s, h.from, len(s)) }

// hasByte reports whether a byte of w may be c: surely when it is, and
// rarely when it is not.
func hasByte(w uint64, c byte) bool {
	const lsb, msb = 0x0101010101010101, 0x8080808080808080
	x := w ^ uint64(c)*lsb
	return (x-lsb)&^x&msb != 0
}

// htmlIn reports whether one of the characters HasHTML looks for starts in
// s[i:j].
func htmlIn(s string, i, j int) bool {
	for ; i < j; i++ {
		switch s[i] {
		case '<', '>', '&':
			return true
		case 0xE2:
			if i+2 < len(s) && s[i+1] == 0x80 && s[i+2]&^1 == 0xA8 {
				return true
			}
		}
	}
	return false
}
