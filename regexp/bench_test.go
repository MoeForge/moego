package regexp

import (
	stdre "regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var benchHeaders = []string{"Content-Type", "Authorization", "X-Request-Id", "X-Trace-Id", "User-Agent", "Accept", "X-Stainless-Os", "X-Stainless-Lang", "Anthropic-Version", "Anthropic-Beta", "Openai-Organization", "X-Forwarded-For", "Accept-Encoding", "Connection", "X-Api-Key", "Cookie"}

func BenchmarkShortHeaders(b *testing.B) {
	re := MustCompile(`(?i)^x-(request|trace)-id$`)
	for i := 0; i < b.N; i++ {
		for _, h := range benchHeaders {
			re.MatchString(h)
		}
	}
}

func BenchmarkShortSlug(b *testing.B) {
	re := MustCompile(`^[a-zA-Z0-9_-]+$`)
	for i := 0; i < b.N; i++ {
		re.MatchString("my_plugin-key_2026-v1")
	}
}

func BenchmarkLongRun(b *testing.B) {
	re := MustCompile(`[A-Za-z0-9+/]{64,}={0,2}`)
	s := "x " + strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo", 30000) + "== y"
	b.SetBytes(int64(len(s)))
	for i := 0; i < b.N; i++ {
		re.FindStringIndex(s)
	}
}

func BenchmarkWild(b *testing.B) {
	re := MustCompile(`^claude-.*-thinking$`)
	ins := []string{"gpt-4o-mini-2024-07-18", "claude-sonnet-4-5-20250929", "gemini-2.5-pro", "o3-mini-high", "deepseek-r1-0528", "qwen3-235b-a22b-thinking-2507", "gpt-5-codex", "claude-opus-4-1-thinking"}
	for i := 0; i < b.N; i++ {
		for _, s := range ins {
			re.MatchString(s)
		}
	}
}

func BenchmarkWSRun(b *testing.B) {
	var sb strings.Builder
	for sb.Len() < 64<<10 {
		sb.WriteString("The model replied in  two\twords, then\n\nthree more. ")
	}
	s := sb.String()
	re := MustCompile(`\s+`)
	b.SetBytes(int64(len(s)))
	for i := 0; i < b.N; i++ {
		re.FindAllStringIndex(s, -1)
	}
}

func BenchmarkCompileCases(b *testing.B) {
	defer SetCacheSize(SetCacheSize(0))
	for _, p := range []string{`(?i)\bapi3\.example\.com\b`, `[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`, `\pL+`} {
		b.Run(p[:4], func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				MustCompile(p)
			}
		})
	}
}

func BenchmarkNegIndex(b *testing.B) {
	re := MustCompile(`\.(-\d+)`)
	paths := []string{"messages.-1.content", "messages.0.content.-2.text", "tools.3.function.name", "metadata.user_id", "input.-1"}
	for _, s := range paths {
		b.Run(s, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				re.FindAllStringSubmatchIndex(s, -1)
			}
		})
	}
	b.Run("find", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			for _, s := range paths {
				re.FindStringIndex(s)
			}
		}
	})
	b.Run("all", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for _, s := range paths {
				re.FindAllStringSubmatchIndex(s, -1)
			}
		}
	})
}

// isoDate is the anchored pattern with 11 capture groups from issue #6.
const isoDate = `^([+-][0-9]{6}|[0-9]{4})(?:-([0-9]{2})(?:-([0-9]{2}))?)?(?:[Tt]([0-9]{2}):([0-9]{2})(?::([0-9]{2})(?:\.([0-9]+))?)?(?:([Zz])|([+-])([0-9]{2}):?([0-9]{2}))?)?$`
const isoDateHit = "2026-11-20T15:29:32.010+08:00"

func BenchmarkISODate(b *testing.B) {
	std, re := stdre.MustCompile(isoDate), MustCompile(isoDate)
	for _, in := range []struct{ name, text string }{
		{"hit", isoDateHit},
		{"miss", "Fri, 20 Nov 2026 15:29:32 +0800"},
		{"bad-zone-digit", "2026-11-20T15:29:32.010+08:0!"},
		{"bad-minute-digit", "2026-11-20T15:2!:32.010+08:00"},
	} {
		if !slices.Equal(std.FindStringSubmatch(in.text), re.FindStringSubmatch(in.text)) {
			b.Fatalf("%s: submatches differ", in.name)
		}
		for _, impl := range []struct {
			name     string
			match    func(string) bool
			submatch func(string) []string
		}{
			{"stdlib", std.MatchString, std.FindStringSubmatch},
			{"moego", re.MatchString, re.FindStringSubmatch},
		} {
			b.Run(in.name+"/MatchString/"+impl.name, func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					impl.match(in.text)
				}
			})
			b.Run(in.name+"/FindStringSubmatch/"+impl.name, func(b *testing.B) {
				b.ReportAllocs()
				// Measure misses on the same warmed expression as hits.
				for range 128 {
					impl.submatch(isoDateHit)
				}
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					impl.submatch(in.text)
				}
			})
		}
	}
}

// Measure the search with an owned index result. These hits have the same
// result allocation in both engines, including when the scratch is pooled.
func BenchmarkOnePassSubmatchIndex(b *testing.B) {
	for _, tc := range []struct{ name, pat, text string }{
		{"date", isoDate, isoDateHit},
		{"repeated", `^a(/b+(#c+)*)*$`, "a/b#cc/bb#ccc"},
		{"unicode", `(?i)^((k)|(s)|(日)|([\x{FFFD}]))+$`, "K日ſ\xffK"},
		{"many", "^" + strings.Repeat("(a)", 32) + "(z)?$", strings.Repeat("a", 32) + "z"},
	} {
		re, std := MustCompile(tc.pat), stdre.MustCompile(tc.pat)
		want := std.FindStringSubmatchIndex(tc.text)
		if want == nil || !slices.Equal(re.FindStringSubmatchIndex(tc.text), want) {
			b.Fatalf("%s: submatch indexes differ or input does not match", tc.name)
		}
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				re.FindStringSubmatchIndex(tc.text)
			}
		})
	}
}

// Measure the cost over the lifetime of a freshly compiled expression,
// including setup that a steady-state submatch benchmark amortizes away.
func BenchmarkISODateReuse(b *testing.B) {
	defer SetCacheSize(SetCacheSize(0))
	for _, uses := range []int{1, 256} {
		b.Run(strconv.Itoa(uses), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				re := MustCompile(isoDate)
				for range uses {
					re.FindStringSubmatch(isoDateHit)
				}
			}
		})
	}
}
