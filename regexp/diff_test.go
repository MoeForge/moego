package regexp

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	stdre "regexp"
	"slices"
	"strings"
	"testing"
)

// Differential tests: every result must equal the standard library's.

var diffPatterns = []string{
	``, `a`, `abc`, `a|b`, `a*`, `a+`, `a?`, `a*?`, `(a|ab)(c|bcd)(d*)`, `x*`, `x+y`,
	`^abc`, `abc$`, `^$`, `^`, `$`, `(?m)^abc$`, `(?m)^`, `(?m)$`, `\bfoo\b`, `\Bo\B`, `\b`,
	`.`, `.*`, `(?s).*`, `.+?`, `[^a]`, `[^a]+`, `\S+`, `\w+`, `\d+`, `\pL+`, `\p{Han}+`,
	`[\x{FFFD}]`, `\x{FFFD}+`, `[^\x00-\x7f]+`, `日本`, `日本語|本`, `(?i)straße`, `(?i)k`,
	`(?i)error`, `(?i)ǅ`, `a{2,4}`, `(ab){1,3}?`, `[a-c]{3}`, `x{0}`, `(a*)*`, `(a*)+`, `(a|b)*c`,
	`(?U)a+`, `sk-[A-Za-z0-9_-]{4,}`, `[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`,
	`\b(?:\d{1,3}\.){3}\d{1,3}\b`, `(?s)<think>.*?</think>`, `"content":"((?:[^"\\]|\\.)*)"`,
	`^data: (.*)$`, `(?m)^Action:\s*(.+)$`, `\.(-\d+)`, `(?i)\bapi\.example\.com\b`,
	`[ -~]*ABC$`, `(a|aa)*b`, `\Aab`, `ab\z`, `a\z|b`, `(?m)a$|b^`, `\B`, `(?m)\B$`,
}

var diffInputs = []string{
	"", "a", "ab", "abc", "aab", "xabcx", "abcabc", "foo bar foo_bar", "a\nb\n\nabc\n",
	"日本語", "日本", "x日本語y", "\xff", "a\xffb", "\xe6\x97", "\xe6\x97\xa5\xe6", "\xef\xbf\xbd",
	"STRASSE straße", "K k K", "Error ERROR error", "sk-abcd sk-ab sk-abcdef",
	"mail me@example.com or x@y.z", "ip 10.0.0.1 and 1.2.3.4.5", "<think>a\nb</think>x<think>c</think>",
	`"content":"a\"b" "content":"x"`, "data: {\"a\":1}\ndata: x", "Action: read\nAction:  write \n",
	"messages.-1.content.-22.x", "API.example.com api.example.comx", "  ABC", "aaab", "aaaa",
	"\n", "\n\n", "ab\n", "ǅ ǆ Ǆ", "\xed\xa0\x80", "\xf4\x90\x80\x80", "\xc0\x80", "a\x80b\xbfc",
}

func check(t *testing.T, pat, in string) {
	t.Helper()
	want, err := stdre.Compile(pat)
	got, err2 := Compile(pat)
	if (err == nil) != (err2 == nil) {
		t.Fatalf("Compile(%q): err %v, want %v", pat, err2, err)
	}
	if err != nil {
		if err.Error() != err2.Error() {
			t.Fatalf("Compile(%q): err %q, want %q", pat, err2, err)
		}
		return
	}
	if g, w := got.MatchString(in), want.MatchString(in); g != w {
		t.Errorf("%q.MatchString(%q) = %v, want %v", pat, in, g, w)
	}
	if g, w := got.FindStringIndex(in), want.FindStringIndex(in); !slices.Equal(g, w) {
		t.Errorf("%q.FindStringIndex(%q) = %v, want %v", pat, in, g, w)
	}
	if g, w := got.FindAllStringIndex(in, -1), want.FindAllStringIndex(in, -1); fmt.Sprint(g) != fmt.Sprint(w) {
		t.Errorf("%q.FindAllStringIndex(%q) = %v, want %v", pat, in, g, w)
	}
	checkLongest(t, pat, in)
}

// checkLongest compares leftmost-longest results: Longest() on a Perl
// pattern and CompilePOSIX, whose parse errors must match too.
func checkLongest(t *testing.T, pat, in string) {
	t.Helper()
	want, got := stdre.MustCompile(pat), MustCompile(pat)
	want.Longest()
	got.Longest()
	if g, w := got.FindStringIndex(in), want.FindStringIndex(in); !slices.Equal(g, w) {
		t.Errorf("Longest %q.FindStringIndex(%q) = %v, want %v", pat, in, g, w)
	}
	if g, w := got.FindAllStringIndex(in, -1), want.FindAllStringIndex(in, -1); fmt.Sprint(g) != fmt.Sprint(w) {
		t.Errorf("Longest %q.FindAllStringIndex(%q) = %v, want %v", pat, in, g, w)
	}
	pw, err := stdre.CompilePOSIX(pat)
	pg, err2 := CompilePOSIX(pat)
	if (err == nil) != (err2 == nil) || err != nil && err.Error() != err2.Error() {
		t.Fatalf("CompilePOSIX(%q): err %v, want %v", pat, err2, err)
	}
	if err != nil {
		return
	}
	if g, w := pg.FindStringIndex(in), pw.FindStringIndex(in); !slices.Equal(g, w) {
		t.Errorf("POSIX %q.FindStringIndex(%q) = %v, want %v", pat, in, g, w)
	}
	if g, w := pg.FindAllStringIndex(in, -1), pw.FindAllStringIndex(in, -1); fmt.Sprint(g) != fmt.Sprint(w) {
		t.Errorf("POSIX %q.FindAllStringIndex(%q) = %v, want %v", pat, in, g, w)
	}
}

func TestDiffTable(t *testing.T) {
	for _, p := range diffPatterns {
		for _, in := range diffInputs {
			check(t, p, in)
		}
	}
}

// randPattern builds a random pattern over a small alphabet so that
// matches, near-matches and UTF-8 edge cases are common.
func randPattern(r *rand.Rand, depth int) string {
	atoms := []string{"a", "b", "c", "日", ".", `\w`, `\W`, `\d`, `\s`, `\S`, "[ab]", "[^a]", "[^日]",
		`\pL`, `\x{FFFD}`, "(?i:k)", "(?i:a)", "\n", "^", "$", `\b`, `\B`, `\A`, `\z`, "(?m:^)", "(?m:$)",
		"(?s:.)", "[a-c日-本]", "x", "é", `\x80`}
	if depth <= 0 || r.IntN(3) == 0 {
		return atoms[r.IntN(len(atoms))]
	}
	switch r.IntN(9) {
	case 0, 1:
		return randPattern(r, depth-1) + randPattern(r, depth-1)
	case 2:
		return randPattern(r, depth-1) + "|" + randPattern(r, depth-1)
	case 3:
		return "(" + randPattern(r, depth-1) + ")*"
	case 4:
		return "(?:" + randPattern(r, depth-1) + ")+?"
	case 5:
		return "(" + randPattern(r, depth-1) + ")?"
	case 6:
		return fmt.Sprintf("(?:%s){%d,%d}", randPattern(r, depth-1), r.IntN(3), 2+r.IntN(3))
	case 7:
		return "(?:" + randPattern(r, depth-1) + ")*?"
	}
	return randPattern(r, depth-1) + randPattern(r, depth-1) + randPattern(r, depth-1)
}

func randInput(r *rand.Rand) string {
	parts := []string{"a", "b", "c", "k", "K", "x", " ", "\n", "日", "本", "é", "_", "1",
		"\xff", "\xe6", "\x97", "\xe6\x97", "\xef\xbf\xbd", "\x80", "\xc3", "A", "@", ":", "."}
	var b strings.Builder
	n := r.IntN(12)
	for i := 0; i < n; i++ {
		b.WriteString(parts[r.IntN(len(parts))])
	}
	return b.String()
}

// randomIterations limits repeated random cases under the race detector.
// Table cases, boundary checks and concurrent tests keep their full coverage.
func randomIterations(n int) int {
	if raceEnabled {
		return max(1, n/10)
	}
	return n
}

func TestDiffRandom(t *testing.T) {
	n := randomIterations(20000)
	if testing.Short() {
		n = min(n, 2000)
	}
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < n; i++ {
		p := randPattern(r, 3)
		for j := 0; j < 8; j++ {
			check(t, p, randInput(r))
		}
		if t.Failed() {
			t.Fatalf("stopping after first failing pattern %q", p)
		}
	}
}

// randShape builds patterns in the simple shapes that get dedicated plans:
// character-class runs, literals, case-insensitive literals, and patterns
// anchored at either end.
func randShape(r *rand.Rand) string {
	classes := []string{`\s`, `\d`, `\w`, `[a-c]`, `[^a]`, `.`, `(?s:.)`, `\pL`, `\p{Han}`, `[日本]`, `\x{FFFD}`, `(?i:k)`, `é`, `[^\n日]`}
	lits := []string{"ab", "abc", "日本", "aé", "kKs", "xKy", "a.b", "ÿ"}
	cl := classes[r.IntN(len(classes))]
	lit := regexpQuote(lits[r.IntN(len(lits))])
	switch r.IntN(16) {
	case 15:
		anch := []string{`(?i)^x-(request|trace)-id$`, `(?i)^ab[a-c]+KS$`, `^ab\w*(?i:kk)$`, `(?i)^日本\w+$`, `^(?i:aé)[a-c]*$`, `^\x{FFFD}a\w+$`, `(?i)^abcdefghij\d+xyz$`, `^ab(c|d)$`}
		return anch[r.IntN(len(anch))]
	case 14:
		runs := []string{`[a-c]`, `[ab]`, `\w`, `[^=\n]`, `x`, `\d`}
		tails := []string{`=`, `[=_]`, `\n`, `c`, `[^a-c]`, `日`}
		reps := []string{"+", "*", "{2,}", "{1,3}", "+?"}
		treps := []string{"?", "*", "{0,2}", "{1,2}", "??", "+"}
		return runs[r.IntN(len(runs))] + reps[r.IntN(len(reps))] + tails[r.IntN(len(tails))] + treps[r.IntN(len(treps))]
	case 12:
		mids := []string{"(?s:.*?)", "(?s:.*)", "(?s:.+?)", "(?s:.+)", "((?s).*?)", ".*?", "(?s:.)*?"}
		return lit + mids[r.IntN(len(mids))] + regexpQuote(lits[r.IntN(len(lits))])
	case 13:
		pres := []string{`[a-c]+`, `\w+`, `[^@\s]+`, `\d*`, `(?i:k)+`, `[日本]+`, `\w*`, `(a|bc)+`, `[a-c]?`, `\pL+`, `[^:]*`}
		mids := []string{"@", ":", "@a", "ab", "日"}
		sufs := []string{`[a-c]+`, `\w+\.\w{2,}`, ``, ``, `.*`, `[^@]+`, `(?i:k)+$`}
		return pres[r.IntN(len(pres))] + regexpQuote(mids[r.IntN(len(mids))]) + sufs[r.IntN(len(sufs))]
	case 10:
		dots := []string{".*", ".+", "(?s:.*)", ".*?", "(.*)", "(?s:.+?)"}
		pre, suf := "", ""
		if r.IntN(3) > 0 {
			pre = lit
		}
		if r.IntN(3) > 0 {
			suf = regexpQuote(lits[r.IntN(len(lits))])
		}
		return "^" + pre + dots[r.IntN(len(dots))] + suf + "$"
	case 11:
		return "(?m)^" + lit + ".*" + lit + "$"
	case 0:
		return cl + "+"
	case 1:
		return fmt.Sprintf("%s{%d,%d}", cl, 1+r.IntN(2), 2+r.IntN(3))
	case 2:
		return cl + "+?"
	case 3:
		return lit
	case 4:
		return "(?i)" + lit
	case 5:
		return cl + "*" + lit + "$"
	case 6:
		return "^" + lit + cl + "*" + lit + "$"
	case 7:
		return "(" + lit + "|" + cl + ")" + cl + `\z`
	case 8:
		return "^" + lit
	}
	return `\b` + lit + cl + "+"
}

func regexpQuote(s string) string { return stdre.QuoteMeta(s) }

func TestDiffShapes(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	n := randomIterations(20000)
	if testing.Short() {
		n = min(n, 2000)
	}
	extra := []string{"ab", "abc", "日本", "aé", "kKs", "xKy", "a.b", "ÿ", "K", "ſ", "AB"}
	for i := 0; i < n; i++ {
		p := randShape(r)
		for j := 0; j < 8; j++ {
			in := randInput(r)
			switch j % 4 {
			case 1:
				in += extra[r.IntN(len(extra))] + randInput(r)
			case 2:
				in = extra[r.IntN(len(extra))] + in + extra[r.IntN(len(extra))]
			}
			check(t, p, in)
		}
		if t.Failed() {
			t.Fatalf("stopping after first failing pattern %q", p)
		}
	}
}

// TestDiffAllCounts checks the All routines across the switch from the
// small buffer to pooled scratch, with limits around it.
func TestDiffAllCounts(t *testing.T) {
	pats := []string{`a`, `a*`, `\s+`, `[ab]`, `x*`, `\b`, `(?i)ab`, `a|b|`, `\pL`}
	r := rand.New(rand.NewPCG(9, 10))
	for _, p := range pats {
		want := stdre.MustCompile(p)
		got := MustCompile(p)
		for iter := 0; iter < randomIterations(200); iter++ {
			var b strings.Builder
			for k := r.IntN(80); k > 0; k-- {
				b.WriteString([]string{"a", "b", " ", "ab", "日", "\xff", "aa"}[r.IntN(7)])
			}
			in := b.String()
			for _, n := range []int{-1, 0, 1, 15, 16, 17, 31, 40} {
				if g, w := got.FindAllStringIndex(in, n), want.FindAllStringIndex(in, n); fmt.Sprint(g) != fmt.Sprint(w) {
					t.Fatalf("%q.FindAllStringIndex(%q, %d) = %v, want %v", p, in, n, g, w)
				}
				if g, w := got.FindAllString(in, n), want.FindAllString(in, n); fmt.Sprint(g) != fmt.Sprint(w) {
					t.Fatalf("%q.FindAllString(%q, %d) = %v, want %v", p, in, n, g, w)
				}
				if g, w := got.FindAll([]byte(in), n), want.FindAll([]byte(in), n); fmt.Sprint(g) != fmt.Sprint(w) {
					t.Fatalf("%q.FindAll(%q, %d) = %v, want %v", p, in, n, g, w)
				}
			}
		}
	}
}

func TestDiffAllLargeResults(t *testing.T) {
	inputs := []string{
		strings.Repeat("ab日\xff ", 3000),
		strings.Repeat("a", 6000) + strings.Repeat("x", 12000),
		strings.Repeat("x", 12000) + strings.Repeat("a", 6000),
	}
	for _, pat := range []string{`a`, `a*`, `[ab]`, `\s+`, `(?i)ab`, `\b`, `\pL`, `a|ab`} {
		got, want := MustCompile(pat), stdre.MustCompile(pat)
		for _, in := range inputs {
			for _, n := range []int{0, 1, 15, 16, 17, 255, 256, 257, 1023, 1024, 1025, -1} {
				expected := want.FindAllStringIndex(in, n)
				for _, actual := range [][][]int{got.FindAllStringIndex(in, n), got.FindAllIndex([]byte(in), n)} {
					if !reflect.DeepEqual(actual, expected) {
						t.Fatalf("%q, n=%d: result differs from stdlib", pat, n)
					}
					if len(actual) > 1 {
						actual[0][0] = -17
						if !reflect.DeepEqual(actual[1:], expected[1:]) || !reflect.DeepEqual(got.FindAllStringIndex(in, n), expected) {
							t.Fatalf("%q, n=%d: result storage is shared", pat, n)
						}
					}
				}
			}
		}
	}
}

// TestDiffLongRuns checks accelerated DFA loops: long runs reach the vector
// span scan, and runs end at non-ASCII members, other classes and the end.
func TestDiffLongRuns(t *testing.T) {
	pats := []string{`[a-z0-9._-]*x`, `^gpt-[a-z0-9][a-z0-9._-]*$`, `\w+@`, `"(?:[^"\\]|\\.)*"`, `[^\n]*\n`,
		`a[^b]*b`, `(?s)a.*?END`, `\pL+日`, `[^日]+本`, `x\d*y`, `\w+\b`, `(?m)^[a-z ]+$`, `[^q]+`,
		`(?i)zz|qq`, `\s+\S`, `[a-c]*d|[a-z]*e`, `(?U)[a-y]+z`, `日+[a-z]*`, `[^"]*"$`, `\B[a-z]+`}
	r := rand.New(rand.NewPCG(11, 12))
	parts := []string{"a", "b", "x", "y", "z", "q", "e", "d", "END", "@", " ", "\n", "\"", "\\", "1", "-", ".", "日", "本", "é", "\xff", "gpt-", "_"}
	for _, p := range pats {
		for iter := 0; iter < randomIterations(300); iter++ {
			var b strings.Builder
			for k := r.IntN(12); k > 0; k-- {
				if r.IntN(3) == 0 {
					b.WriteString(parts[r.IntN(len(parts))])
					continue
				}
				run := []string{"abcxyz.-_09", "aaaa", "    ", "日本日", "zzzz", "\"\\\"x"}[r.IntN(6)]
				for m := r.IntN(60); m > 0; m-- {
					b.WriteByte(run[r.IntN(len(run))])
				}
			}
			in := b.String()
			check(t, p, in)
			if t.Failed() {
				t.Fatalf("stopping after first failing input for %q", p)
			}
		}
	}
}

// TestDiffFFFDPlans checks the reverse-scanning plans on patterns that can
// match U+FFFD, with invalid UTF-8 inside, before and after the parts the
// reverse automata scan.
func TestDiffFFFDPlans(t *testing.T) {
	pats := []string{`[^@]+@x`, `\x{FFFD}+@`, `.*@ab$`, `[^a]*b$`, `(?s).+:x`, `[^\n]*\n`, `\W+=`, `[^日]+本`,
		`[^"]*"$`, `\x{FFFD}\w+$`, `[^:]*:(.*)`, `[^,]+,\d+`}
	parts := []string{"a", "b", "@", "x", "@x", "ab", ":", ":x", "=", "\n", "\"", ",1", "日", "本", "\xff", "\xe6\x97",
		"\xef\xbf\xbd", "\x80", "\xc3", "\xed\xa0\x80", "\xf4\x90\x80\x80", " "}
	r := rand.New(rand.NewPCG(15, 16))
	for _, p := range pats {
		for iter := 0; iter < randomIterations(400); iter++ {
			var b strings.Builder
			for k := r.IntN(10); k > 0; k-- {
				b.WriteString(parts[r.IntN(len(parts))])
			}
			check(t, p, b.String())
			checkAPI(t, p, b.String(), false)
		}
		if t.Failed() {
			t.Fatalf("stopping after first failing pattern %q", p)
		}
	}
}
