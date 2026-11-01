package ojson

import (
	"math/rand"
	"testing"
	"unsafe"
)

// SetBytesOptions and SetRawBytesOptions with ReplaceInPlace must give the
// result they give without it, in the input's memory when it has room.

func sameMemory(a, b []byte) bool {
	return cap(a) > 0 && cap(b) > 0 && unsafe.SliceData(a) == unsafe.SliceData(b)
}

func TestReplaceInPlace(t *testing.T) {
	r := rand.New(rand.NewSource(9))
	opts := &Options{Optimistic: true, ReplaceInPlace: true}
	copyOpts := &Options{Optimistic: true}
	for n := 0; n < 40000; n++ {
		doc, paths := randomDoc(r)
		if n%4 == 3 && doc != "" {
			b := []byte(doc)
			b[r.Intn(len(b))] = "{}[],:\" x"[r.Intn(9)]
			doc = string(b)
		}
		p := docPaths[r.Intn(len(docPaths))]
		if r.Intn(3) > 0 {
			p = paths[r.Intn(len(paths))]
		}
		in := make([]byte, len(doc), len(doc)+r.Intn(3)*r.Intn(80))
		copy(in, doc)
		var want, got []byte
		var werr, err error
		fromInput := false
		switch k := r.Intn(3); {
		case k == 0:
			v := docValues[r.Intn(len(docValues))]
			want, werr = SetBytesOptions([]byte(doc), p, v, copyOpts)
			got, err = SetBytesOptions(in, p, v, opts)
		case k == 1 || len(doc) < 2:
			raw := docRaws[r.Intn(len(docRaws))]
			want, werr = SetRawBytesOptions([]byte(doc), p, []byte(raw), copyOpts)
			got, err = SetRawBytesOptions(in, p, []byte(raw), opts)
		default:
			// A raw value cut from the input itself.
			i := r.Intn(len(doc))
			j := i + r.Intn(len(doc)-i+1)
			fromInput = j > i
			want, werr = SetRawBytesOptions([]byte(doc), p, []byte(doc[i:j]), copyOpts)
			got, err = SetRawBytesOptions(in, p, in[i:j], opts)
		}
		if n%4 == 3 {
			continue // malformed input: only no panic
		}
		if string(got) != string(want) || errText(err) != errText(werr) || (got == nil) != (want == nil) {
			t.Fatalf("doc %q path %q: in place %q, %v; want %q, %v", doc, p, got, err, want, werr)
		}
		if err == nil && !fromInput && len(want) > 0 && len(want) <= cap(in) && !sameMemory(got, in) {
			t.Fatalf("doc %q path %q: result %q fits in cap %d but is not in place", doc, p, got, cap(in))
		}
	}
}

func TestReplaceInPlaceUsesInput(t *testing.T) {
	opts := &Options{Optimistic: true, ReplaceInPlace: true}
	for _, c := range []struct{ doc, path string }{
		{`{"temperature":0.7,"x":1}`, "temperature"},
		{`{"a":"long value here","b":2}`, "a"},
		{`{"a":1}`, "b"},
		{` {"a":1} `, "b"},
		{`[1,2,3]`, "1"},
		{`[1,2,3]`, "-1"},
		{`{"a":{"b":[1]}}`, "a.b.0"},
	} {
		in := make([]byte, len(c.doc), len(c.doc)+32)
		copy(in, c.doc)
		want, _ := SetBytes([]byte(c.doc), c.path, 0.2)
		got, err := SetBytesOptions(in, c.path, 0.2, opts)
		if err != nil || string(got) != string(want) || !sameMemory(got, in) {
			t.Errorf("%q %q: got %q, %v, in place %v; want %q in place", c.doc, c.path, got, err, sameMemory(got, in), want)
		}
	}
	// Without Optimistic, or without room, a new slice.
	in := []byte(`{"a":1}`)
	if got, _ := SetBytesOptions(in, "b", 2, &Options{ReplaceInPlace: true}); sameMemory(got, in) {
		t.Errorf("ReplaceInPlace without Optimistic wrote into the input")
	}
	if got, _ := SetBytesOptions(in[:len(in):len(in)], "b", 2, opts); sameMemory(got, in) || string(got) != `{"a":1,"b":2}` {
		t.Errorf("no room: got %q", got)
	}
}
