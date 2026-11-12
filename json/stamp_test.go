//go:build go1.26

package json

import (
	"bytes"
	stdjson "encoding/json"
	"github.com/MoeForge/moego/internal/jsonsimd"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type stampItem struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
	Note string   `json:"note"`
}

type stampAlias stampItem

// stampEdit marshals an alias of its item with marshal, as methods that
// marshal an alias of their type do, then changes the output with edit.
type stampEdit struct {
	Item    stampItem
	Edit    func([]byte) []byte
	Marshal func(any) ([]byte, error)
}

func (s stampEdit) MarshalJSON() ([]byte, error) {
	b, err := s.Marshal((*stampAlias)(&s.Item))
	if err != nil || s.Edit == nil {
		return b, err
	}
	return s.Edit(b), nil
}

func newStampItem(i int) stampItem {
	return stampItem{Name: "item", Tags: []string{"a", "b"}, Note: strings.Repeat("note ", 40) + string(rune('a'+i%26))}
}

var stampEdits = []struct {
	name string
	edit func([]byte) []byte
}{
	{"same", nil},
	{"letter", func(b []byte) []byte { b[10] = 'X'; return b }},
	{"html", func(b []byte) []byte { b[10] = '<'; return b }},
	{"invalid", func(b []byte) []byte { b[0] = '['; return b }},
	{"control", func(b []byte) []byte { b[10] = '\n'; return b }},
	{"shorter", func(b []byte) []byte { return b[:len(b)-1] }},
	{"inner", func(b []byte) []byte { return b[1 : len(b)-1] }},
	{"spaced", func(b []byte) []byte { return append(b, ' ', '\n') }},
	{"copy", bytes.Clone},
}

// TestStampedOutput checks that method output that Marshal gave, changed
// or not, encodes as with encoding/json, once Marshal stamps the output
// of the type.
func TestStampedOutput(t *testing.T) {
	for _, c := range stampEdits {
		for i := range 300 {
			v := []stampEdit{{newStampItem(i), c.edit, Marshal}}
			got, gerr := Marshal(v)
			v[0].Marshal = stdjson.Marshal
			want, werr := stdjson.Marshal(v)
			if !sameError(gerr, werr) || !bytes.Equal(got, want) {
				t.Fatalf("%s %d:\n got %.300s %s\nwant %.300s %s", c.name, i, got, describe(gerr), want, describe(werr))
			}
			var buf bytes.Buffer
			enc := NewEncoder(&buf)
			enc.SetEscapeHTML(false)
			v[0].Marshal = Marshal
			gerr = enc.Encode(v)
			var wbuf bytes.Buffer
			wenc := stdjson.NewEncoder(&wbuf)
			wenc.SetEscapeHTML(false)
			v[0].Marshal = stdjson.Marshal
			werr = wenc.Encode(v)
			if !sameError(gerr, werr) || !bytes.Equal(buf.Bytes(), wbuf.Bytes()) {
				t.Fatalf("Encode %s %d:\n got %.300s %s\nwant %.300s %s", c.name, i, buf.Bytes(), describe(gerr), wbuf.Bytes(), describe(werr))
			}
		}
	}
	// The output of the alias type is stamped once Marshal has noted some
	// and the encoder has seen them. The race detector makes sync.Pool
	// drop encode states, which note one output in stampNote each.
	item := newStampItem(0)
	var b []byte
	for i := 0; ; i++ {
		out, err := Marshal((*stampAlias)(&item))
		if err != nil {
			t.Fatal(err)
		}
		if isStamped(out) {
			b = out
			break
		}
		if i == 100000 {
			t.Fatal("output not stamped")
		}
		if _, err := Marshal(stampEdit{item, nil, Marshal}); err != nil {
			t.Fatal(err)
		}
	}
	b[10] = 'X'
	if isStamped(b) {
		t.Fatal("changed output stamped")
	}
	b[10] = 't'
	if !isStamped(b) || isStamped(b[:len(b)-1]) || isStamped(bytes.Clone(b)) {
		t.Fatal("stamp lookup")
	}
}

func isStamped(b []byte) bool {
	ok, _ := stamped(b)
	return ok
}

// TestStampedRaw checks that RawMessage values the decoder stores, changed
// or not after the decode, encode as with encoding/json once the decoder
// stamps them.
func TestStampedRaw(t *testing.T) {
	values := []string{
		`{"k":[1,2,{"x":null}],"s":"` + strings.Repeat("plain ", 30) + `"}`,
		`{"k":"<b>&amp;` + strings.Repeat("x", 150) + `"}`,
		"{\"k\":\"\u2028" + strings.Repeat("y", 150) + "\"}",
		`{"k": [1, 2], "s":"` + strings.Repeat("spaced ", 30) + `"}`,
		`[` + strings.Repeat(`"item",`, 40) + `"last"]`,
		`{"long":"` + strings.Repeat("z", 3000) + `"}`,
	}
	edits := []func([]byte){
		nil,
		func(b []byte) { b[len(b)/2] = '<' },
		func(b []byte) { b[len(b)/2] = 'Q' },
		func(b []byte) { b[0] = '!' },
		func(b []byte) { b[len(b)-1] = ' ' },
	}
	type rawStruct struct {
		A RawMessage `json:"a"`
		B RawMessage `json:"b"`
	}
	// Without AVX2 the decoder does not stamp: one round checks the
	// values as they are.
	learned := !jsonsimd.HaveAVX2
	for round := 0; round == 0 || round < 100000 && !learned; round++ {
		for _, v := range values {
			for ei, edit := range edits {
				doc := []byte(`{"a":` + v + `,"b":` + v + `,"pad":"` + strings.Repeat("p", 64) + `"}`)
				var m map[string]RawMessage
				var st rawStruct
				if err := Unmarshal(doc, &m); err != nil {
					t.Fatal(err)
				}
				if err := Unmarshal(doc, &st); err != nil {
					t.Fatal(err)
				}
				if ei == 0 && v == values[0] {
					if ok, raw := stamped(m["a"]); ok && raw {
						learned = true
					}
				}
				if edit != nil {
					edit(m["a"])
					edit(st.B)
				}
				for _, x := range []any{m, st} {
					got, gerr := Marshal(x)
					want, werr := stdjson.Marshal(x)
					if !sameError(gerr, werr) || !bytes.Equal(got, want) {
						t.Fatalf("value %.40s edit %d:\n got %.300s %s\nwant %.300s %s", v, ei, got, describe(gerr), want, describe(werr))
					}
					var buf, wbuf bytes.Buffer
					enc, wenc := NewEncoder(&buf), stdjson.NewEncoder(&wbuf)
					enc.SetEscapeHTML(false)
					wenc.SetEscapeHTML(false)
					gerr, werr = enc.Encode(x), wenc.Encode(x)
					if !sameError(gerr, werr) || !bytes.Equal(buf.Bytes(), wbuf.Bytes()) {
						t.Fatalf("Encode value %.40s edit %d:\n got %.300s %s\nwant %.300s %s", v, ei, buf.Bytes(), describe(gerr), wbuf.Bytes(), describe(werr))
					}
				}
			}
		}
	}
	if !learned {
		t.Fatal("RawMessage values not stamped")
	}
}

// TestStampedConcurrent runs TestStampedOutput's cases on several
// goroutines at once, for the race detector and for stamps that overwrite
// each other.
func TestStampedConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 200 {
				c := stampEdits[(g+i)%len(stampEdits)]
				v := []stampEdit{{newStampItem(i), c.edit, Marshal}}
				got, gerr := Marshal(v)
				v[0].Marshal = stdjson.Marshal
				want, werr := stdjson.Marshal(v)
				if !sameError(gerr, werr) || !bytes.Equal(got, want) {
					t.Errorf("%s %d %d:\n got %.300s %s\nwant %.300s %s", c.name, g, i, got, describe(gerr), want, describe(werr))
					return
				}
			}
		})
	}
	wg.Wait()
}

type stampIn struct {
	Name string             `json:"name"`
	Tags []string           `json:"tags"`
	Note string             `json:"note"`
	Raw  stdjson.RawMessage `json:"raw"`
}

type stampInAlias stampIn

// stampDecode unmarshals its input into an alias of stampIn with
// unmarshal, as methods that unmarshal into an alias of their type do,
// after changing the input with edit. With pre set, the alias starts from
// a value that is not zero.
type stampDecode struct {
	Value stampIn
}

var stampDecodeHow struct {
	edit      func([]byte) []byte
	unmarshal func([]byte, any) error
	pre       bool
}

func (s *stampDecode) UnmarshalJSON(b []byte) error {
	if stampDecodeHow.edit != nil {
		b = stampDecodeHow.edit(b)
	}
	var a stampInAlias
	if stampDecodeHow.pre {
		a.Tags = []string{"x"}
	}
	err := stampDecodeHow.unmarshal(b, &a)
	s.Value = stampIn(a)
	return err
}

// stampRoot is stampDecode with a method of the alias's own: Unmarshal
// passes it the whole input.
type stampRoot struct {
	Value stampIn
}

type stampRootAlias stampDecode

func (s *stampRoot) UnmarshalJSON(b []byte) error {
	var a stampRootAlias
	err := stampDecodeHow.unmarshal(b, (*stampDecode)(&a))
	s.Value = a.Value
	return err
}

func stampInput(i int, long bool) string {
	note := strings.Repeat("note ", 40) + string(rune('a'+i%26))
	if long {
		note = strings.Repeat("n", 4000)
	}
	return `{"name":"item","tags":["a","b"],"note":"` + note + `","raw":{"k":[1,2,{"x":null}]}}`
}

// TestStampedInput checks that UnmarshalJSON methods that pass on their
// input, changed or not, decode as with encoding/json, once the input of
// the method is stamped, for a field and for the root of a nested
// Unmarshal.
func TestStampedInput(t *testing.T) {
	for _, pre := range []bool{false, true} {
		for _, long := range []bool{false, true} {
			for _, c := range stampEdits {
				if c.name == "spaced" {
					// Appending to the input writes over what follows it.
					continue
				}
				stampDecodeHow.edit, stampDecodeHow.pre = c.edit, pre
				for i := range 100 {
					in := stampInput(i, long)
					field, root := `{"a":`+in+`}`, `[`+in+`,`+in+`]`
					var got, want struct {
						A stampDecode `json:"a"`
					}
					var gotRoot, wantRoot []stampRoot
					stampDecodeHow.unmarshal = Unmarshal
					gerr := Unmarshal([]byte(field), &got)
					grerr := Unmarshal([]byte(root), &gotRoot)
					stampDecodeHow.unmarshal = stdjson.Unmarshal
					werr := stdjson.Unmarshal([]byte(field), &want)
					wrerr := stdjson.Unmarshal([]byte(root), &wantRoot)
					if !sameError(gerr, werr) || !reflect.DeepEqual(got, want) {
						t.Fatalf("%s pre %v long %v %d:\n got %+v %s\nwant %+v %s", c.name, pre, long, i, got, describe(gerr), want, describe(werr))
					}
					if !sameError(grerr, wrerr) || !reflect.DeepEqual(gotRoot, wantRoot) {
						t.Fatalf("root %s pre %v long %v %d:\n got %+v %s\nwant %+v %s", c.name, pre, long, i, gotRoot, describe(grerr), wantRoot, describe(wrerr))
					}
				}
			}
		}
	}
	stampDecodeHow.edit, stampDecodeHow.pre = nil, false
	// Input with a long string is not stamped; after some without, the
	// input of the method is.
	in := []byte(stampInput(0, false))
	var v stampDecode
	for i := 0; ; i++ {
		var ok bool
		stampDecodeHow.unmarshal = func(b []byte, v any) error {
			ok = bytes.Equal(b, in) && (&decodeState{}).inputStamped(b)
			return Unmarshal(b, v)
		}
		if err := Unmarshal([]byte(`{"a":`+string(in)+`}`), &struct{ A *stampDecode }{&v}); err != nil {
			t.Fatal(err)
		}
		if ok {
			break
		}
		if i == 100000 {
			t.Fatal("input not stamped")
		}
	}
}
