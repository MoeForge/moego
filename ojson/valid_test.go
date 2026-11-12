package ojson

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// validGen writes random JSON with runs that cross 64-byte blocks.
type validGen struct {
	seed uint64
	b    strings.Builder
}

func (g *validGen) n(k int) int {
	g.seed = g.seed*6364136223846793005 + 1442695040888963407
	return int(g.seed>>33) % k
}

func (g *validGen) space() {
	switch g.n(6) {
	case 0:
		g.b.WriteString(strings.Repeat(" ", g.n(80)))
	case 1:
		g.b.WriteString([]string{" ", "\t", "\n", "\r", "\r\n  ", "\n\t\t"}[g.n(6)])
	}
}

func (g *validGen) str() {
	g.b.WriteByte('"')
	for k := g.n(12); k > 0; k-- {
		switch g.n(10) {
		case 0:
			g.b.WriteString(strings.Repeat("a", g.n(90)))
		case 1:
			g.b.WriteString(strings.Repeat(`\\`, 1+g.n(4)))
		case 2:
			g.b.WriteString([]string{`\"`, `\/`, `\b`, `\f`, `\n`, `\r`, `\t`}[g.n(7)])
		case 3:
			g.b.WriteString([]string{`é`, `😀`, `"`, `\`, `ꯍ`}[g.n(5)])
		case 4:
			g.b.WriteString([]string{"é", "世界", "\x7f", "\xff", "{[:,]}", " ", "'"}[g.n(7)])
		default:
			g.b.WriteString([]string{"x", "ab", "true", "1", "-"}[g.n(5)])
		}
	}
	g.b.WriteByte('"')
}

func (g *validGen) value(depth int) {
	g.space()
	switch k := g.n(10); {
	case depth < 6 && k < 2:
		g.b.WriteByte('{')
		for m, n := 0, g.n(5); m < n; m++ {
			if m > 0 {
				g.space()
				g.b.WriteByte(',')
			}
			g.space()
			g.str()
			g.space()
			g.b.WriteByte(':')
			g.value(depth + 1)
		}
		g.space()
		g.b.WriteByte('}')
	case depth < 6 && k < 4:
		g.b.WriteByte('[')
		for m, n := 0, g.n(6); m < n; m++ {
			if m > 0 {
				g.space()
				g.b.WriteByte(',')
			}
			g.value(depth + 1)
		}
		g.space()
		g.b.WriteByte(']')
	case k < 6:
		g.str()
	case k < 8:
		g.b.WriteString([]string{"0", "-0", "12", "-3.25", "1e5", "6.02E+23", "-7e-3", "0.5", "123456789012345678901234567890", "1.5e300"}[g.n(10)])
	default:
		g.b.WriteString([]string{"true", "false", "null"}[g.n(3)])
	}
	g.space()
}

// TestValidBlocks compares Valid with encoding/json on random
// documents and on mutations of them.
func TestValidBlocks(t *testing.T) {
	if !blockKernels() {
		t.Skip("no block kernels")
	}
	const nasty = "\"\\{}[]:,0123456789-+.eEtrufalsn \t\n\r\x00\x01\x1f\x7f\x80\xff/bu"
	g := &validGen{seed: 11}
	check := func(s string) {
		if got, want := Valid(s), json.Valid([]byte(s)); got != want {
			t.Fatalf("Valid(%q) = %v, want %v", s, got, want)
		}
	}
	valid := 0
	for n := 0; n < 40000; n++ {
		g.b.Reset()
		g.value(0)
		s := g.b.String()
		if json.Valid([]byte(s)) {
			valid++
		}
		check(s)
		for k := 0; k < 6 && len(s) > 0; k++ {
			b := []byte(s)
			i := g.n(len(b))
			switch g.n(4) {
			case 0:
				b[i] = nasty[g.n(len(nasty))]
			case 1:
				b = append(b[:i], b[i+1:]...)
			case 2:
				b = append(b[:i], append([]byte{nasty[g.n(len(nasty))]}, b[i:]...)...)
			default:
				b = b[:i]
			}
			check(string(b))
		}
	}
	if valid < 30000 {
		t.Fatalf("only %d of the generated documents are valid", valid)
	}
	for _, s := range []string{"", " ", "1", `"`, `"\`, `"\u`, `"\u12`, `[1,`, strings.Repeat(" ", 63) + "1", strings.Repeat(" ", 64) + "1",
		strings.Repeat(" ", 62) + `"\` + `"`, strings.Repeat(" ", 63) + `"\"`, "[" + strings.Repeat(`"\\",`, 40) + "1]"} {
		check(s)
	}
}

// TestValidBlocksDeep compares Valid with encoding/json on mixed
// nesting around the words of its stack and its depth limit.
func TestValidBlocksDeep(t *testing.T) {
	if !blockKernels() {
		t.Skip("no block kernels")
	}
	g := &validGen{seed: 3}
	for _, depth := range []int{1, 2, 63, 64, 65, 127, 128, 129, 300, 4096 - 1, 4096, 4096 + 1} {
		for n := 0; n < 20; n++ {
			var open, close []string
			for k := 0; k < depth; k++ {
				if g.n(2) == 0 {
					open = append(open, []string{`{"a":`, `{ "b" : 1, "c":`, `{"":`}[g.n(3)])
					close = append(close, "}")
				} else {
					open = append(open, []string{"[", "[1,", `[ "x" , `}[g.n(3)])
					close = append(close, "]")
				}
			}
			slices.Reverse(close)
			if n > 0 {
				// Break or change one closer, or add a member after it.
				k := g.n(depth)
				switch n % 4 {
				case 0:
					close[k] = map[string]string{"}": "]", "]": "}"}[close[k]]
				case 1:
					close[k] = ""
				case 2:
					close[k] += ",2"
				default:
					close[k] += " , 2"
				}
			}
			s := strings.Join(open, "") + "true" + strings.Join(close, "")
			want := json.Valid([]byte(s))
			if n == 0 && !want {
				t.Fatalf("depth %d: %.40q... is not valid", depth, s)
			}
			if got := Valid(s); got != want {
				t.Fatalf("Valid(%d-deep, %d bytes, case %d) = %v, want %v", depth, len(s), n, got, want)
			}
		}
	}
}

// FuzzValid compares Valid with encoding/json.
func FuzzValid(f *testing.F) {
	f.Add(`{"a":[1,2.5e3,"x\"y\\zé"],"b":{"c":null,"d":true}}`)
	f.Add(strings.Repeat(" ", 61) + `"\\\"" ` + strings.Repeat("[", 70) + strings.Repeat("]", 70))
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := Valid(s), json.Valid([]byte(s)); got != want {
			t.Fatalf("Valid(%q) = %v, want %v", s, got, want)
		}
		if jsonsimd.HaveAVX2 {
			if got, want := Valid(s), json.Valid([]byte(s)); got != want {
				t.Fatalf("Valid(%q) = %v, want %v", s, got, want)
			}
		}
	})
}
