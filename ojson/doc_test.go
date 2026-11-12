package ojson

import (
	"bytes"
	"math/rand"
	"slices"
	"testing"
)

// The Doc must give the same documents and errors as the flat edit
// functions called in sequence, and its reads must match Get on its text.

var docValues = []interface{}{1, -2.5, "s", `q"uote`, "é<>&\n", true, false, nil,
	map[string]int{"z": 1}, []byte("b"), []int{1, 2}, float32(0.1), uint8(7), 1e21, int64(-9), ""}

var docRaws = []string{`{"k":[1,2]}`, `7`, `"s"`, `[]`, `{ }`, `x`, ` 1`, `1 `, `[1,`, `""`, `{"a":{"b":1}}`}

var docPaths = []string{"new", "a.new", "a.b.c", "b.-1", "b.", "b.3", "b.:1", ":0", "0", "-1",
	"c.0.d", "model", "x.-1.y", "a.5000", "a.99999999999", "arr.5.k", `a\.b`, "a.-1", "b.0.-1",
	"x.a.1", "0.a", "1", "a.b.0", "", "a*", "a.#", "b|c", "a.:k", "a.01"}

type docOp struct {
	kind  int // 0 set, 1 raw, 2 delete, 3 get
	path  string
	value interface{}
	raw   string
}

func (o docOp) applyDoc(d *Doc) error {
	switch o.kind {
	case 0:
		return d.Set(o.path, o.value)
	case 1:
		return d.SetRaw(o.path, o.raw)
	}
	return d.Delete(o.path)
}

func (o docOp) applyFlat(b []byte) ([]byte, error) {
	switch o.kind {
	case 0:
		return SetBytes(b, o.path, o.value)
	case 1:
		return SetRawBytes(b, o.path, []byte(o.raw))
	}
	return DeleteBytes(b, o.path)
}

func randomOps(r *rand.Rand, paths []string) []docOp {
	ops := make([]docOp, 1+r.Intn(12))
	for i := range ops {
		p := docPaths[r.Intn(len(docPaths))]
		if r.Intn(3) > 0 {
			p = paths[r.Intn(len(paths))]
		}
		ops[i] = docOp{kind: r.Intn(4), path: p, value: docValues[r.Intn(len(docValues))], raw: docRaws[r.Intn(len(docRaws))]}
	}
	return ops
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func checkDoc(t *testing.T, doc string, ops []docOp) {
	t.Helper()
	d := NewDocString(doc)
	flat := []byte(doc)
	for k, o := range ops {
		if o.kind == 3 {
			got, want := d.Get(o.path), Get(string(flat), o.path)
			if !sameResult(got, want) {
				t.Fatalf("doc %q ops %+v: Get(%q) after %d ops = %#v, want %#v", doc, ops, o.path, k, got, want)
			}
			continue
		}
		derr := o.applyDoc(d)
		var ferr error
		flat, ferr = o.applyFlat(flat)
		if errText(derr) != errText(ferr) {
			t.Fatalf("doc %q ops %+v: op %d error %v, want %v", doc, ops, k, derr, ferr)
		}
		if got := d.Bytes(); string(got) != string(flat) || d.Len() != len(flat) {
			t.Fatalf("doc %q ops %+v: after op %d got %q (len %d), want %q", doc, ops, k, got, d.Len(), flat)
		}
	}
	var w bytes.Buffer
	if n, err := d.WriteTo(&w); err != nil || int(n) != len(flat) || w.String() != string(flat) {
		t.Fatalf("doc %q ops %+v: WriteTo = %q, %d, %v, want %q", doc, ops, w.String(), n, err, flat)
	}
}

func TestDocMatchesFlat(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for n := 0; n < randomIterations(30000); n++ {
		doc, paths := randomDoc(r)
		checkDoc(t, doc, randomOps(r, paths))
	}
}

// TestDocMalformed runs the same check on documents cut short or with a
// byte changed, where the Doc must still match the flat functions.
func TestDocMalformed(t *testing.T) {
	r := rand.New(rand.NewSource(8))
	for n := 0; n < randomIterations(20000); n++ {
		doc, paths := randomDoc(r)
		if doc == "" {
			continue
		}
		b := []byte(doc)
		if r.Intn(2) == 0 {
			b = b[:r.Intn(len(b))]
		} else {
			b[r.Intn(len(b))] = "{}[],:\" x"[r.Intn(9)]
		}
		checkDoc(t, string(b), randomOps(r, paths))
	}
}

func TestDocOverrides(t *testing.T) {
	body := `{
  "model": "gpt-4o",
  "stream": true,
  "temperature": 0.7,
  "metadata": {"user_id": "u-42", "trace": {"span": "abc", "tags": ["x", "y"]}},
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."}
  ]
}
`
	ops := []docOp{{path: "temperature", value: 0.2}, {path: "top_p", value: 0.9},
		{path: "metadata.trace.span", value: "override"}, {kind: 2, path: "stream"},
		{path: "messages.0.content", value: "You are terse."}, {path: "stream_options.include_usage", value: true},
		{path: "metadata.k1", value: "v"}, {kind: 3, path: "metadata.k1"}, {kind: 3, path: "messages.0"},
		{kind: 2, path: "metadata.user_id"}, {kind: 2, path: "messages.0"}, {path: "messages.-1", value: 1},
		{kind: 3, path: "stream_options"}}
	for n := 1; n <= len(ops); n++ {
		checkDoc(t, body, ops[:n])
	}
}

// complexSetPathRef reports whether sjson treats path as a query path.
func complexSetPathRef(path string) bool {
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '\\':
			i++
		case '|', '#', '*', '?', '@':
			return true
		}
	}
	return false
}

// setPathKindRef is the kind parseSetPath reports, one component at a
// time.
func setPathKindRef(path string) int {
	if complexSetPathRef(path) {
		return setComplex
	}
	for i := 0; i >= 0; {
		raw, next, _ := nextComp(path, i)
		if raw != "" && raw[0] == ':' {
			raw = raw[1:]
		}
		if lookupComp(raw) {
			return setLookup
		}
		i = next
	}
	return setPlain
}

// parseSetPathRef is parseSetPath one component at a time with nextComp.
func parseSetPathRef(path string) []setComp {
	var comps []setComp
	for i := 0; i >= 0; {
		raw, next, esc := nextComp(path, i)
		var c setComp
		if raw != "" && raw[0] == ':' {
			c.force = true
			raw = raw[1:]
		}
		c.esc, c.look = esc, lookupComp(raw)
		if c.look {
			c.key = raw
		} else {
			c.key = compKey(raw, esc)
		}
		comps = append(comps, c)
		i = next
	}
	return comps
}

func TestSetPathKind(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	alphabet := "ab0.::\\[{!]}|#*?@\""
	var buf [8]setComp
	for n := 0; n < 200000; n++ {
		b := make([]byte, r.Intn(12))
		for i := range b {
			b[i] = alphabet[r.Intn(len(alphabet))]
			if r.Intn(4) > 0 && setClass[b[i]] == pathQuery {
				b[i] = 'k'
			}
		}
		p := string(b)
		comps, kind := parseSetPath(p, buf[:])
		if want := setPathKindRef(p); kind != want {
			t.Fatalf("parseSetPath(%q) kind %d, want %d", p, kind, want)
		}
		if _, err := planSet(`{}`, p, textPiece("1"), nil); p != "" && (err == errQuery) != (kind == setComplex) {
			t.Fatalf("planSet(%q): %v, kind %d", p, err, kind)
		}
		if kind == setComplex {
			continue
		}
		if want := parseSetPathRef(p); !slices.Equal(comps, want) {
			t.Fatalf("parseSetPath(%q) = %+v, want %+v", p, comps, want)
		}
	}
}
