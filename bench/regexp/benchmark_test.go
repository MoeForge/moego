package bench

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	moego "github.com/MoeForge/moego/regexp"
)

// mismatches records, per case, the implementations whose results differ
// from the standard library. Their timings do not count for that case.
func mismatches(t testing.TB) map[string][]string {
	out := map[string][]string{}
	for i := range cases() {
		c := &cases()[i]
		var want []any
		for _, im := range impls {
			e, err := im.compile(c.pat)
			if err != nil {
				out[c.class+"/"+c.name] = append(out[c.class+"/"+c.name], im.name)
				continue
			}
			var got []any
			for _, s := range c.in {
				got = append(got, run(e, c, s))
			}
			if im.name == "Stdlib" {
				want = got
			} else if !reflect.DeepEqual(got, want) {
				out[c.class+"/"+c.name] = append(out[c.class+"/"+c.name], im.name)
			}
		}
	}
	return out
}

func TestCompetitors(t *testing.T) {
	for k, v := range mismatches(t) {
		for _, name := range v {
			if name == "Moego" {
				t.Errorf("%s: Moego differs from the standard library", k)
			} else {
				t.Logf("%s: %s differs from the standard library (excluded for this case)", k, name)
			}
		}
	}
}

// TestSensitiveFilter checks that every implementation filters the
// sensitive-filter inputs to the same bytes, and that the inputs have
// about one hit per KiB.
func TestSensitiveFilter(t *testing.T) {
	for _, c := range casesForClass("C") {
		if !strings.HasPrefix(c.name, "sensitive-filter-") {
			continue
		}
		var want string
		for _, im := range impls {
			e, err := im.compile(c.pat)
			if err != nil {
				t.Fatalf("%s %s: %v", c.name, im.name, err)
			}
			got := e.ReplaceAllString(c.in[0], replacements[c.name])
			if im.name == "Stdlib" {
				want = got
				hits := len(e.FindAllStringIndex(c.in[0], -1))
				kib := float64(len(c.in[0])) / 1024
				t.Logf("%s: %d bytes, %d hits, %.2f per KiB", c.name, len(c.in[0]), hits, float64(hits)/kib)
				if float64(hits) < 0.6*kib || float64(hits) > 1.5*kib {
					t.Errorf("%s: %d hits in %.0f KiB", c.name, hits, kib)
				}
			} else if got != want {
				t.Errorf("%s: %s output differs from the standard library", c.name, im.name)
			}
		}
	}
}

func BenchmarkRegexp(b *testing.B) {
	for i := range cases() {
		c := &cases()[i]
		for _, im := range impls {
			b.Run(fmt.Sprintf("%s/%s/%s", c.class, c.name, im.name), func(b *testing.B) {
				body, err := direct(im.name, c)
				if err != nil {
					b.Skip(err)
				}
				total := 0
				for _, s := range c.in {
					total += len(s)
				}
				b.SetBytes(int64(total))
				b.ReportAllocs()
				body(&testing.B{N: 1}) // warm up caches outside the timed loop
				b.ResetTimer()
				body(b)
			})
		}
	}
}

var compilePats = []struct{ name, pat string }{
	{"slug", `^[a-zA-Z0-9_-]+$`},
	{"model-alt", `^(gpt-[a-z0-9][a-z0-9._-]*|o[1-9][a-z0-9._-]*)$`},
	{"host-mask", `(?i)\bapi3\.example\.com\b`},
	{"email", `[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`},
	{"url-validator", `^https?://(?:(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?|(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?))(?::[0-9]{1,5})?(?:/[^\s]*)?$`},
	{"letters", `\pL+`},
	{"think", `(?s)<think>(.*?)</think>`},
	{"sse-content", `"content":"((?:[^"\\]|\\.)*)"`},
}

// BenchmarkCompile measures cold compiles: moego's compile cache is off.
func BenchmarkCompile(b *testing.B) {
	defer moego.SetCacheSize(moego.SetCacheSize(0))
	for _, cp := range compilePats {
		for _, im := range impls {
			b.Run(cp.name+"/"+im.name, func(b *testing.B) {
				b.ReportAllocs()
				for k := 0; k < b.N; k++ {
					if _, err := im.compile(cp.pat); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkCompileWarm measures repeated compiles of one expression with
// moego's compile cache on (the default).
func BenchmarkCompileWarm(b *testing.B) {
	for _, cp := range compilePats {
		b.Run(cp.name+"/Moego", func(b *testing.B) {
			b.ReportAllocs()
			for k := 0; k < b.N; k++ {
				if _, err := moego.Compile(cp.pat); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkCompileSensitive measures cold compiles of the 1000-word pattern
// of the sensitive-filter cases: moego's compile cache is off. It is not
// part of BenchmarkCompile's summary.
func BenchmarkCompileSensitive(b *testing.B) {
	defer moego.SetCacheSize(moego.SetCacheSize(0))
	pat := SensitivePattern(SensitiveWords(1000, 10))
	for _, im := range impls {
		b.Run("sensitive-1000/"+im.name, func(b *testing.B) {
			b.ReportAllocs()
			for k := 0; k < b.N; k++ {
				if _, err := im.compile(pat); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
