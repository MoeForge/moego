package regexp

import (
	"math/bits"
	"regexp/syntax"
	"unicode/utf8"
)

func bits32(x uint32) int    { return bits.OnesCount32(x) }
func leading32(x uint32) int { return bits.LeadingZeros32(x) }

// choosePrefilter picks a prefilter from what every match must start with:
// a literal, an ASCII case-insensitive literal, or a small set of first
// bytes.
func choosePrefilter(re *syntax.Regexp) *prefilter {
	if lit := prefixLiteral(re); len(lit) > 0 {
		return newLitPrefilter(lit)
	}
	if lit := foldPrefix(re); len(lit) > 1 {
		return newFoldPrefilter(lit)
	}
	var set [8]uint32
	if !firstBytes(re, &set) {
		return nil
	}
	n := 0
	var only byte
	for w, bits := range set {
		n += bits32(bits)
		if bits != 0 {
			only = byte(w*32 + 31 - leading32(bits))
		}
	}
	switch {
	case n == 0:
		return nil
	case n == 1:
		return &prefilter{kind: pfByte, b0: only}
	case n <= maxSetBytes:
		return &prefilter{kind: pfSet, set: set}
	}
	return nil
}

// foldPrefix returns the lowercased leading part of a case-insensitive
// literal that every match starts with, up to the first rune whose case
// forms are not just an ASCII pair, or "".
func foldPrefix(re *syntax.Regexp) string {
	re = leadingSub(re)
	if re == nil || re.Op != syntax.OpLiteral || re.Flags&syntax.FoldCase == 0 {
		return ""
	}
	var buf [64]byte
	b := buf[:0]
	for _, r := range re.Rune {
		if !asciiFoldOnly(r) {
			break
		}
		b = append(b, lower(byte(r)))
	}
	return string(b)
}

// leadingSub returns the subexpression every match begins with, looking
// through captures, required repetition and leading empty-width
// assertions.
func leadingSub(re *syntax.Regexp) *syntax.Regexp {
	for {
		switch re.Op {
		case syntax.OpCapture, syntax.OpPlus:
			re = re.Sub[0]
		case syntax.OpRepeat:
			if re.Min < 1 {
				return nil
			}
			re = re.Sub[0]
		case syntax.OpConcat:
			i := 0
			for i < len(re.Sub) && isZeroWidth(re.Sub[i]) {
				i++
			}
			if i == len(re.Sub) {
				return nil
			}
			re = re.Sub[i]
		default:
			return re
		}
	}
}

// prefixLiteral returns a case-sensitive literal that every match starts
// with, or "". The literal continues across concatenated literals and
// captures, so `\.(-\d+)` gives ".-".
func prefixLiteral(re *syntax.Regexp) string {
	var buf [64]byte
	b, _ := appendPrefix(buf[:0], re)
	return string(b)
}

// appendPrefix appends the literal every match of re starts with and
// reports whether that literal is all of re.
func appendPrefix(b []byte, re *syntax.Regexp) ([]byte, bool) {
	for {
		switch re.Op {
		case syntax.OpCapture:
			re = re.Sub[0]
			continue
		case syntax.OpLiteral:
			if re.Flags&syntax.FoldCase != 0 {
				return b, false
			}
			for _, r := range re.Rune {
				if !plainRune(r) {
					return b, false
				}
				b = utf8.AppendRune(b, r)
			}
			return b, true
		case syntax.OpPlus:
			b, _ = appendPrefix(b, re.Sub[0])
			return b, false
		case syntax.OpRepeat:
			if re.Min < 1 {
				return b, false
			}
			b, _ = appendPrefix(b, re.Sub[0])
			return b, false
		case syntax.OpConcat:
			i := 0
			for i < len(re.Sub) && isZeroWidth(re.Sub[i]) {
				i++
			}
			if i == len(re.Sub) {
				return b, false
			}
			for ; i < len(re.Sub); i++ {
				var whole bool
				if b, whole = appendPrefix(b, re.Sub[i]); !whole {
					return b, false
				}
			}
			return b, true
		}
		return b, false
	}
}

func isZeroWidth(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary, syntax.OpEmptyMatch:
		return true
	}
	return false
}

func setByte(set *[8]uint32, b byte) { set[b>>5] |= 1 << (b & 31) }

// firstBytes adds the possible first bytes of a non-empty match of re to
// set. It reports false if re can match empty or the set is unbounded.
func firstBytes(re *syntax.Regexp, set *[8]uint32) bool {
	switch re.Op {
	case syntax.OpCapture, syntax.OpPlus:
		return firstBytes(re.Sub[0], set)
	case syntax.OpRepeat:
		return re.Min >= 1 && firstBytes(re.Sub[0], set)
	case syntax.OpLiteral:
		if len(re.Rune) == 0 {
			return false
		}
		r := re.Rune[0]
		if re.Flags&syntax.FoldCase != 0 {
			if cls := foldOrbit(r); cls != nil {
				return classFirstBytes(cls, set)
			}
		}
		return classFirstBytes([]rune{r, r}, set)
	case syntax.OpCharClass:
		return classFirstBytes(re.Rune, set)
	case syntax.OpConcat:
		i := 0
		for i < len(re.Sub) && isZeroWidth(re.Sub[i]) {
			i++
		}
		return i < len(re.Sub) && firstBytes(re.Sub[i], set)
	case syntax.OpAlternate:
		for _, s := range re.Sub {
			if !firstBytes(s, set) {
				return false
			}
		}
		return true
	}
	return false
}

// classFirstBytes adds the first bytes of the encodings of class to set.
// Classes containing U+FFFD also match arbitrary invalid bytes. It reports
// false once the set holds more bytes than a set prefilter uses.
func classFirstBytes(class []rune, set *[8]uint32) bool {
	if classHasFFFD(class) {
		return false
	}
	for i := 0; i+1 < len(class); i += 2 {
		lo, hi := class[i], class[i+1]
		if lo < utf8.RuneSelf {
			h := min(hi, utf8.RuneSelf-1)
			if h-lo >= maxSetBytes {
				return false
			}
			for r := lo; r <= h; r++ {
				setByte(set, byte(r))
			}
			lo = utf8.RuneSelf
		}
		if lo > hi {
			continue
		}
		if setCount(set) > maxSetBytes {
			return false
		}
		utf8Seqs(lo, hi, func(sq utf8Seq) {
			for b := int(sq.seq[0].lo); b <= int(sq.seq[0].hi); b++ {
				setByte(set, byte(b))
			}
		})
	}
	return setCount(set) <= maxSetBytes
}

// maxSetBytes is the most first bytes a set prefilter takes.
const maxSetBytes = 4

func setCount(set *[8]uint32) int {
	n := 0
	for _, w := range set {
		n += bits32(w)
	}
	return n
}
