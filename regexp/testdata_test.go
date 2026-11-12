package regexp

import (
	"bufio"
	"compress/bzip2"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	stdre "regexp"
)

// The standard library's regexp test data (RE2 search tests and the
// AT&T POSIX suites) supply patterns and inputs; the expected results come
// from the standard library itself.

func goroot() string {
	if root := runtime.GOROOT(); root != "" {
		return root
	}
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func testdataDir(t *testing.T) string {
	t.Helper()
	root := goroot()
	if root == "" {
		t.Skip("GOROOT not found")
	}
	dir := filepath.Join(root, "src", "regexp", "testdata")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no regexp testdata in GOROOT")
	}
	return dir
}

// re2Cases reads an RE2 search test file: sections of quoted strings
// followed by quoted patterns, each followed by result lines this test
// ignores. It calls f for each pattern with the strings of its section.
func re2Cases(r io.Reader, f func(pat string, strs []string) bool) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var strs []string
	inStrings := false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "strings":
			strs, inStrings = strs[:0], true
		case line == "regexps":
			inStrings = false
		case strings.HasPrefix(line, `"`):
			s, err := strconv.Unquote(line)
			if err != nil {
				return err
			}
			if inStrings {
				strs = append(strs, s)
			} else if !f(s, strs) {
				return nil
			}
		}
	}
	return sc.Err()
}

// re2ExhaustiveCases samples every sixteenth pattern across the whole file
// under the race detector. Selected patterns keep all inputs and assertions.
func re2ExhaustiveCases(r io.Reader, f func(pat string, strs []string) bool) error {
	i := 0
	return re2Cases(r, func(pat string, strs []string) bool {
		use := !raceEnabled || i%16 == 0
		i++
		return !use || f(pat, strs)
	})
}

func TestRE2Search(t *testing.T) {
	file, err := os.Open(filepath.Join(testdataDir(t), "re2-search.txt"))
	if err != nil {
		t.Skip(err)
	}
	defer file.Close()
	n := 0
	err = re2Cases(file, func(pat string, strs []string) bool {
		for _, s := range strs {
			check(t, pat, s)
			checkAPI(t, pat, s, false)
			checkAPI(t, pat, s, true)
		}
		n++
		return !t.Failed()
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d patterns", n)
}

// TestRE2Exhaustive runs the large generated RE2 suite with the core
// methods: matches, indexes and submatches, leftmost-first and longest.
func TestRE2Exhaustive(t *testing.T) {
	if testing.Short() {
		t.Skip("long")
	}
	file, err := os.Open(filepath.Join(testdataDir(t), "re2-exhaustive.txt.bz2"))
	if err != nil {
		t.Skip(err)
	}
	defer file.Close()
	n := 0
	err = re2ExhaustiveCases(bzip2.NewReader(file), func(pat string, strs []string) bool {
		want, err := stdre.Compile(pat)
		got, err2 := Compile(pat)
		if (err == nil) != (err2 == nil) {
			t.Fatalf("Compile(%q): err %v, want %v", pat, err2, err)
		}
		if err != nil {
			return true
		}
		wantL, gotL := want.Copy(), got.Copy()
		wantL.Longest()
		gotL.Longest()
		for _, s := range strs {
			if g, w := got.MatchString(s), want.MatchString(s); g != w {
				t.Errorf("%q.MatchString(%q) = %v, want %v", pat, s, g, w)
			}
			if g, w := got.FindStringSubmatchIndex(s), want.FindStringSubmatchIndex(s); !slices.Equal(g, w) {
				t.Errorf("%q.FindStringSubmatchIndex(%q) = %v, want %v", pat, s, g, w)
			}
			if g, w := gotL.FindStringSubmatchIndex(s), wantL.FindStringSubmatchIndex(s); !slices.Equal(g, w) {
				t.Errorf("Longest %q.FindStringSubmatchIndex(%q) = %v, want %v", pat, s, g, w)
			}
			if g, w := got.FindAllStringSubmatchIndex(s, -1), want.FindAllStringSubmatchIndex(s, -1); !equalNested(g, w) {
				t.Errorf("%q.FindAllStringSubmatchIndex(%q) = %v, want %v", pat, s, g, w)
			}
		}
		n++
		return !t.Failed()
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d patterns", n)
}

func equalNested(a, b [][]int) bool {
	return slices.EqualFunc(a, b, func(x, y []int) bool { return slices.Equal(x, y) })
}

// TestPOSIXSuites runs the AT&T POSIX test files (basic, nullsubexpr,
// repetition): tab-separated flags, pattern, text and result.
func TestPOSIXSuites(t *testing.T) {
	dir := testdataDir(t)
	for _, name := range []string{"basic.dat", "nullsubexpr.dat", "repetition.dat"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Skip(err)
		}
		prev := ""
		for _, line := range strings.Split(string(data), "\n") {
			if line == "" || line[0] == '#' || strings.HasPrefix(line, "NOTE") || line[0] == ':' {
				continue
			}
			var fields []string
			for _, f := range strings.Split(line, "\t") {
				if f != "" {
					fields = append(fields, f)
				}
			}
			if len(fields) < 3 {
				continue
			}
			pat, text := fields[1], fields[2]
			if pat == "SAME" {
				pat = prev
			}
			prev = pat
			if text == "NULL" {
				text = ""
			}
			texts := []string{text}
			if u, err := strconv.Unquote(`"` + text + `"`); err == nil && u != text {
				texts = append(texts, u)
			}
			pats := []string{pat}
			if u, err := strconv.Unquote(`"` + pat + `"`); err == nil && u != pat {
				pats = append(pats, u)
			}
			for _, p := range pats {
				for _, s := range texts {
					check(t, p, s)
					checkAPI(t, p, s, false)
					checkAPI(t, p, s, true)
				}
			}
			if t.Failed() {
				t.Fatalf("%s: stopping at %q", name, line)
			}
		}
	}
}
