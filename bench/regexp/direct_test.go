// Code generated from one template for every implementation; DO NOT EDIT.

package bench

import (
	"testing"

	stdre "regexp"

	moego "github.com/MoeForge/moego/regexp"
	"github.com/coregx/coregex"
	grafana "github.com/grafana/regexp"
	re2 "github.com/wasilibs/go-re2"
)

// sink keeps benchmark results observable.
var sink int

// direct returns the benchmark body for implementation name on case c. Each
// implementation has its own copy of the loop with its concrete type, so the
// calls are direct, as in user code, rather than through an interface.
func direct(name string, c *benchCase) (func(b *testing.B), error) {
	switch name {
	case "Stdlib":
		return directStdlib(c)
	case "Grafana":
		return directGrafana(c)
	case "Coregex":
		return directCoregex(c)
	case "GoRE2":
		return directGoRE2(c)
	case "Moego":
		return directMoego(c)
	}
	panic(name)
}

func directStdlib(c *benchCase) (func(b *testing.B), error) {
	re, err := stdre.Compile(c.pat)
	if err != nil {
		return nil, err
	}
	in, repl := c.in, replacements[c.name]
	loop := func(f func(s string) int) func(b *testing.B) {
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					k += f(s)
				}
			}
			sink += k
		}
	}
	switch c.op {
	case opMatch:
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					if re.MatchString(s) {
						k++
					}
				}
			}
			sink += k
		}, nil
	case opFind:
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					k += len(re.FindStringIndex(s))
				}
			}
			sink += k
		}, nil
	case opFindAll:
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					k += len(re.FindAllStringIndex(s, -1))
				}
			}
			sink += k
		}, nil
	case opSubmatch:
		return loop(func(s string) int { return len(re.FindStringSubmatchIndex(s)) }), nil
	case opSubAll:
		return loop(func(s string) int { return len(re.FindAllStringSubmatchIndex(s, -1)) }), nil
	case opReplace:
		return loop(func(s string) int { return len(re.ReplaceAllString(s, repl)) }), nil
	}
	panic(c.op)
}

func directGrafana(c *benchCase) (func(b *testing.B), error) {
	re, err := grafana.Compile(c.pat)
	if err != nil {
		return nil, err
	}
	in, repl := c.in, replacements[c.name]
	loop := func(f func(s string) int) func(b *testing.B) {
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					k += f(s)
				}
			}
			sink += k
		}
	}
	switch c.op {
	case opMatch:
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					if re.MatchString(s) {
						k++
					}
				}
			}
			sink += k
		}, nil
	case opFind:
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					k += len(re.FindStringIndex(s))
				}
			}
			sink += k
		}, nil
	case opFindAll:
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					k += len(re.FindAllStringIndex(s, -1))
				}
			}
			sink += k
		}, nil
	case opSubmatch:
		return loop(func(s string) int { return len(re.FindStringSubmatchIndex(s)) }), nil
	case opSubAll:
		return loop(func(s string) int { return len(re.FindAllStringSubmatchIndex(s, -1)) }), nil
	case opReplace:
		return loop(func(s string) int { return len(re.ReplaceAllString(s, repl)) }), nil
	}
	panic(c.op)
}

func directCoregex(c *benchCase) (func(b *testing.B), error) {
	re, err := coregex.Compile(c.pat)
	if err != nil {
		return nil, err
	}
	in, repl := c.in, replacements[c.name]
	loop := func(f func(s string) int) func(b *testing.B) {
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					k += f(s)
				}
			}
			sink += k
		}
	}
	switch c.op {
	case opMatch:
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					if re.MatchString(s) {
						k++
					}
				}
			}
			sink += k
		}, nil
	case opFind:
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					k += len(re.FindStringIndex(s))
				}
			}
			sink += k
		}, nil
	case opFindAll:
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					k += len(re.FindAllStringIndex(s, -1))
				}
			}
			sink += k
		}, nil
	case opSubmatch:
		return loop(func(s string) int { return len(re.FindStringSubmatchIndex(s)) }), nil
	case opSubAll:
		return loop(func(s string) int { return len(re.FindAllStringSubmatchIndex(s, -1)) }), nil
	case opReplace:
		return loop(func(s string) int { return len(re.ReplaceAllString(s, repl)) }), nil
	}
	panic(c.op)
}

func directGoRE2(c *benchCase) (func(b *testing.B), error) {
	re, err := re2.Compile(c.pat)
	if err != nil {
		return nil, err
	}
	in, repl := c.in, replacements[c.name]
	loop := func(f func(s string) int) func(b *testing.B) {
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					k += f(s)
				}
			}
			sink += k
		}
	}
	switch c.op {
	case opMatch:
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					if re.MatchString(s) {
						k++
					}
				}
			}
			sink += k
		}, nil
	case opFind:
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					k += len(re.FindStringIndex(s))
				}
			}
			sink += k
		}, nil
	case opFindAll:
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					k += len(re.FindAllStringIndex(s, -1))
				}
			}
			sink += k
		}, nil
	case opSubmatch:
		return loop(func(s string) int { return len(re.FindStringSubmatchIndex(s)) }), nil
	case opSubAll:
		return loop(func(s string) int { return len(re.FindAllStringSubmatchIndex(s, -1)) }), nil
	case opReplace:
		return loop(func(s string) int { return len(re.ReplaceAllString(s, repl)) }), nil
	}
	panic(c.op)
}

func directMoego(c *benchCase) (func(b *testing.B), error) {
	re, err := moego.Compile(c.pat)
	if err != nil {
		return nil, err
	}
	in, repl := c.in, replacements[c.name]
	loop := func(f func(s string) int) func(b *testing.B) {
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					k += f(s)
				}
			}
			sink += k
		}
	}
	switch c.op {
	case opMatch:
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					if re.MatchString(s) {
						k++
					}
				}
			}
			sink += k
		}, nil
	case opFind:
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					k += len(re.FindStringIndex(s))
				}
			}
			sink += k
		}, nil
	case opFindAll:
		return func(b *testing.B) {
			k := 0
			for i := 0; i < b.N; i++ {
				for _, s := range in {
					k += len(re.FindAllStringIndex(s, -1))
				}
			}
			sink += k
		}, nil
	case opSubmatch:
		return loop(func(s string) int { return len(re.FindStringSubmatchIndex(s)) }), nil
	case opSubAll:
		return loop(func(s string) int { return len(re.FindAllStringSubmatchIndex(s, -1)) }), nil
	case opReplace:
		return loop(func(s string) int { return len(re.ReplaceAllString(s, repl)) }), nil
	}
	panic(c.op)
}
