package ojson

import (
	"encoding/binary"
	"math/bits"
	"slices"
	"unicode/utf8"
)

// DisableEscapeHTML stops AppendJSONString and @tostr from escaping '<',
// '>' and '&'.
var DisableEscapeHTML = false

// Escape returns comp with a backslash before every character that has a
// meaning in a path, so that it matches as one literal key.
//
//	Get(json, "user."+Escape("first.name"))
func Escape(comp string) string {
	n := 0
	for i := 0; i < len(comp); i++ {
		if !safeKeyByte(comp[i]) {
			n++
		}
	}
	if n == 0 {
		return comp
	}
	b := make([]byte, 0, len(comp)+n)
	for i := 0; i < len(comp); i++ {
		if !safeKeyByte(comp[i]) {
			b = append(b, '\\')
		}
		b = append(b, comp[i])
	}
	return bytesView(b)
}

// safeKeyByte reports whether c needs no escape in a path component:
// letters, digits, '_', '-', ':', spaces and control bytes, and bytes of
// multi-byte UTF-8.
func safeKeyByte(c byte) bool {
	return c <= ' ' || c > '~' || c == '_' || c == '-' || c == ':' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// AppendJSONString appends s to dst as a JSON string. Invalid UTF-8 becomes
// U+FFFD, and '<', '>' and '&' are escaped unless DisableEscapeHTML is set.
func AppendJSONString(dst []byte, s string) []byte {
	return appendJSONString(dst, s, !DisableEscapeHTML)
}

// appendJSONString is AppendJSONString with the HTML escaping chosen by
// html. A short string is written once to a buffer on the stack, then
// appended. For a longer one, a first pass finds the length of the result,
// so dst grows once, and a string that needs no escapes is copied whole.
func appendJSONString(dst []byte, s string, html bool) []byte {
	if len(s) <= shortQuoted {
		var buf [6*shortQuoted + 2 + 8]byte
		return append(dst, buf[:writeQuoted(buf[:], s, html)]...)
	}
	n := quotedLen(s, html)
	at := len(dst)
	if n == len(s)+2 {
		dst = slices.Grow(dst, n)
		return append(append(append(dst, '"'), s...), '"')
	}
	dst = slices.Grow(dst, n+8)
	writeQuoted(dst[at:at+n+8], s, html)
	return dst[:at+n]
}

// shortQuoted is the longest string appendJSONString writes in one pass.
const shortQuoted = 32

// writeQuoted writes s as a JSON string to out and returns its length. out
// must hold the result and eight bytes more. It reads 64-byte blocks with
// AVX2, else eight bytes at a time: a block or word with nothing to escape
// is stored whole, one whose only escapes are quotes and backslashes
// around them, and others go one byte at a time.
func writeQuoted(out []byte, s string, html bool) int {
	out[0] = '"'
	w := 1
	for i := 0; i < len(s); {
		i, w = quoteBlocks(out, s, i, w, html)
		i, w = writeWords(out, s, i, min(i+64, len(s)), w, html)
	}
	out[w] = '"'
	return w + 1
}

// writeWords is writeQuoted for s[i:stop], written to out[w:]. It returns
// the new i, past stop when a multi-byte rune crosses it, and w.
func writeWords(out []byte, s string, i, stop, w int, html bool) (int, int) {
	for i < stop {
		end := stop
		if i+8 <= stop {
			v := load64(s, i)
			m := escWord(v, html)
			if m == 0 {
				binary.LittleEndian.PutUint64(out[w:], v)
				i, w = i+8, w+8
				continue
			}
			// When the only escapes are quotes and backslashes, store the
			// bytes between them as words.
			if m&^(exactZero(v^lsb*'"')|exactZero(v^lsb*'\\')) == 0 {
				prev := 0
				for ; m != 0; m &= m - 1 {
					k := bits.TrailingZeros64(m) >> 3
					binary.LittleEndian.PutUint64(out[w:], v>>(8*prev))
					w += k - prev
					out[w], out[w+1] = '\\', s[i+k]
					w += 2
					prev = k + 1
				}
				binary.LittleEndian.PutUint64(out[w:], v>>(8*prev))
				i, w = i+8, w+8-prev
				continue
			}
			end = i + 8
		}
		for i < end {
			c := s[i]
			switch e := jsonEsc[c]; {
			case e == 0 || (e == 'h' && !html):
				out[w] = c
				i, w = i+1, w+1
			case e == 'x':
				r, size := utf8.DecodeRuneInString(s[i:])
				switch {
				case r == utf8.RuneError && size == 1:
					w += copy(out[w:], `\ufffd`)
				case r == '\u2028' || r == '\u2029':
					w += copy(out[w:], `\u202`)
					out[w] = hexDigits[r&15]
					w++
				default:
					w += copy(out[w:], s[i:i+size])
				}
				i += size
			case e == 'u' || e == 'h':
				_ = out[w+5]
				out[w], out[w+1], out[w+2], out[w+3], out[w+4], out[w+5] = '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&15]
				i, w = i+1, w+6
			default:
				out[w], out[w+1] = '\\', e
				i, w = i+1, w+2
			}
		}
	}
	return i, w
}

// jsonEsc says how appendJSONString writes each byte: 0 as it is, 'u' as
// \u00XX, 'h' as \u00XX when escaping HTML, 'x' for the bytes of multi-byte
// UTF-8, and any other letter as a backslash and that letter.
var jsonEsc = func() (t [256]byte) {
	for c := 0; c < 0x20; c++ {
		t[c] = 'u'
	}
	for c := 0x80; c < 0x100; c++ {
		t[c] = 'x'
	}
	t['\b'], t['\f'], t['\n'], t['\r'], t['\t'] = 'b', 'f', 'n', 'r', 't'
	t['"'], t['\\'] = '"', '\\'
	t['<'], t['>'], t['&'] = 'h', 'h', 'h'
	return t
}()

// escWord marks the bytes of w that may need an escape: control bytes,
// quotes, backslashes, bytes of multi-byte UTF-8, and with html '<', '>'
// and '&'. The lowest mark is exact; marks above it may be wrong.
func escWord(w uint64, html bool) uint64 {
	m := (w-lsb*0x20)&^w | w | zeroBytes(w^lsb*'"') | zeroBytes(w^lsb*'\\')
	if html {
		m |= zeroBytes(w^lsb*'<') | zeroBytes(w^lsb*'>') | zeroBytes(w^lsb*'&')
	}
	return m & msb
}

// quotedLen returns the length of s as appendJSONString writes it. Blocks
// (with AVX2) and words whose only escapes are quotes and backslashes, as
// in JSON text, are counted whole; others go one byte at a time.
func quotedLen(s string, html bool) int {
	n := len(s) + 2
	for i := 0; i < len(s); {
		var k int
		i, k = quoteCountBlocks(s, i, html)
		n += k
		i, k = quotedWords(s, i, min(i+64, len(s)), html)
		n += k
	}
	return n
}

// quotedWords returns the bytes the escapes of s[i:stop] add to
// quotedLen, and the new i, past stop when a multi-byte rune crosses it.
func quotedWords(s string, i, stop int, html bool) (int, int) {
	n := 0
	for i < stop {
		end := stop
		if i+8 <= stop {
			v := load64(s, i)
			other := (v-lsb*0x20)&^v | v
			if html {
				other |= zeroBytes(v^lsb*'<') | zeroBytes(v^lsb*'>') | zeroBytes(v^lsb*'&')
			}
			if other&msb == 0 {
				n += bits.OnesCount64(exactZero(v^lsb*'"') | exactZero(v^lsb*'\\'))
				i += 8
				continue
			}
			end = i + 8
		}
		for i < end {
			switch e := jsonEsc[s[i]]; e {
			case 0:
			case 'h':
				if html {
					n += 5
				}
			case 'u':
				n += 5
			case 'x':
				r, size := utf8.DecodeRuneInString(s[i:])
				if r == utf8.RuneError && size == 1 {
					n += 5
				} else if r == '\u2028' || r == '\u2029' {
					n += 3
				}
				i += size
				continue
			default:
				n++
			}
			i++
		}
	}
	return i, n
}

// exactZero marks exactly the zero bytes of w with their high bit.
func exactZero(w uint64) uint64 { return ^(w&^msb + lsb*0x7f | w) & msb }

// ForEachLine calls iterator for each value of a JSON Lines document, in
// order, until it returns false. Values may be separated by any whitespace.
// Objects and arrays carry their offset in Index; other values have Index 0.
// Like gjson, bytes that cannot start a value are skipped.
func ForEachLine(json string, iterator func(line Result) bool) {
	for i := skipSpace(json, 0); i < len(json); i = skipSpace(json, i) {
		var r Result
		end := i + 1
		switch c := json[i]; {
		case c == '{' || c == '[':
			if end = containerEnd(json, i); end < 0 {
				end = len(json)
			}
			r = Result{Type: JSON, Raw: json[i:end], Index: i}
		case c == '"':
			if end = stringEnd(json, i+1); end < 0 {
				return
			}
			r = stringResult(json, i, end, true)
		case c == 't' || c == 'f' || c == 'n':
			for end < len(json) && json[end] >= 'a' && json[end] <= 'z' {
				end++
			}
			r = Result{Type: True, Raw: json[i:end]}
			if c == 'f' {
				r.Type = False
			} else if c == 'n' {
				r.Type = Null
			}
		case c == '-' || (c >= '0' && c <= '9'):
			for end < len(json) && !delim[json[end]] {
				end++
			}
			r = Result{Type: Number, Raw: json[i:end], Num: parseNum(json[i:end])}
		default:
			i++
			continue
		}
		if r.Type != JSON {
			r.Index = 0
		}
		if !iterator(r) {
			return
		}
		i = end
	}
}
