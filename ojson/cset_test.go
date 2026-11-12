package ojson

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unsafe"
)

// Query paths with ReplaceInPlace: spans that grow and shrink in one call
// must give the result of SetBytes, in the input's memory.
func TestSetQueryInPlace(t *testing.T) {
	opts := &Options{Optimistic: true, ReplaceInPlace: true}
	docs := []string{
		`[1,22,333,4444,5]`,
		`[4444,1,333,22,5,55555,6]`,
		`{"a":[{"b":1},{"b":4444},{"b":22},{"b":333}]}`,
		` [ 1 , 4444 ,1, 22 ] `,
	}
	paths := []string{`#(>0)#`, `a.#.b`, `#(%"*")#`, `#(>2)`, `a.#(b>2).b`}
	for _, doc := range docs {
		for _, p := range paths {
			for _, raw := range []string{"", "0", "77", "999", strings.Repeat("8", 9)} {
				want, werr := SetRawBytes([]byte(doc), p, []byte(raw))
				for _, extra := range []int{0, 40} {
					in := make([]byte, len(doc), len(doc)+extra)
					copy(in, doc)
					got, err := SetRawBytesOptions(in, p, []byte(raw), opts)
					if string(got) != string(want) || errText(err) != errText(werr) {
						t.Fatalf("%q %q %q: got %q, %v; want %q, %v", doc, p, raw, got, err, want, werr)
					}
					if len(want) <= cap(in) && !sameMemory(got, in) {
						t.Fatalf("%q %q %q: not in place", doc, p, raw)
					}
				}
			}
		}
	}
}

// Optimistic on a root string never edits the text of the string.
func TestOptimisticStringRoot(t *testing.T) {
	for _, o := range []*Options{{Optimistic: true}, {Optimistic: true, ReplaceInPlace: true}} {
		got, err := SetBytesOptions([]byte(`"[Q,2]"`), "1", 7, o)
		if err != nil || string(got) != `[null,7]` {
			t.Errorf("%+v: got %q, %v; want [null,7]", *o, got, err)
		}
	}
}

// Doc edits on lookup paths whose component ends in an escaped dot.
func TestDocLookupEscapedDot(t *testing.T) {
	for _, p := range []string{`{\.`, `"\.`, `[\.`, `!\.`, `\.`, `a.{\.`} {
		d := NewDocString("0")
		for _, v := range []string{`""`, "0"} {
			if err := d.SetRaw(p, v); err != nil {
				t.Fatalf("%q: SetRaw: %v", p, err)
			}
			w, _ := SetRaw(d.String(), p, v)
			_ = w
		}
		if err := d.Delete(p); err != nil {
			t.Fatalf("%q: Delete: %v", p, err)
		}
		if !Valid(d.String()) {
			t.Fatalf("%q: %q", p, d.String())
		}
	}
}

// gjson ends an array component at the first '.' or '|', escaped or not;
// what follows applies to the missing element.
func TestArrayEscapedSplit(t *testing.T) {
	for _, c := range []struct{ doc, path, want string }{
		{`[]`, `\|[]`, `[]`},
		{`[1]`, `\.{}`, `{}`},
		{`[1]`, `a\|[0]`, `[]`},
		{`[]`, `\||@keys`, ``},
		{`[]`, `\|[]x`, ``},
		{`{"|[]":1}`, `\|[]`, `1`},
		{`{"a":[1]}`, `a.x\|[]`, `[]`},
		{`{"a":[1]}`, `a.0\.x`, ``},
		{`{"a.b":{"c":1}}`, `a\.b.c`, `1`},
		{`{"a":[1]}`, `a\.b`, ``},
		{`{"c":{"x":[1],"y":{"a|b":0}}}`, `c.*.a\|b`, ``},
		{`{"\"":0}`, `..0.`, ``},
		{`{"":0}`, `..0.`, `0`},
		{`{"":{"":1}}`, `..0..`, `1`},
	} {
		if r := Get(c.doc, c.path); r.Raw != c.want {
			t.Errorf("Get(%q, %q) = %q, want %q", c.doc, c.path, r.Raw, c.want)
		}
		if r := NewDocString(c.doc).Get(c.path); r.Raw != c.want {
			t.Errorf("Doc.Get(%q, %q) = %q, want %q", c.doc, c.path, r.Raw, c.want)
		}
		// More than three paths take GetMany's shared walk.
		if r := GetMany(c.doc, c.path, "x1", "x2", "x3")[0]; r.Raw != c.want {
			t.Errorf("GetMany(%q, %q) = %q, want %q", c.doc, c.path, r.Raw, c.want)
		}
		if r := GetManyBytes([]byte(c.doc), "x1", "x2", "x3", c.path)[3]; r.Raw != c.want {
			t.Errorf("GetManyBytes(%q, %q) = %q, want %q", c.doc, c.path, r.Raw, c.want)
		}
	}
}

// Optimistic replaces a scalar root that a JSON Lines path finds.
func TestOptimisticLinesScalar(t *testing.T) {
	for _, c := range []struct{ doc, path, want string }{
		{"\n0", "..0", "\n7"},
		{` "x"`, "..0", " 7"},
		{"0", "..0", "[[[7]]]"},
		{"\n0", "..0.a", `[[[{"a":7}]]]`},
	} {
		got, err := SetRawBytesOptions([]byte(c.doc), c.path, []byte("7"), &Options{Optimistic: true})
		if err != nil || string(got) != c.want {
			t.Errorf("%q %q: got %q, %v; want %q", c.doc, c.path, got, err, c.want)
		}
	}
}

// sjson's deletion of a member whose key starts with an escaped quote can
// start in an earlier element; where its result is valid ojson's matches.
func TestDeleteQuoteKeyAcross(t *testing.T) {
	for _, c := range []struct{ doc, path, want string }{
		{`[[{"\"":0},{"\"":0}]]`, `0.1."`, `[[{}]]`},
		{`[[{"x":1,"\"":0},{"\"":0}]]`, `0.-1."`, `[[{"x":1}]]`},
		{`{"a":[{"\"":0},{"\"":0}]}`, `a.1."`, `{"a":[{}]}`},
		{`[[{"x":1,"\"":0},{"\"":0}]]`, `0.0."`, `[[{},{"\"":0}]]`},
		// sjson's results are malformed: the member alone goes.
		{`[[{"\"":0},{"\"":0}]]`, `0.0."`, `[[{},{"\"":0}]]`},
		{`{"":{"\"":0}}`, `."`, `{"":{}}`},
	} {
		if got, err := Delete(c.doc, c.path); err != nil || got != c.want {
			t.Errorf("Delete(%q, %q) = %q, %v; want %q", c.doc, c.path, got, err, c.want)
		}
		d := NewDocString(c.doc)
		if err := d.Delete(c.path); err != nil || d.String() != c.want {
			t.Errorf("Doc.Delete(%q, %q) = %q, %v; want %q", c.doc, c.path, d.String(), err, c.want)
		}
	}
}

// A ':' component with an escaped number names no array element: Set fails
// as on a non-numeric key, Delete leaves the document as it is. An object
// still takes it as a key.
func TestForcedEscapedIndex(t *testing.T) {
	for _, c := range []struct{ doc, path, set string }{
		{`[0,0]`, `:\0`, ``},
		{`[0,0]`, `:\1`, ``},
		{`[0,0]`, `:\2`, ``},
		{`[0,0]`, `:\00`, ``},
		{`[]`, `:\0`, ``},
		{`[0,1,2,3,4,5,6,7,8,9,10,11]`, `:1\0`, ``},
		{`[0]`, `:\0.a`, ``},
		{`[[0,0]]`, `0.:\1`, ``},
		{`{"a":[0,0]}`, `a.:\1`, ``},
		{` [ 0 , [ 1 , 2 ] ] `, `1.:\1`, ``},
		{`{"a":[0,0]}`, `:\0`, `{"a":[0,0],"0":1}`},
		{`{"a":[0,0]}`, `:\0.a`, `{"a":[0,0],"0":{"a":1}}`},
		{`[0,0]`, `0.:\0`, `[{"0":1},0]`},
	} {
		got, err := Set(c.doc, c.path, 1)
		want, fails := c.set, c.set == ""
		if fails {
			want = c.doc
		}
		if got != want || (err != nil) != fails {
			t.Errorf("Set(%q, %q) = %q, %v; want %q (error %v)", c.doc, c.path, got, err, want, fails)
		}
		d := NewDocString(c.doc)
		if err := d.Set(c.path, 1); d.String() != want || (err != nil) != fails {
			t.Errorf("Doc.Set(%q, %q) = %q, %v; want %q (error %v)", c.doc, c.path, d.String(), err, want, fails)
		}
		if got, err := Delete(c.doc, c.path); err != nil || got != c.doc {
			t.Errorf("Delete(%q, %q) = %q, %v; want the input", c.doc, c.path, got, err)
		}
		d = NewDocString(c.doc)
		if err := d.Delete(c.path); err != nil || d.String() != c.doc {
			t.Errorf("Doc.Delete(%q, %q) = %q, %v; want the input", c.doc, c.path, d.String(), err)
		}
	}
}

// A query path that reads into the text of a string finds there what looks
// like a value; Set leaves the document as it is.
func TestSetQueryInString(t *testing.T) {
	for _, c := range []struct{ doc, path string }{
		{`{ "a\\b" : [ "[1,2]" ] }`, `a\\b.#(>"").1`},
		{`{"x":["[1,2]"]}`, `x.#(>"").1`},
		{`{"x":["[1,2]",{"y":"[3]"}]}`, `x.#(>"").0`},
	} {
		for _, o := range []*Options{nil, {ReplaceInPlace: true}} {
			got, err := SetRawBytesOptions([]byte(c.doc), c.path, []byte(`{"":[]}`), o)
			if err != nil || string(got) != c.doc {
				t.Errorf("%q %q: got %q, %v; want the input", c.doc, c.path, got, err)
			}
		}
	}
}

// gjson's @ugly always makes new text, so a query result that passes
// through it has no Index in the input, even where ojson's @ugly returns
// its compact input as it is.
func TestQueryUglyIndex(t *testing.T) {
	for _, c := range []struct {
		doc, path string
		index     int
	}{
		{`[ { "first" : 0 } ]`, `#(first).first.@ugly`, 0},
		{`[{"f":{"a":1}}]`, `#(f).f.@ugly.a`, 0},
		{`[{"f":{"a":1}}]`, `#(f).f.@this.a`, 11},
		{`[{"f":{"a":1}}]`, `#(f).f`, 6},
	} {
		if r := Get(c.doc, c.path); r.Index != c.index {
			t.Errorf("Get(%q, %q).Index = %d, want %d", c.doc, c.path, r.Index, c.index)
		}
	}
}

// Small '#.' and '#(...)#' results keep their text and indexes in one
// allocation, and '#' counts below 1000 allocate nothing.
func TestQueryResultAllocs(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"a":[`)
	for i := 0; i < 200; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"b":%d,"c":"x%d"}`, i%3, i)
	}
	b.WriteString(`],"m":[{"r":"system"},{"r":"user"}]}`)
	doc := b.String()
	for _, c := range []struct {
		path   string
		allocs float64
	}{{"m.#.r", 1}, {`m.#(r!="")#.r`, 1}, {"a.#", 0}, {"m.#", 0}} {
		if n := testing.AllocsPerRun(20, func() { Get(doc, c.path) }); n > c.allocs {
			t.Errorf("%s: %v allocs, want %v", c.path, n, c.allocs)
		}
	}
	r := Get(doc, "m.#.r")
	if r.Raw != `["system","user"]` || fmt.Sprint(r.Indexes) != fmt.Sprint([]int{strings.Index(doc, `"system"`), strings.Index(doc, `"user"`)}) || cap(r.Indexes) != 2 {
		t.Fatalf("m.#.r: %q %v cap %d", r.Raw, r.Indexes, cap(r.Indexes))
	}
	r.Indexes[0], r.Indexes[1] = -1, -1
	if r.Raw != `["system","user"]` {
		t.Fatalf("writing Indexes changed Raw: %q", r.Raw)
	}
	q := Get(doc, `a.#(b==2)#.c`)
	if len(q.Indexes) != 66 || cap(q.Indexes) != 66 || q.Indexes[0] != strings.Index(doc, `"x2"`) || q.Indexes[65] != strings.Index(doc, `"x197"`) || !strings.HasPrefix(q.Raw, `["x2","x5",`) {
		t.Fatalf("query: %.40q %d indexes", q.Raw, len(q.Indexes))
	}
	if r := Get(doc, "a.#"); r.Raw != "200" || r.Num != 200 {
		t.Fatalf("a.#: %q", r.Raw)
	}
	for n := 0; n < 1200; n++ {
		if got := countText(n); got != strconv.Itoa(n) {
			t.Fatalf("countText(%d) = %q", n, got)
		}
	}
	// Any base alignment, with and without room to spare.
	for off := 0; off < 8; off++ {
		for _, room := range []int{0, 3, 64} {
			buf := append(make([]byte, off, off+5+room)[off:], "[1,2]"...)
			raw, ix := withIndexes(buf, []int{7, 8, 9})
			if raw != "[1,2]" || fmt.Sprint(ix) != "[7 8 9]" || cap(ix) != 3 || uintptr(unsafe.Pointer(&ix[0]))%unsafe.Alignof(ix[0]) != 0 {
				t.Fatalf("off %d room %d: %q %v", off, room, raw, ix)
			}
		}
	}
}
