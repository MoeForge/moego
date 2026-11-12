package ojson

import (
	"encoding/binary"
	"math/bits"
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// The scanner works on strings. []byte inputs are viewed as strings without
// copying (see bytesView), so every lookup and edit shares one implementation.

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func skipSpace(s string, i int) int {
	for i < len(s) && isSpace(s[i]) {
		i++
	}
	return i
}

// trimSpace returns the bounds of s without leading and trailing whitespace.
func trimSpace(s string) (lo, hi int) {
	lo, hi = 0, len(s)
	for lo < hi && isSpace(s[lo]) {
		lo++
	}
	for hi > lo && isSpace(s[hi-1]) {
		hi--
	}
	return lo, hi
}

// stringEnd returns the index just past the closing quote of the string whose
// contents start at i, or -1 if it is unterminated.
func stringEnd(s string, i int) int {
	end, _ := scanString(s, i)
	return end
}

// scanString is stringEnd that also reports whether the contents may hold
// escapes. It reads eight bytes at a time, which ends keys and most short
// values within four words; scanLong goes on from there.
func scanString(s string, i int) (end int, esc bool) {
	for k := 0; k < 4 && i+8 <= len(s); k++ {
		m := quoteOrSlash(jsonsimd.Load64(s, i))
		if m == 0 {
			i += 8
			continue
		}
		i += bits.TrailingZeros64(m) >> 3
		if s[i] == '"' {
			return i + 1, esc
		}
		esc = true
		i += 2
	}
	return scanLong(s, i, esc)
}

// scanLong is scanString past its first four words; esc reports whether
// they held a backslash. After four words without a quote or backslash, as
// in multi-megabyte base64 payloads, it finds quotes with the runtime's
// vectorized IndexByte, and goes back to words when escaped quotes come
// close together, as in JSON text held in a string.
func scanLong(s string, i int, esc bool) (int, bool) {
	plain := 4 // words without a quote or backslash just before i
	if esc {
		plain = 0
	}
	for i+8 <= len(s) {
		if plain == 4 {
			// i is not inside an escape: a quote ends the string when an
			// even number of backslashes since i precede it.
			j := strings.IndexByte(s[i:], '"')
			if j < 0 {
				return -1, true
			}
			q := i + j
			k := q - 1
			for k >= i && s[k] == '\\' {
				k--
			}
			if (q-1-k)&1 == 0 {
				return q + 1, true
			}
			if j < 32 {
				plain = 0
			}
			i, esc = q+1, true
			continue
		}
		m := quoteOrSlash(jsonsimd.Load64(s, i))
		if m == 0 {
			i += 8
			plain++
			continue
		}
		i += bits.TrailingZeros64(m) >> 3
		if s[i] == '"' {
			return i + 1, esc
		}
		esc = true
		i += 2
		plain = 0
	}
	for i < len(s) {
		switch s[i] {
		case '"':
			return i + 1, esc
		case '\\':
			esc = true
			i++
		}
		i++
	}
	return -1, esc
}

// quickEnd returns the end of the string whose contents start at s[i] when
// its closing quote is among the next eight bytes with no backslash before
// it, and 0 otherwise. It is small enough to inline, so the short keys and
// values that make up most JSON skip the call to scanString.
func quickEnd(s string, i int) int {
	if i+8 > len(s) {
		return 0
	}
	w := jsonsimd.Load64(s, i)
	q := zeroBytes(w ^ lsb*'"')
	if q == 0 || zeroBytes(w^lsb*'\\')&(q^(q-1)) != 0 {
		return 0
	}
	return i + bits.TrailingZeros64(q)>>3 + 1
}

// zeroBytes sets the high bit of the lowest zero byte of w. Bits above it
// may be set wrongly, so callers use only the lowest set bit.
func zeroBytes(w uint64) uint64 { return (w - lsb) &^ w & msb }

func quoteOrSlash(w uint64) uint64 {
	return zeroBytes(w^lsb*'"') | zeroBytes(w^lsb*'\\')
}

// structural marks the bytes containerEnd must look at inside containers.
var structural = [256]bool{'"': true, '{': true, '[': true, '}': true, ']': true}

// delim marks the bytes that end a number or literal.
var delim = [256]bool{',': true, '}': true, ']': true, ' ': true, '\t': true, '\n': true, '\r': true}

// valueEnd returns the end of the value starting at s[i], or -1 when a
// string or container is unterminated.
func valueEnd(s string, i int) int {
	switch s[i] {
	case '"':
		return stringEnd(s, i+1)
	case '{', '[':
		return containerEnd(s, i)
	}
	for i < len(s) && !delim[s[i]] {
		i++
	}
	return i
}

// containerEnd skips a balanced container and returns the index after its
// closing bracket, or -1. Brackets only count depth, so it is
// non-recursive and has no nesting limit.
func containerEnd(s string, i int) int { return containerEndFast(s, i) }

// containerEndScalar is containerEnd eight bytes at a time, for CPUs
// without AVX2.
func containerEndScalar(s string, i int) int {
	depth := 0
	i++
	for {
		for i+8 <= len(s) {
			w := jsonsimd.Load64(s, i)
			b := w | lsb*0x20
			m := zeroBytes(w^lsb*'"') | zeroBytes(b^lsb*'{') | zeroBytes(b^lsb*'}')
			if m == 0 {
				i += 8
				continue
			}
			i += bits.TrailingZeros64(m) >> 3
			break
		}
		if i+8 > len(s) {
			for i < len(s) && !structural[s[i]] {
				i++
			}
			if i >= len(s) {
				return -1
			}
		}
		switch s[i] {
		case '"':
			i = stringEnd(s, i+1)
			if i < 0 {
				return -1
			}
			continue
		case '{', '[':
			depth++
		default:
			if depth == 0 {
				return i + 1
			}
			depth--
		}
		i++
	}
}

// keyIs reports whether the JSON string contents raw decode to key. esc
// reports whether raw may hold escapes, as scanString found.
func keyIs(raw string, esc bool, key string) bool {
	if !esc {
		return raw == key
	}
	return keyEqual(raw, key)
}

// keyEqual reports whether the JSON string contents raw (without quotes)
// decode to key.
func keyEqual(raw, key string) bool {
	// Every escape decodes to fewer bytes than it takes, so a raw key no
	// longer than key matches only when it is key itself without escapes.
	if len(raw) <= len(key) {
		return raw == key && strings.IndexByte(raw, '\\') < 0
	}
	if strings.IndexByte(raw, '\\') < 0 {
		return false
	}
	var buf [64]byte
	dec, ok := unescape(buf[:0], raw)
	return ok && string(dec) == key
}

// unescape appends the decoded JSON string contents raw to dst. It stops at
// the first malformed escape and reports false. No escape decodes to more
// bytes than it takes, so dst grows once, by len(raw), and the runs between
// escapes are copied eight bytes at a time: a whole word is stored, then the
// write position moves past the bytes before the backslash only.
func unescape(dst []byte, raw string) ([]byte, bool) {
	w := len(dst)
	b := slices.Grow(dst, len(raw))[:w+len(raw)]
	i := 0
	for i < len(raw) {
		for i+8 <= len(raw) {
			x := jsonsimd.Load64(raw, i)
			binary.LittleEndian.PutUint64(b[w:], x)
			m := zeroBytes(x ^ lsb*'\\')
			if m == 0 {
				i, w = i+8, w+8
				continue
			}
			k := bits.TrailingZeros64(m) >> 3
			i, w = i+k, w+k
			break
		}
		if i >= len(raw) {
			break
		}
		if c := raw[i]; c != '\\' {
			b[w] = c
			i, w = i+1, w+1
			continue
		}
		if i+1 >= len(raw) {
			return b[:w], false
		}
		var c byte
		switch raw[i+1] {
		case '"', '\\', '/':
			c = raw[i+1]
		case 'b':
			c = '\b'
		case 'f':
			c = '\f'
		case 'n':
			c = '\n'
		case 'r':
			c = '\r'
		case 't':
			c = '\t'
		case 'u':
			r, ok := hex4(raw, i+2)
			if !ok {
				return b[:w], false
			}
			i += 6
			// A surrogate consumes a following \u escape whether or not the
			// two form a valid pair.
			if utf16.IsSurrogate(r) {
				if r2, ok := hex4(raw, i+2); ok && i+1 < len(raw) && raw[i] == '\\' && raw[i+1] == 'u' {
					r = utf16.DecodeRune(r, r2)
					i += 6
				} else {
					r = utf8.RuneError
				}
			}
			w += utf8.EncodeRune(b[w:], r)
			continue
		default:
			return b[:w], false
		}
		b[w] = c
		i, w = i+2, w+1
	}
	return b[:w], true
}

func hex4(s string, i int) (rune, bool) {
	if i+4 > len(s) {
		return 0, false
	}
	var r rune
	for _, c := range []byte(s[i : i+4]) {
		switch {
		case c >= '0' && c <= '9':
			c -= '0'
		case c >= 'a' && c <= 'f':
			c -= 'a' - 10
		case c >= 'A' && c <= 'F':
			c -= 'A' - 10
		default:
			return 0, false
		}
		r = r<<4 | rune(c)
	}
	return r, true
}
