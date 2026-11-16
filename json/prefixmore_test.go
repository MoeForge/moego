package json

import (
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// prefixWant returns what ValidatePrefix of s[:n] gives with the block
// kernels, from the scanner: the end of the value once it is complete,
// PrefixInvalid from the byte where the scanner fails, and before them
// PrefixIncomplete.
func prefixWant(s string) func(n int) int {
	end, bad := -1, len(s)
	var sc scanner
	sc.reset()
	for i := 0; i < len(s) && end < 0; i++ {
		if sc.step(s[i]) == scanError {
			bad = i
			break
		}
		if sc.endTop {
			end = i + 1
		}
	}
	return func(n int) int {
		switch {
		case end >= 0 && n >= end:
			return end
		case n > bad:
			return jsonsimd.PrefixInvalid
		}
		return jsonsimd.PrefixIncomplete
	}
}

// genJSON appends a random value to b, with long strings, escapes,
// numbers, literals and space between the tokens.
func genJSON(r *rand.Rand, b *strings.Builder, depth int) {
	space := func() {
		b.WriteString([]string{"", "", "", " ", "\n  ", "\t", "  "}[r.Intn(7)])
	}
	switch k := r.Intn(12); {
	case depth < 5 && k < 3:
		b.WriteByte('{')
		for i, n := 0, r.Intn(6); i < n; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			space()
			genString(r, b, 3)
			space()
			b.WriteByte(':')
			space()
			genJSON(r, b, depth+1)
			space()
		}
		b.WriteByte('}')
	case depth < 5 && k < 5:
		b.WriteByte('[')
		for i, n := 0, r.Intn(6); i < n; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			space()
			genJSON(r, b, depth+1)
			space()
		}
		b.WriteByte(']')
	case k < 8:
		genString(r, b, 40)
	case k < 10:
		b.WriteString([]string{"0", "-1", "7", "123456789012", "1.5", "-0.25e-10", "1E+5", "3.14159", "-0", "10e3"}[r.Intn(10)])
	default:
		b.WriteString([]string{"true", "false", "null"}[r.Intn(3)])
	}
}

func genString(r *rand.Rand, b *strings.Builder, pieces int) {
	b.WriteByte('"')
	for i, n := 0, r.Intn(pieces); i < n; i++ {
		switch k := r.Intn(14); {
		case k < 2:
			b.WriteString(strings.Repeat("abcdefgh", r.Intn(48)))
		default:
			b.WriteString([]string{"a", "word ", `\n`, `\"`, `\\`, `\/`, `\u00e9`, `\uD83D\uDE00`, `\u4e2d`, "é", "中文", "😀"}[k-2])
		}
	}
	b.WriteByte('"')
}

// genDoc returns an array or object of at least n bytes.
func genDoc(r *rand.Rand, n int) string {
	var b strings.Builder
	obj := r.Intn(2) == 0
	if obj {
		b.WriteByte('{')
	} else {
		b.WriteByte('[')
	}
	for i := 0; b.Len() < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		if obj {
			genString(r, &b, 3)
			b.WriteByte(':')
		}
		genJSON(r, &b, 1)
	}
	if obj {
		b.WriteByte('}')
	} else {
		b.WriteByte(']')
	}
	return b.String()
}

// checkPrefix fails t when got, what name gave for s[:n], is not what
// want gives for n.
func checkPrefix(t *testing.T, want func(int) int, s string, n, got int, name string) {
	t.Helper()
	w := want(n)
	if got == w || !jsonsimd.HaveValidBlocks && got == jsonsimd.PrefixInvalid && w == jsonsimd.PrefixIncomplete {
		return
	}
	t.Fatalf("%s of %d of %d bytes, ending %q, = %d, want %d", name, n, len(s), s[max(0, n-100):n], got, w)
}

// checkTables fails t when the tables in tab, from calls that ended with
// a whole value in s[:n], differ from those of one call on s[:n].
func checkTables(t *testing.T, s string, n int, tab *jsonsimd.Tables) {
	t.Helper()
	var one jsonsimd.Tables
	jsonsimd.ValidatePrefix(s[:n], maxNestingDepth, &one)
	if !slices.Equal(tab.Containers, one.Containers) || !slices.Equal(tab.Spans, one.Spans) {
		t.Fatalf("tables of %d bytes in steps differ from one call:\n%v %v\n%v %v", n, tab.Containers, tab.Spans, one.Containers, one.Spans)
	}
}

// TestValidatePrefixMore checks ValidatePrefix and ValidatePrefixMore on
// each length of valid and broken documents, and ValidatePrefixMore on
// them as reads would grow them, against the scanner, and that a value
// completed that way has the tables of one call on all of it.
func TestValidatePrefixMore(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	docs := 300
	if raceEnabled {
		docs = 40
	}
	var tab jsonsimd.Tables
	var ps jsonsimd.PrefixState
	steps := []int{1, 2, 3, 4, 7, 31, 63, 64, 65, 100, 129, 500, 1460, 4096}
	for d := range docs {
		s := genDoc(r, 64+r.Intn(6000))
		if d%2 == 1 {
			// Break it at a random place.
			at := r.Intn(len(s))
			bad := []string{"\x01", "}", "]", ",", ":", `"`, "x", `\`, "-", ".", "e", "0", "t", "\x00", " 1", "\\u12g"}[r.Intn(16)]
			s = s[:at] + bad + s[at:]
		}
		s += "  "
		want := prefixWant(s)
		if d < 20 {
			for n := 64; n <= len(s); n++ {
				got := jsonsimd.ValidatePrefix(s[:n], maxNestingDepth, &tab)
				checkPrefix(t, want, s, n, got, "ValidatePrefix")
				ps = jsonsimd.PrefixState{}
				got = jsonsimd.ValidatePrefixMore(s[:n], maxNestingDepth, &tab, &ps)
				checkPrefix(t, want, s, n, got, "ValidatePrefixMore")
				if got == jsonsimd.PrefixIncomplete && n < len(s) {
					m := min(len(s), n+steps[r.Intn(len(steps))])
					got = jsonsimd.ValidatePrefixMore(s[:m], maxNestingDepth, &tab, &ps)
					checkPrefix(t, want, s, m, got, "ValidatePrefixMore")
				}
			}
		}
		for range 3 {
			n := min(len(s), 64+r.Intn(200))
			ps = jsonsimd.PrefixState{}
			got := jsonsimd.ValidatePrefixMore(s[:n], maxNestingDepth, &tab, &ps)
			checkPrefix(t, want, s, n, got, "ValidatePrefixMore")
			for got == jsonsimd.PrefixIncomplete && n < len(s) {
				n = min(len(s), n+steps[r.Intn(len(steps))])
				got = jsonsimd.ValidatePrefixMore(s[:n], maxNestingDepth, &tab, &ps)
				checkPrefix(t, want, s, n, got, "ValidatePrefixMore")
			}
			if got >= 0 {
				checkTables(t, s, got, &tab)
			}
		}
	}
}

// TestValidatePrefixMoreCuts puts tokens, valid and not, at each offset
// from a block boundary in a document, and checks ValidatePrefix and
// ValidatePrefixMore on each prefix that cuts it, then ValidatePrefixMore
// on a few longer ones.
func TestValidatePrefixMoreCuts(t *testing.T) {
	toks := []string{
		`"%u12a4"`, `"%u12g4"`, `"%u12a"`, `"%uD83D%uDE00"`, `"a%u00e9b"`,
		`"\""`, `"\\"`, `"\x"`, `"\`, `"\/\b\f\n\r\t"`, `"` + "\x01" + `"`,
		`true`, `trux`, `false`, `fals`, `null`, `nul `, `nulll`,
		`-12.5e+3`, `-12.`, `1e`, `-`, `0.5`, `01`, `1.e3`,
		`{"a":[1,{}]}`, `{"a" 1}`, `[1,]`, `{,}`, `[]]`,
	}
	offsets := 140
	if raceEnabled {
		offsets = 70
	}
	var tab jsonsimd.Tables
	var ps jsonsimd.PrefixState
	head := `[` + strings.Repeat(`"abcdefgh",`, 6)
	for _, tok := range toks {
		tok = strings.ReplaceAll(tok, "%u", `\u`)
		for o := range offsets {
			s := head + strings.Repeat(" ", o) + tok + `, 0 ` + strings.Repeat(" ", 70) + `]  `
			want := prefixWant(s)
			for n := len(head); n <= len(s); n++ {
				got := jsonsimd.ValidatePrefix(s[:n], maxNestingDepth, &tab)
				checkPrefix(t, want, s, n, got, "ValidatePrefix")
				for _, m := range []int{n + 1, n + 2, n + 4, n + 64, len(s)} {
					if m > len(s) {
						continue
					}
					ps = jsonsimd.PrefixState{}
					got := jsonsimd.ValidatePrefixMore(s[:n], maxNestingDepth, &tab, &ps)
					checkPrefix(t, want, s, n, got, "ValidatePrefixMore")
					if got != jsonsimd.PrefixIncomplete {
						continue
					}
					got = jsonsimd.ValidatePrefixMore(s[:m], maxNestingDepth, &tab, &ps)
					checkPrefix(t, want, s, m, got, "ValidatePrefixMore")
					if got >= 0 {
						checkTables(t, s, got, &tab)
					}
				}
			}
		}
	}
}

// TestValidatePrefixMoreContainers grows values with more containers than
// the tables of their first prefixes take, and checks that the tables in
// the end are those of one call.
func TestValidatePrefixMoreContainers(t *testing.T) {
	if !jsonsimd.HaveValidBlocks {
		t.Skip("without the block kernels a prefix that cuts a value is invalid")
	}
	var tab jsonsimd.Tables
	var ps jsonsimd.PrefixState
	for _, s := range []string{
		"[" + strings.Repeat("[],", 3000) + "0]",
		"[" + strings.Repeat(`{"a":[1]},`, 900) + "0]",
		"{" + strings.Repeat(`"k":[[]],`, 900) + `"z":0}`,
	} {
		want := prefixWant(s)
		for _, step := range []int{64, 100, 512, 1000} {
			n := 600
			ps = jsonsimd.PrefixState{}
			got := jsonsimd.ValidatePrefixMore(s[:n], maxNestingDepth, &tab, &ps)
			checkPrefix(t, want, s, n, got, "ValidatePrefixMore")
			for got == jsonsimd.PrefixIncomplete && n < len(s) {
				n = min(len(s), n+step)
				got = jsonsimd.ValidatePrefixMore(s[:n], maxNestingDepth, &tab, &ps)
				checkPrefix(t, want, s, n, got, "ValidatePrefixMore")
			}
			if got < 0 {
				t.Fatalf("%q...: %d", s[:20], got)
			}
			checkTables(t, s, got, &tab)
		}
	}
}
