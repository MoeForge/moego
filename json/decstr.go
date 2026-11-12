package json

import (
	"math/bits"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

const (
	lsb = 0x0101010101010101
	msb = 0x8080808080808080
)

// stringEnd returns the index of the quote closing the string whose
// opening quote is at q, and its flags: jsonsimd.SpanEscaped when it holds
// a backslash, jsonsimd.SpanHigh when it holds a byte of 0x80 or above. It
// stops a fused decode at a malformed string.
func (d *decodeState) stringEnd(q int) (int, uint32) {
	if d.si < len(d.tab.Spans) {
		if sp := d.span(q); sp != nil {
			return int(sp.End), sp.Flags
		}
	}
	end, flags := jsonsimd.ScanString(d.s, q+1)
	if end < 0 {
		d.bad()
	}
	return end, flags
}

// stringScan is stringEnd for a string whose value the caller takes. When
// it reads a short string with escapes from the input, it decodes the
// string as it reads it: the value is then in d.scratch, and the flags have
// spanUnquoted set.
func (d *decodeState) stringScan(q int) (int, uint32) {
	if d.si < len(d.tab.Spans) {
		if sp := d.span(q); sp != nil {
			return int(sp.End), sp.Flags
		}
	}
	i, hi := jsonsimd.PlainRun(d.s, q+1, 0)
	if i < len(d.s) && d.s[i] == '"' {
		if hi&msb != 0 {
			return i, jsonsimd.SpanHigh
		}
		return i, 0
	}
	out, end, flags := scanUnquote(d.scratch[:0], d.s, q+1, i, hi)
	if end < 0 {
		d.bad()
	}
	if flags&spanUnquoted != 0 {
		d.scratch = out
	}
	return end, flags
}

// spanUnquoted marks the flags of a string whose value stringScan left in
// d.scratch.
const spanUnquoted = 1 << 3

// unquoteMax is the length from which scanUnquote leaves the contents of a
// string to be decoded once its end is known, into memory of their size.
// With the AVX2 kernel, scanUnquote goes on to unquoteBlocksMax bytes of
// value, so that d.scratch stays of a size putDecodeState keeps.
const (
	unquoteMax       = slabMax
	unquoteBlocksMax = 32 << 10
)

// scanUnquote returns the index of the quote that closes the string whose
// contents start at s[start], or -1 when the string is not valid, and its
// flags, as jsonsimd.ScanString does. When the string holds an escape and
// its contents are shorter than unquoteMax, or its value with the kernel
// shorter than unquoteBlocksMax, it also appends their value to dst, as
// unquote does, and sets spanUnquoted in the flags. The caller has read
// the plain bytes s[start:i], whose bytes ORed are hi, as
// jsonsimd.PlainRun returns them.
func scanUnquote(dst []byte, s string, start, i int, hi uint64) (out []byte, end int, flags uint32) {
	if i >= len(s) || s[i] < 0x20 {
		return dst, -1, 0
	}
	if s[i] == '"' {
		if hi&msb != 0 {
			flags = jsonsimd.SpanHigh
		}
		return dst, i, flags
	}
	flags = jsonsimd.SpanEscaped
	run := start
	// With the kernel, bytes of 0x80 and above are copied as they are, and
	// the value from checked on is checked at the end.
	checked := -1
	if jsonsimd.HaveAVX2 {
		checked = len(dst)
	}
	for {
		// s[i] is a backslash, or the closing quote, after the run of plain
		// bytes s[run:i], whose bytes ORed are hi.
		if hi&msb != 0 {
			flags |= jsonsimd.SpanHigh
		}
		if i-start >= unquoteMax && (!jsonsimd.HaveAVX2 || len(dst)+i-run > unquoteBlocksMax) {
			if s[i] == '"' {
				return dst, i, flags
			}
			if end, fl := jsonsimd.ScanString(s, i); end >= 0 {
				return dst, end, flags | fl
			}
			return dst, -1, 0
		}
		if checked >= 0 || hi&msb == 0 || utf8.ValidString(s[run:i]) {
			dst = append(dst, s[run:i]...)
		} else {
			dst = appendValidUTF8(dst, s[run:i])
		}
		if s[i] == '"' {
			if checked >= 0 && flags&jsonsimd.SpanHigh != 0 && jsonsimd.CheckUTF8(bytesView(dst[checked:]))&jsonsimd.UTF8Invalid != 0 {
				// Left to unquote, which replaces the bytes that are
				// not part of valid UTF-8.
				return dst, i, flags
			}
			return dst, i, flags | spanUnquoted
		}
		if i+1 >= len(s) {
			return dst, -1, 0
		}
		switch c := s[i+1]; c {
		case '"', '\\', '/':
			dst = append(dst, c)
			i += 2
		case 'b':
			dst = append(dst, '\b')
			i += 2
		case 'f':
			dst = append(dst, '\f')
			i += 2
		case 'n':
			dst = append(dst, '\n')
			i += 2
		case 'r':
			dst = append(dst, '\r')
			i += 2
		case 't':
			dst = append(dst, '\t')
			i += 2
		case 'u':
			r, ok := hexRuneAt(s, i+2)
			if !ok {
				return dst, -1, 0
			}
			i += 6
			if utf16.IsSurrogate(r) {
				dec := unicode.ReplacementChar
				if i+6 <= len(s) && s[i] == '\\' && s[i+1] == 'u' {
					if r2, ok := hexRuneAt(s, i+2); ok {
						if dec = utf16.DecodeRune(r, r2); dec != unicode.ReplacementChar {
							i += 6
						}
					}
				}
				r = dec
			}
			dst = utf8.AppendRune(dst, r)
		default:
			return dst, -1, 0
		}
		if jsonsimd.HaveAVX2 && i-start >= 64 && (cap(dst)-len(dst) >= 256 || cap(dst) <= unquoteBlocksMax) {
			// A long string: the kernel copies blocks and decodes the
			// escapes of two bytes, into dst grown to at most twice
			// unquoteBlocksMax.
			if cap(dst)-len(dst) < 256 {
				dst = growBytes(dst, 256)
			}
			var w int
			var h bool
			i, w, h = jsonsimd.UnescapeBlocks(dst[:cap(dst)], s, i, len(dst))
			dst = dst[:w]
			if h {
				flags |= jsonsimd.SpanHigh
			}
		}
		run = i
		if i, hi = jsonsimd.PlainRun(s, i, 0); i >= len(s) || s[i] < 0x20 {
			return dst, -1, 0
		}
	}
}

// hexRuneAt returns the value of the four hexadecimal digits at s[i:], or
// false when they are not there.
func hexRuneAt(s string, i int) (rune, bool) {
	if i+4 > len(s) {
		return 0, false
	}
	var r rune
	for _, c := range []byte(s[i : i+4]) {
		switch {
		case '0' <= c && c <= '9':
			c -= '0'
		case 'a' <= c && c <= 'f':
			c -= 'a' - 10
		case 'A' <= c && c <= 'F':
			c -= 'A' - 10
		default:
			return 0, false
		}
		r = r<<4 | rune(c)
	}
	return r, true
}

// stringValue reads the string at d.i and returns a copy of its value.
func (d *decodeState) stringValue() string {
	start := d.i
	end, flags := d.stringScan(start)
	d.i = end + 1
	return d.stringFrom(start, end, flags)
}

// stringFrom returns a copy of the value of the string whose quotes are
// at start and end, with the given flags.
func (d *decodeState) stringFrom(start, end int, flags uint32) string {
	if flags == 0 {
		return d.copyString(start+1, end)
	}
	return d.decodeString(start+1, end, flags)
}

// decodeString returns the value of the string contents d.s[start:end]
// with the given flags.
func (d *decodeState) decodeString(start, end int, flags uint32) string {
	if flags&spanUnquoted != 0 {
		return d.ownString(d.scratch)
	}
	if flags&spanHole != 0 {
		return d.holeString(flags)
	}
	raw := d.s[start:end]
	if flags == jsonsimd.SpanHigh && utf8.ValidString(raw) {
		return d.copyString(start, end)
	}
	if len(raw) >= slabMax {
		// Unescaping only shrinks valid UTF-8: write into an allocation of
		// the input's size, then trim, unless invalid bytes made it grow.
		d.long += len(raw)
		buf := d.bigBlock(len(raw))
		out := unquote(buf[:0], raw)
		if &out[0] == &buf[0] && cap(out)-len(out) < len(raw)/8 {
			return bytesView(out)
		}
		return d.ownString(out)
	}
	d.scratch = unquote(d.scratch[:0], raw)
	return d.ownString(d.scratch)
}

// stringBytes reads the string at d.i and returns its value. The result
// views the input or d.scratch and must not be kept.
func (d *decodeState) stringBytes() []byte {
	start := d.i
	end, flags := d.stringEnd(start)
	d.i = end + 1
	if flags&spanHole != 0 {
		return stringView(d.holeString(flags))
	}
	raw := d.s[start+1 : end]
	if flags == 0 || flags == jsonsimd.SpanHigh && utf8.ValidString(raw) {
		return d.data[start+1 : end]
	}
	d.scratch = unquote(d.scratch[:0], raw)
	return d.scratch
}

func validUTF8(s string) bool { return utf8.ValidString(s) }

// unquote appends the value of the valid JSON string contents s to dst:
// escapes are decoded, a surrogate escape that is not part of a pair and
// each byte that is not part of valid UTF-8 become U+FFFD.
func unquote(dst []byte, s string) []byte {
	if len(s) >= 64 && jsonsimd.HaveAVX2 {
		if out, ok := unquoteBlocks(dst, s); ok {
			return out
		}
	}
	for i := 0; i < len(s); {
		// The run up to the next backslash.
		j := i
		var hi uint64
		for j+8 <= len(s) {
			w := load64(s, j)
			b := w ^ ('\\' * lsb)
			if m := (b - lsb) &^ b & msb; m != 0 {
				k := uint(bits.TrailingZeros64(m))
				hi |= w & (uint64(1)<<(k&^7) - 1)
				j += int(k >> 3)
				goto run
			}
			hi |= w
			j += 8
		}
		for j < len(s) && s[j] != '\\' {
			hi |= uint64(s[j])
			j++
		}
	run:
		if hi&msb == 0 || utf8.ValidString(s[i:j]) {
			dst = append(dst, s[i:j]...)
		} else {
			dst = appendValidUTF8(dst, s[i:j])
		}
		i = j
		if i >= len(s) {
			break
		}
		switch c := s[i+1]; c {
		case 'b':
			dst = append(dst, '\b')
		case 'f':
			dst = append(dst, '\f')
		case 'n':
			dst = append(dst, '\n')
		case 'r':
			dst = append(dst, '\r')
		case 't':
			dst = append(dst, '\t')
		case 'u':
			r := hexRune(s, i+2)
			i += 6
			if utf16.IsSurrogate(r) {
				if i+6 <= len(s) && s[i] == '\\' && s[i+1] == 'u' {
					if dec := utf16.DecodeRune(r, hexRune(s, i+2)); dec != unicode.ReplacementChar {
						dst = utf8.AppendRune(dst, dec)
						i += 6
						continue
					}
				}
				r = unicode.ReplacementChar
			}
			dst = utf8.AppendRune(dst, r)
			continue
		default: // '"', '\\', '/'
			dst = append(dst, c)
		}
		i += 2
	}
	return dst
}

// unquoteBlocks is unquote for long s with the AVX2 kernel. The kernel
// copies bytes of 0x80 and above as they are: when they are not valid
// UTF-8, unquoteBlocks reports false, for unquote to decode s word by word.
func unquoteBlocks(dst []byte, s string) ([]byte, bool) {
	from := len(dst)
	// The value is no longer than s, so the kernel never runs out of room.
	dst = growBytes(dst, len(s))
	high := false
	i := 0
	for {
		var w int
		var h bool
		i, w, h = jsonsimd.UnescapeBlocks(dst[:cap(dst)], s, i, len(dst))
		dst = dst[:w]
		high = high || h
		if len(s)-i < 34 {
			// The tail, from the start of the character the kernel stopped
			// in, at most three bytes back: the tail stays short of the
			// kernel, and a longer run of bytes of 0x80 and above is not
			// valid UTF-8 anyway.
			for k := 0; k < 3 && i > 0 && i < len(s) && s[i]&0xc0 == 0x80 && s[i-1] >= 0x80; k++ {
				i--
				dst = dst[:len(dst)-1]
			}
			dst = unquote(dst, s[i:])
			break
		}
		// An escape of six bytes, with the next one when they make a
		// surrogate pair.
		r := hexRune(s, i+2)
		i += 6
		if utf16.IsSurrogate(r) {
			dec := unicode.ReplacementChar
			if i+6 <= len(s) && s[i] == '\\' && s[i+1] == 'u' {
				if dec = utf16.DecodeRune(r, hexRune(s, i+2)); dec != unicode.ReplacementChar {
					i += 6
				}
			}
			r = dec
		}
		dst = utf8.AppendRune(dst, r)
	}
	if high && jsonsimd.CheckUTF8(bytesView(dst[from:]))&jsonsimd.UTF8Invalid != 0 {
		return dst[:from], false
	}
	return dst, true
}

// appendValidUTF8 appends s to dst with each byte that is not part of
// valid UTF-8 replaced by U+FFFD.
func appendValidUTF8(dst []byte, s string) []byte {
	for i := 0; i < len(s); {
		if s[i] < utf8.RuneSelf {
			dst = append(dst, s[i])
			i++
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 {
			dst = append(dst, "\uFFFD"...)
		} else {
			dst = append(dst, s[i:i+n]...)
		}
		i += n
	}
	return dst
}

// hexRune returns the value of the four hexadecimal digits at s[i:].
func hexRune(s string, i int) rune {
	var r rune
	for _, c := range []byte(s[i : i+4]) {
		switch {
		case c <= '9':
			c -= '0'
		case c >= 'a':
			c -= 'a' - 10
		default:
			c -= 'A' - 10
		}
		r = r<<4 | rune(c)
	}
	return r
}
