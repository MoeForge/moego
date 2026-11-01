// Command gq prints what gjson and sjson return for the cases on stdin, one
// JSON array of strings per line:
//
//	["get", json, path]          Get, plus Path and Paths of the result
//	["set", json, path, value]   Set with a JSON-decoded value
//	["setraw", json, path, raw]  SetRaw
//	["setopt", json, path, value, flags]  SetBytesOptions; flags o, i
//	["del", json, path]          Delete
//	["esc", comp]                Escape
//	["ajs", s]                   AppendJSONString
//	["ajsx", hex]                AppendJSONString of hex-decoded bytes
//	["lines", json]              ForEachLine
//	["less", a, b]               Parse(a).Less(Parse(b)), both cases
//	["mod", name]                ModifierExists
//	["methods", json, path]      Result methods of Get
//	["iter", json, path]         All, Keys and Values of Get
//	["noesc", json, path]        Get with DisableEscapeHTML
//	["nomod", json, path]        Get with DisableModifiers
//	["fe", json, path]           ForEach keys and values of Get
//	["map", json, path]          Map of Get, sorted by key
//	["value", json, path]        Value of Get
//	["paths", json, path]        Path and Paths of Get, Go syntax
//	["rget", json, path, path2]  Get(json, path).Get(path2)
//	["parse", json]              Parse and ParseBytes
//	["ajsnoesc", s]              AppendJSONString with DisableEscapeHTML
//	["wp", json, rest, pipe]     classify Get(json, "#."+rest+"|"+pipe) as
//	                             whole-array pipe (W) or per-element pipe (P)
//
// Lines starting with # are echoed. Run it under timeout and ulimit.
package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func res(r gjson.Result) string {
	s := fmt.Sprintf("T=%d Raw=%q Str=%q Num=%v(%x) Idx=%d Idxs=%v", r.Type, r.Raw, r.Str, r.Num, math.Float64bits(r.Num), r.Index, r.Indexes)
	if r.Indexes != nil && len(r.Indexes) == 0 {
		s += "(empty)"
	}
	return s
}

func errs(err error) string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%q", err.Error())
}

func safe(f func() string) (s string) {
	defer func() {
		if e := recover(); e != nil {
			s = fmt.Sprintf("PANIC(%v)", e)
		}
	}()
	return f()
}

func main() {
	gjson.AddModifier("case", func(json, arg string) string {
		if arg == "upper" {
			return strings.ToUpper(json)
		}
		if arg == "lower" {
			return strings.ToLower(json)
		}
		return json
	})
	gjson.AddModifier("echo", func(json, arg string) string {
		return "[" + fmt.Sprintf("%q", json) + "," + fmt.Sprintf("%q", arg) + "]"
	})
	gjson.AddModifier("empty", func(json, arg string) string { return "" })
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			fmt.Println(line)
			continue
		}
		var a []string
		if err := json.Unmarshal([]byte(line), &a); err != nil || len(a) < 2 {
			fmt.Printf("BAD %s %v\n", line, err)
			continue
		}
		fmt.Printf("%s => %s\n", line, safe(func() string { return run(a) }))
	}
}

func run(a []string) string {
	arg := func(i int) string {
		if i < len(a) {
			return a[i]
		}
		return ""
	}
	switch a[0] {
	case "get":
		r := gjson.Get(a[1], arg(2))
		rb := gjson.GetBytes([]byte(a[1]), arg(2))
		s := res(r)
		if res(rb) != s {
			s += " BYTES " + res(rb)
		}
		return s + " Path=" + safe(func() string { return fmt.Sprintf("%q", r.Path(a[1])) }) + " Paths=" + safe(func() string { return fmt.Sprintf("%q", r.Paths(a[1])) })
	case "noesc":
		gjson.DisableEscapeHTML = true
		defer func() { gjson.DisableEscapeHTML = false }()
		return res(gjson.Get(a[1], arg(2)))
	case "nomod":
		gjson.DisableModifiers = true
		defer func() { gjson.DisableModifiers = false }()
		return res(gjson.Get(a[1], arg(2)))
	case "set":
		var v any
		if err := json.Unmarshal([]byte(arg(3)), &v); err != nil {
			return "BADVALUE"
		}
		s, err := sjson.Set(a[1], arg(2), v)
		return fmt.Sprintf("%q %s", s, errs(err))
	case "setraw":
		s, err := sjson.SetRaw(a[1], arg(2), arg(3))
		return fmt.Sprintf("%q %s", s, errs(err))
	case "setopt":
		var v any
		if err := json.Unmarshal([]byte(arg(3)), &v); err != nil {
			return "BADVALUE"
		}
		o := &sjson.Options{Optimistic: strings.Contains(arg(4), "o"), ReplaceInPlace: strings.Contains(arg(4), "i")}
		b, err := sjson.SetBytesOptions([]byte(a[1]), arg(2), v, o)
		return fmt.Sprintf("%q %s", b, errs(err))
	case "del":
		s, err := sjson.Delete(a[1], arg(2))
		return fmt.Sprintf("%q %s", s, errs(err))
	case "esc":
		return fmt.Sprintf("%q", gjson.Escape(a[1]))
	case "ajs":
		return fmt.Sprintf("%q", gjson.AppendJSONString([]byte("x"), a[1]))
	case "ajsx":
		b, err := hex.DecodeString(a[1])
		if err != nil {
			return "BADHEX"
		}
		return fmt.Sprintf("%q", gjson.AppendJSONString(nil, string(b)))
	case "iter":
		r := gjson.Get(a[1], arg(2))
		var all, keys, vals []string
		for k, v := range r.All() {
			all = append(all, res(k)+"="+res(v))
		}
		for k := range r.Keys() {
			keys = append(keys, res(k))
		}
		for v := range r.Values() {
			vals = append(vals, res(v))
		}
		return fmt.Sprintf("All=%v Keys=%v Values=%v", all, keys, vals)
	case "lines":
		var out []string
		gjson.ForEachLine(a[1], func(l gjson.Result) bool {
			out = append(out, res(l))
			return len(out) < 50
		})
		return strings.Join(out, " | ")
	case "less":
		x, y := gjson.Parse(a[1]), gjson.Parse(arg(2))
		return fmt.Sprintf("cs=%v ci=%v rev-cs=%v rev-ci=%v", x.Less(y, true), x.Less(y, false), y.Less(x, true), y.Less(x, false))
	case "mod":
		return fmt.Sprint(gjson.ModifierExists(a[1], nil))
	case "fe":
		var out []string
		gjson.Get(a[1], arg(2)).ForEach(func(k, v gjson.Result) bool {
			out = append(out, res(k)+" = "+res(v))
			return true
		})
		return strings.Join(out, " ; ")
	case "map":
		m := gjson.Get(a[1], arg(2)).Map()
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var out []string
		for _, k := range keys {
			out = append(out, fmt.Sprintf("%q:%s", k, res(m[k])))
		}
		return strings.Join(out, " ; ")
	case "value":
		return fmt.Sprintf("%#v", gjson.Get(a[1], arg(2)).Value())
	case "rget":
		r := gjson.Get(a[1], arg(2))
		return res(r) + " GET " + res(r.Get(arg(3)))
	case "parse":
		return res(gjson.Parse(a[1])) + " BYTES " + res(gjson.ParseBytes([]byte(a[1])))
	case "ajsnoesc":
		gjson.DisableEscapeHTML = true
		defer func() { gjson.DisableEscapeHTML = false }()
		return fmt.Sprintf("%q", gjson.AppendJSONString(nil, a[1]))
	case "wp":
		full := gjson.Get(a[1], "#."+arg(2)+"|"+arg(3))
		whole := gjson.Get(gjson.Get(a[1], "#."+arg(2)).Raw, arg(3))
		var parts []string
		gjson.Parse(a[1]).ForEach(func(_, e gjson.Result) bool {
			r := gjson.Get(e.Raw, arg(2)+"|"+arg(3))
			if r.Exists() {
				raw := r.Raw
				if raw == "" {
					raw = r.String()
				}
				parts = append(parts, raw)
			}
			return true
		})
		per := "[" + strings.Join(parts, ",") + "]"
		tag := ""
		if full.Raw == whole.Raw {
			tag += "W"
		}
		if full.Raw == per {
			tag += "P"
		}
		if tag == "" {
			tag = "-"
		}
		return fmt.Sprintf("%s full=%q whole=%q per=%q", tag, full.Raw, whole.Raw, per)
	case "paths":
		r := gjson.Get(a[1], arg(2))
		return fmt.Sprintf("%#v %#v", r.Path(a[1]), r.Paths(a[1]))
	case "methods":
		r := gjson.Get(a[1], arg(2))
		var fe []string
		r.ForEach(func(k, v gjson.Result) bool {
			fe = append(fe, k.Raw+"="+v.Raw+fmt.Sprintf("@%d", v.Index))
			return true
		})
		arr := r.Array()
		var as []string
		for _, x := range arr {
			as = append(as, fmt.Sprintf("%s@%d", x.Raw, x.Index))
		}
		tm := r.Time()
		return fmt.Sprintf("%s S=%q I=%d U=%d F=%v B=%v Arr=%v FE=%v Map=%d Val=%#v Time=%s", res(r), r.String(), r.Int(), r.Uint(), r.Float(), r.Bool(), as, fe, len(r.Map()), r.Value(), tm.Format("2006-01-02T15:04:05.999999999Z07:00"))
	}
	return "UNKNOWN"
}
