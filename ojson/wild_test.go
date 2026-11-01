package ojson

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// A final '*' after ASCII bytes matches every key that starts with those
// bytes, as globMatch decides, for keys over bytes that matter: multibyte,
// truncated and invalid UTF-8, U+FFFD itself, quotes and brackets.
func TestPrefixGlob(t *testing.T) {
	keys := tokenStrings([]string{"a", "b", "\"", "[", "\xc3", "\xa9", "\xff", "\xef\xbf\xbd"}, 4)
	prefixes := tokenStrings([]string{"a", "b", "\"", "["}, 3)
	for _, p := range prefixes {
		for _, k := range keys {
			if got, want := strings.HasPrefix(k, p), globMatch(k, p+"*"); got != want {
				t.Fatalf("key %q, prefix %q: HasPrefix %v, globMatch %v", k, p, got, want)
			}
		}
	}
}

// tokenStrings returns every string of at most n tokens.
func tokenStrings(tokens []string, n int) []string {
	out := []string{""}
	last := out
	for ; n > 0; n-- {
		var next []string
		for _, s := range last {
			for _, c := range tokens {
				next = append(next, s+c)
			}
		}
		out = append(out, next...)
		last = next
	}
	return out
}

// globMatch compares characters as runes, each byte outside valid UTF-8
// reading as U+FFFD, as globRef does over the runes of both strings.
func TestGlobRunes(t *testing.T) {
	subjects := tokenStrings([]string{"a", "\xff", "\xef\xbf\xbd", "\xc3", "\xa9", "*"}, 4)
	patterns := tokenStrings([]string{"a", "*", "?", "\\", "\xff", "\xef\xbf\xbd", "\xc3\xa9", "\xc3", "\xa9"}, 3)
	for _, p := range patterns {
		pr := []rune(p)
		for _, s := range subjects {
			if got, want := globMatch(s, p), globRef([]rune(s), pr); got != want {
				t.Fatalf("globMatch(%q, %q) = %v, want %v", s, p, got, want)
			}
		}
	}
}

// globRef matches the runes s against the pattern runes p by trying every
// split at each '*'.
func globRef(s, p []rune) bool {
	if len(p) == 0 {
		return len(s) == 0
	}
	switch p[0] {
	case '*':
		for i := 0; i <= len(s); i++ {
			if globRef(s[i:], p[1:]) {
				return true
			}
		}
		return false
	case '?':
		return len(s) > 0 && globRef(s[1:], p[1:])
	case '\\':
		return len(p) > 1 && len(s) > 0 && s[0] == p[1] && globRef(s[1:], p[2:])
	}
	return len(s) > 0 && s[0] == p[0] && globRef(s[1:], p[1:])
}

// oldWildComp is the scan findQuery made before wildScan: the component at
// path[pi] can be a wildcard component.
func oldWildComp(path string, pi int) bool {
	if pi > 0 && (path[pi] == '[' || path[pi] == '{') {
		return false
	}
	for j := pi; j < len(path); j++ {
		switch path[j] {
		case '\\':
			j++
		case '|', '#', '@':
			return false
		case '.':
			if j+1 < len(path) && (path[j+1] == '[' || path[j+1] == '{' || path[j+1] == '@') {
				return false
			}
			j = len(path)
		}
	}
	return true
}

// wildPaths returns components with wildcards, escapes and query bytes,
// followed by rests that end the path, continue it or start pipes.
func wildPaths() []string {
	comps := []string{
		"*", "a*", "ab*", "abc*", "b*", "x*", "a\"*", "\xc3*", "abcdefgh*", "abcdefghi*",
		"a[*", "a?", "?*", "a**", "*a", "a*b", `a\**`, `a\.*`, `\a*`, `a*\`, "a*#", "#*", "a|*",
		"a@*", "[a*", "{a*", "a?*", "a*?", "\xff*", "a\xff*", "\xef\xbf\xbd*", "a\xff", "?\xff", "a?\xc3",
	}
	rests := []string{"", ".", ".a", ".ab*", ".b.a", ".0", ".#", ".a?", "|a", ".[a]", ".{a}", ".@this", ".@nope", `.a\.b`, ".*",
		`.a\|*`, `.a\.*`, `.?\|*`, `.*\|*`, `.a\|b`, `.b.a\|*`, `.a\|*.b`}
	var out []string
	for _, c := range comps {
		for _, r := range rests {
			out = append(out, c+r)
		}
	}
	return out
}

// wildScan classifies components as the old scan did, and the components it
// leaves to findPrefix are those readObjComp reads as a wildcard key that
// is the prefix and a '*', with the same rest.
func TestWildScan(t *testing.T) {
	for _, path := range wildPaths() {
		for _, pre := range []string{"", "k.", "k.l."} {
			p, pi := pre+path, len(pre)
			star := wildScan(p, pi)
			if (star != -2) != oldWildComp(p, pi) {
				t.Fatalf("%q at %d: wildScan %d, old scan %v", p, pi, star, oldWildComp(p, pi))
			}
			if star < 0 {
				continue
			}
			for j := pi; j < star; j++ {
				if p[j] >= 0x80 {
					t.Fatalf("%q at %d: star %d after a non-ASCII byte", p, pi, star)
				}
			}
			oc := readObjComp(p[pi:])
			if !oc.wild || oc.piped || oc.key != p[pi:star+1] || oc.more != (star+1 < len(p)) {
				t.Fatalf("%q at %d: star %d, readObjComp %+v", p, pi, star, oc)
			}
			if oc.more && len(p)-len(oc.rest) != star+2 {
				t.Fatalf("%q at %d: star %d, rest %q", p, pi, star, oc.rest)
			}
		}
	}
}

// wildDoc returns a random object whose keys share prefixes, written with
// and without escapes and spacing.
func wildDoc(r *rand.Rand, depth int) string {
	keys := []string{
		`a`, `ab`, `abc`, `b`, `ba`, ``, `a\"b`, `ab`, `a\\`, `é`, `é`, `abcdefgh`,
		`abcdefghij`, `a[`, `x`, `\ud800a`, "\xff", "a\xff", "\xef\xbf\xbd", "a\xc3", `a|b`, `a.b`, `.b`,
	}
	sp := []string{"", " ", "\n  "}
	var b strings.Builder
	b.WriteString("{")
	n := r.Intn(5)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(sp[r.Intn(len(sp))])
		fmt.Fprintf(&b, `"%s"`, keys[r.Intn(len(keys))])
		b.WriteString(sp[r.Intn(len(sp))])
		b.WriteString(":")
		b.WriteString(sp[r.Intn(len(sp))])
		switch k := r.Intn(6); {
		case k < 2 && depth < 3:
			b.WriteString(wildDoc(r, depth+1))
		case k == 2:
			fmt.Fprintf(&b, "[%d,%s]", r.Intn(9), wildDoc(r, depth+1))
		case k == 3:
			b.WriteString(`"a long string value with \" escapes"`)
		case k == 4:
			b.WriteString(`"v"`)
		default:
			fmt.Fprint(&b, r.Intn(1000))
		}
		b.WriteString(sp[r.Intn(len(sp))])
	}
	b.WriteString("}")
	return b.String()
}

// findPrefix finds what findWild finds for the same component, in random
// objects, and Get what the full evaluation finds.
func TestFindPrefix(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	paths := wildPaths()
	for n := 0; n < 3000; n++ {
		doc := wildDoc(r, 0)
		// The document ends at the closing brace, and also with room
		// after it, so that member keys are read near the end of the
		// input and away from it.
		for _, json := range []string{doc, doc + strings.Repeat(" ", 16)} {
			for _, path := range paths {
				star := wildScan(path, 0)
				if star >= 0 {
					oc := readObjComp(path)
					gs, ge, gesc := findPrefix(json, 0, path, 0, star)
					ws, we, wesc := findWild(json, 0, path, &oc)
					if gs != ws || ge != we || gesc != wesc {
						t.Fatalf("%s %q: findPrefix %d %d %v, findWild %d %d %v", json, path, gs, ge, gesc, ws, we, wesc)
					}
				}
				if star == -2 || path[0] == '#' {
					continue
				}
				got, want := Get(json, path), getComplex(json, path)
				if !sameResult(got, want) {
					t.Fatalf("%s %q: Get %+v, getComplex %+v", json, path, got, want)
				}
			}
		}
	}
}

// memberPrefix stops at the member nextMember and keyMatch stop at, with
// keys of every length around the word compare, keys with escapes, and
// keys that end near the end of the input.
func TestMemberPrefix(t *testing.T) {
	keys := []string{``, `a`, `ab`, `abcdefg`, `abcdefgh`, `abcdefghi`, `a"`, `a\"b`, `abc`, `a\\b`, `ab\/`, `éx`, `é`, `😀`, `"`,
		"a\xff", "ab\xc3", "\xff", `a\u00e9`, `ab\ud800`}
	// Prefixes are ASCII without backslashes: wildScan leaves the other
	// components to findWild.
	prefixes := []string{"", "a", "ab", "abcdefg", "abcdefgh", "abcdefghi", "a\"", "a\"b", "ab/", "b"}
	for _, k1 := range keys {
		for _, k2 := range keys {
			for _, sp := range []string{"", " ", "\t\n"} {
				body := fmt.Sprintf(`{%s"%s"%s:%s"v",%s"%s":{}}`, sp, k1, sp, sp, sp, k2)
				for _, p := range prefixes {
					pw, pm := prefixWord(p)
					got := memberPrefix(body, 1, p, pw, pm)
					want := memberRef(body, 1, p)
					if got != want {
						t.Fatalf("%s prefix %q: got %d, want %d", body, p, got, want)
					}
				}
			}
		}
	}
}

// memberRef is memberPrefix with the general wildcard loop of findWild.
func memberRef(json string, i int, prefix string) int {
	oc := objComp{key: prefix + "*", wild: true}
	for {
		ks, ke, vs, kesc, ok := nextMember(json, i)
		if !ok {
			return -1
		}
		if keyMatch(json[ks+1:ke-1], kesc, &oc) {
			return vs
		}
		if i = skipValue(json, vs); i < 0 {
			return -1
		}
	}
}

// Wildcards and queries compare bytes outside valid UTF-8 as U+FFFD, in
// Get and in Set, as gjson and sjson do. The expected values are theirs.
func TestWildUTF8(t *testing.T) {
	gets := []struct {
		json, path, raw string
		index           int
		indexes         []int
	}{
		{"{\"\xc4\":0}", "\xa8*", "0", 5, nil},
		{"{\"friends\":[{\"fi\xb7s\":0}]}", "*.#(*\xd2*)", "{\"fi\xb7s\":0}", 12, nil},
		{"[{\"\xae\":0}]", "#.\x91*", "[0]", 0, []int{6}},
		{"{\"\":[{\"\xf9\xf9\xf9\":0}]}", ".#.{\xfc*)", `[{"\ufffd*":0}]`, 0, []int{5}},
		{"{\"fr\xf7ends\":[]}", "*\xce*.#", "0", 0, nil},
	}
	for _, c := range gets {
		r := Get(c.json, c.path)
		if r.Raw != c.raw || r.Index != c.index || fmt.Sprint(r.Indexes) != fmt.Sprint(c.indexes) {
			t.Errorf("Get(%q, %q) = %q %d %v, want %q %d %v", c.json, c.path, r.Raw, r.Index, r.Indexes, c.raw, c.index, c.indexes)
		}
	}
	sets := []struct{ json, path, want string }{
		{"{\"\xc4\":0}", "\xa8*", "{\"\xc4\":1}"},
		{"{\"friends\":[{\"fi\xb7s\":0}]}", "*.#(*\xd2*)", "{\"friends\":[1]}"},
		{"[{\"\xae\":0}]", "#.\x91*", "[{\"\xae\":1}]"},
	}
	for _, c := range sets {
		out, err := SetBytes([]byte(c.json), c.path, 1)
		if string(out) != c.want || err != nil {
			t.Errorf("SetBytes(%q, %q) = %q, %v, want %q", c.json, c.path, out, err, c.want)
		}
	}
}

// A wildcard component read on an array is an index, which an escaped '.'
// or '|' ends, as gjson reads it: after an escaped '|' the path goes on
// from the missing element, and the members after the array are not
// tried. The expected values are gjson's and sjson's.
func TestWildArrayPipe(t *testing.T) {
	gets := []struct {
		json, path, raw string
		index           int
	}{
		{`{"children":{".b":[],"a":{"a|b":0}}}`, `children.*.a\|*`, "", 0},
		{`{"children":{"a.b":[],"a":{"a|b":0}}}`, `children.*.a\|*`, "", 0},
		{`{"children":{"xb":[],"xa":{"a|b":0}}}`, `children.x*.a\|*`, "", 0},
		{`{"xb":[1,2],"a":{"a|b":0}}`, `*.a\|*`, "", 0},
		{`{"xb":[{"a|b":5}],"a":{"a|b":0}}`, `*.a\|*`, "", 0},
		{`{"xb":[],"a":{"a|b":{"x":1}}}`, `*.a\|*.x`, "", 0},
		{`{"xb":[],"ab":{"a|b":0}}`, `??.a\|*`, "", 0},
		{`{"xb":{"b":[]},"a":{"b":{"a|b":0}}}`, `*.b.a\|*`, "", 0},
		{`{"xb":[],"a":{"a|b":0}}`, `*.?\|*`, "", 0},
		{`{"xb":[],"a":{"a|b":0}}`, `*.a\|?`, "", 0},
		{`{"xb":[],"a":{"a|b":0}}`, `*.*\|*`, "", 0},
		{`{"xb":[],"a":{"a|b":0}}`, `*.a\|b`, "", 0},
		{`[{"a|b":0}]`, `a\|*`, "", 0},
		// No array before the match, or none matched: found.
		{`{"children":{".b":{},"a":{"a|b":0}}}`, `children.*.a\|*`, "0", 32},
		{`{"children":{".b":1,"a":{"a|b":0}}}`, `children.*.a\|*`, "0", 31},
		{`{"xb":[],"ab":{"a|b":0}}`, `a?.a\|*`, "0", 21},
		{`{"xb":[],"a":{"b":{"a|b":0}}}`, `*.b.a\|*`, "0", 25},
		// An escaped '.' ends the index too, but the search goes on.
		{`{"xb":[],"a":{"a.b":0}}`, `*.a\.*`, "0", 20},
		{`{"children":{"xb":[],"a":{"a.b":0}}}`, `children.*.a\.b`, "0", 32},
		{`{"xb":[],"a":{"ab":0}}`, `*.a*`, "0", 19},
	}
	for _, c := range gets {
		r := Get(c.json, c.path)
		if r.Raw != c.raw || r.Index != c.index {
			t.Errorf("Get(%s, %s) = %q %d, want %q %d", c.json, c.path, r.Raw, r.Index, c.raw, c.index)
		}
	}
	sets := []struct{ json, path, want string }{
		{`{"children":{".b":[],"a":{"a|b":0}}}`, `children.*.a\|*`, `{"children":{".b":[],"a":{"a|b":0}}}`},
		{`{"xb":[1,2],"a":{"a|b":0}}`, `*.a\|*`, `{"xb":[1,2],"a":{"a|b":0}}`},
		{`{"xb":[],"ab":{"a|b":0}}`, `??.a\|*`, `{"xb":[],"ab":{"a|b":0}}`},
		{`{"children":{".b":{},"a":{"a|b":0}}}`, `children.*.a\|*`, `{"children":{".b":{},"a":{"a|b":9}}}`},
	}
	for _, c := range sets {
		out, err := Set(c.json, c.path, 9)
		if out != c.want || err != nil {
			t.Errorf("Set(%s, %s) = %s, %v, want %s", c.json, c.path, out, err, c.want)
		}
	}
}
