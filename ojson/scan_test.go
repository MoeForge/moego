package ojson

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

func TestContainerEnd(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int
	}{
		{`[]`, 2},
		{`{}`, 2},
		{`[1,2]`, 5},
		{`{"a":[1,{"b":"]"}]}`, 19},
		{`{"a":"x\"}"}`, 12},
		{`[[[[[[[[[[1]]]]]]]]]]`, 21},
		{`{"k":"0123456789abcdef"} tail`, 24},
		{`[1,2`, -1},
		{`{"a":"`, -1},
		{`[`, -1},
		{`{"a":[1,2,3,4,5,6,7,8,9]}`, 25},
	} {
		if got := containerEnd(c.in, 0); got != c.want {
			t.Errorf("containerEnd(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// unescapeRef is unescape one byte at a time.
func unescapeRef(dst []byte, raw string) ([]byte, bool) {
	for i := 0; i < len(raw); {
		if raw[i] != '\\' {
			dst = append(dst, raw[i])
			i++
			continue
		}
		if i+1 >= len(raw) {
			return dst, false
		}
		switch c := raw[i+1]; c {
		case '"', '\\', '/':
			dst = append(dst, c)
		case 'b', 'f', 'n', 'r', 't':
			dst = append(dst, map[byte]byte{'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t'}[c])
		case 'u':
			r, ok := hex4(raw, i+2)
			if !ok {
				return dst, false
			}
			i += 6
			if utf16.IsSurrogate(r) {
				if r2, ok := hex4(raw, i+2); ok && i+1 < len(raw) && raw[i] == '\\' && raw[i+1] == 'u' {
					r = utf16.DecodeRune(r, r2)
					i += 6
				} else {
					r = utf8.RuneError
				}
			}
			dst = utf8.AppendRune(dst, r)
			continue
		default:
			return dst, false
		}
		i += 2
	}
	return dst, true
}

func TestUnescapeRef(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	parts := []string{"a", "bc", "xyz01234", `\"`, `\\`, `\/`, `\n`, `\t`, `\b`, `\f`, `\r`, `A`, `é`, `中`,
		`😀`, `\ud83d`, `\ude00x`, `\ud83dabcdef`, `\u12`, `\x`, `\`, "é", "\x00", "longer run of plain text"}
	for n := 0; n < 200000; n++ {
		var b strings.Builder
		for k := r.Intn(12); k > 0; k-- {
			b.WriteString(parts[r.Intn(len(parts))])
		}
		raw := b.String()
		pre := []byte("pre")[:r.Intn(4)]
		want, wok := unescapeRef(append([]byte(nil), pre...), raw)
		got, gok := unescape(append([]byte(nil), pre...), raw)
		if string(got) != string(want) || gok != wok {
			t.Fatalf("unescape(%q, %q) = %q, %v; want %q, %v", pre, raw, got, gok, want, wok)
		}
	}
}

// stringEndRef is stringEnd one byte at a time.
func stringEndRef(s string, i int) int {
	for ; i < len(s); i++ {
		switch s[i] {
		case '"':
			return i + 1
		case '\\':
			i++
		}
	}
	return -1
}

// TestScanStringRef mixes long plain runs, where scanString uses IndexByte,
// with dense escaped quotes and backslash runs, where it reads words.
func TestScanStringRef(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	parts := []string{"a", `\"`, `\\`, `\\\"`, `\\\\`, `\n`, strings.Repeat("x", 40), strings.Repeat("y", 7), `"`, `\`}
	for n := 0; n < 300000; n++ {
		var b strings.Builder
		for k := r.Intn(30); k > 0; k-- {
			b.WriteString(parts[r.Intn(len(parts))])
		}
		s := b.String()
		i := 0
		if s != "" {
			i = r.Intn(len(s))
		}
		want := stringEndRef(s, i)
		got, esc := scanString(s, i)
		if got != want {
			t.Fatalf("scanString(%q, %d) = %d, want %d", s, i, got, want)
		}
		if want > 0 && !esc && strings.IndexByte(s[i:want], '\\') >= 0 {
			t.Fatalf("scanString(%q, %d): esc false with a backslash", s, i)
		}
	}
}
