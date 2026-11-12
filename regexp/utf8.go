package regexp

import "unicode/utf8"

// byteRange is an inclusive byte interval.
type byteRange struct{ lo, hi byte }

// utf8Seq is a sequence of byte ranges, the first n of seq.
type utf8Seq struct {
	seq [utf8.UTFMax]byteRange
	n   int
}

// utf8Seqs calls emit with the byte-range sequences that encode exactly the
// valid runes in [lo, hi]. Every sequence is aligned: once a position holds
// a range wider than one byte, all later positions are full continuation
// ranges, so sequences from disjoint rune ranges never partially overlap.
func utf8Seqs(lo, hi rune, emit func(seq utf8Seq)) {
	if hi > utf8.MaxRune {
		hi = utf8.MaxRune
	}
	if lo > hi {
		return
	}
	// Surrogates have no encoding.
	if lo <= 0xDFFF && hi >= 0xD800 {
		if lo < 0xD800 {
			utf8Seqs(lo, 0xD7FF, emit)
		}
		if hi > 0xDFFF {
			utf8Seqs(0xE000, hi, emit)
		}
		return
	}
	// Split at encoding-length boundaries.
	for _, b := range [...]rune{0x7F, 0x7FF, 0xFFFF} {
		if lo <= b && b < hi {
			utf8Seqs(lo, b, emit)
			utf8Seqs(b+1, hi, emit)
			return
		}
	}
	if hi <= 0x7F {
		emit(utf8Seq{seq: [utf8.UTFMax]byteRange{{byte(lo), byte(hi)}}, n: 1})
		return
	}
	n := utf8.RuneLen(lo)
	for i := 1; i < n; i++ {
		m := rune(1)<<(6*i) - 1
		if lo&^m != hi&^m {
			if lo&m != 0 {
				utf8Seqs(lo, lo|m, emit)
				utf8Seqs((lo|m)+1, hi, emit)
				return
			}
			if hi&m != m {
				utf8Seqs(lo, hi&^m-1, emit)
				utf8Seqs(hi&^m, hi, emit)
				return
			}
		}
	}
	var a, b [4]byte
	utf8.EncodeRune(a[:], lo)
	utf8.EncodeRune(b[:], hi)
	seq := utf8Seq{n: n}
	for i := 0; i < n; i++ {
		seq.seq[i] = byteRange{a[i], b[i]}
	}
	emit(seq)
}

// classHasFFFD reports whether a class matches U+FFFD, which is also how the
// standard library decodes each byte of invalid UTF-8.
func classHasFFFD(c []rune) bool {
	for i := 0; i+1 < len(c); i += 2 {
		if c[i] <= utf8.RuneError && utf8.RuneError <= c[i+1] {
			return true
		}
		if c[i] > utf8.RuneError {
			break
		}
	}
	return false
}
