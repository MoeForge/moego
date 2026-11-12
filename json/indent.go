package json

import (
	"bytes"
	"math/bits"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// Valid reports whether data is a valid JSON encoding.
func Valid(data []byte) bool {
	return jsonsimd.Validate(bytesView(data), maxNestingDepth, nil)
}

// Compact appends to dst the JSON-encoded src with insignificant space
// characters elided.
func Compact(dst *bytes.Buffer, src []byte) error {
	dst.Grow(len(src))
	b := dst.AvailableBuffer()
	b, err := appendCompactChecked(b, src, false)
	dst.Write(b)
	return err
}

// HTMLEscape appends to dst the JSON-encoded src with <, >, &, U+2028 and
// U+2029 characters inside string literals changed to \u003c, \u003e,
// \u0026, \u2028, \u2029 so that the JSON will be safe to embed inside
// HTML <script> tags. For historical reasons, web browsers don't honor
// standard HTML escaping within <script> tags, so an alternative JSON
// encoding must be used.
func HTMLEscape(dst *bytes.Buffer, src []byte) {
	dst.Grow(len(src))
	dst.Write(appendHTMLEscape(dst.AvailableBuffer(), bytesView(src)))
}

// Indent appends to dst an indented form of the JSON-encoded src. Each
// element in a JSON object or array begins on a new, indented line
// beginning with prefix followed by one or more copies of indent according
// to the indentation nesting. The data appended to dst does not begin with
// the prefix nor any indentation, to make it easier to embed inside other
// formatted JSON data. Although leading space characters (space, tab,
// carriage return, newline) at the beginning of src are dropped, trailing
// space characters at the end of src are preserved and copied to dst. For
// example, if src has no trailing spaces, neither will dst; if src ends in
// a trailing newline, so will dst.
func Indent(dst *bytes.Buffer, src []byte, prefix, indent string) error {
	dst.Grow(indentedSize(src, len(prefix), len(indent)))
	b := dst.AvailableBuffer()
	b, err := appendIndent(b, src, prefix, indent)
	dst.Write(b)
	return err
}

// appendCompactChecked appends src without insignificant space, with
// escape also '<', '>', '&', U+2028 and U+2029 escaped. When src is not
// valid JSON it returns dst unchanged and the syntax error.
func appendCompactChecked(dst, src []byte, escape bool) ([]byte, error) {
	s := bytesView(src)
	ok, spaced, escape := jsonsimd.ValidateSpaced(s, maxNestingDepth, escape)
	if !ok {
		return dst, compactSyntaxError(src)
	}
	if !spaced && !escape {
		return append(dst, src...), nil
	}
	return appendCompactValid(dst, src, escape), nil
}

// appendCompactValid is appendCompactChecked for valid src.
func appendCompactValid(dst, src []byte, escape bool) []byte {
	s := bytesView(src)
	if !escape && jsonsimd.HaveAVX2 {
		return jsonsimd.AppendCompact(dst, s)
	}
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '"':
			end := jsonsimd.StringEnd(s, i+1) + 1
			if escape {
				dst = append(dst, s[start:i]...)
				dst = appendHTMLEscape(dst, s[i:end])
				start = end
			}
			i = end
		case c <= ' ':
			dst = append(dst, s[start:i]...)
			for i++; i < len(s) && s[i] <= ' '; i++ {
			}
			start = i
		default:
			i++
		}
	}
	return append(dst, s[start:]...)
}

// indentedSize estimates the size of the indented form of src.
func indentedSize(src []byte, prefix, indent int) int {
	return len(src) + len(src)/4*(1+prefix+indent)
}

func appendIndent(dst, src []byte, prefix, indent string) ([]byte, error) {
	if out, ok := appendIndentChecked(dst, src, prefix, indent); ok {
		return out, nil
	}
	if !jsonsimd.Validate(bytesView(src), maxNestingDepth, nil) {
		return dst, indentSyntaxError(src)
	}
	return appendIndentValid(dst, src, prefix, indent), nil
}

// indentKinds is the deepest nesting appendIndentChecked follows.
const indentKinds = 256

// appendIndentChecked is appendIndentValid that validates src as it goes,
// in one pass. It reports false when src is not valid or nests deeper than
// indentKinds.
func appendIndentChecked(dst, src []byte, prefix, indent string) ([]byte, bool) {
	s := bytesView(src)
	orig := len(dst)
	var buf [256]byte
	lines := append(append(buf[:0], '\n'), prefix...)
	base := len(lines)
	deep := 0
	if len(lines)+indentLines*len(indent) <= len(buf) {
		for range indentLines {
			lines = append(lines, indent...)
		}
		deep = indentLines
	}
	newline := func(dst []byte, depth int) []byte {
		if depth <= deep {
			return append(dst, lines[:base+depth*len(indent)]...)
		}
		return appendNewline(dst, prefix, indent, depth)
	}
	// Bit k of kinds is set when the container at depth k is an object.
	var kinds [indentKinds / 64]uint64
	depth, i, j := 0, 0, 0
	var c byte
	for i < len(s) && isSpace[s[i]] {
		i++
	}
value:
	if i >= len(s) {
		goto bad
	}
	switch c = s[i]; c {
	case '{', '[':
		goto open
	case '"':
		if j = indentString(s, i); j < 0 {
			goto bad
		}
	case 't':
		if j = i + 4; len(s) < j || s[i:j] != "true" {
			goto bad
		}
	case 'f':
		if j = i + 5; len(s) < j || s[i:j] != "false" {
			goto bad
		}
	case 'n':
		if j = i + 4; len(s) < j || s[i:j] != "null" {
			goto bad
		}
	default:
		if j = numberSpan(s, i); j < 0 {
			goto bad
		}
	}
	dst = append(dst, s[i:j]...)
	i = j
	goto after
open: // s[i] is c, '{' or '['
	if depth == indentKinds {
		goto bad
	}
	if c == '{' {
		kinds[depth>>6] |= 1 << (depth & 63)
	} else {
		kinds[depth>>6] &^= 1 << (depth & 63)
	}
	dst = append(dst, c)
	for i++; i < len(s) && isSpace[s[i]]; i++ {
	}
	if i >= len(s) {
		goto bad
	}
	if s[i] == c+2 { // '}' or ']'
		dst = append(dst, c+2)
		i++
		goto after
	}
	depth++
	dst = newline(dst, depth)
	if c == '[' {
		goto value
	}
key:
	if i >= len(s) || s[i] != '"' {
		goto bad
	}
	if j = indentString(s, i); j < 0 {
		goto bad
	}
	dst = append(dst, s[i:j]...)
	for i = j; i < len(s) && isSpace[s[i]]; i++ {
	}
	if i >= len(s) || s[i] != ':' {
		goto bad
	}
	dst = append(dst, ':', ' ')
	for i++; i < len(s) && isSpace[s[i]]; i++ {
	}
	goto value
after: // a value ends at i, inside depth containers
	if depth == 0 {
		goto end
	}
	for ; i < len(s) && isSpace[s[i]]; i++ {
	}
	if i >= len(s) {
		goto bad
	}
	c = s[i]
	i++
	if c == ',' {
		dst = append(dst, ',')
		dst = newline(dst, depth)
		for ; i < len(s) && isSpace[s[i]]; i++ {
		}
		if kinds[(depth-1)>>6]>>((depth-1)&63)&1 != 0 {
			goto key
		}
		goto value
	}
	if kinds[(depth-1)>>6]>>((depth-1)&63)&1 != 0 {
		if c != '}' {
			goto bad
		}
	} else if c != ']' {
		goto bad
	}
	depth--
	dst = newline(dst, depth)
	dst = append(dst, c)
	goto after
end:
	// Only space may follow the value, and it is kept.
	for j = i; j < len(s); j++ {
		if !isSpace[s[j]] {
			goto bad
		}
	}
	return appendTrailing(dst, s[i:], prefix, indent), true
bad:
	return dst[:orig], false
}

// indentString returns the index after the string whose opening quote is
// at q, or -1 when it is not valid.
func indentString(s string, q int) int {
	if q+9 <= len(s) {
		// A string whose first stop byte, in its first eight, is its end.
		w := load64(s, q+1)
		x, b := w^('"'*lsb), w^('\\'*lsb)
		if m := ((x-lsb)&^x | (b-lsb)&^b | (w-0x20*lsb)&^w) & msb; m != 0 {
			if j := q + 1 + bits.TrailingZeros64(m)>>3; s[j] == '"' {
				return j + 1
			}
		}
	}
	end := jsonsimd.StringEnd(s, q+1)
	if end < 0 {
		return -1
	}
	return end + 1
}

// numberSpan returns the index after the number at s[i], or -1 when no
// valid number starts there.
func numberSpan(s string, i int) int {
	if i < len(s) && s[i] == '-' {
		i++
	}
	j := digitsEnd(s, i)
	if j == i || s[i] == '0' && j > i+1 {
		return -1
	}
	if j < len(s) && s[j] == '.' {
		k := digitsEnd(s, j+1)
		if k == j+1 {
			return -1
		}
		j = k
	}
	if j < len(s) && s[j]|0x20 == 'e' {
		j++
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		k := digitsEnd(s, j)
		if k == j {
			return -1
		}
		j = k
	}
	return j
}

// indentLines is the depth up to which appendIndentValid writes line
// breaks from one prepared run.
const indentLines = 16

func appendNewline(dst []byte, prefix, indent string, depth int) []byte {
	dst = append(dst, '\n')
	dst = append(dst, prefix...)
	for i := 0; i < depth; i++ {
		dst = append(dst, indent...)
	}
	return dst
}

// appendIndentValid is appendIndent for valid src. Space before and inside
// the value is dropped; space after it is kept.
func appendIndentValid(dst, src []byte, prefix, indent string) []byte {
	i := 0
	for i < len(src) && src[i] <= ' ' {
		i++
	}
	// lines holds a newline, the prefix and indentLines copies of the
	// indent, whose start is the line break of any of those depths.
	var buf [256]byte
	lines := append(append(buf[:0], '\n'), prefix...)
	base := len(lines)
	deep := 0
	if len(lines)+indentLines*len(indent) <= len(buf) {
		for range indentLines {
			lines = append(lines, indent...)
		}
		deep = indentLines
	}
	newline := func(dst []byte, depth int) []byte {
		if depth <= deep {
			return append(dst, lines[:base+depth*len(indent)]...)
		}
		return appendNewline(dst, prefix, indent, depth)
	}
	depth := 0
	needIndent := false
	for ; i < len(src); i++ {
		c := src[i]
		if c <= ' ' {
			if depth == 0 && !needIndent {
				// After the top-level value.
				dst = appendTrailing(dst, bytesView(src[i:]), prefix, indent)
				break
			}
			continue
		}
		if needIndent && c != '}' && c != ']' {
			needIndent = false
			depth++
			dst = newline(dst, depth)
		}
		switch c {
		case '"':
			end := validStringEnd(bytesView(src), i+1)
			dst = append(dst, src[i:end+1]...)
			i = end
		case '{', '[':
			needIndent = true
			dst = append(dst, c)
		case ',':
			dst = append(dst, c)
			dst = newline(dst, depth)
		case ':':
			dst = append(dst, c, ' ')
		case '}', ']':
			if needIndent {
				needIndent = false
			} else {
				depth--
				dst = newline(dst, depth)
			}
			dst = append(dst, c)
		default:
			dst = append(dst, c)
		}
	}
	return dst
}

// appendTrailing appends ws, the space after the top-level value. On
// encoding/json/v2, when the prefix or the indent holds a byte other than a
// space or a tab, the spaces that start a line become as many bytes of the
// prefix followed by copies of the indent. Where that needs more bytes than
// a prefix and empty indent have, the spaces past the prefix are kept;
// encoding/json does not return there.
func appendTrailing(dst []byte, ws string, prefix, indent string) []byte {
	if !jsonv2 || onlyBlanks(prefix) && onlyBlanks(indent) {
		return append(dst, ws...)
	}
	for i := 0; i < len(ws); {
		c := ws[i]
		dst = append(dst, c)
		i++
		if c != '\n' {
			continue
		}
		n := 0
		for i+n < len(ws) && ws[i+n] == ' ' {
			n++
		}
		k := min(n, len(prefix))
		dst = append(dst, prefix[:k]...)
		for k < n && indent != "" {
			m := min(n-k, len(indent))
			dst = append(dst, indent[:m]...)
			k += m
		}
		i += k
	}
	return dst
}

// onlyBlanks reports whether s holds only spaces and tabs.
func onlyBlanks(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return false
		}
	}
	return true
}

// validStringEnd returns the index of the quote that closes the string
// whose contents start at s[i], in valid JSON: past each backslash, the
// next byte is not a quote that ends it. A long string goes on in blocks.
func validStringEnd(s string, i int) int {
	for {
		for n := 0; i+8 <= len(s); n++ {
			if n == 8 {
				return jsonsimd.StringEnd(s, i)
			}
			w := load64(s, i)
			q := w ^ ('"' * lsb)
			b := w ^ ('\\' * lsb)
			if m := ((q-lsb)&^q | (b-lsb)&^b) & msb; m != 0 {
				i += bits.TrailingZeros64(m) >> 3
				goto found
			}
			i += 8
		}
		for s[i] != '"' && s[i] != '\\' {
			i++
		}
	found:
		if s[i] == '"' {
			return i
		}
		i += 2
	}
}
