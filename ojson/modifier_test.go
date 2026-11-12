package ojson

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// Expected outputs below come from gjson v1.19.0.

func TestModifierExamples(t *testing.T) {
	const doc = `{"name":{"first":"Tom","last":"Anderson"},"age":37,"children":["Sara","Alex","Jack"],"fav.movie":"Deer Hunter","friends":[{"first":"Dale","last":"Murphy","age":44,"nets":["ig","fb","tw"]},{"first":"Roger","last":"Craig","age":68,"nets":["fb","tw"]},{"first":"Jane","last":"Murphy","age":47,"nets":["ig","tw"]}]}`
	for _, c := range []struct{ json, name, arg, want string }{
		{doc, "keys", "", `["name","age","children","fav.movie","friends"]`},
		{`["Sara","Alex","Jack"]`, "reverse", "", `["Jack","Alex","Sara"]`},
		{`{"a":[],"b":{}}`, "reverse", "", `{"b":{},"a":[]}`},
		{`  [ 1 , 2 ]  `, "ugly", "", `[1,2]`},
		{`{"a":[1,2,3]}`, "pretty", "", "{\n  \"a\": [1, 2, 3]\n}\n"},
		{`{"a":[1,2,3]}`, "pretty", `{"width":10}`, "{\n  \"a\": [\n    1,\n    2,\n    3\n  ]\n}\n"},
		{`[[1,2],[3]]`, "pretty", `{"width":13}`, "[[1, 2], [3]]\n"},
		{`[[1,2],[3]]`, "pretty", `{"width":8}`, "[\n  [\n    1,\n    2\n  ],\n  [3]\n]\n"},
		{`{"\u0062":1,"a":2,"\"":3}`, "pretty", `{"sortKeys":true}`, "{\n  \"\\\"\": 3,\n  \"a\": 2,\n  \"\\u0062\": 1\n}\n"},
		{`{"a":[1,2,3]}`, "pretty", `{"prefix":"> ","indent":"_"}`, " {\n \"a\": [1, 2, 3]\n }\n"},
		{`{"a":[1,[2,[3,[4]]],[]]}`, "flatten", "", `{"a":[1,[2,[3,[4]]],[]]}`},
		{`[1,[2,[3,[4]]],[]]`, "flatten", "", `[1,2,[3,[4]]]`},
		{`[1,[2,[3,[4]]],[]]`, "flatten", `{"deep":true}`, `[1,2,3,4]`},
		{`[{"a":1,"b":2},{"a":3,"c":4},{"d":5},7,[8]]`, "join", "", `{"a":3,"b":2,"c":4,"d":5}`},
		{`[{"a":1,"b":2},{"a":3,"c":4},{"d":5},7,[8]]`, "join", `{"preserve":true}`, `{"a":1,"b":2,"a":3,"c":4,"d":5}`},
		{`[{},{"a":1}]`, "join", `{"preserve":true}`, `{,"a":1}`},
		{`{"a":1, "b" : [ 1 , 2 ] ,"c":{ "d" : "e f" }}`, "values", "", `[1,[ 1 , 2 ],{ "d" : "e f" }]`},
		{`[1,2]`, "keys", "", `[null,null]`},
		{`"s"`, "keys", "", `[null]`},
		{``, "keys", "", `[]`},
		{`{"id":["123","456","789"],"val":[2,1]}`, "group", "", `[{"id":"123","val":2},{"id":"456","val":1},{"id":"789"}]`},
		{`[1]`, "group", "", ``},
		{`{"a":{"b":{"c":1}},"c":2,"d":[{"c":3},{"e":{"c":4}}]}`, "dig", "c", `[2,1,3,4]`},
		{``, "dig", "@dig", `[[]]`},
		{` `, "dig", "a", `[]`},
		{`<b>&`, "tostr", "", `"\u003cb\u003e\u0026"`},
		{`"{\"a\":1}"`, "fromstr", "", `{"a":1}`},
		{`1.50`, "fromstr", "", `1.5`},
		{`{"a":1`, "valid", "", ``},
	} {
		got, ok := applyModifier(c.name, c.json, c.arg)
		if !ok || got != c.want {
			t.Errorf("@%s:%s on %q = %q, %v, want %q", c.name, c.arg, c.json, got, ok, c.want)
		}
	}
	if _, ok := applyModifier("zz", "{}", ""); ok {
		t.Error("@zz exists")
	}
}

func TestCustomModifier(t *testing.T) {
	AddModifier("b-test-upper", func(json, arg string) string { return strings.ToUpper(json) + arg })
	if !ModifierExists("b-test-upper", nil) || !modifierKnown("b-test-upper") || !modifierKnown("pretty") {
		t.Fatal("modifier not registered")
	}
	if got, ok := applyModifier("b-test-upper", `"x"`, "!"); !ok || got != `"X"!` {
		t.Errorf("custom modifier = %q, %v", got, ok)
	}
	DisableModifiers = true
	_, ok := applyModifier("pretty", "[]", "")
	known := modifierKnown("pretty")
	DisableModifiers = false
	if ok || known {
		t.Error("DisableModifiers left @pretty on")
	}
}

// TestModifiersDeep runs every modifier on nesting far deeper than a
// recursive implementation's stack allows, and checks that output stays
// bounded.
func TestModifiersDeep(t *testing.T) {
	// @dig runs a Get on every value, so it is quadratic in depth, as in
	// gjson; it gets a smaller depth m. The race detector run uses
	// smaller depths.
	n, m := 1<<18, 1<<12
	if raceEnabled {
		n, m = 1<<14, 1<<10
	}
	docs := []string{
		strings.Repeat("[", n) + strings.Repeat("]", n),
		strings.Repeat(`{"a":`, n) + "1" + strings.Repeat("}", n),
		strings.Repeat(`{"a":[`, n/2) + "1" + strings.Repeat("]}", n/2),
		strings.Repeat("[", n), // unterminated
		strings.Repeat(`{"a":{"a":1,"a":`, n/4) + "2" + strings.Repeat("}}", n/4),
		strings.Repeat(`{"a":[],"a":`, n/2) + "2" + strings.Repeat("}", n/2),
		strings.Repeat(`{"a":[[],{"a":[`, n/8) + strings.Repeat("]}]", n/8),
	}
	mods := []struct{ name, arg string }{
		{"reverse", ""}, {"ugly", ""}, {"pretty", ""}, {"pretty", `{"sortKeys":true}`}, {"pretty", `{"width":0}`},
		{"this", ""}, {"valid", ""}, {"flatten", ""}, {"flatten", `{"deep":true}`}, {"join", ""},
		{"join", `{"preserve":true}`}, {"keys", ""}, {"values", ""}, {"tostr", ""}, {"fromstr", ""}, {"group", ""},
	}
	for _, d := range docs {
		for _, m := range mods {
			out, _ := applyModifier(m.name, d, m.arg)
			if len(out) > outLimit(len(d))+1<<20 {
				t.Errorf("@%s on %d deep bytes: %d bytes out", m.name, len(d), len(out))
			}
		}
		r := Parse(d)
		_ = r.Value()
		_ = r.Path(d)
		r.ForEach(func(k, v Result) bool { return true })
	}
	for _, d := range []string{strings.Repeat("[", m) + strings.Repeat("]", m), strings.Repeat(`{"a":`, m) + "1" + strings.Repeat("}", m)} {
		for _, arg := range []string{"a", "@this", "0", "zz"} {
			out, _ := applyModifier("dig", d, arg)
			if len(out) > outLimit(len(d))+len(d) {
				t.Errorf("@dig:%s: %d bytes out", arg, len(out))
			}
		}
	}
}

// uglyRef is @ugly one byte at a time.
func uglyRef(s string) string {
	var b []byte
	in, esc := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case in:
			esc, in = c == '\\', c != '"'
		case c <= ' ':
			continue
		case c == '"':
			in = true
		}
		b = append(b, c)
	}
	return string(b)
}

// quoteRef is appendJSONString one byte at a time.
func quoteRef(s string, html bool) string {
	const hex = "0123456789abcdef"
	b := []byte{'"'}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '"' || c == '\\':
			b = append(b, '\\', c)
		case c == '\n':
			b = append(b, `\n`...)
		case c == '\r':
			b = append(b, `\r`...)
		case c == '\t':
			b = append(b, `\t`...)
		case c == '\b':
			b = append(b, `\b`...)
		case c == '\f':
			b = append(b, `\f`...)
		case c < 0x20 || html && (c == '<' || c == '>' || c == '&'):
			b = append(b, '\\', 'u', '0', '0', hex[c>>4], hex[c&15])
		case c < 0x80:
			b = append(b, c)
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			switch {
			case r == utf8.RuneError && size == 1:
				b = append(b, `\ufffd`...)
			case r == '\u2028':
				b = append(b, `\u2028`...)
			case r == '\u2029':
				b = append(b, `\u2029`...)
			default:
				b = append(b, s[i:i+size]...)
			}
			i += size
			continue
		}
		i++
	}
	return string(append(b, '"'))
}

// TestUglyBlocks compares @ugly, which reads 64-byte blocks, with a
// byte-at-a-time version on inputs dense in quotes, backslashes and
// whitespace across block edges.
func TestUglyBlocks(t *testing.T) {
	ref := uglyRef
	const alphabet = " \t\n\"\"\\\\ab{}[]:,\x00\x7f\xff"
	const plain = " \t\n\"\"ab{}[]:,\x00\x7f\xff" // no backslashes
	seed := uint64(1)
	for n := 0; n < 60000; n++ {
		size := n % 600
		b := make([]byte, size)
		for i := range b {
			seed = seed*6364136223846793005 + 1442695040888963407
			switch n % 5 {
			case 0: // long runs with few special bytes
				b[i] = 'x'
				if seed>>60 == 0 {
					b[i] = alphabet[seed>>32%uint64(len(alphabet))]
				}
			case 1:
				b[i] = alphabet[seed>>32%6]
			case 2:
				b[i] = plain[seed>>32%uint64(len(plain))]
			case 3: // rare whitespace and quotes, no backslashes
				b[i] = 'x'
				if seed>>58 == 0 {
					b[i] = plain[seed>>32%5]
				}
			default:
				b[i] = alphabet[seed>>32%uint64(len(alphabet))]
			}
		}
		s := string(b)
		if got, want := modUgly(s), ref(s); got != want {
			t.Fatalf("@ugly(%q) = %q, want %q", s, got, want)
		}
	}
}

// TestQuoteBlocks checks appendJSONString and quotedLen on long strings,
// where AVX2 takes 64-byte blocks, against a byte-at-a-time reference.
func TestQuoteBlocks(t *testing.T) {
	ref := quoteRef
	pieces := []string{"a", "b", "x", "\"", "\\", " ", "<", ">", "&", "\n", "\x00", "\x1f", "é", "世", "\u2028", "\xff", "\xe4\xb8", "{", ":"}
	seed := uint64(7)
	for n := 0; n < 30000; n++ {
		size := n % 700
		var b strings.Builder
		for b.Len() < size {
			seed = seed*6364136223846793005 + 1442695040888963407
			switch r := seed >> 59; {
			case n%4 == 0 && r < 28: // plain runs
				b.WriteByte('a' + byte(r%26))
			case n%4 == 1 && r < 24: // JSON-like: quotes and backslashes only
				b.WriteString([]string{"\"", "\\", "k", "1", ",", ":"}[r%6])
			default:
				b.WriteString(pieces[seed>>32%uint64(len(pieces))])
			}
		}
		s := b.String()
		for _, html := range []bool{false, true} {
			want := ref(s, html)
			if got := string(appendJSONString([]byte("pre"), s, html)); got != "pre"+want {
				t.Fatalf("appendJSONString(%q, %v) = %q, want %q", s, html, got, want)
			}
			if got := quotedLen(s, html); got != len(want) {
				t.Fatalf("quotedLen(%q, %v) = %d, want %d", s, html, got, len(want))
			}
		}
	}
}

// TestPrettyAgain checks that @pretty of its own output, which comes back
// as it is, is the same text.
func TestPrettyAgain(t *testing.T) {
	for _, in := range []string{`{"a":[1,2],"b":{"c":"d"}}`, `[]`, `"s"`, `{"a":[1,[2,{"b":3}]],"c":""}`} {
		once, _ := applyModifier("pretty", in, "")
		twice, _ := applyModifier("pretty", once, "")
		if once == "" || twice != once {
			t.Fatalf("@pretty of %q = %q, want %q", once, twice, once)
		}
	}
}

// TestPrettySortDeep checks sortKeys past the scan budget, where the ends
// of containers come from one pass over the document: the output must
// match that of the same document with its keys already in order.
func TestPrettySortDeep(t *testing.T) {
	const n = 1000
	for _, c := range []struct{ in, sorted string }{
		{`{"y":1,"x":[2,{"d":1,"c":2}]}`, `{"x":[2,{"c":2,"d":1}],"y":1}`},
		{`{"a":{"k":[1]},"b":0,"a":{"k":[0]}}`, `{"a":{"k":[0]},"a":{"k":[1]},"b":0}`},
	} {
		in := strings.Repeat(`{"z":0,"b":`, n) + c.in + strings.Repeat("}", n)
		want := strings.Repeat(`{"b":`, n) + c.sorted + strings.Repeat(`,"z":0}`, n)
		got, _ := applyModifier("pretty", in, `{"sortKeys":true}`)
		exp, _ := applyModifier("pretty", want, "")
		if got != exp || got == "" {
			t.Errorf("sortKeys on %s: %d bytes, want %d", c.in, len(got), len(exp))
		}
	}
}

// TestPathForward checks that Path and Paths, which walk forward, give the
// paths of the walk back from each value, for every value of random
// documents, and that mixed and unsorted Indexes fall back where needed.
func TestPathForward(t *testing.T) {
	seed := uint64(7)
	rnd := func(n int) int {
		seed = seed*6364136223846793005 + 1442695040888963407
		return int(seed>>33) % n
	}
	keys := []string{`"a"`, `"b.c"`, `"d\"e"`, `"*?"`, `"\u0041"`, `""`, `"x y"`, `"e\\"`}
	sp := []string{"", " ", "\n  "}
	var gen func(b *strings.Builder, depth int)
	gen = func(b *strings.Builder, depth int) {
		switch k := rnd(8); {
		case depth < 4 && k < 3:
			b.WriteString("{" + sp[rnd(3)])
			for i, n := 0, rnd(4); i < n; i++ {
				if i > 0 {
					b.WriteString("," + sp[rnd(3)])
				}
				b.WriteString(keys[rnd(len(keys))] + sp[rnd(3)] + ":" + sp[rnd(3)])
				gen(b, depth+1)
			}
			b.WriteString(sp[rnd(3)] + "}")
		case depth < 4 && k < 6:
			b.WriteString("[" + sp[rnd(3)])
			for i, n := 0, rnd(4); i < n; i++ {
				if i > 0 {
					b.WriteString(sp[rnd(3)] + ",")
				}
				gen(b, depth+1)
			}
			b.WriteString("]")
		case k == 6:
			b.WriteString(`"s\"[{,\\"`)
		default:
			b.WriteString("-1.5")
		}
	}
	for n := 0; n < 3000; n++ {
		var b strings.Builder
		b.WriteString(sp[rnd(3)])
		gen(&b, 0)
		json := b.String()
		var all []Result
		var walk func(r Result)
		walk = func(r Result) {
			all = append(all, r)
			if r.Type == JSON {
				r.ForEach(func(_, v Result) bool { walk(v); return true })
			}
		}
		walk(Parse(json))
		idx := make([]int, len(all))
		for k, r := range all {
			idx[k] = r.Index
			if got, want := r.Path(json), pathBack(json, r.Index); got != want {
				t.Fatalf("Path at %d of %s = %q, want %q", r.Index, json, got, want)
			}
		}
		// All the values as the elements of one result, in document
		// order, then reversed with a bad index mixed in.
		for pass := 0; pass < 2; pass++ {
			raws := make([]string, len(all))
			for k, r := range all {
				raws[k] = r.Raw
			}
			arr := Result{Type: JSON, Raw: "[" + strings.Join(raws, ",") + "]", Indexes: idx}
			got := arr.Paths(json)
			for k := range idx {
				want := ""
				if idx[k] >= 0 && strings.HasPrefix(json[idx[k]:], all[k].Raw) {
					want = pathBack(json, idx[k])
				}
				if k >= len(got) || got[k] != want {
					t.Fatalf("Paths[%d] (at %d) of %s = %v, want %q", k, idx[k], json, got, want)
				}
			}
			slices.Reverse(all)
			slices.Reverse(idx)
			if len(idx) > 1 {
				idx[0]++
			}
		}
	}
}

// TestPathMalformed covers gjson issue #400, where Result.Path panics on
// malformed input, and Path on results that do not point into json.
func TestPathMalformed(t *testing.T) {
	for _, c := range []struct{ json, path string }{
		{`{"b:"2}`, "b:"}, {`{"b:"2}`, "b"}, {`{"a":"b\`, "a"}, {`[1,`, "1"}, {`"\"`, "@this"},
		{`{"a":[1,2]}`, "a.#"}, {`{"a\\":1}`, `a\\`}, {`:"x`, "@this"}, {`"a":1`, "a"},
	} {
		r := Get(c.json, c.path)
		_ = r.Path(c.json)
		_ = r.Paths(c.json)
		for i := -1; i <= len(c.json)+1; i++ {
			for j := i; j <= len(c.json); j++ {
				if i >= 0 {
					_ = Result{Raw: c.json[i:j], Index: i}.Path(c.json)
				}
				_ = Result{Raw: "x", Index: i, Indexes: []int{i, j, -5, int(^uint(0) >> 1)}}.Paths(c.json)
			}
		}
	}
}

// TestPathStringRoot covers values that Get finds in the text of a string
// root, where Path walks back over that text as gjson does.
func TestPathStringRoot(t *testing.T) {
	for _, c := range []struct {
		json string
		idx  int
		want string
	}{
		{`"xx:,[1,2]"`, 8, ""}, {`"xx,[1,2]"`, 7, "1"}, {`"[:,1,22\"a\""`, 4, ""}, {`"[{,x}[A"`, 6, "1"},
		{`"[1{{,[x:{"`, 6, "1"}, {`"[{AA1,]{\"a\":"`, 8, "1"}, {`"22[\n[{{,{"`, 10, "0.1"},
	} {
		if got := (Result{Raw: c.json[c.idx : c.idx+1], Index: c.idx}).Path(c.json); got != c.want {
			t.Errorf("Path at %d of %s = %q, want %q", c.idx, c.json, got, c.want)
		}
	}
}

// TestCount compares count with the calls of ForEach, on valid and broken
// JSON, and checks that Array and Map are sized from it.
func TestCount(t *testing.T) {
	g := &validGen{seed: 21}
	const nasty = "\"\\{}[]:,0 \t\n\x00\x80"
	check := func(r Result) {
		n := 0
		r.ForEach(func(_, _ Result) bool { n++; return true })
		if c := r.count(); c != n {
			t.Fatalf("count of %q = %d, ForEach calls %d", r.Raw, c, n)
		}
		if a := r.Array(); len(a) != cap(a) {
			t.Fatalf("Array of %q: len %d, cap %d", r.Raw, len(a), cap(a))
		}
	}
	for n := 0; n < 20000; n++ {
		g.b.Reset()
		g.value(0)
		s := g.b.String()
		for k := 0; k < 4; k++ {
			r := Result{Type: JSON, Raw: s}
			if k > 0 && len(s) > 0 {
				b := []byte(s)
				b[g.n(len(b))] = nasty[g.n(len(nasty))]
				r.Raw = string(b[:g.n(len(b)+1)])
			}
			check(r)
			check(Parse(r.Raw))
		}
	}
	for _, s := range []string{"", "[", "{", "[,,]", `{"a"}`, `{"a":}`, "[1 2 3]", `{"a":1 "b":2}`, `["x`, `{"a":[}`, "[]]", "x"} {
		check(Result{Type: JSON, Raw: s})
		check(Parse(s))
	}
}

func FuzzModifiers(f *testing.F) {
	f.Add(`{"a":[1,{"b":2}],"c":"x"}`, "", uint8(0))
	f.Add(`[[1,2],[3,[4]],{"a":[5]}]`, `{"deep":true}`, uint8(7))
	f.Add(`{"b":1,"a":2,"a":[3]}`, `{"sortKeys":true,"width":8,"prefix":" ","indent":"\t"}`, uint8(2))
	f.Add(`"{\"a\":1}"`, "", uint8(14))
	f.Add(`{"a":[1,2],"b":["x"]}`, "a", uint8(12))
	names := []string{"reverse", "ugly", "pretty", "this", "valid", "flatten", "join", "keys", "values",
		"tostr", "fromstr", "group", "dig", "zz"}
	f.Fuzz(func(t *testing.T, json, arg string, k uint8) {
		name := names[int(k)%len(names)]
		out, _ := applyModifier(name, json, arg)
		if len(out) > outLimit(len(json))+(len(arg)+1)*(len(json)+1)*4 {
			t.Fatalf("@%s: %d bytes out of %d", name, len(out), len(json))
		}
		if Valid(json) && name == "ugly" && !Valid(out) {
			t.Fatalf("@ugly made %q invalid: %q", json, out)
		}
		r := Parse(json)
		_ = r.Value()
		_ = r.Map()
		_ = r.Array()
		_ = r.Path(json)
		ForEachLine(json, func(Result) bool { return true })
		_ = Escape(json)
		if b := AppendJSONString(nil, json); !utf8.Valid(b) || !Valid(string(b)) {
			t.Fatalf("AppendJSONString(%q) = %q", json, b)
		}
		for _, html := range []bool{false, true} {
			if got, want := string(appendJSONString(nil, json, html)), quoteRef(json, html); got != want {
				t.Fatalf("appendJSONString(%q, %v) = %q, want %q", json, html, got, want)
			}
		}
		if got, want := modUgly(json), uglyRef(json); got != want {
			t.Fatalf("@ugly(%q) = %q, want %q", json, got, want)
		}
	})
}

// TestModifierRegistryRace adds modifiers while other goroutines use them;
// run it with -race.
func TestModifierRegistryRace(t *testing.T) {
	done := make(chan bool)
	for g := 0; g < 4; g++ {
		go func() {
			for i := 0; i < 2000; i++ {
				applyModifier("b-race-1", `[1,2]`, "")
				applyModifier("reverse", `[1,2]`, "")
				ModifierExists("b-race-2", nil)
				modifierKnown("pretty")
			}
			done <- true
		}()
	}
	for i := 0; i < 200; i++ {
		AddModifier("b-race-1", func(json, arg string) string { return json })
		AddModifier("b-race-"+strings.Repeat("x", i%7), func(json, arg string) string { return arg })
	}
	for g := 0; g < 4; g++ {
		<-done
	}
	if got, ok := applyModifier("b-race-1", "[1]", ""); !ok || got != "[1]" {
		t.Errorf("b-race-1 = %q, %v", got, ok)
	}
}
