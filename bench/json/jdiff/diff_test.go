// Package jdiff checks moego/json against encoding/json, its oracle, on
// random types, values, documents and streams. Every case is derived from
// one seed, which a failure reports.
package jdiff

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"reflect"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"

	mj "github.com/MoeForge/moego/json"
)

// cases returns the number of seeds a test runs, scaled by JDIFF_SCALE.
func cases(n int) int {
	if s, err := strconv.Atoi(os.Getenv("JDIFF_SCALE")); err == nil && s > 0 {
		n *= s
	}
	if testing.Short() {
		n /= 10
	}
	return n
}

// errDiff describes how two errors differ, or returns "" when they are the
// same: the same type, message and fields, recursively for wrapped errors.
func errDiff(a, b error) string {
	if a == nil || b == nil {
		if a == nil && b == nil {
			return ""
		}
		return "one is nil"
	}
	if reflect.TypeOf(a) != reflect.TypeOf(b) {
		return fmt.Sprintf("types %T and %T", a, b)
	}
	if a.Error() != b.Error() {
		return "messages differ"
	}
	switch x := a.(type) {
	case *json.SyntaxError:
		if y := b.(*json.SyntaxError); x.Offset != y.Offset {
			return fmt.Sprintf("offsets %d and %d", x.Offset, y.Offset)
		}
	case *json.UnmarshalTypeError:
		y := b.(*json.UnmarshalTypeError)
		if x.Value != y.Value || x.Type != y.Type || x.Offset != y.Offset || x.Struct != y.Struct || x.Field != y.Field {
			return fmt.Sprintf("fields %+v and %+v", *x, *y)
		}
	case *json.InvalidUnmarshalError:
		if x.Type != b.(*json.InvalidUnmarshalError).Type {
			return "types differ"
		}
	case *json.UnsupportedTypeError:
		if x.Type != b.(*json.UnsupportedTypeError).Type {
			return "types differ"
		}
	case *json.UnsupportedValueError:
		y := b.(*json.UnsupportedValueError)
		if x.Str != y.Str || x.Value.Kind() != y.Value.Kind() || x.Value.IsValid() && x.Value.Type() != y.Value.Type() {
			return "values differ"
		}
	case *json.MarshalerError:
		y := b.(*json.MarshalerError)
		if x.Type != y.Type {
			return "types differ"
		}
		if d := errDiff(x.Err, y.Err); d != "" {
			return "wrapped: " + d
		}
		return ""
	}
	ua, ub := errors.Unwrap(a), errors.Unwrap(b)
	if ua != nil || ub != nil {
		if d := errDiff(ua, ub); d != "" {
			return "wrapped: " + d
		}
	}
	return ""
}

func describe(err error) string {
	if err == nil {
		return "<nil>"
	}
	s := fmt.Sprintf("%T %q", err, err.Error())
	switch x := err.(type) {
	case *json.SyntaxError:
		s += fmt.Sprintf(" offset %d", x.Offset)
	case *json.UnmarshalTypeError:
		s += fmt.Sprintf(" %+v", *x)
	case *json.MarshalerError:
		s += " wrapping " + describe(x.Err)
	}
	return s
}

// try runs f and returns what it panicked with.
func try(f func()) (p any) {
	defer func() { p = recover() }()
	f()
	return nil
}

// failures stops a test after a few failures so a systematic mismatch does
// not flood the log.
type failures struct {
	t *testing.T
	n int
}

func (f *failures) fail(seed int64, format string, args ...any) bool {
	f.t.Helper()
	f.n++
	f.t.Errorf("seed %d: "+format, append([]any{seed}, args...)...)
	if f.n >= 8 {
		f.t.FailNow()
	}
	return true
}

// where shows the first difference of two outputs when they are long.
func where(got, want []byte) string {
	if len(got)+len(want) < 1200 {
		return ""
	}
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}
	lo := max(i-100, 0)
	return fmt.Sprintf("\nfirst difference at %d:\n got ...%q\nwant ...%q", i, got[lo:min(i+100, len(got))], want[lo:min(i+100, len(want))])
}

func clip(b []byte) string {
	if len(b) > 600 {
		return fmt.Sprintf("%q...(%d bytes)", b[:600], len(b))
	}
	return fmt.Sprintf("%q", b)
}

// marshalCase compares Marshal and MarshalIndent of v.
func marshalCase(v any, prefix, indent string) string {
	var got, want []byte
	var gerr, werr error
	gp := try(func() { got, gerr = mj.Marshal(v) })
	wp := try(func() { want, werr = json.Marshal(v) })
	if gp != nil || wp != nil {
		if fmt.Sprint(gp) != fmt.Sprint(wp) {
			return fmt.Sprintf("Marshal panics: got %v, want %v", gp, wp)
		}
		return ""
	}
	if d := errDiff(gerr, werr); d != "" || !bytes.Equal(got, want) {
		return fmt.Sprintf("Marshal (%s):\n got %s %s\nwant %s %s%s", d, clip(got), describe(gerr), clip(want), describe(werr), where(got, want))
	}
	got, gerr = mj.MarshalIndent(v, prefix, indent)
	want, werr = json.MarshalIndent(v, prefix, indent)
	if d := errDiff(gerr, werr); d != "" || !bytes.Equal(got, want) {
		return fmt.Sprintf("MarshalIndent(%q, %q) (%s):\n got %s %s\nwant %s %s%s", prefix, indent, d, clip(got), describe(gerr), clip(want), describe(werr), where(got, want))
	}
	return ""
}

var indents = []struct{ prefix, indent string }{{"", ""}, {"", "\t"}, {">", "  "}, {" ", ""}, {"\n", "\t\t"}, {"", "x"}}

func TestMarshal(t *testing.T) {
	f := &failures{t: t}
	for seed := int64(0); seed < int64(cases(20000)); seed++ {
		g := newGen(seed)
		typ := g.typ(3)
		v := g.value(typ, 4)
		in := indents[g.n(len(indents))]
		if d := marshalCase(v.Interface(), in.prefix, in.indent); d != "" {
			f.fail(seed, "%v: %s", typ, d)
			continue
		}
		// Through a pointer, values are addressable and pointer methods run.
		p := reflect.New(typ)
		p.Elem().Set(v)
		if d := marshalCase(p.Interface(), in.prefix, in.indent); d != "" {
			f.fail(seed, "*%v: %s", typ, d)
		}
	}
}

// unmarshalCase decodes doc into two targets made by mk, one with each
// package, and compares the targets and the errors.
func unmarshalCase(doc []byte, mk func() reflect.Value) string {
	got, want, nc := mk(), mk(), mk()
	var gerr, werr, nerr error
	gp := try(func() { gerr = mj.Unmarshal(doc, got.Interface()) })
	wp := try(func() { werr = json.Unmarshal(doc, want.Interface()) })
	// UnmarshalNoCopy reads a copy of doc, which its results may view.
	np := try(func() { nerr = mj.UnmarshalNoCopy(bytes.Clone(doc), nc.Interface()) })
	if gp != nil || wp != nil || np != nil {
		if fmt.Sprint(gp) != fmt.Sprint(wp) || fmt.Sprint(np) != fmt.Sprint(wp) {
			return fmt.Sprintf("panics: got %v, UnmarshalNoCopy %v, want %v", gp, np, wp)
		}
		return ""
	}
	if d := errDiff(gerr, werr); d != "" {
		return fmt.Sprintf("errors differ (%s):\n got %s\nwant %s", d, describe(gerr), describe(werr))
	}
	if d := errDiff(nerr, werr); d != "" {
		return fmt.Sprintf("UnmarshalNoCopy errors differ (%s):\n got %s\nwant %s", d, describe(nerr), describe(werr))
	}
	if !reflect.DeepEqual(got.Interface(), want.Interface()) {
		return fmt.Sprintf("values differ (error %s):\n got %#v\nwant %#v", describe(werr), got.Elem().Interface(), want.Elem().Interface())
	}
	if !reflect.DeepEqual(nc.Interface(), want.Interface()) {
		return fmt.Sprintf("UnmarshalNoCopy values differ (error %s):\n got %#v\nwant %#v", describe(werr), nc.Elem().Interface(), want.Elem().Interface())
	}
	return ""
}

// doc returns a document for typ: an encoding of a value of typ, a random
// document, or a mutation of either.
func (g *gen) docFor(typ reflect.Type) []byte {
	var doc []byte
	if g.chance(2) {
		doc = g.docOf(typ, 4)
	} else if g.n(3) != 0 {
		v := g.value(typ, 4)
		doc, _ = json.Marshal(v.Interface())
	}
	if doc == nil {
		doc = g.doc(4)
	}
	if g.chance(3) {
		doc = g.mutate(doc)
	}
	return doc
}

func TestUnmarshal(t *testing.T) {
	f := &failures{t: t}
	for seed := int64(0); seed < int64(cases(20000)); seed++ {
		g := newGen(seed)
		typ := g.typ(3)
		doc := g.docFor(typ)
		mk := func() reflect.Value { return reflect.New(typ) }
		if g.chance(2) {
			// A prefilled target, made twice from one seed.
			fill := g.r.Int63()
			mk = func() reflect.Value {
				p := reflect.New(typ)
				fg := newGen(fill)
				fg.finite = true
				fg.fill(p.Elem(), 3)
				return p
			}
		}
		if d := unmarshalCase(doc, mk); d != "" {
			f.fail(seed, "%v from %s: %s", typ, clip(doc), d)
		}
	}
}

// TestUnmarshalAny decodes random documents into the generic types.
func TestUnmarshalAny(t *testing.T) {
	f := &failures{t: t}
	types := []reflect.Type{
		anyType, reflect.TypeFor[map[string]any](), reflect.TypeFor[[]any](), reflect.TypeFor[string](),
		reflect.TypeFor[float64](), reflect.TypeFor[json.RawMessage](), reflect.TypeFor[map[string]json.RawMessage](),
		reflect.TypeFor[json.Number](), reflect.TypeFor[*any](),
	}
	for seed := int64(0); seed < int64(cases(20000)); seed++ {
		g := newGen(seed)
		doc := g.doc(5)
		if g.chance(2) {
			doc = g.mutate(doc)
		}
		for _, typ := range types {
			if d := unmarshalCase(doc, func() reflect.Value { return reflect.New(typ) }); d != "" {
				f.fail(seed, "%v from %s: %s", typ, clip(doc), d)
			}
		}
	}
}

func TestUnmarshalInvalidTargets(t *testing.T) {
	var x int
	var nilMap map[string]int
	for _, v := range []any{nil, x, (*int)(nil), nilMap, &nilMap, new(any), []int{}, struct{}{}} {
		for _, doc := range []string{`1`, `{"a":1}`, `x`, ``} {
			if d := errDiff(mj.Unmarshal([]byte(doc), v), json.Unmarshal([]byte(doc), v)); d != "" {
				t.Errorf("Unmarshal(%q) into %T: %s", doc, v, d)
			}
		}
	}
}

// TestUnmarshalIfaceTargets decodes into interfaces that already hold
// values, which decoding follows when they are non-nil pointers.
func TestUnmarshalIfaceTargets(t *testing.T) {
	mks := []func() any{
		func() any { var a any = new(int); return &a },
		func() any { p := new(int); var a any = &p; return &a },
		func() any { var a any; a = &a; return &a },
		func() any { var a any = &Node{V: 7}; return &a },
		func() any { var a any = Node{V: 7}; return &a },
		func() any { var a any = (*int)(nil); return &a },
		func() any { var a any = &[]int{9, 9, 9}; return &a },
		func() any { var a any = &map[string]int{"z": 1}; return &a },
		func() any { var a any = new(any); return &a },
		func() any { var a any = &PMarsh{N: 1}; return &a },
		func() any { var a any = new(PText); return &a },
		func() any { var s Stringer = StrVal("x"); return &s },
		func() any { var m json.Marshaler = &PMarsh{N: 2}; return &m },
		func() any { var m json.Marshaler = VMarsh(3); return &m },
		func() any { return &Ifaces{Any: new(int), Str: StrVal("s"), M: &PMarsh{}, PAny: new(any)} },
		func() any { p := new(any); *p = new(string); return &p },
	}
	docs := []string{`null`, `1`, `"x"`, `{"v":1}`, `[1,2]`, `{"a":1}`, `true`, `{"Any":1,"Str":"q","M":{"N":5},"T":"a|b","PAny":[1]}`, `{"Any":null,"PAny":null}`}
	for i, mk := range mks {
		if i == 2 && jsonv2 {
			// The interface that points to itself: encoding/json built on
			// encoding/json/v2 follows it without end and overflows the
			// stack, so it is compared only with the classic encoding/json.
			continue
		}
		for _, doc := range docs {
			if d := unmarshalCase([]byte(doc), func() reflect.Value { return reflect.ValueOf(mk()) }); d != "" {
				t.Errorf("target %d from %s: %s", i, doc, d)
			}
		}
	}
}

// Stream tests. A reader kind and a seed give identical readers for both
// decoders.

var errRead = errors.New("read failed")

type chunkReader struct {
	r    *rand.Rand
	data []byte
	err  error // returned after data, io.EOF when nil
	eofs bool  // io.EOF also between the data, and errRead after it
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.eofs && c.r.Intn(5) == 0 {
		return 0, io.EOF
	}
	if len(c.data) == 0 {
		if c.eofs && c.r.Intn(3) == 0 {
			return 0, errRead
		}
		if c.err != nil {
			return 0, c.err
		}
		return 0, io.EOF
	}
	n := 1 + c.r.Intn(min(len(c.data), 1+c.r.Intn(700)))
	if c.r.Intn(9) == 0 {
		n = 0
	}
	n = copy(p, c.data[:n])
	c.data = c.data[n:]
	if len(c.data) == 0 && c.err == nil && c.r.Intn(2) == 0 {
		return n, io.EOF
	}
	return n, nil
}

func newReader(kind int, data []byte, seed int64) io.Reader {
	switch kind {
	case 0:
		return bytes.NewReader(data)
	case 1:
		return iotest.OneByteReader(bytes.NewReader(data))
	case 2:
		return iotest.HalfReader(bytes.NewReader(data))
	case 3:
		return iotest.DataErrReader(bytes.NewReader(data))
	case 4:
		return iotest.TimeoutReader(bytes.NewReader(data))
	case 5:
		return &chunkReader{r: rand.New(rand.NewSource(seed)), data: data, err: errRead}
	case 7:
		return &chunkReader{r: rand.New(rand.NewSource(seed)), data: data, eofs: true}
	}
	return &chunkReader{r: rand.New(rand.NewSource(seed)), data: data}
}

// stream returns a sequence of documents with or without space between.
func (g *gen) stream() []byte {
	var b []byte
	for n := g.n(6); n >= 0; n-- {
		if g.chance(2) {
			b = append(b, g.docOf(g.typ(2), 3)...)
		} else {
			b = g.appendDoc(b, 4)
		}
		if g.chance(2) {
			b = append(b, '\n')
		}
	}
	if g.chance(6) {
		b = append(b, strings.Repeat(" ", 500+g.n(2000))...)
	}
	if g.chance(4) {
		b = g.mutate(b)
	}
	return b
}

type streamOp struct {
	kind int
	typ  reflect.Type
}

func (g *gen) streamOps() []streamOp {
	ops := make([]streamOp, 4+g.n(40))
	types := []reflect.Type{anyType, reflect.TypeFor[map[string]any](), reflect.TypeFor[[]any](), reflect.TypeFor[json.RawMessage](), reflect.TypeFor[string](), reflect.TypeFor[int](), reflect.TypeFor[json.Number]()}
	for i := range ops {
		ops[i].kind = g.n(10)
		if g.chance(3) {
			ops[i].typ = g.typ(2)
		} else {
			ops[i].typ = types[g.n(len(types))]
		}
	}
	return ops
}

// streamCase runs the ops on decoders of both packages over identical
// readers and compares every result.
func streamCase(data []byte, kind int, rseed int64, ops []streamOp, useNumber, strict bool) string {
	gd := mj.NewDecoder(newReader(kind, data, rseed))
	wd := json.NewDecoder(newReader(kind, data, rseed))
	if useNumber {
		gd.UseNumber()
		wd.UseNumber()
	}
	if strict {
		gd.DisallowUnknownFields()
		wd.DisallowUnknownFields()
	}
	eofs := 0
	for i, op := range ops {
		var what string
		var gv, wv any
		var gerr, werr error
		var gp, wp any
		switch {
		case op.kind < 4:
			what = "Decode " + op.typ.String()
			g, w := reflect.New(op.typ), reflect.New(op.typ)
			gp = try(func() { gerr = gd.Decode(g.Interface()) })
			wp = try(func() { werr = wd.Decode(w.Interface()) })
			gv, wv = g.Interface(), w.Interface()
		case op.kind < 8:
			what = "Token"
			gp = try(func() { gv, gerr = gd.Token() })
			wp = try(func() { wv, werr = wd.Token() })
		case op.kind == 8:
			what = "More"
			gv, wv = gd.More(), wd.More()
		default:
			what = "Buffered"
			gb, _ := io.ReadAll(gd.Buffered())
			wb, _ := io.ReadAll(wd.Buffered())
			gv, wv = string(gb), string(wb)
		}
		if gp != nil || wp != nil {
			if fmt.Sprint(gp) != fmt.Sprint(wp) {
				return fmt.Sprintf("op %d %s: panics: got %v, want %v", i, what, gp, wp)
			}
			return ""
		}
		if d := errDiff(gerr, werr); d != "" {
			return fmt.Sprintf("op %d %s: errors differ (%s):\n got %s\nwant %s", i, what, d, describe(gerr), describe(werr))
		}
		if !reflect.DeepEqual(gv, wv) {
			return fmt.Sprintf("op %d %s: values differ (error %s):\n got %#v\nwant %#v", i, what, describe(werr), gv, wv)
		}
		if g, w := gd.InputOffset(), wd.InputOffset(); g != w {
			return fmt.Sprintf("op %d %s: InputOffset %d, want %d", i, what, g, w)
		}
		if werr != nil {
			if eofs++; eofs > 3 {
				break
			}
		}
	}
	return ""
}

func TestDecoder(t *testing.T) {
	f := &failures{t: t}
	for seed := int64(0); seed < int64(cases(20000)); seed++ {
		g := newGen(seed)
		data := g.stream()
		kind := g.n(7)
		if seed%8 == 7 {
			kind = 7
		}
		ops := g.streamOps()
		if d := streamCase(data, kind, g.r.Int63(), ops, g.chance(3), g.chance(4)); d != "" {
			f.fail(seed, "reader %d over %s: %s", kind, clip(data), d)
		}
	}
}

// TestDecoderLarge reads values larger than the decoder buffer.
func TestDecoderLarge(t *testing.T) {
	f := &failures{t: t}
	for seed := int64(0); seed < int64(cases(300)); seed++ {
		g := newGen(seed)
		var b []byte
		for n := 1 + g.n(3); n > 0; n-- {
			b = append(b, '[')
			for i, m := 0, 200+g.n(3000); i < m; i++ {
				if i > 0 {
					b = append(b, ',')
				}
				b = g.appendDoc(b, 2)
			}
			b = append(b, "]\n"...)
		}
		if g.chance(3) {
			b = g.mutate(b)
		}
		ops := g.streamOps()
		for i := range ops {
			if ops[i].kind == 9 {
				ops[i].kind = 0
			}
		}
		if d := streamCase(b, g.n(7), g.r.Int63(), ops, g.chance(3), false); d != "" {
			f.fail(seed, "large stream of %d bytes: %s", len(b), d)
		}
	}
}

// writes records each Write call, and fails the failAt-th.
type writes struct {
	calls  [][]byte
	failAt int
	short  bool
}

var errWrite = errors.New("write failed")

func (w *writes) Write(p []byte) (int, error) {
	w.calls = append(w.calls, append([]byte(nil), p...))
	if len(w.calls) == w.failAt {
		if w.short {
			return len(p) / 2, errWrite
		}
		return 0, errWrite
	}
	return len(p), nil
}

func TestEncoder(t *testing.T) {
	f := &failures{t: t}
	for seed := int64(0); seed < int64(cases(10000)); seed++ {
		g := newGen(seed)
		failAt := 0
		if g.chance(4) {
			failAt = 1 + g.n(4)
		}
		gw := &writes{failAt: failAt, short: g.chance(2)}
		ww := &writes{failAt: gw.failAt, short: gw.short}
		ge, we := mj.NewEncoder(gw), json.NewEncoder(ww)
		for n := 1 + g.n(6); n > 0; n-- {
			if g.chance(3) {
				on := g.chance(2)
				ge.SetEscapeHTML(on)
				we.SetEscapeHTML(on)
			}
			if g.chance(3) {
				in := indents[g.n(len(indents))]
				ge.SetIndent(in.prefix, in.indent)
				we.SetIndent(in.prefix, in.indent)
			}
			typ := g.typ(3)
			v := g.value(typ, 3).Interface()
			var gerr, werr error
			gp := try(func() { gerr = ge.Encode(v) })
			wp := try(func() { werr = we.Encode(v) })
			if gp != nil || wp != nil {
				if fmt.Sprint(gp) != fmt.Sprint(wp) {
					f.fail(seed, "Encode %v: panics: got %v, want %v", typ, gp, wp)
				}
				break
			}
			if d := errDiff(gerr, werr); d != "" {
				f.fail(seed, "Encode %v: errors differ (%s):\n got %s\nwant %s", typ, d, describe(gerr), describe(werr))
				break
			}
			if !reflect.DeepEqual(gw.calls, ww.calls) {
				gb, wb := bytes.Join(gw.calls, []byte("|")), bytes.Join(ww.calls, []byte("|"))
				f.fail(seed, "Encode %v: writes differ:\n got %s\nwant %s%s", typ, clip(gb), clip(wb), where(gb, wb))
				break
			}
		}
	}
}

// formatCase compares Valid, Compact, Indent and HTMLEscape on doc, each
// appending to a buffer that already holds data.
func formatCase(doc []byte, prefix, indent string) string {
	if g, w := mj.Valid(doc), json.Valid(doc); g != w {
		return fmt.Sprintf("Valid = %v, want %v", g, w)
	}
	pre := []byte("pre\x00")
	var gb, wb bytes.Buffer
	gb.Write(pre)
	wb.Write(pre)
	gerr, werr := mj.Compact(&gb, doc), json.Compact(&wb, doc)
	if d := errDiff(gerr, werr); d != "" || !bytes.Equal(gb.Bytes(), wb.Bytes()) {
		return fmt.Sprintf("Compact (%s):\n got %s %s\nwant %s %s%s", d, clip(gb.Bytes()), describe(gerr), clip(wb.Bytes()), describe(werr), where(gb.Bytes(), wb.Bytes()))
	}
	gb.Reset()
	wb.Reset()
	gb.Write(pre)
	wb.Write(pre)
	gerr, werr = mj.Indent(&gb, doc, prefix, indent), json.Indent(&wb, doc, prefix, indent)
	if d := errDiff(gerr, werr); d != "" || !bytes.Equal(gb.Bytes(), wb.Bytes()) {
		return fmt.Sprintf("Indent(%q, %q) (%s):\n got %s %s\nwant %s %s%s", prefix, indent, d, clip(gb.Bytes()), describe(gerr), clip(wb.Bytes()), describe(werr), where(gb.Bytes(), wb.Bytes()))
	}
	gb.Reset()
	wb.Reset()
	gb.Write(pre)
	wb.Write(pre)
	mj.HTMLEscape(&gb, doc)
	json.HTMLEscape(&wb, doc)
	if !bytes.Equal(gb.Bytes(), wb.Bytes()) {
		return fmt.Sprintf("HTMLEscape:\n got %s\nwant %s%s", clip(gb.Bytes()), clip(wb.Bytes()), where(gb.Bytes(), wb.Bytes()))
	}
	return ""
}

func TestFormat(t *testing.T) {
	f := &failures{t: t}
	for seed := int64(0); seed < int64(cases(30000)); seed++ {
		g := newGen(seed)
		doc := g.doc(5)
		if g.chance(2) {
			doc = g.mutate(doc)
		}
		in := indents[g.n(len(indents))]
		if d := formatCase(doc, in.prefix, in.indent); d != "" {
			f.fail(seed, "%s: %s", clip(doc), d)
		}
	}
}

func TestDeep(t *testing.T) {
	// Indented output grows with the square of the depth.
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	for _, n := range []int{9999, 10000, 10001, 20000} {
		for _, open := range []string{"[", `{"a":`} {
			var b []byte
			for i := 0; i < n; i++ {
				b = append(b, open...)
			}
			for i := 0; i < n; i++ {
				if open == "[" {
					b = append(b, ']')
				} else {
					b = append(b, '}')
				}
			}
			if d := formatCase(b, "", " "); d != "" {
				t.Errorf("depth %d %s: %s", n, open, d)
			}
			if d := unmarshalCase(b, func() reflect.Value { return reflect.New(anyType) }); d != "" {
				t.Errorf("depth %d %s: Unmarshal: %s", n, open, d)
			}
			if d := streamCase(b, 6, int64(n), []streamOp{{kind: 0, typ: anyType}, {kind: 4}}, false, false); d != "" {
				t.Errorf("depth %d %s: Decoder: %s", n, open, d)
			}
		}
	}
}

// Fuzz targets. Their inputs are documents; the seed selects the types,
// readers and options.

var fuzzTypes = []reflect.Type{
	anyType, reflect.TypeFor[map[string]any](), reflect.TypeFor[Outer](), reflect.TypeFor[Tagged](),
	reflect.TypeFor[Node](), reflect.TypeFor[[]VText](), reflect.TypeFor[map[KeyInt]int](), reflect.TypeFor[Ifaces](),
	reflect.TypeFor[[]PMarsh](), reflect.TypeFor[[3]PText](), reflect.TypeFor[map[string]*Capture](), reflect.TypeFor[Both](),
	reflect.TypeFor[PtrUnexp](), reflect.TypeFor[[]json.Number](), reflect.TypeFor[map[VText]Base](), reflect.TypeFor[[]byte](),
	reflect.TypeFor[Quoted](), reflect.TypeFor[*Quoted](), reflect.TypeFor[[]Inner](), reflect.TypeFor[*any](),
}

func addSeeds(f *testing.F, extra func(g *gen) []any) {
	for seed := int64(0); seed < 64; seed++ {
		g := newGen(seed)
		args := []any{g.doc(4)}
		f.Add(append(args, extra(g)...)...)
	}
	for _, doc := range []string{`{"id":1,"name":"x","Dup":2,"own":"o","q":"5","qf":"1.5","qb":"true","qs":"\"s\"","qp":"7"}`, `{"v":1,"next":{"v":2},"kids":[{"v":3}],"m":{"a":null}}`, `["a|b","x"]`, `{"k1":1,"k2":2}`} {
		f.Add(append([]any{[]byte(doc)}, extra(newGen(1))...)...)
	}
}

func FuzzUnmarshal(f *testing.F) {
	addSeeds(f, func(g *gen) []any { return []any{uint8(g.n(256))} })
	f.Fuzz(func(t *testing.T, doc []byte, sel uint8) {
		typ := fuzzTypes[int(sel)%len(fuzzTypes)]
		if d := unmarshalCase(doc, func() reflect.Value { return reflect.New(typ) }); d != "" {
			t.Fatalf("%v from %s: %s", typ, clip(doc), d)
		}
	})
}

// FuzzDecoder reads with the reader of a negative seed, which no generated
// one is, io.EOF between the data.
func FuzzDecoder(f *testing.F) {
	addSeeds(f, func(g *gen) []any { return []any{g.r.Int63()} })
	addSeeds(f, func(g *gen) []any { return []any{-1 - g.r.Int63()} })
	f.Fuzz(func(t *testing.T, data []byte, seed int64) {
		g := newGen(seed)
		kind := g.n(7)
		if seed < 0 {
			kind = 7
		}
		if d := streamCase(data, kind, seed, g.streamOps(), g.chance(3), g.chance(4)); d != "" {
			t.Fatalf("%s: %s", clip(data), d)
		}
	})
}

func FuzzFormat(f *testing.F) {
	addSeeds(f, func(g *gen) []any { return []any{uint8(g.n(256))} })
	f.Fuzz(func(t *testing.T, doc []byte, sel uint8) {
		in := indents[int(sel)%len(indents)]
		if d := formatCase(doc, in.prefix, in.indent); d != "" {
			t.Fatalf("%s: %s", clip(doc), d)
		}
	})
}

// FuzzMarshal decodes the input into a type with encoding/json and
// compares the encodings of the result.
func FuzzMarshal(f *testing.F) {
	addSeeds(f, func(g *gen) []any { return []any{uint8(g.n(256))} })
	f.Fuzz(func(t *testing.T, doc []byte, sel uint8) {
		typ := fuzzTypes[int(sel)%len(fuzzTypes)]
		p := reflect.New(typ)
		json.Unmarshal(doc, p.Interface())
		in := indents[int(sel>>4)%len(indents)]
		if d := marshalCase(p.Interface(), in.prefix, in.indent); d != "" {
			t.Fatalf("%v from %s: %s", typ, clip(doc), d)
		}
		if d := marshalCase(p.Elem().Interface(), in.prefix, in.indent); d != "" {
			t.Fatalf("%v from %s: %s", typ, clip(doc), d)
		}
	})
}
