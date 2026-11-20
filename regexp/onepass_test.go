package regexp

import (
	"reflect"
	stdre "regexp"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestDiffOnePassSubmatch(t *testing.T) {
	defer SetCacheSize(SetCacheSize(0))
	for _, tc := range []struct {
		pat    string
		inputs []string
	}{
		{isoDate, []string{"2026", "+020026-11", "2026-11-20t15:29z", "2026-11-20T15:29:32.010+08:00", "-000001T00:00-1200", "2026-11-20T15:29:32.", "2026\n", ""}},
		{`^((a)|(b))*?()$`, []string{"", "a", "b", "aba", "bab", "ba!"}},
		{`^((ab){1,3}?)(c?)$`, []string{"ab", "ababc", "ababab", "abababab", ""}},
		{`^prefix/((?:a+)*)/(b*)$`, []string{"prefix//", "prefix/aaa/bb", "prefix/a/!", "wrong/aaa/bb"}},
		{`^a(/b+(#c+)*)*$`, []string{"a", "a/b#c", "a/b#cc/bb", "a/b#c/"}},
		{`\A(\bA\B\w|(?m:\n^B$\n))?(\B!)?\z`, []string{"", "Ax", "A!", "\nB\n", "\nB\n!", "\nB!", "!"}},
		{`(?i)^((k)|(s)|(日)|([\x{FFFD}]))+$`, []string{"kS", "Kſ日", "\xff", "K\xe6\x97", "\uFFFD\xff", "日!"}},
		{`^(?:(\p{Greek})|(\p{Han})|([0-9]))+$`, []string{"α日1", "日", "9", "α語2é", "\xff"}},
		{`^(?s:(.))(.)?$`, []string{"", "\n", "\n\n", "日\x80", "\xff", "\uFFFD"}},
		{`^(.)*$`, []string{"", "a", "é日", "a\xff", "a\n"}},
		{`^a\x{D800}((b)|(c))*$`, []string{"a\uFFFD", "a\uFFFDbc", "a\xffbc", "a\xed\xa0\x80b"}},
		{"^" + strings.Repeat("(x)", 32) + "(y)?$", []string{strings.Repeat("x", 32) + "y", strings.Repeat("x", 32), strings.Repeat("x", 32) + "!"}},
		{`^(a){0}(b)?(c){0}$`, []string{"b", "", "a", "c"}},
	} {
		re, std := MustCompile(tc.pat), stdre.MustCompile(tc.pat)
		for _, longest := range []bool{false, true} {
			if longest {
				re.Longest()
				std.Longest()
			}
			for _, s := range tc.inputs {
				if got, want := re.FindStringSubmatchIndex(s), std.FindStringSubmatchIndex(s); !slices.Equal(got, want) {
					t.Fatalf("%q.FindStringSubmatchIndex(%q), longest=%v: got %v, want %v", tc.pat, s, longest, got, want)
				}
				if got, want := re.FindStringSubmatch(s), std.FindStringSubmatch(s); !slices.Equal(got, want) {
					t.Fatalf("%q.FindStringSubmatch(%q): got %q, want %q", tc.pat, s, got, want)
				}
				b := []byte(s)
				if got, want := re.FindSubmatch(b), std.FindSubmatch(b); !reflect.DeepEqual(got, want) {
					t.Fatalf("%q.FindSubmatch(%q): got %#v, want %#v", tc.pat, s, got, want)
				}
				wantAll := std.FindAllStringSubmatchIndex(s, -1)
				if got := re.FindAllStringSubmatchIndex(s, -1); !reflect.DeepEqual(got, wantAll) {
					t.Fatalf("%q.FindAllStringSubmatchIndex(%q): got %v, want %v", tc.pat, s, got, wantAll)
				}
				if got := slices.Collect(re.AllStringSubmatchIndex(s)); !reflect.DeepEqual(got, wantAll) {
					t.Fatalf("%q.AllStringSubmatchIndex(%q): got %v, want %v", tc.pat, s, got, wantAll)
				}
				const repl = "[$0:$1:$2]"
				if got, want := re.ReplaceAllString(s, repl), std.ReplaceAllString(s, repl); got != want {
					t.Fatalf("%q.ReplaceAllString(%q): got %q, want %q", tc.pat, s, got, want)
				}
			}
		}
		e := re.c.engines()
		if e.one == nil || e.caps.Load() != nil || e.fwd.Load() != nil {
			t.Errorf("%q: expected the one-pass capture engine to handle the search", tc.pat)
		}
	}
}

func TestDiffOnePassSelection(t *testing.T) {
	for _, tc := range []struct {
		pat, input string
		onePass    bool
	}{
		{`^(.)(.)([0-9])`, "日\xff3 trailing!", !onePassEndOnly},
		{`^()(x){0}`, "trailing", !onePassEndOnly},
		{`^((a)|ab)(b?)$`, "ab", false}, // both branches can read a
		{`^(a*?)(a*)`, "aaa", false},    // the match can end before another a
		{`^(a?)?(b?)$`, "b", false},     // two empty paths
		{`(?m)^((a)|(b))*$`, "x\naba", false},
		{`((a)|(b))*$`, "xaba", false},
		{"^" + strings.Repeat("a", 1000) + "(b)?$", strings.Repeat("a", 1000), false},
	} {
		re := MustCompile(tc.pat)
		if got := re.c.program().onePass(nil); got != tc.onePass {
			t.Errorf("%q: onePass = %v, want %v", tc.pat, got, tc.onePass)
		}
		checkAPI(t, tc.pat, tc.input, false)
		checkAPI(t, tc.pat, tc.input, true)
		std := stdre.MustCompile(tc.pat)
		std.Longest()
		re.Longest()
		if got, want := re.FindStringSubmatchIndex(tc.input), std.FindStringSubmatchIndex(tc.input); !slices.Equal(got, want) {
			t.Errorf("Longest %q on %q: got %v, want %v", tc.pat, tc.input, got, want)
		}
	}
}

// Concurrent first use and reuse of the large capture scratch must leave
// caller-owned indexes intact, including groups skipped by a later match.
func TestOnePassSubmatchStorage(t *testing.T) {
	pat := "^" + strings.Repeat("(a)", 32) + "((b)|(c))?$"
	re, std := MustCompile(pat), stdre.MustCompile(pat)
	base := strings.Repeat("a", 32)
	inputs := []string{base + "b", base, base + "c", base + "!"}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 20; k++ {
				var results [][]int
				for _, s := range inputs {
					results = append(results, re.FindStringSubmatchIndex(s))
				}
				for j, s := range inputs {
					if want := std.FindStringSubmatchIndex(s); !slices.Equal(results[j], want) {
						t.Errorf("FindStringSubmatchIndex(%q) = %v, want %v", s, results[j], want)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}

// A literal followed by a run uses a separate dispatch in the All index
// search. Check the handoff from stack to pooled pairs at its exact limit.
func TestDiffLitRunSubmatchCounts(t *testing.T) {
	const pat = `\.(-\d+)`
	re, std := MustCompile(pat), stdre.MustCompile(pat)
	const limit = smallPairs / 2
	for _, count := range []int{0, 1, limit - 1, limit, limit + 1, 3 * limit} {
		s := strings.Repeat("field.-12 ", count)
		for _, n := range []int{0, 1, limit - 1, limit, limit + 1, -1} {
			if got, want := re.FindAllStringSubmatchIndex(s, n), std.FindAllStringSubmatchIndex(s, n); !reflect.DeepEqual(got, want) {
				t.Fatalf("%d matches, n=%d: got %v, want %v", count, n, got, want)
			}
		}
	}
}

func TestDiffOnePassTable(t *testing.T) {
	defer SetCacheSize(SetCacheSize(0))
	abc := "abcdefghijklmnopqrstuvwxyz"
	for _, tc := range []struct {
		pat, hit string
		inputs   []string
		table    bool
	}{
		{isoDate, "2026", []string{"2026-11-20T15:29:32.010+08:00", "+020026", "2026T01:02z", "2026T12:3!Z", "2026T12:\xff0", ""}, true},
		{`^prefix/((a)|(b))*?()$`, "prefix/aba", []string{"prefix/", "prefix/ba", "prefix/a\xffb", "prefix/ayb"}, true},
		{`^(([\x00-\x40])|([A-Z])|([\x5b-\x7f]))*$`, "", []string{"\x00@A[Z\x7f", "\x00\x80\x7f", "a\xc3\xa9z"}, true},
		{`^(?i:(a+)|(B+))?()$`, "aAa", []string{"", "Bb", "AB", "\x00"}, true},
		{"^" + strings.Repeat("(a)", 22) + "(z)?$", strings.Repeat("a", 22), []string{strings.Repeat("a", 22) + "z"}, true},
		{"^" + strings.Repeat("(a)", 23) + "(z)?$", strings.Repeat("a", 23), []string{strings.Repeat("a", 23) + "z"}, false},
		{`^((a)|(\x{fffd}))*$`, "a", []string{"", "a\xffa", "a\ufffda", "a\xff\xfea", "a\ufffea"}, true},
		{`^(?i:((s)|(k)|(日)))+$`, "KK", []string{"ſ日s", "日\xff", "Ŝ", "ÅK"}, true},
		{`^(([\x7f-\x81])|([\x{83}-\x{85}])|(\x{10ffff}))*$`, "\u0081", []string{"\x7f\u0080\u0083", "\u0081\u0082", "\u0085\U0010ffff", "\u0086", "\ufffd"}, true},
		{`^(\pL+)?$`, "a", []string{"", "中华ſ", "a!"}, false},
		{`^(\ba+)?$`, "a", []string{"", "aa", "ba"}, false},
		{"^(" + strings.Repeat(abc, 3) + ")+$", strings.Repeat(abc, 3), []string{strings.Repeat(abc, 6), abc}, false},
	} {
		re, std := MustCompile(tc.pat), stdre.MustCompile(tc.pat)
		p := re.c.engines().onePassCaptures()
		if p == nil {
			t.Fatalf("%q did not compile as one-pass", tc.pat)
		}
		want := std.FindStringSubmatchIndex(tc.hit)
		for range onePassTableHits - 1 {
			if got := re.FindStringSubmatchIndex(tc.hit); got == nil || !slices.Equal(got, want) {
				t.Fatalf("%q on %q before table construction: got %v, want %v", tc.pat, tc.hit, got, want)
			}
		}
		if p.table.Load() != nil {
			t.Fatalf("%q built its table early", tc.pat)
		}
		re.FindStringSubmatchIndex(tc.hit)
		if got := p.table.Load() != nil; got != tc.table {
			t.Fatalf("%q: built table = %v, want %v", tc.pat, got, tc.table)
		}
		for _, s := range append(tc.inputs, tc.hit) {
			if got, want := re.FindStringSubmatchIndex(s), std.FindStringSubmatchIndex(s); !slices.Equal(got, want) {
				t.Errorf("%q on %q after warmup: got %v, want %v", tc.pat, s, got, want)
			}
		}
	}
}

func TestOnePassTableConcurrent(t *testing.T) {
	defer SetCacheSize(SetCacheSize(0))
	re := MustCompile(isoDate)
	const hit = "2026-11-20T15:29:32.010+08:00"
	const miss = "2026-11-20T15:29:32.010+08:!0"
	p := re.c.engines().onePassCaptures()
	for range 2 * onePassTableHits {
		re.FindStringSubmatchIndex(miss)
	}
	if p.hits.Load() != 0 {
		t.Fatal("failed searches counted toward table construction")
	}
	want := stdre.MustCompile(isoDate).FindStringSubmatchIndex(hit)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range onePassTableHits {
				if got := re.FindStringSubmatchIndex(hit); !slices.Equal(got, want) {
					t.Errorf("got %v, want %v", got, want)
					return
				}
			}
		}()
	}
	wg.Wait()
	if p.table.Load() == nil {
		t.Fatal("concurrent hits did not publish the table")
	}
}
