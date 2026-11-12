package regexp

import (
	"bufio"
	"compress/bzip2"
	"os"
	"path/filepath"
	"regexp/syntax"
	"testing"
)

// analyzePats are patterns whose start anchoring and U+FFFD handling are
// easy to get wrong from the syntax tree.
var analyzePats = []string{
	`^abc`, `\Aabc`, `(?m)^abc`, `^a|^b`, `^a|b`, `(^a)`, `(^)+a`, `(?:^)*a`, `(?:^){2}a`,
	`(?:^){0,2}a`, `\b^a`, `(?:\b|x)^a`, `(^|\b)^a`, `a^b`, `(?:)^a`, `^*a`, `^?a`, `$^a`,
	`(?:^a|^b)c$`, `(?:^|x)a`, `\x{FFFD}`, `[\x{FFF0}-\x{FFFF}]`, `[^a]`, `.`, `(?s).`,
	`[^\x00-\x{10FFFF}]\x{FFFD}`, `(?i)\x{FFFD}`, `a|\x{FFFD}`, `\pL`, `\PL`, `[^\n]`,
	`\x{D800}`, `^\x{D800}`, `^a\x{DFFF}b`, `[\x{D800}]`, `^(?:\x{D800}|a)`,
}

func TestTreeAnalysis(t *testing.T) {
	pats := append([]string(nil), analyzePats...)
	if dir := testdataDirOrEmpty(); dir != "" {
		if f, err := os.Open(filepath.Join(dir, "re2-search.txt")); err == nil {
			re2Cases(f, func(pat string, _ []string) bool {
				pats = append(pats, pat)
				return true
			})
			f.Close()
		}
		if f, err := os.Open(filepath.Join(dir, "re2-exhaustive.txt.bz2")); err == nil && !testing.Short() {
			re2ExhaustiveCases(bzip2.NewReader(bufio.NewReader(f)), func(pat string, _ []string) bool {
				pats = append(pats, pat)
				return true
			})
			f.Close()
		}
	}
	loose := 0
	for _, pat := range pats {
		for _, mode := range []syntax.Flags{syntax.Perl, syntax.POSIX} {
			re, err := syntax.Parse(pat, mode)
			if err != nil {
				continue
			}
			p := compileProg(re, re.MaxCap())
			anchored, fffd := p.anchoredStart(), p.hasFFFD(p.reachable())
			if startAnchored(re) && !anchored {
				t.Errorf("%q: startAnchored true, program not anchored", pat)
			}
			if fffd && !mayMatchFFFD(re) {
				t.Errorf("%q: mayMatchFFFD false, program matches U+FFFD", pat)
			}
			if p.onePassLitPlan() != nil && !hasUnencodable(re) {
				t.Errorf("%q: one-pass literal plan without an unencodable rune", pat)
			}
			if anchored && !startAnchored(re) {
				loose++
			}
		}
	}
	t.Logf("%d patterns, %d anchored only by the program", len(pats), loose)
}

func testdataDirOrEmpty() string {
	root := goroot()
	if root == "" {
		return ""
	}
	dir := filepath.Join(root, "src", "regexp", "testdata")
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	return dir
}
