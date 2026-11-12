//go:build amd64 && !purego

package jsonsimd

import "unsafe"

//go:noescape
func escapeAVX2(dst *byte, dn int, src *byte, n, i, w int, html bool, tab *[256]uint64, exp *[256][16]byte) (int, int)

//go:noescape
func unescapeAVX2(dst *byte, dn int, src *byte, n, i, w int, tab *unescapeTables) (int, int, bool)

//go:noescape
func utf8AVX2(src *byte, n int) int

//go:noescape
func escCountAVX2(src *byte, n int, html bool) int

// escapeTab[c] holds the escape of byte c in a JSON string in its low
// bytes and its length in its top byte: for the bytes escapeAVX2 escapes,
// and for the other ASCII bytes the byte itself.
var escapeTab = func() (t [256]uint64) {
	const hex = "0123456789abcdef"
	for c := range t {
		e := []byte{'\\', 'u', '0', '0', hex[c>>4], hex[c&15]}
		if c >= 0x20 && c < 0x80 && c != '<' && c != '>' && c != '&' {
			e = []byte{byte(c)}
		}
		switch c {
		case '"', '\\':
			e = []byte{'\\', byte(c)}
		case '\b':
			e = []byte{'\\', 'b'}
		case '\f':
			e = []byte{'\\', 'f'}
		case '\n':
			e = []byte{'\\', 'n'}
		case '\r':
			e = []byte{'\\', 'r'}
		case '\t':
			e = []byte{'\\', 't'}
		}
		for k, b := range e {
			t[c] |= uint64(b) << (8 * k)
		}
		t[c] |= uint64(len(e)) << 56
	}
	return t
}()

// escapeExp[m] shuffles eight bytes, followed by eight backslashes, into
// the eight bytes with a backslash before each byte whose bit is set in m:
// the VPSHUFB control of escapeAVX2 and quoteAVX2.
var escapeExp = func() (t [256][16]byte) {
	for m := range t {
		k := 0
		for j := range 8 {
			if m>>j&1 != 0 {
				t[m][k] = 8
				k++
			}
			t[m][k] = byte(j)
			k++
		}
		for ; k < 16; k++ {
			t[m][k] = 0x80
		}
	}
	return t
}()

// unescapeTables are the tables of unescapeAVX2, which finds comp 256
// bytes after tab and hex 4352 bytes after it.
type unescapeTables struct {
	// tab[c] is the byte that the escape of a backslash and c stands for,
	// or 0 for c that does not make an escape of two bytes.
	tab [256]byte
	// comp[m] shuffles the bytes of eight whose bit is clear in m to the
	// start.
	comp [256][16]byte
	// hex[c] is the value of the hexadecimal digit c, or 0xffff0000 for c
	// that is none.
	hex [256]uint32
}

var unescapeTabs = func() (t unescapeTables) {
	for c, b := range map[byte]byte{'"': '"', '\\': '\\', '/': '/', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t'} {
		t.tab[c] = b
	}
	for c := range t.hex {
		switch {
		case '0' <= c && c <= '9':
			t.hex[c] = uint32(c - '0')
		case 'a' <= c|0x20 && c|0x20 <= 'f':
			t.hex[c] = uint32(c|0x20-'a') + 10
		default:
			t.hex[c] = 0xffff0000
		}
	}
	for m := range t.comp {
		k := 0
		for j := range 8 {
			if m>>j&1 == 0 {
				t.comp[m][k] = byte(j)
				k++
			}
		}
		for ; k < 16; k++ {
			t.comp[m][k] = 0x80
		}
	}
	return t
}()

// EscapeBlocks writes s[i:], valid UTF-8 without U+2028 or U+2029, to
// out[w:] as JSON string contents, escaping quotes, backslashes, control
// bytes and, with html, '<', '>' and '&' as encoding/json does. It works
// 32 bytes at a time, while 32 bytes of s are left and out has room for
// 40 more bytes, faster with room for 320, and returns the new i, at the
// start of a character, and the new w.
func EscapeBlocks(out []byte, s string, i, w int, html bool) (int, int) {
	if !HaveAVX2 || len(s)-i < 32 || len(out)-w < 40 {
		return i, w
	}
	i, w = escapeAVX2(unsafe.SliceData(out), len(out), unsafe.StringData(s), len(s), i, w, html, &escapeTab, &escapeExp)
	// Bytes of 0x80 and above were copied as they are: step back over
	// those of a character the last block ends inside.
	for i < len(s) && s[i]&0xc0 == 0x80 {
		i--
		w--
	}
	return i, w
}

// UnescapeBlocks writes the value of the JSON string contents s[i:] to
// out[w:], 32 bytes at a time, while 34 bytes of s are left and out has
// room for 32 more bytes, faster with room for 128. It stops at a quote,
// a control byte, and a backslash that does not start an escape of two
// bytes, except that with room for 128 it also decodes an escape of six
// bytes of a character outside the surrogates while 38 bytes of s are left
// from it; bytes of 0x80 and above it copies as they are. It returns the
// new i and w, and whether a byte it copied from s may be 0x80 or above.
func UnescapeBlocks(out []byte, s string, i, w int) (int, int, bool) {
	if !HaveAVX2 || len(s)-i < 34 || len(out)-w < 32 {
		return i, w, false
	}
	return unescapeAVX2(unsafe.SliceData(out), len(out), unsafe.StringData(s), len(s), i, w, &unescapeTabs)
}

// EscapedLen returns the length of s as JSON string contents escaped as
// EscapeBlocks escapes them, when s is valid UTF-8 without U+2028 or
// U+2029, else -1. Without AVX2 it returns -1.
func EscapedLen(s string, html bool) int {
	if !HaveAVX2 || CheckUTF8(s) != 0 {
		return -1
	}
	n := len(s) &^ 31
	m := len(s)
	if n > 0 {
		m += escCountAVX2(unsafe.StringData(s), n, html)
	}
	for _, c := range []byte(s[n:]) {
		if c < 0x80 && (html || c != '<' && c != '>' && c != '&') {
			m += int(escapeTab[c]>>56) - 1
		}
	}
	return m
}

// CheckUTF8 returns 0 when s is valid UTF-8 without U+2028 or U+2029,
// else UTF8Invalid, UTF8LineSep or both.
func CheckUTF8(s string) int {
	n := len(s) &^ 31
	if !HaveAVX2 || n == 0 {
		return checkUTF8(s)
	}
	r := utf8AVX2(unsafe.StringData(s), n)
	// The kernel leaves a character that starts in the last three bytes
	// of its last block and does not end there: check from its first byte.
	k := n
	for j := n - 1; j >= n-3; j-- {
		if c := s[j]; c >= 0xc0 {
			k = j
			break
		} else if c < 0x80 {
			break
		}
	}
	if k < len(s) {
		r |= checkUTF8(s[k:])
	}
	return r
}
