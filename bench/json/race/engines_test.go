package race

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"unsafe"

	"github.com/MoeForge/moego/ojson"
	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func init() {
	if sonic.APIKind != sonic.UseSonicJSON {
		panic("race: sonic is not using its JIT implementation")
	}
}

// absent marks a path that was not found.
const absent = "\x00absent"

// A reader reads paths from a body. make returns the timed function and a
// function reporting the raw text of each result of the last run.
type reader struct {
	name string
	make func(paths []string) (run func([]byte), out func() []string)
}

var readers = []reader{
	{"ojson", ojsonReader},
	{"ojson-nocopy", ojsonNoCopyReader},
	{"gjson", gjsonReader},
	{"sonic", sonicReader},
	{"encjson-map", mapReader(json.Unmarshal)},
	{"sonic-map", mapReader(sonic.Unmarshal)},
}

func ojsonReader(paths []string) (func([]byte), func() []string) {
	var many []ojson.Result
	var one ojson.Result
	run := func(body []byte) {
		if len(paths) == 1 {
			one = ojson.GetBytes(body, paths[0])
		} else {
			many = ojson.GetManyBytes(body, paths...)
		}
	}
	out := func() []string {
		if len(paths) == 1 {
			many = []ojson.Result{one}
		}
		s := make([]string, len(many))
		for i, r := range many {
			s[i] = rawOf(r.Exists(), r.Raw)
		}
		return s
	}
	return run, out
}

// ojsonNoCopyReader uses the zero-copy reads, whose results view the body
// as sonic's do, with a reused result slice.
func ojsonNoCopyReader(paths []string) (func([]byte), func() []string) {
	var many []ojson.Result
	var one ojson.Result
	run := func(body []byte) {
		if len(paths) == 1 {
			one = ojson.GetBytesNoCopy(body, paths[0])
		} else {
			many = ojson.GetManyBytesNoCopy(many[:0], body, paths...)
		}
	}
	out := func() []string {
		rs := many
		if len(paths) == 1 {
			rs = []ojson.Result{one}
		}
		s := make([]string, len(rs))
		for i, r := range rs {
			s[i] = rawOf(r.Exists(), r.Raw)
		}
		return s
	}
	return run, out
}

func gjsonReader(paths []string) (func([]byte), func() []string) {
	var many []gjson.Result
	var one gjson.Result
	run := func(body []byte) {
		if len(paths) == 1 {
			one = gjson.GetBytes(body, paths[0])
		} else {
			many = gjson.GetManyBytes(body, paths...)
		}
	}
	out := func() []string {
		if len(paths) == 1 {
			many = []gjson.Result{one}
		}
		s := make([]string, len(many))
		for i, r := range many {
			s[i] = rawOf(r.Exists(), r.Raw)
		}
		return s
	}
	return run, out
}

// sonicReader calls sonic.Get once per path; sonic has no multi-path read.
func sonicReader(paths []string) (func([]byte), func() []string) {
	parts := make([][]any, len(paths))
	for i, p := range paths {
		for _, s := range strings.Split(p, ".") {
			if n, err := strconv.Atoi(s); err == nil {
				parts[i] = append(parts[i], n)
			} else {
				parts[i] = append(parts[i], s)
			}
		}
	}
	raws := make([]string, len(paths))
	run := func(body []byte) {
		for i, p := range parts {
			n, err := sonic.Get(body, p...)
			if err != nil {
				raws[i] = absent
				continue
			}
			raws[i], _ = n.Raw()
		}
	}
	return run, func() []string { return append([]string(nil), raws...) }
}

// mapReader decodes the whole body into a map and looks the paths up.
func mapReader(unmarshal func([]byte, any) error) func([]string) (func([]byte), func() []string) {
	return func(paths []string) (func([]byte), func() []string) {
		vals := make([]any, len(paths))
		oks := make([]bool, len(paths))
		run := func(body []byte) {
			var m map[string]any
			_ = unmarshal(body, &m)
			for i, p := range paths {
				vals[i], oks[i] = mapGet(m, strings.Split(p, "."))
			}
		}
		out := func() []string {
			s := make([]string, len(paths))
			for i := range s {
				b, _ := json.Marshal(vals[i])
				s[i] = rawOf(oks[i], string(b))
			}
			return s
		}
		return run, out
	}
}

func rawOf(ok bool, raw string) string {
	if !ok {
		return absent
	}
	return raw
}

// Output checks for editors.
const (
	exact     = iota // same bytes as sjson
	ordered          // same values and member order; whitespace may differ
	unordered        // same values only
)

// An editor applies a sequence of overrides and returns the new body.
type editor struct {
	name string
	kind int
	run  func(body []byte, ops []Override) ([]byte, error)
	// bench replaces run in the benchmark when the output is not one slice.
	bench func(body []byte, ops []Override) error
	// inPlace marks an editor that writes into body. It is measured only
	// for one edit, on its own copy of the body: that edit replaces a value
	// with one of the same length, so each repetition does the same work.
	inPlace bool
}

// The options that ask for the edit in the input's memory.
var (
	ojsonInPlace = &ojson.Options{Optimistic: true, ReplaceInPlace: true}
	sjsonInPlace = &sjson.Options{Optimistic: true, ReplaceInPlace: true}
)

var editors = []editor{
	{name: "ojson-doc", kind: exact, run: ojsonDoc},
	{name: "ojson-doc-append", kind: exact, run: ojsonDocAppend()},
	{name: "ojson-doc-writeto", kind: exact, run: ojsonDocWriteTo, bench: ojsonDocDiscard},
	{name: "ojson-setbytes", kind: exact, run: flat(ojson.SetBytes, ojson.DeleteBytes)},
	{name: "ojson-inplace", kind: exact, inPlace: true, run: flat(func(b []byte, p string, v any) ([]byte, error) {
		return ojson.SetBytesOptions(b, p, v, ojsonInPlace)
	}, ojson.DeleteBytes)},
	{name: "sjson", kind: exact, run: flat(sjson.SetBytes, sjson.DeleteBytes)},
	{name: "sjson-inplace", kind: exact, inPlace: true, run: flat(func(b []byte, p string, v any) ([]byte, error) {
		return sjson.SetBytesOptions(b, p, v, sjsonInPlace)
	}, sjson.DeleteBytes)},
	{name: "sonic-ast", kind: ordered, run: sonicAST},
	{name: "encjson-map", kind: unordered, run: mapEditor(json.Unmarshal, json.Marshal)},
	{name: "sonic-map", kind: unordered, run: mapEditor(sonic.Unmarshal, sonic.Marshal)},
}

func ojsonDoc(body []byte, ops []Override) ([]byte, error) {
	d, err := ojsonEdit(body, ops)
	if err != nil {
		return nil, err
	}
	return d.Bytes(), nil
}

// ojsonDocAppend writes each result into one buffer reused across calls, as
// a proxy that sends the body before it reads the next request can.
func ojsonDocAppend() func([]byte, []Override) ([]byte, error) {
	var buf []byte
	return func(body []byte, ops []Override) ([]byte, error) {
		d, err := ojsonEdit(body, ops)
		if err != nil {
			return nil, err
		}
		buf = d.AppendTo(buf[:0])
		return buf, nil
	}
}

func ojsonDocWriteTo(body []byte, ops []Override) ([]byte, error) {
	d, err := ojsonEdit(body, ops)
	if err != nil {
		return nil, err
	}
	var w bytes.Buffer
	_, err = d.WriteTo(&w)
	return w.Bytes(), err
}

// ojsonDocDiscard writes the result in segments to a writer that drops
// them. Sending the body costs the same for every editor, so the benchmark
// leaves it out.
func ojsonDocDiscard(body []byte, ops []Override) error {
	d, err := ojsonEdit(body, ops)
	if err != nil {
		return err
	}
	_, err = d.WriteTo(io.Discard)
	return err
}

func ojsonEdit(body []byte, ops []Override) (*ojson.Doc, error) {
	d := ojson.NewDoc(body)
	for _, op := range ops {
		var err error
		if op.Del {
			err = d.Delete(op.Path)
		} else {
			err = d.Set(op.Path, op.Value)
		}
		if err != nil {
			return nil, err
		}
	}
	return d, nil
}

// flat applies each override with one call that copies the body.
func flat(set func([]byte, string, any) ([]byte, error), del func([]byte, string) ([]byte, error)) func([]byte, []Override) ([]byte, error) {
	return func(body []byte, ops []Override) ([]byte, error) {
		var err error
		for _, op := range ops {
			if op.Del {
				body, err = del(body, op.Path)
			} else {
				body, err = set(body, op.Path, op.Value)
			}
			if err != nil {
				return nil, err
			}
		}
		return body, nil
	}
}

// sonicAST parses lazily without copying the body, the cheapest sonic entry
// point, then walks each path with Get/Index and edits with Set/Unset.
func sonicAST(body []byte, ops []Override) ([]byte, error) {
	root, err := sonic.GetFromString(unsafe.String(unsafe.SliceData(body), len(body)))
	if err != nil {
		return nil, err
	}
	for _, op := range ops {
		if err := astApply(&root, op); err != nil {
			return nil, err
		}
	}
	return root.MarshalJSON()
}

// astApply walks op.Parts, creating missing objects like sjson does, and
// sets or removes the last key.
func astApply(root *ast.Node, op Override) error {
	n := root
	last := len(op.Parts) - 1
	for _, p := range op.Parts[:last] {
		var next *ast.Node
		if i, err := strconv.Atoi(p); err == nil && n.TypeSafe() == ast.V_ARRAY {
			next = n.Index(i)
		} else {
			next = n.Get(p)
		}
		if !next.Exists() {
			if op.Del {
				return nil
			}
			if _, err := n.Set(p, ast.NewObject(nil)); err != nil {
				return err
			}
			next = n.Get(p)
		}
		n = next
	}
	k := op.Parts[last]
	if op.Del {
		_, err := n.Unset(k)
		return err
	}
	v, err := astValue(op.Value)
	if err != nil {
		return err
	}
	if i, err := strconv.Atoi(k); err == nil && n.TypeSafe() == ast.V_ARRAY {
		_, err = n.SetByIndex(i, v)
		return err
	}
	_, err = n.Set(k, v)
	return err
}

func astValue(v any) (ast.Node, error) {
	switch v := v.(type) {
	case string:
		return ast.NewString(v), nil
	case bool:
		return ast.NewBool(v), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ast.Node{}, err
	}
	return ast.NewRaw(string(b)), nil
}

// mapEditor decodes into a map, edits it and encodes it again. Member order
// is lost; these are the unordered reference rows.
func mapEditor(unmarshal func([]byte, any) error, marshal func(any) ([]byte, error)) func([]byte, []Override) ([]byte, error) {
	return func(body []byte, ops []Override) ([]byte, error) {
		var m map[string]any
		if err := unmarshal(body, &m); err != nil {
			return nil, err
		}
		for _, op := range ops {
			mapSet(m, op)
		}
		return marshal(m)
	}
}

func mapSet(m map[string]any, op Override) {
	var cur any = m
	last := len(op.Parts) - 1
	for _, p := range op.Parts[:last] {
		switch c := cur.(type) {
		case map[string]any:
			next, ok := c[p]
			if !ok {
				if op.Del {
					return
				}
				next = map[string]any{}
				c[p] = next
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i >= len(c) {
				return
			}
			cur = c[i]
		default:
			return
		}
	}
	k := op.Parts[last]
	switch c := cur.(type) {
	case map[string]any:
		if op.Del {
			delete(c, k)
		} else {
			c[k] = op.Value
		}
	case []any:
		if i, err := strconv.Atoi(k); err == nil && i < len(c) && !op.Del {
			c[i] = op.Value
		}
	}
}

func mapGet(m map[string]any, parts []string) (any, bool) {
	var cur any = m
	for _, p := range parts {
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[p]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i >= len(c) {
				return nil, false
			}
			cur = c[i]
		default:
			return nil, false
		}
	}
	return cur, true
}
