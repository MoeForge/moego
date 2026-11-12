package jsonsimd

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// escapeRef is the escape of s, valid UTF-8 without U+2028 or U+2029, as
// JSON string contents.
func escapeRef(s string, html bool) string {
	const hex = "0123456789abcdef"
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' || c == '\\':
			b = append(b, '\\', c)
		case c == '\b':
			b = append(b, '\\', 'b')
		case c == '\f':
			b = append(b, '\\', 'f')
		case c == '\n':
			b = append(b, '\\', 'n')
		case c == '\r':
			b = append(b, '\\', 'r')
		case c == '\t':
			b = append(b, '\\', 't')
		case c < 0x20 || html && (c == '<' || c == '>' || c == '&'):
			b = append(b, '\\', 'u', '0', '0', hex[c>>4], hex[c&15])
		default:
			b = append(b, c)
		}
	}
	return string(b)
}

// randText returns n pieces of text drawn from pieces.
func randText(r *rand.Rand, n int, pieces []string) string {
	var b strings.Builder
	for range n {
		b.WriteString(pieces[r.Intn(len(pieces))])
	}
	return b.String()
}

var textPieces = []string{
	"a", "b", "c", "d", " ", "0", "abcdefgh", "the quick brown fox ", "é", "中文", "😀", "“", "—",
	"\"", "\\", "\n", "\t", "\r", "\b", "\f", "\x00", "\x01", "\x1f", "\x7f", "<", ">", "&", "/",
}

// shortEscapePieces need no escape of six bytes, but for '<', '>' and '&'
// with HTML escaping.
var shortEscapePieces = []string{
	"a", " ", "abcdefgh", "the quick brown fox ", "é", "中文", "😀", "\"", "\\", "\n", "\t", "\r", "\b", "\f", "/", "<", ">", "&",
}

func repeats() int {
	if raceEnabled {
		return 300
	}
	return 3000
}

func TestEscapeBlocks(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for k := 0; k < repeats(); k++ {
		pieces := textPieces
		if k%3 == 2 {
			pieces = shortEscapePieces
		}
		s := strings.Repeat("x", r.Intn(40)) + randText(r, r.Intn(120), pieces)
		html := k&1 != 0
		w0 := r.Intn(8)
		out := make([]byte, w0+r.Intn(8*len(s)+64))
		i, w := EscapeBlocks(out, s, 0, w0, html)
		if got, want := string(out[w0:w]), escapeRef(s[:i], html); got != want {
			t.Fatalf("EscapeBlocks(%q, html %v) to %d: %q, want %q", s, html, i, got, want)
		}
		if i < len(s) && !utf8.RuneStart(s[i]) {
			t.Fatalf("EscapeBlocks(%q): stops inside a character at %d", s, i)
		}
		if HaveAVX2 && len(s)-i >= 35 && len(out)-w >= 43 {
			t.Fatalf("EscapeBlocks(%q): stops at %d with room %d", s, i, len(out)-w)
		}
	}
}

// unescapeRef is the value of contents s that hold only escapes of two
// bytes and escapes of six bytes of characters outside the surrogates.
func unescapeRef(s string) (string, bool) {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b = append(b, c)
			continue
		}
		if i+1 < len(s) && s[i+1] == 'u' {
			if i+6 > len(s) {
				return "", false
			}
			r, err := strconv.ParseUint(s[i+2:i+6], 16, 16)
			if err != nil || 0xd800 <= r && r < 0xe000 {
				return "", false
			}
			b = utf8.AppendRune(b, rune(r))
			i += 5
			continue
		}
		if i+1 == len(s) || unescapeByte(s[i+1]) == 0 {
			return "", false
		}
		b = append(b, unescapeByte(s[i+1]))
		i++
	}
	return string(b), true
}

func unescapeByte(c byte) byte {
	switch c {
	case '"', '\\', '/':
		return c
	case 'b':
		return '\b'
	case 'f':
		return '\f'
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 't':
		return '\t'
	}
	return 0
}

var escapedPieces = []string{
	"a", "b", " ", "abcdefgh", "the quick brown fox ", "é", "中文", "😀", "\x7f",
	`\"`, `\\`, `\/`, `\n`, `\t`, `\r`, `\b`, `\f`, `\n\n`,
	`\u0026`, `\u00e9`, `\u4e2d`, `\u3002\uff0c`, `\uFFFF`, `\uAbCd`, `\u0000`, `\u007f`, `\u0080`, `\u07ff`, `\u0800`,
}

func TestUnescapeBlocks(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	stops := []string{`"`, "\x00", "\x1f", `\ud83d\ude00`, `\udc00`, `\u00g9`, `\u12`, `\x`, `\`}
	for k := 0; k < repeats(); k++ {
		s := strings.Repeat("y", r.Intn(40)) + randText(r, r.Intn(100), escapedPieces)
		if k&1 != 0 {
			s += stops[r.Intn(len(stops))] + randText(r, r.Intn(20), escapedPieces)
		}
		w0 := r.Intn(8)
		out := make([]byte, w0+r.Intn(len(s)+64))
		i, w, hi := UnescapeBlocks(out, s, 0, w0)
		want, ok := unescapeRef(s[:i])
		if !ok || string(out[w0:w]) != want {
			t.Fatalf("UnescapeBlocks(%q) to %d: %q, want %q", s, i, out[w0:w], want)
		}
		if !hi && strings.IndexFunc(s[:i], func(c rune) bool { return c >= 0x80 }) >= 0 {
			t.Fatalf("UnescapeBlocks(%q) to %d: no high bytes reported", s, i)
		}
		if HaveAVX2 && len(s)-i >= 34 && len(out)-w >= 32 {
			switch c := s[i]; {
			case c == '"' || c < 0x20:
			case c == '\\' && unescapeByte(s[i+1]) == 0:
			default:
				t.Fatalf("UnescapeBlocks(%q): stops at %d (%q) with room %d", s, i, c, len(out)-w)
			}
		}
	}
}

// TestUnescapeBlocksSix checks that the kernel decodes an escape of six
// bytes at each place in a block while 38 bytes are left from it.
func TestUnescapeBlocksSix(t *testing.T) {
	if !HaveAVX2 {
		t.Skip("no AVX2 kernel")
	}
	for _, e := range []string{`\u0026`, `\u00e9`, `\u4e2d`, `\uFFFF`, `\u0000`} {
		for k := range 96 {
			s := strings.Repeat("z", k) + e + `\n` + strings.Repeat("z", 100)
			out := make([]byte, len(s)+128)
			i, w, _ := UnescapeBlocks(out, s, 0, 0)
			if want, ok := unescapeRef(s[:i]); !ok || string(out[:w]) != want || i <= k {
				t.Fatalf("UnescapeBlocks(%q) to %d: %q, want past %d", s, i, out[:w], k)
			}
		}
	}
}

func TestCheckUTF8(t *testing.T) {
	pieces := []string{
		"a", "abcdefghijklmnopqrstuvwxyz", "é", "中", "😀", "\u2028", "\u2029", "“", "\u2027", "\u202a",
		"\x80", "\xbf", "\xc0\x80", "\xc1\xbf", "\xc2", "\xe0\x80\x80", "\xe0\xa0", "\xed\xa0\x80", "\xed\x9f\xbf",
		"\xf0\x80\x80\x80", "\xf0\x90\x80", "\xf4\x90\x80\x80", "\xf4\x8f\xbf\xbf", "\xf5\x80\x80\x80", "\xff", "\xe2\x80",
	}
	check := func(s string) {
		t.Helper()
		if got, want := CheckUTF8(s), checkUTF8(s); got != want {
			t.Fatalf("CheckUTF8(%q) = %d, want %d", s, got, want)
		}
	}
	r := rand.New(rand.NewSource(3))
	for k := 0; k < 4*repeats(); k++ {
		check(strings.Repeat("z", r.Intn(70)) + randText(r, r.Intn(40), pieces))
	}
	// Every pair of bytes, and every piece, around the end of a block.
	base := strings.Repeat("q", 96)
	for _, at := range []int{0, 29, 30, 31, 32, 62, 63, 64, 94} {
		for _, p := range pieces {
			check(base[:at] + p + base[at:])
			check(base[:at] + p)
		}
		if raceEnabled && at != 31 {
			continue
		}
		for c := 0x80; c < 0x10000; c++ {
			check(base[:at] + string([]byte{byte(c >> 8), byte(c)}) + base[at:])
		}
	}
}

func TestEscapedLen(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	check := func(s string, html bool) {
		t.Helper()
		want := len(escapeRef(s, html))
		if CheckUTF8(s) != 0 || !HaveAVX2 {
			want = -1
		}
		if got := EscapedLen(s, html); got != want {
			t.Fatalf("EscapedLen(%q, html %v) = %d, want %d", s, html, got, want)
		}
	}
	for k := 0; k < repeats(); k++ {
		s := strings.Repeat("x", r.Intn(40)) + randText(r, r.Intn(400), textPieces)
		if k%5 == 0 {
			s += "\xff"
		} else if k%5 == 1 {
			s = " " + s
		}
		check(s, k&1 != 0)
	}
	// Each byte at each place of a block, and enough blocks to sum the
	// byte counters more than once.
	for c := 0; c < 0x80; c++ {
		for _, at := range []int{0, 1, 15, 16, 31, 32, 63} {
			s := []byte(strings.Repeat("y", 64))
			s[at] = byte(c)
			check(string(s), false)
			check(string(s), true)
		}
	}
	long := strings.Repeat("\x01\"<a&\n\x1f\\", 32*300)
	check(long, false)
	check(long, true)
}
