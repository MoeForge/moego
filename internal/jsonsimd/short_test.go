package jsonsimd

import (
	"strings"
	"testing"
)

// plainRef reports whether no byte of s needs an escape in a JSON string,
// counting bytes of 0x80 and above, and with html '<', '>' and '&'.
func plainRef(s string, html bool) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c >= 0x80 || c == '"' || c == '\\' || html && (c == '<' || c == '>' || c == '&') {
			return false
		}
	}
	return true
}

// checkShort compares CopyShort with plainRef on s, and checks the copy.
func checkShort(t *testing.T, s string) {
	t.Helper()
	for _, html := range []bool{false, true} {
		dst := make([]byte, 80)
		ok := CopyShort(dst, s, html)
		want := plainRef(s, html)
		if ok && (!want || string(dst[:len(s)]) != s) {
			t.Fatalf("CopyShort(%q, %v) = true, copy %q, plain %v", s, html, dst[:len(s)], want)
		}
		if !ok && want && HaveAVX2 && len(s) > 0 && len(s) >= 32 {
			t.Fatalf("CopyShort(%q, %v) = false for a plain string", s, html)
		}
	}
}

// TestCopyShort puts each byte that needs an escape, and some that do not,
// at each position of strings of every length CopyShort takes.
func TestCopyShort(t *testing.T) {
	special := []byte{0, 1, 0x1f, 0x20, '"', '\\', '<', '>', '&', '/', 0x7f, 0x80, 0xc3, 0xe2, 0xff, 'a'}
	buf := make([]byte, 4096+64)
	for n := 0; n < 64; n++ {
		base := []byte(strings.Repeat("abcdefgh", 8)[:n])
		checkShort(t, string(base))
		for i := 0; i < n; i++ {
			for _, c := range special {
				b := append([]byte(nil), base...)
				b[i] = c
				checkShort(t, string(b))
			}
		}
		// A plain string at every offset of a page, as reads near its end
		// must not cross into the next page.
		for _, at := range []int{0, 7, 4096 - 32, 4096 - 31, 4096 - n} {
			copy(buf[at:], base)
			s := string(buf[at : at+n])
			checkShort(t, s)
		}
	}
}
