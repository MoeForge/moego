//go:build go1.26

package json

import (
	stdjson "encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

// readSizes returns the first n bytes of its data, then the next ones, a
// read at a time, and counts the bytes read.
type readSizes struct {
	s     string
	sizes []int
	read  int
}

func (r *readSizes) Read(p []byte) (int, error) {
	if r.read == len(r.s) {
		return 0, io.EOF
	}
	n := min(len(p), len(r.s)-r.read)
	if len(r.sizes) > 0 {
		n, r.sizes = min(n, r.sizes[0]), r.sizes[1:]
	}
	copy(p, r.s[r.read:r.read+n])
	r.read += n
	return n, nil
}

// TestDecoderReadSizes compares Decode with encoding/json, the values, the
// errors, the offsets and how much was read, on readers that give an array
// or object in reads that double the buffered data, in reads that do not,
// and with an error in each of them.
func TestDecoderReadSizes(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; b.Len() < 12<<10; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"name":"item %d","tags":["a","b\n"],"note":"the \"%d\" é","ok":true,"n":-1.5e3}`, i, i, i)
	}
	b.WriteString(`],"last":null}`)
	doc := b.String()
	// The reads that the Decoder asks for double its buffer: 512 bytes,
	// 1536, 3584, 7680. The sizes below cut them short at places before
	// and after a validating pass.
	reads := [][]int{nil, {1}, {1, 1, 1}, {511}, {512}, {512, 1}, {512, 1024}, {512, 1024, 1}, {512, 1024, 2048, 100},
		{512, 1024, 2048, 100, 100, 100}, {300, 300, 300}, {512, 1024, 2048, 4096, 1, 1, 1}, {4096}, {7, 9000}}
	for i := range 40 {
		reads = append(reads, []int{512 + 37*i, 61, 4000 - 97*i})
	}
	inputs := []string{doc, doc + " " + doc, doc + `{"a":1}`, "  \n" + doc + "\n"}
	for _, at := range []int{20, 600, 1100, 1700, 3000, 5000, 9000, len(doc) - 5} {
		inputs = append(inputs, doc[:at]+"\x01"+doc[at:], doc[:at]+`}`+doc[at:], doc[:at])
	}
	for _, in := range inputs {
		for _, sizes := range reads {
			gr, wr := &readSizes{s: in, sizes: sizes}, &readSizes{s: in, sizes: sizes}
			gd, wd := NewDecoder(gr), stdjson.NewDecoder(wr)
			for k := 0; k < 3; k++ {
				var g, w any
				gerr, werr := gd.Decode(&g), wd.Decode(&w)
				if !sameError(gerr, werr) || !reflect.DeepEqual(g, w) || gd.InputOffset() != wd.InputOffset() {
					t.Fatalf("%.40q... (%d bytes), reads %v, value %d: got %v at %d, want %v at %d", in, len(in), sizes, k,
						describe(gerr), gd.InputOffset(), describe(werr), wd.InputOffset())
				}
				// A syntax error is reported once the read that holds it is
				// checked, without reading on.
				if _, ok := werr.(*stdjson.SyntaxError); ok && gr.read != wr.read {
					t.Fatalf("%.40q... (%d bytes), reads %v, value %d: read %d bytes, want %d", in, len(in), sizes, k, gr.read, wr.read)
				}
				if gerr != nil {
					break
				}
			}
		}
	}
}
