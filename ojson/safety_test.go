package ojson

import (
	"strings"
	"testing"
)

// Inputs that crash the reference libraries get an error or a plain answer.

// TestHugeIndex checks that an index far past the end of an array is an
// error, where sjson pads with nulls until memory runs out.
func TestHugeIndex(t *testing.T) {
	for _, c := range []struct{ json, path string }{
		{`{"a":[]}`, "a.99999999999"},
		{`{"a":[1]}`, "a.16777218"},
		{`{}`, "x.99999999999"},
		{`{}`, "x.y.99999999999.z"},
		{`[]`, "99999999999"},
	} {
		out, err := SetBytes([]byte(c.json), c.path, 1)
		if err != errHugeIndex || string(out) != c.json {
			t.Errorf("SetBytes(%q, %q) = %q, %v, want the input and %v", c.json, c.path, out, err, errHugeIndex)
		}
		d := NewDocString(c.json)
		d.Set("b", 2) // the second edit goes to the tree
		want := d.String()
		if err := d.Set(c.path, 1); err != errHugeIndex || d.String() != want {
			t.Errorf("Doc.Set(%q) on %q = %v, %q, want %v, %q", c.path, c.json, err, d.String(), errHugeIndex, want)
		}
	}
	if out, err := SetBytes([]byte(`{"a":[1]}`), "a.3", true); err != nil || string(out) != `{"a":[1,null,null,true]}` {
		t.Errorf("SetBytes a.3 = %q, %v", out, err)
	}
}

// TestValidDeep checks that Valid answers for nesting deeper than a
// recursive validator's stack allows.
func TestValidDeep(t *testing.T) {
	const n = 1 << 20
	for _, c := range []struct {
		json string
		want bool
	}{
		{strings.Repeat("[", n) + strings.Repeat("]", n), true},
		{strings.Repeat("[", n) + strings.Repeat("]", n-1), false},
		{strings.Repeat(`{"a":`, n) + "1" + strings.Repeat("}", n), true},
		{strings.Repeat(`{"a":[`, n/2) + "1" + strings.Repeat("]}", n/2), true},
		{strings.Repeat(`{"a":[`, n/2) + "1" + strings.Repeat("}]", n/2), false},
	} {
		if got := Valid(c.json); got != c.want {
			t.Errorf("Valid(%d bytes starting %q) = %v, want %v", len(c.json), c.json[:12], got, c.want)
		}
	}
}
