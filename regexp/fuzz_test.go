package regexp

import (
	"fmt"
	stdre "regexp"
	"slices"
	"testing"
	"unicode/utf8"
)

// FuzzDiff compares indexes, submatches and replacements with the standard
// library for fuzzed patterns and texts, including texts cut inside UTF-8
// sequences.
func FuzzDiff(f *testing.F) {
	seeds := []struct{ pat, text string }{
		{`a+`, "baaab"}, {`(?i)error`, "An ERROR"}, {`(\w+)@(\w+)`, "x@y z@w"}, {`(?s)<think>(.*?)</think>`, "<think>a\nb</think>"},
		{`\p{Han}+`, "日本語ab日"}, {`[^a]+`, "a\xffb\xe6\x97"}, {`\x{FFFD}`, "\xef\xbf\xbd\xff"}, {`(a|ab)(c|bcd)(d*)`, "abcd"},
		{`^$`, ""}, {`\b`, "ab cd"}, {`(?m)^(\w+):\s*(.*)$`, "k: v\nx:y"}, {`(a*)+`, "b"}, {`[a-z]{2,4}?`, "abcdef"},
	}
	for _, s := range seeds {
		f.Add(s.pat, s.text, uint8(0))
	}
	f.Fuzz(func(t *testing.T, pat, text string, cut uint8) {
		if len(pat) > 200 || len(text) > 4096 {
			t.Skip()
		}
		want, err := stdre.Compile(pat)
		got, err2 := Compile(pat)
		if (err == nil) != (err2 == nil) || err != nil && err.Error() != err2.Error() {
			t.Fatalf("Compile(%q): err %v, want %v", pat, err2, err)
		}
		if err != nil {
			return
		}
		texts := []string{text}
		// Cut the text inside a multibyte sequence.
		if k := int(cut) % (len(text) + 1); k > 0 && k < len(text) && !utf8.RuneStart(text[k]) {
			texts = append(texts, text[:k], text[k:])
		}
		for _, s := range texts {
			if g, w := got.MatchString(s), want.MatchString(s); g != w {
				t.Fatalf("%q.MatchString(%q) = %v, want %v", pat, s, g, w)
			}
			if g, w := got.FindStringSubmatchIndex(s), want.FindStringSubmatchIndex(s); !slices.Equal(g, w) {
				t.Fatalf("%q.FindStringSubmatchIndex(%q) = %v, want %v", pat, s, g, w)
			}
			if g, w := got.FindAllStringSubmatchIndex(s, -1), want.FindAllStringSubmatchIndex(s, -1); fmt.Sprint(g) != fmt.Sprint(w) {
				t.Fatalf("%q.FindAllStringSubmatchIndex(%q) = %v, want %v", pat, s, g, w)
			}
			if g, w := got.ReplaceAllString(s, "[$1]"), want.ReplaceAllString(s, "[$1]"); g != w {
				t.Fatalf("%q.ReplaceAllString(%q) = %q, want %q", pat, s, g, w)
			}
			if g, w := got.Split(s, -1), want.Split(s, -1); !slices.Equal(g, w) {
				t.Fatalf("%q.Split(%q) = %q, want %q", pat, s, g, w)
			}
			gl, wl := got.Copy(), want.Copy()
			gl.Longest()
			wl.Longest()
			if g, w := gl.FindAllStringSubmatchIndex(s, -1), wl.FindAllStringSubmatchIndex(s, -1); fmt.Sprint(g) != fmt.Sprint(w) {
				t.Fatalf("Longest %q.FindAllStringSubmatchIndex(%q) = %v, want %v", pat, s, g, w)
			}
		}
	})
}
