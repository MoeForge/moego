package ojson

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"strconv"
	"unicode/utf8"
)

// piece is an encoded value split into strings, so a string value that needs
// no escaping is written straight from the caller's memory. Parts 0 and 4
// hold the containers built around the value for missing path components.
type piece [5]string

func (p *piece) len() int { return len(p[0]) + len(p[1]) + len(p[2]) + len(p[3]) + len(p[4]) }

func (p *piece) appendTo(dst []byte) []byte {
	for _, s := range p {
		dst = append(dst, s...)
	}
	return dst
}

// join returns the piece as one string, copying only when it has several
// non-empty parts.
func (p *piece) join() string {
	only, n := "", 0
	for _, s := range p {
		if s != "" {
			only = s
			n++
		}
	}
	if n <= 1 {
		return only
	}
	return bytesView(p.appendTo(make([]byte, 0, p.len())))
}

// wrap places v inside the containers pre and post.
func wrap(v piece, pre, post string) piece {
	v[0], v[4] = pre, post
	return v
}

func textPiece(s string) piece { return piece{1: s} }

// encodeValue encodes v the way sjson.Set does: strings as JSON strings,
// floats in the shortest 'f' form, other types through encoding/json. A
// string that needs no escaping is used in place; other short results go
// into buf.
func encodeValue(v interface{}, buf []byte) (piece, error) {
	var s string
	switch v := v.(type) {
	case string:
		s = v
	case []byte:
		s = bytesView(v)
	default:
		if b, ok := appendScalar(buf, v); ok {
			return textPiece(bytesView(b)), nil
		}
		out, err := json.Marshal(v)
		if err != nil {
			return piece{}, err
		}
		return textPiece(bytesView(out)), nil
	}
	if !needsEscape(s) {
		return piece{1: `"`, 2: s, 3: `"`}, nil
	}
	return textPiece(bytesView(appendString(buf, s))), nil
}

// appendScalar appends the encoding of v when it is nil, a bool or a
// number, and reports whether it was.
func appendScalar(dst []byte, v interface{}) ([]byte, bool) {
	switch v := v.(type) {
	case nil:
		return append(dst, "null"...), true
	case bool:
		if v {
			return append(dst, "true"...), true
		}
		return append(dst, "false"...), true
	case int:
		return strconv.AppendInt(dst, int64(v), 10), true
	case int8:
		return strconv.AppendInt(dst, int64(v), 10), true
	case int16:
		return strconv.AppendInt(dst, int64(v), 10), true
	case int32:
		return strconv.AppendInt(dst, int64(v), 10), true
	case int64:
		return strconv.AppendInt(dst, v, 10), true
	case uint:
		return strconv.AppendUint(dst, uint64(v), 10), true
	case uint8:
		return strconv.AppendUint(dst, uint64(v), 10), true
	case uint16:
		return strconv.AppendUint(dst, uint64(v), 10), true
	case uint32:
		return strconv.AppendUint(dst, uint64(v), 10), true
	case uint64:
		return strconv.AppendUint(dst, v, 10), true
	case float32:
		return appendFloat(dst, float64(v)), true
	case float64:
		return appendFloat(dst, v), true
	}
	return dst, false
}

// decimalPow10 holds the powers of ten appendFloat tries.
var decimalPow10 = [...]float64{1, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8}

// appendFloat appends f as strconv.AppendFloat(dst, f, 'f', -1, 64) does,
// in the shortest form that reads back as f. Most numbers in requests, such
// as 0.7 or 1024, have a few decimals and a short route: f is r/10^k for
// the smallest k at which the integer nearest f*10^k gives back f exactly
// when divided by 10^k. Below 10^15 that decimal is the only one with k
// places that reads back as f, and none with fewer places does, so it is
// the shortest form.
func appendFloat(dst []byte, f float64) []byte {
	a := math.Abs(f)
	if a >= 1e-8 && a < 1e15 {
		for k, p := range decimalPow10 {
			m := a * p
			if m >= 1e15 {
				break
			}
			// m is within m*2^-52 of r when r/10^k reads back as f, so the
			// first test only skips the division for other k.
			r := math.RoundToEven(m)
			if math.Abs(m-r) <= m*0x1p-50 && r/p == a {
				return appendDecimal(dst, f < 0, uint64(r), k)
			}
		}
	}
	return strconv.AppendFloat(dst, f, 'f', -1, 64)
}

// appendDecimal appends r/10^k with k decimals, negated when neg is set.
func appendDecimal(dst []byte, neg bool, r uint64, k int) []byte {
	var b [24]byte
	i := len(b)
	for n := 0; r > 0 || n <= k; n++ {
		if n == k && k > 0 {
			i--
			b[i] = '.'
		}
		i--
		b[i] = byte('0' + r%10)
		r /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return append(dst, b[i:]...)
}

// appendQuoted appends s as a JSON string, as sjson writes strings and keys.
func appendQuoted(dst []byte, s string) []byte {
	if needsEscape(s) {
		return appendString(dst, s)
	}
	dst = append(dst, '"')
	dst = append(dst, s...)
	return append(dst, '"')
}

const (
	lsb = 0x0101010101010101
	msb = 0x8080808080808080
)

// needsEscape reports whether s must go through the full string encoder.
// sjson writes printable ASCII without '"' and '\\' as is. Any other byte,
// including valid UTF-8, sends the whole string through an encoder that
// matches encoding/json with HTML escaping. ASCII is checked eight bytes at a
// time; a flagged word always holds at least one such byte.
func needsEscape(s string) bool {
	i := 0
	for ; i+8 <= len(s); i += 8 {
		w := binary.LittleEndian.Uint64(stringBytes(s[i : i+8]))
		q := w ^ ('"' * lsb)
		bs := w ^ ('\\' * lsb)
		if ((w-0x20*lsb)|w|((q-lsb)&^q)|((bs-lsb)&^bs))&msb != 0 {
			return true
		}
	}
	for ; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c >= utf8.RuneSelf || c == '"' || c == '\\' {
			return true
		}
	}
	return false
}

const hexDigits = "0123456789abcdef"

// appendString appends s as a quoted JSON string, escaping like
// encoding/json with HTML escaping: '<', '>' and '&' become \u003c, \u003e
// and \u0026, invalid UTF-8 bytes become \ufffd, and U+2028 and U+2029 are
// escaped.
func appendString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if c >= 0x20 && c != '"' && c != '\\' && c != '<' && c != '>' && c != '&' {
				i++
				continue
			}
			dst = append(dst, s[start:i]...)
			switch c {
			case '"', '\\':
				dst = append(dst, '\\', c)
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default: // control bytes, '<', '>' and '&'
				dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&15])
			}
			i++
			start = i
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			dst = append(append(dst, s[start:i]...), `\ufffd`...)
		case r == '\u2028' || r == '\u2029':
			dst = append(append(dst, s[start:i]...), '\\', 'u', '2', '0', '2', hexDigits[r&15])
		default:
			i += n
			continue
		}
		i += n
		start = i
	}
	return append(append(dst, s[start:]...), '"')
}
