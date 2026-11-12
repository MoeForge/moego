//go:build darwin || linux

package regexp

import (
	"fmt"
	stdre "regexp"
	"strings"
	"syscall"
	"testing"
)

// guarded returns a copy of s placed directly before (atEnd) or after an
// inaccessible page, so a load past either end of the text faults.
func guarded(t *testing.T, s string, atEnd bool) string {
	t.Helper()
	page := syscall.Getpagesize()
	dataPages := max(1, (len(s)+page-1)/page)
	m, err := syscall.Mmap(-1, 0, (dataPages+2)*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatalf("mmap: %v", err)
	}
	t.Cleanup(func() { syscall.Munmap(m) })
	if err := syscall.Mprotect(m[:page], syscall.PROT_NONE); err != nil {
		t.Fatalf("mprotect: %v", err)
	}
	end := len(m) - page
	if err := syscall.Mprotect(m[end:], syscall.PROT_NONE); err != nil {
		t.Fatalf("mprotect: %v", err)
	}
	var b []byte
	if atEnd {
		b = m[end-len(s) : end : end]
	} else {
		b = m[page : page+len(s) : page+len(s)]
	}
	copy(b, s)
	return bstr(b)
}

// TestGuardPages runs patterns of every plan over texts that touch an
// inaccessible page at either end, so that word loads, vector scans and
// reverse scans that read outside the text fault.
func TestGuardPages(t *testing.T) {
	pats := []string{`abc`, `(?i)error`, `\s+`, `[A-Za-z0-9+/]{16,}={0,2}`, `\d+`, `^claude-.*-thinking$`,
		`(?i)^x-(request|trace)-id$`, `[^@\s]+@\w+\.\w+`, `(?s)<t>(.*?)</t>`, `\w+=[^&]*$`, `x+[^q]*q`,
		`"content":"((?:[^"\\]|\\.)*)"`, `\b(?:Handler|Writer)\d+\b`, `[a-z]{1,40}`, `\p{Han}+`, `.*z$`,
		`^[a-zA-Z0-9_-]+$`, `\.(-\d+)`, `(a|ab)(c|bcd)(d*)`, `[^\n]*\n`, `TODO: handle`, `(?m)^data: (.*)$`}
	texts := []string{"", "a", "ab", "abc", "error", "ERRor x", "  \t\n", "QUJDREVGR0hJSktMTU5PUFFSU1RVVldY==",
		"claude-opus-4-thinking", "X-Request-Id", "me@example.com", "<t>a\nb</t>", "k=v&x=yz", "xxxxxyq",
		`"content":"a\"b"`, "Handler12 Writer3", "日本語テキスト", "abcz", "my_plugin-key_2026", "a.-1.b",
		"abcd", "line\n", "TODO: handle it", "data: x\n"}
	for _, n := range []int{1, 7, 8, 15, 16, 17, 31, 32, 33, 63, 64, 65, 100} {
		texts = append(texts, strings.Repeat("a", n), strings.Repeat("ab1 ", n)[:n], strings.Repeat("\xff", n), strings.Repeat("日", n))
	}
	for _, p := range pats {
		want, got := stdre.MustCompile(p), MustCompile(p)
		for _, s := range texts {
			for _, atEnd := range []bool{true, false} {
				g := guarded(t, s, atEnd)
				name := fmt.Sprintf("%q on %q (atEnd %v)", p, s, atEnd)
				if a, b := got.MatchString(g), want.MatchString(s); a != b {
					t.Errorf("%s: MatchString = %v, want %v", name, a, b)
				}
				if a, b := fmt.Sprint(got.FindAllStringSubmatchIndex(g, -1)), fmt.Sprint(want.FindAllStringSubmatchIndex(s, -1)); a != b {
					t.Errorf("%s: FindAllStringSubmatchIndex = %v, want %v", name, a, b)
				}
				if a, b := got.ReplaceAllString(g, "<$0>"), want.ReplaceAllString(s, "<$0>"); a != b {
					t.Errorf("%s: ReplaceAllString = %q, want %q", name, a, b)
				}
			}
		}
	}
}
