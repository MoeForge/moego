package regexp

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	stdre "regexp"
	"slices"
	"strings"
	"testing"
)

// checkAPI compares every method of the API, other than the plain Find and
// Match forms that check covers, with the standard library's.
func checkAPI(t *testing.T, pat, in string, posix bool) {
	t.Helper()
	compile, stdCompile := Compile, stdre.Compile
	if posix {
		compile, stdCompile = CompilePOSIX, stdre.CompilePOSIX
	}
	want, err := stdCompile(pat)
	got, err2 := compile(pat)
	if (err == nil) != (err2 == nil) || err != nil && err.Error() != err2.Error() {
		t.Fatalf("compile(%q): err %v, want %v", pat, err2, err)
	}
	if err != nil {
		return
	}
	name := pat
	if posix {
		name = "POSIX " + pat
	}
	eq := func(method string, g, w any) {
		t.Helper()
		if gs, ws := fmt.Sprintf("%#v", g), fmt.Sprintf("%#v", w); gs != ws {
			t.Errorf("%q.%s(%q):\n got %s\nwant %s", name, method, in, gs, ws)
		}
	}
	b := []byte(in)
	eq("FindStringSubmatchIndex", got.FindStringSubmatchIndex(in), want.FindStringSubmatchIndex(in))
	eq("FindStringSubmatch", got.FindStringSubmatch(in), want.FindStringSubmatch(in))
	eq("FindSubmatchIndex", got.FindSubmatchIndex(b), want.FindSubmatchIndex(b))
	eq("FindSubmatch", got.FindSubmatch(b), want.FindSubmatch(b))
	eq("FindString", got.FindString(in), want.FindString(in))
	eq("Find", got.Find(b), want.Find(b))
	eq("FindIndex", got.FindIndex(b), want.FindIndex(b))
	eq("Match", got.Match(b), want.Match(b))
	for _, n := range []int{-1, 0, 1, 2, 5} {
		m := fmt.Sprintf("/%d", n)
		eq("FindAllString"+m, got.FindAllString(in, n), want.FindAllString(in, n))
		eq("FindAllStringSubmatchIndex"+m, got.FindAllStringSubmatchIndex(in, n), want.FindAllStringSubmatchIndex(in, n))
		eq("FindAllStringSubmatch"+m, got.FindAllStringSubmatch(in, n), want.FindAllStringSubmatch(in, n))
		eq("FindAllSubmatchIndex"+m, got.FindAllSubmatchIndex(b, n), want.FindAllSubmatchIndex(b, n))
		eq("FindAllSubmatch"+m, got.FindAllSubmatch(b, n), want.FindAllSubmatch(b, n))
		eq("FindAll"+m, got.FindAll(b, n), want.FindAll(b, n))
		eq("FindAllIndex"+m, got.FindAllIndex(b, n), want.FindAllIndex(b, n))
		eq("Split"+m, got.Split(in, n), want.Split(in, n))
	}
	for _, repl := range []string{"", "x", "<$0>", "$1-${1}x$2$$", "${name}$name$10$01$9999999999$x", "$", "${", "${1", "$+1$-1$１"} {
		eq("ReplaceAllString/"+repl, got.ReplaceAllString(in, repl), want.ReplaceAllString(in, repl))
		eq("ReplaceAllLiteralString/"+repl, got.ReplaceAllLiteralString(in, repl), want.ReplaceAllLiteralString(in, repl))
		eq("ReplaceAll/"+repl, got.ReplaceAll(b, []byte(repl)), want.ReplaceAll(b, []byte(repl)))
		eq("ReplaceAllLiteral/"+repl, got.ReplaceAllLiteral(b, []byte(repl)), want.ReplaceAllLiteral(b, []byte(repl)))
		if m := want.FindStringSubmatchIndex(in); m != nil {
			eq("ExpandString/"+repl, got.ExpandString([]byte("pre:"), repl, in, m), want.ExpandString([]byte("pre:"), repl, in, m))
			eq("Expand/"+repl, got.Expand(nil, []byte(repl), b, m), want.Expand(nil, []byte(repl), b, m))
		}
	}
	f := func(s string) string { return "[" + strings.ToUpper(s) + "]" }
	eq("ReplaceAllStringFunc", got.ReplaceAllStringFunc(in, f), want.ReplaceAllStringFunc(in, f))
	fb := func(s []byte) []byte { return []byte("<" + string(s) + ">") }
	eq("ReplaceAllFunc", got.ReplaceAllFunc(b, fb), want.ReplaceAllFunc(b, fb))
	eq("MatchReader", got.MatchReader(strings.NewReader(in)), want.MatchReader(strings.NewReader(in)))
	eq("FindReaderIndex", got.FindReaderIndex(strings.NewReader(in)), want.FindReaderIndex(strings.NewReader(in)))
	eq("FindReaderSubmatchIndex", got.FindReaderSubmatchIndex(strings.NewReader(in)), want.FindReaderSubmatchIndex(strings.NewReader(in)))
	eq("FindReaderIndex/bytes", got.FindReaderIndex(bytes.NewReader(b)), want.FindReaderIndex(bytes.NewReader(b)))
	p1, c1 := got.LiteralPrefix()
	p2, c2 := want.LiteralPrefix()
	eq("LiteralPrefix", [2]any{p1, c1}, [2]any{p2, c2})
	eq("NumSubexp", got.NumSubexp(), want.NumSubexp())
	eq("SubexpNames", got.SubexpNames(), want.SubexpNames())
	eq("String", got.String(), want.String())

	// Iterators against the slice forms.
	eq("AllString", slices.Collect(got.AllString(in)), nilIfNone(want.FindAllString(in, -1)))
	eq("All", slices.Collect(got.All(b)), nilIfNone(want.FindAll(b, -1)))
	eq("AllStringIndex", slices.Collect(got.AllStringIndex(in)), nilIfNone(want.FindAllStringIndex(in, -1)))
	eq("AllIndex", slices.Collect(got.AllIndex(b)), nilIfNone(want.FindAllIndex(b, -1)))
	eq("AllStringSubmatch", slices.Collect(got.AllStringSubmatch(in)), nilIfNone(want.FindAllStringSubmatch(in, -1)))
	eq("AllSubmatch", slices.Collect(got.AllSubmatch(b)), nilIfNone(want.FindAllSubmatch(b, -1)))
	eq("AllStringSubmatchIndex", slices.Collect(got.AllStringSubmatchIndex(in)), nilIfNone(want.FindAllStringSubmatchIndex(in, -1)))
	eq("AllSubmatchIndex", slices.Collect(got.AllSubmatchIndex(b)), nilIfNone(want.FindAllSubmatchIndex(b, -1)))
	eq("SplitSeq", slices.Collect(got.SplitSeq(in)), nilIfNone(want.Split(in, -1)))
	for m := range got.AllStringIndex(in) { // stopping early
		eq("AllStringIndex/first", m, want.FindStringIndex(in))
		break
	}
}

func nilIfNone[T any](s []T) []T {
	if len(s) == 0 {
		return nil
	}
	return s
}

var apiPatterns = []string{
	`a(b)c`, `(a)(b)?(c)`, `(a*)+`, `(a*)*`, `(a|ab)(c|bcd)(d*)`, `(?P<name>\w+)@(\w+)`, `(?P<x>a)|(?P<x>b)`,
	`(?s)<think>(.*?)</think>`, `"content":"((?:[^"\\]|\\.)*)"`, `^data: (.*)$`, `(?m)^Action:\s*(.+)$`,
	`\.(-\d+)`, `(['"]?)api_key:([^\s'"]+)(['"]?)`, `(\d+)-(\d+)`, `(x*)(x*)`, `((a)|b)+`, `(a)|(b)|(c)`,
	`(?i)(ab)+`, `(日本)(語)?`, `([^\x00-\x7f])`, `(\b)`, `(\B)`, `()`, `(|a)*`, `(a|)+`, `^`, `$`, `(?m)$`,
	`a*`, ``, `x`, `(?U)(a+)(a*)`, `(.)(.)`, `\pL(\pL*)`, `(a{2})(a{1,2})`, `([a-c]*)c`, `(?:(a)|b)*`,
	`^(abc)$`, `^abc$`, `^a`, `^a|b`, `(?:a)`, `abc|abd`, `\Aab(c)\z`, `\x{FFFD}(.)`, `(\x{FFFD}+)`,
	`(?s)(.*)`, `(.*)(\d+)`, `(.+?)(\d*)$`, `((((((((((a))))))))))`, `(a)(b)(c)(d)(e)(f)(g)(h)(i)(j)(k)`,
	`\x{D800}`, `a\x{DFFF}b`, `[\x{D800}-\x{DFFF}]`, `\x{D800}+|a`, `^\x{D800}`, `^ab\x{D800}$`, `(?i)\x{D800}x`,
}

var apiInputs = []string{
	"", "abc", "abcbcd", "aaa", "ab", "x@y z@w", "a<think>t\nu</think>b<think></think>", `"content":"a\"b"`,
	"data: x\ndata: y", "Action: run\nAction:  go  \n", "messages.-1.c.-22", `api_key:"sk-1" 'api_key:k2'`,
	"1-2 33-44", "xx", "ab", "日本語 日本", "a\xffb\xc3", "\xef\xbf\xbd\xff", "aaaaaaaaaaaaaa", "abcdefghijk",
	"name=x $1", "a\n", "\n", "ABab", "a b", "xyz123", "ab12 cd", "\xed\xa0\x80", "a\xed\xbf\xbfb",
}

func TestDiffAPI(t *testing.T) {
	for _, p := range apiPatterns {
		for _, in := range apiInputs {
			checkAPI(t, p, in, false)
			checkAPI(t, p, in, true)
		}
		if t.Failed() {
			t.Fatalf("stopping after first failing pattern %q", p)
		}
	}
}

func TestDiffAPIRandom(t *testing.T) {
	n := randomIterations(3000)
	if testing.Short() {
		n = min(n, 300)
	}
	r := rand.New(rand.NewPCG(5, 6))
	for i := 0; i < n; i++ {
		p := randPattern(r, 3)
		for j := 0; j < 4; j++ {
			in := randInput(r)
			checkAPI(t, p, in, false)
			checkAPI(t, p, in, true)
		}
		if t.Failed() {
			t.Fatalf("stopping after first failing pattern %q", p)
		}
	}
}

func TestDiffAPIShapes(t *testing.T) {
	n := randomIterations(3000)
	if testing.Short() {
		n = min(n, 300)
	}
	r := rand.New(rand.NewPCG(7, 8))
	for i := 0; i < n; i++ {
		p := randShape(r)
		if r.IntN(2) == 0 {
			p = "(" + p + ")"
		}
		for j := 0; j < 4; j++ {
			in := randInput(r) + []string{"ab", "日本", "K", "a.b", ""}[r.IntN(5)] + randInput(r)
			checkAPI(t, p, in, false)
			checkAPI(t, p, in, true)
		}
		if t.Failed() {
			t.Fatalf("stopping after first failing pattern %q", p)
		}
	}
}

// TestDiffLiteralPrefix covers patterns that start with \A and a literal,
// where LiteralPrefix depends on whether the rest is one-pass.
func TestDiffLiteralPrefix(t *testing.T) {
	r := rand.New(rand.NewPCG(13, 14))
	n := randomIterations(20000)
	if testing.Short() {
		n = min(n, 2000)
	}
	for i := 0; i < n; i++ {
		p := []string{"^", `\A`, "^(?:)", "^x{0}"}[r.IntN(4)] + []string{"a", "ab", "日", "abc"}[r.IntN(4)] + randPattern(r, 2) + []string{"", "$", `\z`, "(?m:$)", `\b`}[r.IntN(5)]
		want, err := stdre.Compile(p)
		if err != nil {
			continue
		}
		p1, c1 := MustCompile(p).LiteralPrefix()
		p2, c2 := want.LiteralPrefix()
		if p1 != p2 || c1 != c2 {
			t.Fatalf("%q.LiteralPrefix() = %q, %v, want %q, %v", p, p1, c1, p2, c2)
		}
	}
}

// TestDiffSurrogates covers runes without a UTF-8 encoding in literals,
// alone and after \A, where the standard library compares the start of the
// text with the encoding of U+FFFD.
func TestDiffSurrogates(t *testing.T) {
	r := rand.New(rand.NewPCG(19, 20))
	n := randomIterations(4000)
	if testing.Short() {
		n = min(n, 400)
	}
	sur := []string{`\x{D800}`, `\x{DFFF}`, `\x{DB80}`}
	for i := 0; i < n; i++ {
		p := []string{"^", `\A`, "", "(?m)^"}[r.IntN(4)] + []string{"", "a", "日"}[r.IntN(3)] + sur[r.IntN(len(sur))] +
			[]string{"", "b", sur[0]}[r.IntN(3)] + randPattern(r, 1) + []string{"", "$", `\z`}[r.IntN(3)]
		for j := 0; j < 4; j++ {
			in := []string{"", "a", "日"}[r.IntN(3)] + []string{"\xef\xbf\xbd", "\xed\xa0\x80", "\xff", ""}[r.IntN(4)] + randInput(r)
			check(t, p, in)
			checkAPI(t, p, in, false)
		}
		if t.Failed() {
			t.Fatalf("stopping after first failing pattern %q", p)
		}
	}
}

// TestDiffRunsAndReplace covers runs of ASCII classes over many 64-byte
// blocks, with runs that cross block boundaries, reach the end, and are
// interrupted by non-ASCII bytes, and literal replacements equal to a
// match or not.
func TestDiffRunsAndReplace(t *testing.T) {
	pats := []string{`\s+`, `[a-c]+`, `\d+`, `[^ ]+`, `\w{2,}`, `[\x00-\x7f]+`, `x+`}
	repls := []string{"", " ", "a", "ab", "\t", "$0", "${0}x"}
	r := rand.New(rand.NewPCG(13, 14))
	for _, p := range pats {
		want := stdre.MustCompile(p)
		got := MustCompile(p)
		for iter := 0; iter < randomIterations(300); iter++ {
			var b strings.Builder
			for k := r.IntN(40); k > 0; k-- {
				run := []string{" ", "  \t\n", "abc", "x", "12345", "日本", "\xff", "é"}[r.IntN(8)]
				for m := r.IntN(90) + 1; m > 0; m-- {
					b.WriteString(run)
				}
			}
			in := b.String()
			for _, n := range []int{-1, 1, 7, 64} {
				if g, w := got.FindAllStringIndex(in, n), want.FindAllStringIndex(in, n); fmt.Sprint(g) != fmt.Sprint(w) {
					t.Fatalf("%q.FindAllStringIndex(%q, %d) = %v, want %v", p, in, n, g, w)
				}
			}
			for _, repl := range repls {
				if g, w := got.ReplaceAllString(in, repl), want.ReplaceAllString(in, repl); g != w {
					t.Fatalf("%q.ReplaceAllString(%q, %q) = %q, want %q", p, in, repl, g, w)
				}
				if g, w := got.ReplaceAllLiteral([]byte(in), []byte(repl)), want.ReplaceAllLiteral([]byte(in), []byte(repl)); !bytes.Equal(g, w) || (g == nil) != (w == nil) {
					t.Fatalf("%q.ReplaceAllLiteral(%q, %q) = %q, want %q", p, in, repl, g, w)
				}
			}
			var first [][]int
			for m := range got.AllStringIndex(in) {
				if first = append(first, m); len(first) == 3 {
					break
				}
			}
			if w := want.FindAllStringIndex(in, 3); fmt.Sprint(first) != fmt.Sprint(w) {
				t.Fatalf("%q.AllStringIndex(%q) starts %v, want %v", p, in, first, w)
			}
		}
	}
}

// TestDiffCaptureSteps covers the memoized steps of the single-thread
// capture walk: patterns whose captures it computes, run over inputs
// that mix ASCII, which uses the memoized steps, with other characters
// and with empty-width assertions, which do not.
func TestDiffCaptureSteps(t *testing.T) {
	pats := []string{`(['"]?)api_key:([^\s'"]+)(['"]?)`, `"text":"((?:[^"\\]|\\.)*)"`, `(\w+)=(\d*)`,
		`(?i)(ab)+(c?)`, `(a)(\b)(b*)`, `^(x+)(y*)$`, `(?m)^(\S+) (\S*)$`, `(é|e)(\d+)`, `(a|b)(c|d)*e`,
		`([^,]*),([^,]*)`, `(\.)(\w*)`, `(.)(.)(.)`}
	parts := []string{"api_key:", "'", "\"", "sk-1", " ", "\n", "\"text\":\"", "\\\"", "ab", "AB", "c", "x", "y", "é", "e", "12", ",", ".", "=", "日", "\xff", "d"}
	r := rand.New(rand.NewPCG(15, 16))
	for _, p := range pats {
		for iter := 0; iter < randomIterations(400); iter++ {
			var b strings.Builder
			for k := r.IntN(14); k > 0; k-- {
				b.WriteString(parts[r.IntN(len(parts))])
			}
			checkAPI(t, p, b.String(), false)
		}
		if t.Failed() {
			t.Fatalf("stopping after first failing pattern %q", p)
		}
	}
}

// TestDiffLitRun covers patterns of a literal followed by a class run:
// runs shorter than the minimum, runs capped by the maximum, candidates
// inside an earlier run, and literals that end in run members.
func TestDiffLitRun(t *testing.T) {
	pats := []string{`\.(-\d+)`, `sk-[A-Za-z0-9_-]{20,}`, `ab[0-9]{2,3}`, `x[x]{3,}`, `xx{2,4}`, `日本[a-z]+`,
		`é\d+`, `(a)(b[0-9]+)`, `AKIA[A-Z2-7]{16}`, `a\d{3}`, `--?\w+`, `k[k-m]{2}`}
	parts := []string{".", "-", "1", "23", "sk-", "abcdefghij", "ABCDEFGHIJKLMNOP", "AKIA", "x", "xx", "ab", "日本", "é", "\xff", " ", "k", "lm", "_"}
	r := rand.New(rand.NewPCG(17, 18))
	for _, p := range pats {
		for iter := 0; iter < randomIterations(400); iter++ {
			var b strings.Builder
			for k := r.IntN(16); k > 0; k-- {
				b.WriteString(parts[r.IntN(len(parts))])
			}
			in := b.String()
			check(t, p, in)
			checkAPI(t, p, in, false)
			checkAPI(t, p, in, true)
		}
		if t.Failed() {
			t.Fatalf("stopping after first failing pattern %q", p)
		}
	}
}
