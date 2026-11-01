package diff

import (
	"fmt"
	"iter"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/MoeForge/moego/ojson"
)

// kv is one named observation of a call: a Result field, a method output or
// an error text.
type kv struct{ k, v string }

// mismatch is one differing observation of one operation on one case.
type mismatch struct {
	Op    string `json:"op"`
	Field string `json:"field"`
	Got   string `json:"got"`
	Want  string `json:"want"`
}

func idxs(v []int) string {
	if v == nil {
		return "nil"
	}
	return fmt.Sprint(v)
}

// gcore and ocore list every Result field; Indexes tells nil from empty.
func gcore(p string, r gjson.Result) []kv {
	return []kv{
		{p + "Type", fmt.Sprint(int(r.Type))}, {p + "Raw", fmt.Sprintf("%q", r.Raw)},
		{p + "Str", fmt.Sprintf("%q", r.Str)}, {p + "Num", fmt.Sprintf("%x", math.Float64bits(r.Num))},
		{p + "Index", fmt.Sprint(r.Index)}, {p + "Indexes", idxs(r.Indexes)},
	}
}

func ocore(p string, r ojson.Result) []kv {
	return []kv{
		{p + "Type", fmt.Sprint(int(r.Type))}, {p + "Raw", fmt.Sprintf("%q", r.Raw)},
		{p + "Str", fmt.Sprintf("%q", r.Str)}, {p + "Num", fmt.Sprintf("%x", math.Float64bits(r.Num))},
		{p + "Index", fmt.Sprint(r.Index)}, {p + "Indexes", idxs(r.Indexes)},
	}
}

func gdesc(r gjson.Result) string {
	return fmt.Sprintf("T=%d Raw=%q Str=%q Num=%x Idx=%d Idxs=%s", r.Type, r.Raw, r.Str, math.Float64bits(r.Num), r.Index, idxs(r.Indexes))
}

func odesc(r ojson.Result) string {
	return fmt.Sprintf("T=%d Raw=%q Str=%q Num=%x Idx=%d Idxs=%s", r.Type, r.Raw, r.Str, math.Float64bits(r.Num), r.Index, idxs(r.Indexes))
}

// maxSeq bounds the elements described per sequence method.
const maxSeq = 64

func timeText(t time.Time) string { return t.Format(time.RFC3339Nano) + " " + t.Location().String() }

func valueText(v any) string { return fmt.Sprintf("%#v", v) }

// lessTokens are the right sides of the Less comparisons.
var lessTokens = []string{`1`, `-2.5`, `"a"`, `"B"`, `""`, `true`, `false`, `null`, `{}`, `[1]`}

// gmethods lists the outputs of every gjson Result method on r.
func gmethods(r gjson.Result, doc string) []kv {
	out := []kv{
		{"m.String", fmt.Sprintf("%q", r.String())}, {"m.Bool", fmt.Sprint(r.Bool())},
		{"m.Int", fmt.Sprint(r.Int())}, {"m.Uint", fmt.Sprint(r.Uint())},
		{"m.Float", fmt.Sprintf("%x", math.Float64bits(r.Float()))}, {"m.Exists", fmt.Sprint(r.Exists())},
		{"m.IsArray", fmt.Sprint(r.IsArray())}, {"m.IsObject", fmt.Sprint(r.IsObject())},
		{"m.IsBool", fmt.Sprint(r.IsBool())}, {"m.Value", valueText(r.Value())},
		{"m.Time", timeText(r.Time())},
	}
	if gjson.Valid(doc) {
		out = append(out, kv{"m.Path", fmt.Sprintf("%q", r.Path(doc))}, kv{"m.Paths", fmt.Sprintf("%#v", r.Paths(doc))})
	} else {
		out = append(out, kv{"m.Path", multiValue}, kv{"m.Paths", multiValue})
	}
	var b strings.Builder
	for i, e := range r.Array() {
		if i == maxSeq {
			break
		}
		b.WriteString(gdesc(e) + "; ")
	}
	out = append(out, kv{"m.Array", b.String()})
	b.Reset()
	n := 0
	r.ForEach(func(k, v gjson.Result) bool {
		b.WriteString(gdesc(k) + " => " + gdesc(v) + "; ")
		n++
		return n < maxSeq
	})
	out = append(out, kv{"m.ForEach", b.String()})
	m := r.Map()
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b.Reset()
	for _, k := range keys {
		b.WriteString(fmt.Sprintf("%q: %s; ", k, gdesc(m[k])))
	}
	out = append(out, kv{"m.Map", b.String()})
	b.Reset()
	for _, tok := range lessTokens {
		o := gjson.Parse(tok)
		fmt.Fprintf(&b, "%v%v%v%v ", r.Less(o, true), r.Less(o, false), o.Less(r, true), o.Less(r, false))
	}
	out = append(out, kv{"m.Less", b.String()})
	out = append(out, kv{"m.All", seq2Text(r.All(), gdesc)}, kv{"m.Keys", seqText(r.Keys(), gdesc)}, kv{"m.Values", seqText(r.Values(), gdesc)})
	return out
}

func seqText[T any](s iter.Seq[T], d func(T) string) string {
	var b strings.Builder
	n := 0
	for v := range s {
		b.WriteString(d(v) + "; ")
		if n++; n == maxSeq {
			break
		}
	}
	return b.String()
}

func seq2Text[T any](s iter.Seq2[T, T], d func(T) string) string {
	var b strings.Builder
	n := 0
	for k, v := range s {
		b.WriteString(d(k) + " => " + d(v) + "; ")
		if n++; n == maxSeq {
			break
		}
	}
	return b.String()
}

// The methods below are looked up at run time, so the harness builds before
// ojson has them; a missing method reports as a mismatch.
type (
	oPath  interface{ Path(string) string }
	oPaths interface{ Paths(string) []string }
	oTime  interface{ Time() time.Time }
	oLess  interface{ Less(ojson.Result, bool) bool }
	oAll   interface {
		All() iter.Seq2[ojson.Result, ojson.Result]
	}
	oKeys   interface{ Keys() iter.Seq[ojson.Result] }
	oValues interface{ Values() iter.Seq[ojson.Result] }
)

const missing = "<method missing in ojson>"

// multiValue stands for Path and Paths on a document that is not one
// valid JSON value (JSON Lines): malformed input there,
// so only crashes are checked.
const multiValue = "<not compared: multi-value document>"

// omethods lists the outputs of the same methods on an ojson Result.
func omethods(r ojson.Result, doc string) []kv {
	out := []kv{
		{"m.String", fmt.Sprintf("%q", r.String())}, {"m.Bool", fmt.Sprint(r.Bool())},
		{"m.Int", fmt.Sprint(r.Int())}, {"m.Uint", fmt.Sprint(r.Uint())},
		{"m.Float", fmt.Sprintf("%x", math.Float64bits(r.Float()))}, {"m.Exists", fmt.Sprint(r.Exists())},
		{"m.IsArray", fmt.Sprint(r.IsArray())}, {"m.IsObject", fmt.Sprint(r.IsObject())},
		{"m.IsBool", fmt.Sprint(r.IsBool())}, {"m.Value", valueText(r.Value())},
	}
	var x any = r
	if m, ok := x.(oTime); ok {
		out = append(out, kv{"m.Time", timeText(m.Time())})
	} else {
		out = append(out, kv{"m.Time", missing})
	}
	if m, ok := x.(oPath); ok {
		out = append(out, kv{"m.Path", fmt.Sprintf("%q", m.Path(doc))})
	} else {
		out = append(out, kv{"m.Path", missing})
	}
	if m, ok := x.(oPaths); ok {
		out = append(out, kv{"m.Paths", fmt.Sprintf("%#v", m.Paths(doc))})
	} else {
		out = append(out, kv{"m.Paths", missing})
	}
	if !gjson.Valid(doc) {
		// Called above for crashes only.
		out[len(out)-2].v, out[len(out)-1].v = multiValue, multiValue
	}
	var b strings.Builder
	for i, e := range r.Array() {
		if i == maxSeq {
			break
		}
		b.WriteString(odesc(e) + "; ")
	}
	out = append(out, kv{"m.Array", b.String()})
	b.Reset()
	n := 0
	r.ForEach(func(k, v ojson.Result) bool {
		b.WriteString(odesc(k) + " => " + odesc(v) + "; ")
		n++
		return n < maxSeq
	})
	out = append(out, kv{"m.ForEach", b.String()})
	m := r.Map()
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b.Reset()
	for _, k := range keys {
		b.WriteString(fmt.Sprintf("%q: %s; ", k, odesc(m[k])))
	}
	out = append(out, kv{"m.Map", b.String()})
	if _, ok := x.(oLess); ok {
		b.Reset()
		for _, tok := range lessTokens {
			var o any = ojson.Parse(tok)
			l, ol := x.(oLess), o.(oLess)
			fmt.Fprintf(&b, "%v%v%v%v ", l.Less(o.(ojson.Result), true), l.Less(o.(ojson.Result), false), ol.Less(r, true), ol.Less(r, false))
		}
		out = append(out, kv{"m.Less", b.String()})
	} else {
		out = append(out, kv{"m.Less", missing})
	}
	if m, ok := x.(oAll); ok {
		out = append(out, kv{"m.All", seq2Text(m.All(), odesc)})
	} else {
		out = append(out, kv{"m.All", missing})
	}
	if m, ok := x.(oKeys); ok {
		out = append(out, kv{"m.Keys", seqText(m.Keys(), odesc)})
	} else {
		out = append(out, kv{"m.Keys", missing})
	}
	if m, ok := x.(oValues); ok {
		out = append(out, kv{"m.Values", seqText(m.Values(), odesc)})
	} else {
		out = append(out, kv{"m.Values", missing})
	}
	return out
}

// diffKV compares two observation lists with the same keys in order.
func diffKV(op string, got, want []kv) []mismatch {
	var ms []mismatch
	for i := range want {
		if i >= len(got) || got[i].k != want[i].k {
			panic("diffKV: key lists differ at " + want[i].k)
		}
		if got[i].v != want[i].v {
			ms = append(ms, mismatch{Op: op, Field: want[i].k, Got: got[i].v, Want: want[i].v})
		}
	}
	return ms
}

func errText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// outText describes a Set or Delete result, telling a nil slice apart.
func outText(s string, isNil bool) string {
	if isNil {
		return "nil"
	}
	return fmt.Sprintf("%q", s)
}
