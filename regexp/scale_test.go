package regexp

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestLinearScaling checks that search time grows linearly with the input
// on patterns that are slow or explosive for other engines, and that the
// memory a search allocates does not grow with it. All-matches searches
// count through the iterators, so the time measured is the search and not
// the allocation of millions of results. Searches that finish in constant
// time (a quick reject) are not compared. It times real work, so it runs
// only with RXSCALE=1, on an otherwise idle CPU.
func TestLinearScaling(t *testing.T) {
	if os.Getenv("RXSCALE") == "" {
		t.Skip("set RXSCALE=1")
	}
	var alt []string
	for i := 0; i < 1000; i++ {
		alt = append(alt, fmt.Sprintf("word%dx", i*7919%100000))
	}
	cases := []struct {
		name, pat, unit string
		op              string
	}{
		{"alt-aa", `(a|aa)*b`, "a", "match"},
		{"alt-aa-find", `(a|aa)*b`, "aaaaaaab", "findall"},
		{"range-1000", `[a-z]{1,1000}`, "abcdefghij ", "findall"},
		{"nested-plus", `(x+x+)+y`, "x", "match"},
		{"opt-30", `(?:a?){30}a{30}`, "a", "find"},
		{"dot-groups", `(.*)(.*)(.*)(.*)(.*)$`, "ab\n", "findall-sub"},
		{"letters", `\pL+`, "日本語 abc ", "findall"},
		{"alt-1000", strings.Join(alt, "|"), "word123 x word77x ", "findall"},
		{"fold-alt", `(?i)(?:error|warning|fatal)\b`, "an Error occurred, fatal-ish; ", "findall"},
		{"inner", `[^@\s]+@[a-z]+\.com`, "user name@host x@y.com ", "findall"},
		{"end-anchored", `[a-z ]*zzz$`, "abc def ", "match"},
		{"longest", `(a|ab|abc)+d`, "abcab", "longest"},
		{"submatch-window", `"text":"((?:[^"\\]|\\.)*)"`, `{"text":"a\"b c"} `, "findall-sub"},
	}
	sizes := []int{1 << 20, 2 << 20, 4 << 20, 8 << 20, 16 << 20, 32 << 20, 64 << 20}
	for _, c := range cases {
		re := MustCompile(c.pat)
		if c.op == "longest" {
			re.Longest()
		}
		run := func(s string) {
			switch c.op {
			case "match":
				re.MatchString(s)
			case "find":
				re.FindStringIndex(s)
			case "findall", "longest":
				for range re.AllStringIndex(s) {
				}
			case "findall-sub":
				for range re.AllStringSubmatchIndex(s) {
				}
			}
		}
		var prev time.Duration
		var line strings.Builder
		fmt.Fprintf(&line, "%-16s", c.name)
		for _, n := range sizes {
			s := strings.Repeat(c.unit, n/len(c.unit)+1)[:n]
			best := time.Duration(1 << 62)
			for k := 0; k < 5; k++ {
				runtime.GC()
				t0 := time.Now()
				run(s)
				best = min(best, time.Since(t0))
			}
			fmt.Fprintf(&line, " %dM=%v", n>>20, best.Round(time.Microsecond))
			if prev > 0 && n >= 4<<20 && best > time.Millisecond {
				r := float64(best) / float64(prev)
				if r < 1.7 || r > 2.3 {
					t.Errorf("%s: %d MiB took %.2f× the time of %d MiB", c.name, n>>20, r, n>>21)
				}
			}
			prev = best
			if c.op == "match" || c.op == "find" {
				var m0, m1 runtime.MemStats
				runtime.ReadMemStats(&m0)
				run(s)
				runtime.ReadMemStats(&m1)
				if a := m1.TotalAlloc - m0.TotalAlloc; a > 4<<20 {
					t.Errorf("%s: a search of %d MiB allocated %d bytes", c.name, n>>20, a)
				}
			}
		}
		t.Log(line.String())
	}
}
