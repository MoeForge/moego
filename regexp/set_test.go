package regexp

import (
	"fmt"
	"math/rand/v2"
	stdre "regexp"
	"strings"
	"testing"
)

// The reference for a Set is the standard library on (p0)|(p1)|…, with the
// expression of a match being the first group that took part.
func checkSet(t *testing.T, exprs []string, in string) {
	t.Helper()
	set, err := CompileSet(exprs)
	var b strings.Builder
	groups := make([]int, len(exprs))
	g := 1
	for i, e := range exprs {
		re, err2 := stdre.Compile(e)
		if err2 != nil {
			if err == nil || err.Error() != err2.Error() {
				t.Fatalf("CompileSet(%q) err = %v, want %v", exprs, err, err2)
			}
			return
		}
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString("(" + e + ")")
		groups[i] = g
		g += 1 + re.NumSubexp()
	}
	if err != nil {
		t.Fatalf("CompileSet(%q): %v", exprs, err)
	}
	if len(exprs) == 0 {
		b.WriteString(`[^\x00-\x{10FFFF}]`)
	}
	u := stdre.MustCompile(b.String())
	var want [][]int
	for _, m := range u.FindAllStringSubmatchIndex(in, -1) {
		for i, g := range groups {
			if m[2*g] >= 0 {
				want = append(want, []int{m[0], m[1], i})
				break
			}
		}
	}
	if got := set.FindAllStringIndex(in, -1); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%q.FindAllStringIndex(%q) = %v, want %v", exprs, in, got, want)
	}
	if len(want) > 1 {
		if got := set.FindAllIndex([]byte(in), 1); fmt.Sprint(got) != fmt.Sprint(want[:1]) {
			t.Errorf("%q.FindAllIndex(%q, 1) = %v, want %v", exprs, in, got, want[:1])
		}
	}
	if got, w := set.MatchString(in), u.MatchString(in); got != w {
		t.Errorf("%q.MatchString(%q) = %v, want %v", exprs, in, got, w)
	}
	var which []int
	for i, e := range exprs {
		if stdre.MustCompile(e).MatchString(in) {
			which = append(which, i)
		}
	}
	if got := set.MatchesString(in); fmt.Sprint(got) != fmt.Sprint(which) {
		t.Errorf("%q.MatchesString(%q) = %v, want %v", exprs, in, got, which)
	}
	f := func(i int, m string) string { return fmt.Sprintf("<%d:%s>", i, m) }
	wantRepl := in
	if len(want) > 0 {
		var r strings.Builder
		last := 0
		for _, m := range want {
			r.WriteString(in[last:m[0]])
			r.WriteString(f(m[2], in[m[0]:m[1]]))
			last = m[1]
		}
		r.WriteString(in[last:])
		wantRepl = r.String()
	}
	if got := set.ReplaceAllStringFunc(in, f); got != wantRepl {
		t.Errorf("%q.ReplaceAllStringFunc(%q) = %q, want %q", exprs, in, got, wantRepl)
	}
	fb := func(i int, m []byte) []byte { return []byte(f(i, string(m))) }
	if got := string(set.ReplaceAllFunc([]byte(in), fb)); got != wantRepl {
		t.Errorf("%q.ReplaceAllFunc(%q) = %q, want %q", exprs, in, got, wantRepl)
	}
	if got, w := set.ReplaceAllLiteralString(in, "*"), u.ReplaceAllLiteralString(in, "*"); got != w {
		t.Errorf("%q.ReplaceAllLiteralString(%q) = %q, want %q", exprs, in, got, w)
	}
}

func TestSet(t *testing.T) {
	sets := [][]string{
		nil, {``}, {`a`}, {`a`, `ab`}, {`ab`, `a`}, {`sk-[A-Za-z0-9]{4,}`, `[\w.]+@\w+\.\w+`, `\b1[3-9]\d{9}\b`},
		{`(?i)model`, `mode`, `(x)(y)?`}, {`a*`, `b`}, {`\b`, `x`}, {`(?P<n>a)`, `(?P<n>b)`}, {`日本`, `\pL+`}, {`a(`},
	}
	ins := []string{"", "a", "ab", "ba", "sk-abcd me@x.io 13912345678 x", "Model mode MODEL", "xy x", "日本語 本", "aab b"}
	for _, s := range sets {
		for _, in := range ins {
			checkSet(t, s, in)
		}
	}
}

func TestSetRandom(t *testing.T) {
	r := rand.New(rand.NewPCG(17, 18))
	n := randomIterations(3000)
	if testing.Short() {
		n = min(n, 300)
	}
	for i := 0; i < n; i++ {
		exprs := make([]string, r.IntN(5))
		for k := range exprs {
			exprs[k] = randPattern(r, 2)
		}
		for j := 0; j < 4; j++ {
			checkSet(t, exprs, randInput(r))
		}
		if t.Failed() {
			t.Fatalf("stopping at %q", exprs)
		}
	}
}
