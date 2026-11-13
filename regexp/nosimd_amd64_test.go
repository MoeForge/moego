//go:build amd64 && !purego

package regexp

import (
	"fmt"
	"math/rand/v2"
	stdre "regexp"
	"strings"
	"testing"
)

// kernelPatterns run the AVX2 kernels: literal prefilters, case-sensitive
// and folded, search with indexPair; runs of an ASCII class and the loops
// of the DFA over one skip with span; and FindAll over unbounded runs
// takes class masks.
var kernelPatterns = []string{
	`"model":"`, `api_key`, `hello, world`, `data: \[DONE\]`, `ab`, `"role":"user"`,
	`(?i)error`, `(?i)bearer `, `(?i)content-type: application/json`, `(?i)x-api-key`, `(?i)zq`,
	`[a-z0-9]{16,}`, `[A-Za-z0-9+/]{20,}={0,2}`, `\d{16,}`, `[^\n]{32,}`, `[ -~]{40,80}`, `[a-f0-9]{32}`,
	`[a-z]+`, `\w+`, `\s+`, `[0-9]+`, `[^ ]+`, `x*`,
	`sk-[A-Za-z0-9_-]{20,}`, `Bearer [A-Za-z0-9._~+/-]+=*`, `key=[a-z]{32,}`, `"content":"[^"]{16,}`,
	`"(?:[^"\\]|\\.)*"`, `[a-z0-9._-]*@`, `[^\n]*\n`, `(?s)<think>.*?</think>`, `(?m)^data: (.*)$`,
	`\b[A-Z][a-z]+\b`, `[a-z]{3,}[0-9]{2,}`, `(\w+)@(\w+)\.com`, `(?i)error|warn(?:ing)?`,
}

// kernelText returns text with long runs of the classes of kernelPatterns,
// the literals they look for, non-ASCII bytes and newlines.
func kernelText(r *rand.Rand) string {
	runs := []string{"abcdefghijklmnopqrstuvwxyz0123456789", "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/",
		"0123456789", "    \t", "abcdef0123456789", "aaaa", " !#$%&()*+,-./:;<=>?[]^_{|}~"}
	plants := []string{`"model":"gpt-4o"`, "api_key", "API_KEY", "hello, world", "data: [DONE]\n", "Error", "ERROR",
		"eRRor", "Bearer abc.def-ghi=", "bearer ", "Content-Type: application/json", "X-Api-Key", "zQ",
		"sk-", "key=", "<think>", "</think>", `"content":"`, `"role":"user"`, `\"`, "\"", "\n", "@", ".com",
		"é", "中文", "\xff", "\xe2\x80\xa8", "warning", "Warn", "x", "ab"}
	var b strings.Builder
	n := r.IntN(40)
	if r.IntN(8) == 0 {
		n = 200
	}
	for range n {
		if r.IntN(3) == 0 {
			b.WriteString(plants[r.IntN(len(plants))])
			continue
		}
		run := runs[r.IntN(len(runs))]
		for k := r.IntN(100); k > 0; k-- {
			b.WriteByte(run[r.IntN(len(run))])
		}
	}
	return b.String()
}

// setKernels turns the AVX2 kernels on, as this CPU has them, or off, as
// on CPUs without AVX2.
func setKernels(on bool) {
	if on {
		haveAVX2, hasPairKernel, hasSpanKernel = true, true, true
	} else {
		haveAVX2, hasPairKernel, hasSpanKernel = false, false, false
	}
}

// kernelMatcher is what kernelResults calls, which the standard library
// has too.
type kernelMatcher interface {
	MatchString(s string) bool
	FindStringIndex(s string) []int
	FindAllStringIndex(s string, n int) [][]int
	FindAllStringSubmatchIndex(s string, n int) [][]int
	ReplaceAllString(src, repl string) string
	ReplaceAllLiteralString(src, repl string) string
	Split(s string, n int) []string
}

// kernelResults returns what re gives on s, one line per method.
func kernelResults(re kernelMatcher, s string) string {
	var b strings.Builder
	fmt.Fprintln(&b, re.MatchString(s))
	fmt.Fprintln(&b, re.FindStringIndex(s))
	fmt.Fprintln(&b, re.FindAllStringIndex(s, -1))
	fmt.Fprintln(&b, re.FindAllStringIndex(s, 2))
	fmt.Fprintln(&b, re.FindAllStringSubmatchIndex(s, -1))
	fmt.Fprintf(&b, "%q\n", re.ReplaceAllString(s, "<$0>"))
	fmt.Fprintf(&b, "%q\n", re.ReplaceAllLiteralString(s, "#"))
	fmt.Fprintf(&b, "%q\n", re.Split(s, -1))
	if re, ok := re.(*Regexp); ok {
		n := 0
		for range re.AllStringIndex(s) {
			n++
		}
		fmt.Fprintln(&b, n)
	}
	return b.String()
}

// TestKernelsOff compares what each expression gives with the AVX2 kernels
// and without them, and with the standard library, on text that reaches
// the kernels. Expressions choose some scans when they are compiled, so it
// also compiles with the kernels on and matches with them off, and the
// other way round. The compile cache is off, so each compile chooses anew.
func TestKernelsOff(t *testing.T) {
	requireAVX2(t)
	prev := SetCacheSize(0)
	t.Cleanup(func() {
		setKernels(true)
		SetCacheSize(prev)
	})
	modes := []struct {
		name         string
		compile, run bool
	}{
		{"off", false, false},
		{"compiled on, run off", true, false},
		{"compiled off, run on", false, true},
	}
	r := rand.New(rand.NewPCG(2026, 1113))
	texts := make([]string, randomIterations(100))
	for i := range texts {
		texts[i] = kernelText(r)
	}
	texts = append(texts, "", strings.Repeat("a", 300), strings.Repeat("sk-abcdefghijklmnopqrstuvwxyz ", 40),
		strings.Repeat("0123456789abcdef", 64)+"key="+strings.Repeat("q", 40))
	var kinds [planOnePassLit + 1]int
	vec := 0
	for _, pat := range kernelPatterns {
		for _, longest := range []bool{false, true} {
			setKernels(true)
			on := MustCompile(pat)
			std := stdre.MustCompile(pat)
			if longest {
				on.Longest()
				std.Longest()
			}
			kinds[on.c.kind]++
			if on.c.kind == planRun && on.c.run().vec {
				vec++
			}
			for _, s := range texts {
				want := kernelResults(on, s)
				if got, std := strings.SplitAfterN(want, "\n", 9), kernelResults(std, s); strings.Join(got[:8], "") != std {
					t.Fatalf("%q (longest %v) on %q with AVX2:\n%s\nstandard library:\n%s", pat, longest, s, want, std)
				}
				for _, m := range modes {
					setKernels(m.compile)
					re := MustCompile(pat)
					if longest {
						re.Longest()
					}
					setKernels(m.run)
					if got := kernelResults(re, s); got != want {
						t.Fatalf("%q (longest %v) on %q, kernels %s:\n%s\nwith AVX2:\n%s", pat, longest, s, m.name, got, want)
					}
				}
				setKernels(true)
			}
		}
	}
	// The expressions reach the plans whose scans run the kernels.
	if kinds[planLiteral] == 0 || kinds[planFold] == 0 || kinds[planRun] == 0 || kinds[planLitRun] == 0 || kinds[planDFA] == 0 || vec == 0 {
		t.Fatalf("plans by kind %v, %d runs with the vector scan", kinds, vec)
	}
}
