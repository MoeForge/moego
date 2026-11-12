package json

import (
	"fmt"
	"strings"
	"testing"
	"unsafe"
)

type internDoc struct {
	ID    string         `json:"id"`
	Model string         `json:"model"`
	Tags  []string       `json:"tags"`
	Any   any            `json:"any"`
	Meta  map[string]any `json:"meta"`
	Esc   string         `json:"esc"`
}

const internInput = `{"id":"chatcmpl-1","model":"gpt-4o","tags":["a","bb","ccc"],"any":"x-any","meta":{"k":"v","n":["w"]},"esc":"a\"bé"}`

// TestInternStrings checks that short strings decoded again share the
// kept copy, that each value is still right, and that UnmarshalNoCopy
// does not intern.
func TestInternStrings(t *testing.T) {
	// Decodes on states that share one intern table, as the decodes on one
	// state do.
	m := new(decodeMemo)
	var docs [4]internDoc
	for k := range docs {
		d := &decodeState{memo: m}
		if err := d.unmarshal([]byte(internInput), &docs[k]); err != nil {
			t.Fatal(err)
		}
		m.spare, m.first = nil, 0 // as putDecodeState leaves them
	}
	checkInterned(t, "one table", docs[:], true)

	// Calls of Unmarshal, which take a state from the cache. Under the race
	// detector the states move between the slots and the pool from call to
	// call, so that two calls may decode on states of different tables.
	for k := range docs {
		docs[k] = internDoc{}
		if err := Unmarshal([]byte(internInput), &docs[k]); err != nil {
			t.Fatal(err)
		}
	}
	checkInterned(t, "Unmarshal", docs[:], !raceEnabled)

	in := []byte(internInput)
	var c internDoc
	if err := UnmarshalNoCopy(in, &c); err != nil {
		t.Fatal(err)
	}
	if p := unsafe.StringData(c.Model); p == unsafe.StringData(docs[3].Model) || !views(in, p) {
		t.Error("UnmarshalNoCopy: string does not view the input")
	}
}

// checkInterned checks the values of docs, decodes of internInput, and
// with shared set that the last two share each string the table keeps.
func checkInterned(t *testing.T, how string, docs []internDoc, shared bool) {
	t.Helper()
	want := internDoc{ID: "chatcmpl-1", Model: "gpt-4o", Tags: []string{"a", "bb", "ccc"}, Any: "x-any",
		Meta: map[string]any{"k": "v", "n": []any{"w"}}, Esc: "a\"bé"}
	for k := range docs {
		if got, w := fmt.Sprint(docs[k]), fmt.Sprint(want); got != w {
			t.Fatalf("%s: decode %d: got %s, want %s", how, k, got, w)
		}
	}
	if !shared {
		return
	}
	a, b := docs[len(docs)-2], docs[len(docs)-1]
	same := func(name, x, y string) {
		if unsafe.StringData(x) != unsafe.StringData(y) {
			t.Errorf("%s: %s not interned", how, name)
		}
	}
	same("struct field", a.ID, b.ID)
	same("slice element", a.Tags[2], b.Tags[2])
	same("interface value", a.Any.(string), b.Any.(string))
	same("map value", a.Meta["k"].(string), b.Meta["k"].(string))
	same("escaped string", a.Esc, b.Esc)
	for ka := range a.Meta {
		for kb := range b.Meta {
			if ka == kb && ka == "k" {
				same("map key", ka, kb)
			}
		}
	}
}

func views(b []byte, p *byte) bool {
	return uintptr(unsafe.Pointer(p))-uintptr(unsafe.Pointer(unsafe.SliceData(b))) < uintptr(len(b))
}

// TestInternTable checks the table on its own: equal strings only, the
// bound on the kept bytes, and many strings of few sets.
func TestInternTable(t *testing.T) {
	d := new(decodeState)
	strs := make([]string, 3000)
	for k := range strs {
		strs[k] = fmt.Sprintf("s%d-%s", k, strings.Repeat("y", k%internMax))[:1+k%internMax]
	}
	for round := range 4 {
		for _, s := range strs {
			for range 2 {
				if p := d.intern(s); p != nil && *(*string)(p) != s {
					t.Fatalf("round %d: intern(%q) gave %q", round, s, *(*string)(p))
				}
			}
		}
		kept := 0
		for k := range d.memo.blk {
			for _, p := range d.memo.blk[k] {
				if p != nil {
					kept += classAbove(16 + len(*(*string)(p)))
				}
			}
		}
		if kept != d.memo.kept || kept > internKeep {
			t.Fatalf("round %d: kept %d bytes, counted %d, bound %d", round, kept, d.memo.kept, internKeep)
		}
	}

	// Strings of the same hash: neither is taken for the other.
	d = new(decodeState)
	x, y := "first", "other"
	d.intern(x)
	if p := d.intern(x); p == nil || *(*string)(p) != x {
		t.Fatal("x not kept")
	}
	h := internHash(x)
	k, tag := h>>59, uint32(h)
	for w := range internWays {
		if d.memo.tag[k][w] == tag {
			// y stands for a string of the same hash.
			blk := d.memo.blk[k][w]
			if blk == nil {
				t.Fatal("x has no block")
			}
			hy := internHash(y)
			d.memo.tag[hy>>59][w] = uint32(hy)
			d.memo.blk[hy>>59][w] = blk
		}
	}
	if p := d.intern(y); p != nil {
		t.Fatalf("intern(%q) gave %q", y, *(*string)(p))
	}
}

// TestInternNoAlloc checks that a decode of strings met before allocates
// nothing for them.
func TestInternNoAlloc(t *testing.T) {
	if raceEnabled || checkptrEnabled() {
		t.Skip("allocations differ under the race detector and checkptr")
	}
	in := []byte(`{"id":"chatcmpl-1","model":"gpt-4o","tags":["a","bb","ccc"]}`)
	var v internDoc
	for range 3 {
		v = internDoc{}
		if err := Unmarshal(in, &v); err != nil {
			t.Fatal(err)
		}
	}
	n := testing.AllocsPerRun(100, func() {
		v = internDoc{}
		if err := Unmarshal(in, &v); err != nil {
			t.Fatal(err)
		}
	})
	// The tags array only, with its header where reflect makes it.
	want := 1.0
	if zeroAlloc {
		want = 2
	}
	if n > want {
		t.Errorf("%v allocations, want %v", n, want)
	}
}

type slabDoc struct {
	N    *int     `json:"n"`
	F    *float64 `json:"f"`
	Long string   `json:"long"`
	Tags []string `json:"tags"`
	Any  any      `json:"any"`
}

// TestSlabHint checks decodes into one type whose slab needs change from
// call to call, so that the size the last decode noted is too small or
// too large.
func TestSlabHint(t *testing.T) {
	doc := func(k int) (string, slabDoc) {
		n, f := k, float64(k)/4
		long := strings.Repeat(string(rune('a'+k%26)), 300+k*97%700)
		tags := make([]string, k%9)
		for j := range tags {
			tags[j] = fmt.Sprintf("tag-%d-%s", j, strings.Repeat("z", (k+j)%40))
		}
		v := slabDoc{N: &n, F: &f, Long: long, Tags: tags, Any: map[string]any{"x": float64(k), "s": long[:k%50]}}
		b, err := Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		pad := ""
		if k%3 == 0 {
			// Inputs of more than slabChunkMax bytes too.
			pad = strings.Repeat(" ", 5000)
		}
		return pad + string(b), v
	}
	for k := range 60 {
		in, want := doc(k % 7 * (k%5 + 1))
		var got slabDoc
		if err := Unmarshal([]byte(in), &got); err != nil {
			t.Fatal(err)
		}
		if g, w := fmt.Sprint(*got.N, *got.F, got.Long, got.Tags, got.Any), fmt.Sprint(*want.N, *want.F, want.Long, want.Tags, want.Any); g != w {
			t.Fatalf("decode %d: got %.80s, want %.80s", k, g, w)
		}
	}
}

type bigDoc struct {
	A any    `json:"a"`
	S string `json:"s"`
	E string `json:"e"`
}

// TestBigSpare checks decodes whose first large block changes size from
// call to call, and that a spare, when made, is the block the decode
// takes rather than one more.
func TestBigSpare(t *testing.T) {
	doc := func(n int) ([]byte, bigDoc) {
		a := strings.Repeat(string(rune('a'+n%26)), n)
		s := strings.Repeat("s", n/2+40<<10)
		e := strings.Repeat(`é`, n/6)
		in := `{"a":"` + a + `","s":"` + s + `","e":"` + e + `"}`
		return []byte(in), bigDoc{A: a, S: s, E: strings.Repeat("é", n/6)}
	}
	for k, n := range []int{40 << 10, 40 << 10, 40 << 10, 40 << 10, 90 << 10, 90<<10 + 1, 90 << 10, 40 << 10, 300 << 10, 300 << 10, 300 << 10, 1 << 10, 1 << 10, 40 << 10} {
		in, want := doc(n)
		var got bigDoc
		if err := Unmarshal(in, &got); err != nil {
			t.Fatal(err)
		}
		if got.A != want.A || got.S != want.S || got.E != want.E {
			t.Fatalf("decode %d of %d bytes: values differ", k, n)
		}
	}
	if raceEnabled {
		return
	}
	allocs := func(ns ...int) float64 {
		var ins [][]byte
		for _, n := range ns {
			in, _ := doc(n)
			ins = append(ins, in)
		}
		k := 0
		return testing.AllocsPerRun(30, func() {
			var v bigDoc
			if err := Unmarshal(ins[k%len(ins)], &v); err != nil {
				t.Fatal(err)
			}
			k++
		})
	}
	if same, other := allocs(64<<10), allocs(64<<10, 200<<10); same != other {
		t.Errorf("%v allocations with a spare, %v without", same, other)
	}
	// The count of sure notes stays that of the notes, as notes give way.
	var m decodeMemo
	var roots [len(m.slab) + 5]uint64
	for k := range 400 {
		m.first = int32(k % 3 / 2 * bigPage)
		m.noteSlab(unsafe.Pointer(&roots[k*7%len(roots)]), k)
		n := 0
		for _, e := range m.slab {
			if e.sure {
				n++
			}
		}
		if n != int(m.sures) {
			t.Fatalf("note %d: %d sure notes, counted %d", k, n, m.sures)
		}
	}
}

type noteA struct {
	S string `json:"s"`
}

type noteB struct {
	A noteA `json:"a"`
	N int   `json:"n"`
}

// noteC decodes its members as a noteB in a call of its own, so that each
// decode into noteC makes one into noteB on another state.
type noteC struct{ B noteB }

func (c *noteC) UnmarshalJSON(data []byte) error { return Unmarshal(data, &c.B) }

// TestSlabNotes checks decodes into more types than a memo notes, in
// turns, with nested decodes from methods, and that a memo keeps the notes
// of as many types as it has room for, whatever their addresses.
func TestSlabNotes(t *testing.T) {
	in := []byte(`{"a":{"s":"` + strings.Repeat("x", 600) + `"},"n":7}`)
	decode := func(k int) {
		switch k % 11 {
		case 0, 5:
			var c noteC
			if err := Unmarshal(in, &c); err != nil || c.B.N != 7 || len(c.B.A.S) != 600 {
				t.Fatalf("noteC: %v %d %d", err, c.B.N, len(c.B.A.S))
			}
		case 1:
			var v map[string]any
			if err := Unmarshal(in, &v); err != nil || len(v) != 2 {
				t.Fatalf("map: %v %d", err, len(v))
			}
		case 2:
			var v []any
			if err := Unmarshal([]byte(`[1,"`+strings.Repeat("y", 300)+`",3]`), &v); err != nil || len(v) != 3 {
				t.Fatalf("slice: %v %d", err, len(v))
			}
		case 3:
			var v noteB
			if err := Unmarshal(in, &v); err != nil || v.N != 7 {
				t.Fatalf("noteB: %v %d", err, v.N)
			}
		case 4:
			var v struct{ A any }
			if err := Unmarshal([]byte(`{"A":["`+strings.Repeat("z", 200)+`"]}`), &v); err != nil || v.A == nil {
				t.Fatalf("anon: %v", err)
			}
		case 6:
			var v *noteA
			if err := Unmarshal([]byte(`{"s":"`+strings.Repeat("w", 900)+`"}`), &v); err != nil || len(v.S) != 900 {
				t.Fatalf("*noteA: %v", err)
			}
		case 7:
			var v [2]string
			if err := Unmarshal([]byte(`["`+strings.Repeat("v", 500)+`","u"]`), &v); err != nil || v[1] != "u" {
				t.Fatalf("array: %v", err)
			}
		case 8:
			var v any
			if err := Unmarshal(in, &v); err != nil || v == nil {
				t.Fatalf("any: %v", err)
			}
		case 9:
			var v map[string]noteA
			if err := Unmarshal([]byte(`{"k":{"s":"`+strings.Repeat("t", 400)+`"}}`), &v); err != nil || len(v["k"].S) != 400 {
				t.Fatalf("map of noteA: %v", err)
			}
		case 10:
			var v []noteA
			if err := Unmarshal([]byte(`[{"s":"`+strings.Repeat("s", 700)+`"}]`), &v); err != nil || len(v) != 1 {
				t.Fatalf("slice of noteA: %v", err)
			}
		}
	}
	for k := range 200 {
		decode(k)
	}
	var m decodeMemo
	var roots [len(m.slab) + 3]uint64
	for round := range 3 {
		for k := range roots {
			m.noteSlab(unsafe.Pointer(&roots[k]), 100*k+round)
		}
		// The last len(m.slab) roots noted keep their notes.
		for k := len(roots) - len(m.slab); k < len(roots); k++ {
			if got := m.slabHint(unsafe.Pointer(&roots[k])); got != 100*k+round {
				t.Fatalf("round %d: note of root %d is %d, want %d", round, k, got, 100*k+round)
			}
		}
	}
}
