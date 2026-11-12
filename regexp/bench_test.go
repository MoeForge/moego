package regexp

import (
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
