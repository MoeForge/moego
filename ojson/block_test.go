package ojson

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// genDoc writes a random JSON value and records paths to some of its values.
type genDoc struct {
	r     *rand.Rand
	b     strings.Builder
	paths []string
}

var genKeys = []string{"a", "b", "c", "model", "x", `k\"q`, `\u0061`, "a\\\\b", ""}

func (g *genDoc) ws() {
	for g.r.Intn(3) == 0 {
		g.b.WriteByte(" \t\n\r"[g.r.Intn(4)])
	}
}

func (g *genDoc) str() {
	g.b.WriteByte('"')
	n := g.r.Intn(4)
	if g.r.Intn(4) == 0 {
		n = g.r.Intn(200)
	}
	for i := 0; i < n; i++ {
		switch g.r.Intn(12) {
		case 0:
			g.b.WriteString(`\"`)
		case 1:
			g.b.WriteString(`\\`)
		case 2:
			g.b.WriteString(`\u00e9`)
		case 3:
			g.b.WriteString(`{[`)
		case 4:
			g.b.WriteString(`]}`)
		default:
			g.b.WriteByte("abcdefgh ,:"[g.r.Intn(11)])
		}
	}
	g.b.WriteByte('"')
}

func (g *genDoc) value(path string, depth int) {
	if path != "" && g.r.Intn(3) == 0 {
		g.paths = append(g.paths, path)
	}
	join := func(c string) string {
		if path == "" {
			return c
		}
		return path + "." + c
	}
	switch k := g.r.Intn(10); {
	case depth > 4 || k < 4:
		switch g.r.Intn(5) {
		case 0:
			g.str()
		case 1:
			g.b.WriteString(strconv.Itoa(g.r.Intn(2000) - 1000))
		case 2:
			g.b.WriteString("true")
		case 3:
			g.b.WriteString("null")
		default:
			g.b.WriteString("-1.5e3")
		}
	case k < 7:
		g.b.WriteByte('{')
		n := g.r.Intn(6)
		for i := 0; i < n; i++ {
			if i > 0 {
				g.b.WriteByte(',')
			}
			g.ws()
			key := genKeys[g.r.Intn(len(genKeys))]
			g.b.WriteString(`"` + key + `"`)
			g.ws()
			g.b.WriteByte(':')
			g.ws()
			comp := strings.NewReplacer(`\"`, `"`, `\u0061`, "a", `\\`, `\`).Replace(key)
			g.value(join(escapeComp(comp)), depth+1)
			g.ws()
		}
		g.b.WriteByte('}')
	default:
		g.b.WriteByte('[')
		n := g.r.Intn(6)
		for i := 0; i < n; i++ {
			if i > 0 {
				g.b.WriteByte(',')
			}
			g.ws()
			g.value(join(strconv.Itoa(i)), depth+1)
			g.ws()
		}
		g.b.WriteByte(']')
	}
}

// escapeComp escapes a key for use as a path component.
func escapeComp(k string) string {
	var b strings.Builder
	for i := 0; i < len(k); i++ {
		switch k[i] {
		case '.', '*', '?', '|', '#', '@', '\\':
			b.WriteByte('\\')
		}
		b.WriteByte(k[i])
	}
	return b.String()
}

// randomIterations limits repeated random cases under the race detector.
// Table cases, boundary checks and concurrent tests keep their full coverage.
func randomIterations(n int) int {
	if raceEnabled {
		return max(1, n/10)
	}
	return n
}

func randomDoc(r *rand.Rand) (string, []string) {
	g := &genDoc{r: r}
	g.ws()
	if r.Intn(2) == 0 {
		g.value("", 0)
	} else {
		g.b.WriteByte('{')
		for i := 0; i < 1+r.Intn(8); i++ {
			if i > 0 {
				g.b.WriteByte(',')
			}
			g.ws()
			key := genKeys[r.Intn(len(genKeys)-3)]
			g.b.WriteString(`"` + key + `":`)
			g.ws()
			g.value(escapeComp(strings.ReplaceAll(key, `\"`, `"`)), 1)
		}
		g.b.WriteByte('}')
	}
	g.ws()
	paths := append(g.paths, "a", "b.0", "x.a.1", "model", "1", "0.a", "c.c.c", `k"q`, "a.b.c.d")
	return g.b.String(), paths
}

func mutate(r *rand.Rand, s string) string {
	b := []byte(s)
	for n := 1 + r.Intn(3); n > 0 && len(b) > 0; n-- {
		i := r.Intn(len(b))
		c := `"\{}[],: 1a`[r.Intn(11)]
		switch r.Intn(3) {
		case 0:
			b[i] = c
		case 1:
			b = append(b[:i], b[i+1:]...)
		default:
			b = append(b[:i], append([]byte{c}, b[i:]...)...)
		}
	}
	return string(b)
}

func checkContainerEnd(t *testing.T, doc string) {
	for i := 0; i < len(doc); i++ {
		if doc[i] != '{' && doc[i] != '[' {
			continue
		}
		got, want := containerEndFast(doc, i), containerEndScalar(doc, i)
		if got != want {
			t.Fatalf("containerEndFast(%q, %d) = %d, want %d", doc, i, got, want)
		}
	}
}

func TestContainerEndFast(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for n := 0; n < randomIterations(20000); n++ {
		doc, _ := randomDoc(r)
		checkContainerEnd(t, doc)
		for k := 0; k < 3; k++ {
			checkContainerEnd(t, mutate(r, doc))
		}
	}
	// Escapes and strings across block boundaries, long strings and
	// unterminated input.
	for pad := 0; pad < 140; pad++ {
		p := strings.Repeat(" ", pad)
		for _, body := range []string{
			`["` + strings.Repeat(`\\`, 40) + `\"]"]`,
			`{"a":"` + strings.Repeat("x", 200) + `","b":[1,{"c":"]"}]}`,
			`[` + strings.Repeat(`"\\\"",`, 30) + `0]`,
			`[` + strings.Repeat("[", 100) + strings.Repeat("]", 100) + `]`,
			`{"a":"` + strings.Repeat("y", 300),
			`[1,2,{"k":"v"}` + strings.Repeat(" ", 70),
		} {
			checkContainerEnd(t, p+body)
			checkContainerEnd(t, p+body+p)
		}
	}
}

func FuzzContainerEnd(f *testing.F) {
	f.Add(`{"a":{"b":[1,"x\"y",{"c":true}]},"model":"m"}`)
	f.Add(`[1,"]",{"a":"\\"},[2]]`)
	f.Fuzz(func(t *testing.T, doc string) {
		checkContainerEnd(t, doc)
	})
}
