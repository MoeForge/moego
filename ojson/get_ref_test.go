package ojson

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// The first Get walk, kept as the reference the faster walk is tested
// against.

// plainGetPath reports whether path consists only of plain keys and indexes.
func plainGetPath(path string) bool {
	return path == "" || !queryStart(path) && plainComps(path)
}

func getRef(json, path string) Result {
	if path == "" || !plainGetPath(path) || scalarRoot(json) {
		return getComplex(json, path)
	}
	i := skipSpace(json, 0)
	if i == len(json) || path == "" {
		return Result{}
	}
	vs, ve, esc := findRef(json, i, path, 0)
	if vs < 0 || ve < 0 {
		return Result{}
	}
	return resultAt(json, vs, ve, esc)
}

func findRef(json string, i int, path string, pi int) (vs, ve int, esc bool) {
	raw, next, kesc := nextComp(path, pi)
	key := compKey(raw, kesc)
	switch json[i] {
	case '{':
		i++
		for {
			ks, ke, v, e, ok := nextMemberRef(json, i)
			if !ok {
				return -1, -1, false
			}
			if keyIs(json[ks+1:ke-1], e, key) {
				if next < 0 {
					ve, esc := valueSpan(json, v)
					return v, ve, esc
				}
				if c := json[v]; c == '{' || c == '[' {
					if fs, fe, fesc := findRef(json, v, path, next); fs >= 0 {
						return fs, fe, fesc
					}
				}
			}
			if i = skipValueRef(json, v); i < 0 {
				return -1, -1, false
			}
		}
	case '[':
		n, ok := arrayIndex(key)
		ok = ok && !kesc
		if !ok {
			return -1, -1, false
		}
		i++
		for k := 0; ; k++ {
			if i >= len(json) || json[i] <= ' ' {
				i = skipSpace(json, i)
			}
			if i >= len(json) || json[i] == ']' {
				return -1, -1, false
			}
			if k == n {
				if next < 0 {
					ve, esc := valueSpan(json, i)
					return i, ve, esc
				}
				if c := json[i]; c == '{' || c == '[' {
					return findRef(json, i, path, next)
				}
				return -1, -1, false
			}
			if i = skipValueRef(json, i); i < 0 {
				return -1, -1, false
			}
		}
	}
	return -1, -1, false
}

func skipValueRef(json string, i int) int {
	switch json[i] {
	case '"':
		i, _ = scanString(json, i+1)
	case '{', '[':
		i = containerEnd(json, i)
	default:
		for i < len(json) && !delim[json[i]] {
			i++
		}
	}
	if i < 0 {
		return -1
	}
	return afterValue(json, i)
}

func nextMemberRef(json string, i int) (ks, ke, vs int, esc, ok bool) {
	if i >= len(json) || json[i] != '"' {
		i = skipSpace(json, i)
		if i >= len(json) || json[i] != '"' {
			return 0, 0, 0, false, false
		}
	}
	ks = i
	ke, esc = scanString(json, i+1)
	if ke < 0 {
		return 0, 0, 0, false, false
	}
	i = ke
	if i >= len(json) || json[i] != ':' {
		i = skipSpace(json, i)
		if i >= len(json) || json[i] != ':' {
			return 0, 0, 0, false, false
		}
	}
	i++
	if i >= len(json) || json[i] <= ' ' {
		i = skipSpace(json, i)
		if i >= len(json) {
			return 0, 0, 0, false, false
		}
	}
	return ks, ke, i, esc, true
}

func sameResult(a, b Result) bool {
	numEq := a.Num == b.Num || a.Num != a.Num && b.Num != b.Num
	return a.Type == b.Type && a.Raw == b.Raw && a.Str == b.Str && numEq &&
		a.Index == b.Index && len(a.Indexes) == len(b.Indexes)
}

// getPaths are paths with query bytes, escapes and odd shapes, added to the
// paths of each random document.
var getPaths = []string{"", "a*", "a.b*", "x.#", "@this", "a|b", "..a", "[a]", "{a}",
	"!true", `a\.b`, `k"q`, `a\`, `a\\b`, "0", "1.a", "model.x", "a.@x", "b.0.c",
	"a.b.c.d.e.f.g.h.i", "a.0", "a.-1", "a.01", "c.c", "missing.x*", "x.a.#.b"}

func TestGetMatchesRef(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	for n := 0; n < 30000; n++ {
		doc, paths := randomDoc(r)
		paths = append(paths, getPaths...)
		for _, d := range []string{doc, mutate(r, doc), " " + doc[:r.Intn(len(doc)+1)]} {
			for _, p := range paths {
				want := getRef(d, p)
				if got := Get(d, p); !sameResult(got, want) {
					t.Fatalf("Get(%q, %q) = %+v, want %+v", d, p, got, want)
				}
				b := []byte(d)
				got := GetBytes(b, p)
				clobber(b)
				if !sameResult(got, want) {
					t.Fatalf("GetBytes(%q, %q) = %+v, want %+v", d, p, got, want)
				}
			}
			sub := make([]string, 1+r.Intn(12))
			for i := range sub {
				sub[i] = paths[r.Intn(len(paths))]
			}
			for i, got := range GetMany(d, sub...) {
				if want := getRef(d, sub[i]); !sameResult(got, want) {
					t.Fatalf("GetMany(%q, %q)[%d] = %+v, want %+v", d, sub, i, got, want)
				}
			}
			b := []byte(d)
			many := GetManyBytes(b, sub...)
			clobber(b)
			for i, got := range many {
				if want := getRef(d, sub[i]); !sameResult(got, want) {
					t.Fatalf("GetManyBytes(%q, %q)[%d] = %+v, want %+v", d, sub, i, got, want)
				}
			}
			// The shared walk, which GetMany skips for few paths.
			walked := make([]Result, len(sub))
			getMany(d, sub, walked)
			for i, got := range walked {
				if want := getRef(d, sub[i]); !sameResult(got, want) {
					t.Fatalf("getMany(%q, %q)[%d] = %+v, want %+v", d, sub, i, got, want)
				}
			}
		}
	}
}

// TestGetManyShared covers few paths that begin with the same components,
// which GetMany looks up once, including documents that repeat those keys.
func TestGetManyShared(t *testing.T) {
	docs := []string{`{"a":{"x":1},"a":{"y":2}}`, `{"c":[{"p":1}],"c":[{"q":2}]}`,
		`{"a":null,"a":{"b":{"c":3}},"b":1}`, `{"a":{"b":1}`, `[{"a":1},{"a":2}]`, ` {"a" : [ {"b":"x\"y"} ] } `,
		`{"choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":null}],"usage":null}`}
	paths := [][]string{{"a.x", "a.y"}, {"c.0.p", "c.0.q"}, {"a.b.c", "a.b", "b"}, {"a.b", "a.c"},
		{"a.x", "a.y", "a.z"}, {"a.b.c", "a.b.d", "a.e"}, {"0.a", "1.a", "0"}, {"a.0.b", "a.0", "a.0.c"},
		{"choices.0.delta.content", "choices.0.finish_reason", "usage"}, {"a.*", "a.x"}, {"a..x", "a..y"}, {`a\.x`, `a\.y`}}
	for _, d := range docs {
		for _, ps := range paths {
			check := func(name string, rs []Result) {
				for i, got := range rs {
					if want := getRef(d, ps[i]); !sameResult(got, want) {
						t.Fatalf("%s(%q, %q)[%d] = %+v, want %+v", name, d, ps, i, got, want)
					}
				}
			}
			check("GetMany", GetMany(d, ps...))
			check("GetManyBytes", GetManyBytes([]byte(d), ps...))
			check("GetManyBytesNoCopy", GetManyBytesNoCopy(nil, []byte(d), ps...))
		}
	}
}

// TestGetManyRoot covers few paths that share no components, whose first
// keys GetMany finds in one pass over the root members: keys before and
// after the members read in that pass, repeated keys, escaped and long keys,
// values to search on from, and every truncation of each document.
func TestGetManyRoot(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"model":"gpt-4o", "m\u006fdel":1, "group":{"a":[1,{"b":2}]}`)
	for i := 0; i < 2*rootScan; i++ {
		b.WriteString(`,"k` + strconv.Itoa(i) + `":` + strconv.Itoa(i))
	}
	b.WriteString(`,"a_much_longer_key":"v","group":{"c":3},"x":"y\"z","esc":"q\u0041", "tail" : [true,null] }`)
	docs := []string{b.String(), `{"a":1,"b":2}`, `{"b":{"x":1},"a":null,"b":{"y":2}}`, `{}`,
		` { "a" : "1" , "b" : [ 2 ] } `, `{"a":1 "b":2}`, `{"a":"x","b"}`, `["a","b"]`, `"a"`, ``}
	paths := [][]string{{"model", "group"}, {"group", "model"}, {"model", "group.a.1.b", "tail"},
		{"k3", "k15", "x"}, {"a_much_longer_key", "x", "missing"}, {"group.c", "model"}, {"esc", "tail.1"},
		{"a", "b"}, {"b", "a"}, {"b.y", "a"}, {"b.0", "a"}, {"a", "a"}, {"model.x", "group"},
		{"k1", "group.a.#", "x"}, {"a", "@this"}, {"a", ""}, {"a", ".b"}, {`k\1`, "k2"}}
	for _, doc := range docs {
		for n := 0; n <= len(doc); n++ {
			d := doc[:n]
			for _, ps := range paths {
				check := func(name string, rs []Result) {
					for i, got := range rs {
						if want := getRef(d, ps[i]); !sameResult(got, want) {
							t.Fatalf("%s(%q, %q)[%d] = %+v, want %+v", name, d, ps, i, got, want)
						}
					}
				}
				check("GetMany", GetMany(d, ps...))
				check("GetManyBytes", GetManyBytes([]byte(d), ps...))
				check("GetManyBytesNoCopy", GetManyBytesNoCopy(nil, []byte(d), ps...))
			}
		}
	}
}

// TestRootSpansLimits checks that rootSpans declines more paths than rootKeys
// holds, or than there are spans for, instead of indexing past them.
func TestRootSpansLimits(t *testing.T) {
	json := `{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6,"g":7,"h":8,"i":9}`
	paths := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	var spans [9]span
	if rootSpans(json, paths, spans[:]) {
		t.Fatal("rootSpans took 9 paths")
	}
	if rootSpans(json, paths[:3], spans[:2]) {
		t.Fatal("rootSpans took 3 paths with 2 spans")
	}
	if !rootSpans(json, paths[:8], spans[:8]) {
		t.Fatal("rootSpans declined 8 paths")
	}
	for k, p := range paths[:8] {
		if vs, ve, esc := getSpan(json, p); spans[k] != (span{vs, ve, esc}) {
			t.Fatalf("rootSpans span of %q = %+v, want %+v", p, spans[k], span{vs, ve, esc})
		}
	}
}

// clobber overwrites b, as a caller reusing its buffer would, so the tests
// catch a GetBytes or GetManyBytes result that still points into it.
func clobber(b []byte) {
	for i := range b {
		b[i] = 'x'
	}
}

func FuzzGet(f *testing.F) {
	f.Add(`{"a":{"b":[1,"x\"y",{"c":true}]},"a":{"c":2},"model":"m"}`, "a.c")
	f.Add(`[1,"]",{"a":"\\"},[2]]`, "2.a")
	f.Fuzz(func(t *testing.T, doc, path string) {
		want := getRef(doc, path)
		if got := Get(doc, path); !sameResult(got, want) {
			t.Fatalf("Get(%q, %q) = %+v, want %+v", doc, path, got, want)
		}
		if got := GetMany(doc, path, "a", path)[2]; !sameResult(got, want) {
			t.Fatalf("GetMany(%q, %q) = %+v, want %+v", doc, path, got, want)
		}
	})
}

// getCompRef and plainGetPathRef are getComp and plainGetPath as they were
// before '[' and '{' had a class of their own.
func getCompRef(path string, i int) (raw string, next int, esc bool) {
	if i > 0 && i < len(path) && (path[i] == '[' || path[i] == '{') {
		return "", -2, false
	}
	for j := i; j < len(path); j++ {
		switch path[j] {
		case '.':
			return path[i:j], j + 1, esc
		case '\\':
			esc = true
			j++
		case '|', '#', '*', '?', '@':
			return "", -2, false
		}
	}
	return path[i:], -1, esc
}

func plainGetPathRef(path string) bool {
	if path != "" && queryStart(path) {
		return false
	}
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '\\':
			i++
		case '|', '#', '*', '?', '@':
			return false
		case '.':
			if i+1 < len(path) && (path[i+1] == '[' || path[i+1] == '{') {
				return false
			}
		}
	}
	return true
}

func TestPathClasses(t *testing.T) {
	r := rand.New(rand.NewSource(9))
	alphabet := "ab0..\\[{]}!|#*?@\" :"
	for n := 0; n < 300000; n++ {
		b := make([]byte, r.Intn(10))
		if n%2 == 0 {
			// Longer paths, mostly plain words, for the eight-byte reads.
			b = make([]byte, r.Intn(40))
		}
		for i := range b {
			b[i] = alphabet[r.Intn(len(alphabet))]
			if r.Intn(3) > 0 && strings.IndexByte("|#*?@", b[i]) >= 0 {
				b[i] = 'k'
			}
			if n%2 == 0 && r.Intn(4) > 0 {
				const near = "abz_]^`{|}~.[\\\x00\xff" // bytes at and around the ends of ']'..'z'
				b[i] = near[r.Intn(len(near))]
				if r.Intn(4) > 0 {
					b[i] = 'a' + byte(r.Intn(26))
				}
			}
		}
		p := string(b)
		if got, want := plainGetPath(p), plainGetPathRef(p); got != want {
			t.Fatalf("plainGetPath(%q) = %v, want %v", p, got, want)
		}
		for i := 0; i <= len(p); i++ {
			wraw, wnext, wesc := getCompRef(p, i)
			raw, next, esc := getComp(p, i, i)
			if raw != wraw || next != wnext || esc != wesc {
				t.Fatalf("getComp(%q, %d, %d) = %q, %d, %v, want %q, %d, %v", p, i, i, raw, next, esc, wraw, wnext, wesc)
			}
			// From past the plain words, as walker.pend reads.
			j := i
			for j+8 <= len(p) && plainWord(load64(p, j)) {
				j += 8
			}
			raw, next, esc = getComp(p, i, j)
			if raw != wraw || next != wnext || esc != wesc {
				t.Fatalf("getComp(%q, %d, %d) = %q, %d, %v, want %q, %d, %v", p, i, j, raw, next, esc, wraw, wnext, wesc)
			}
		}
	}
}

func TestPlainWord(t *testing.T) {
	// No byte that getComp stops at lies in ']'..'z'.
	for c := 0; c < 256; c++ {
		if pathClass[c] != 0 && c >= ']' && c <= 'z' {
			t.Fatalf("byte %q has a path class", rune(c))
		}
	}
	// Every byte value at every position of a plain word.
	for c := 0; c < 256; c++ {
		for at := 0; at < 8; at++ {
			b := []byte("abcxyz_]")
			b[at] = byte(c)
			want := c >= ']' && c <= 'z'
			if got := plainWord(load64(string(b), 0)); got != want {
				t.Fatalf("plainWord(%q) = %v, want %v", b, got, want)
			}
		}
	}
	r := rand.New(rand.NewSource(10))
	for n := 0; n < 1000000; n++ {
		w := r.Uint64()
		if n%2 == 0 {
			// Mostly plain bytes, so that both answers are common.
			for k := 0; k < 64; k += 8 {
				if r.Intn(8) > 0 {
					w = w&^(0xff<<k) | uint64(']'+r.Intn('z'-']'+1))<<k
				}
			}
		}
		want := true
		for k := 0; k < 64; k += 8 {
			if c := byte(w >> k); c < ']' || c > 'z' {
				want = false
			}
		}
		if got := plainWord(w); got != want {
			t.Fatalf("plainWord(%#x) = %v, want %v", w, got, want)
		}
	}
}
