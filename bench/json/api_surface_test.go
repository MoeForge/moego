// Package jsonbench holds the checks that ojson exposes the whole public API
// of gjson v1.19.0 and sjson v1.2.5 with the same behavior on valid JSON.
package jsonbench

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/MoeForge/moego/ojson"
)

// normType writes a type with both packages under one name, so that
// signatures from gjson, sjson and ojson compare as text.
func normType(t reflect.Type) string {
	r := strings.NewReplacer(
		"github.com/tidwall/gjson.", "P.", "github.com/tidwall/sjson.", "P.",
		"github.com/MoeForge/moego/ojson.", "P.", "gjson.", "P.", "sjson.", "P.", "ojson.", "P.")
	return r.Replace(t.String())
}

// TestSurface checks every exported identifier of gjson and sjson. The
// package-level lists come from `go doc -short`; methods and fields are
// enumerated by reflection, so additions there are caught too.
func TestSurface(t *testing.T) {
	funcs := []struct {
		name string
		ref  any
		got  any
	}{
		{"gjson.AddModifier", gjson.AddModifier, ojson.AddModifier},
		{"gjson.AppendJSONString", gjson.AppendJSONString, ojson.AppendJSONString},
		{"gjson.Escape", gjson.Escape, ojson.Escape},
		{"gjson.ForEachLine", gjson.ForEachLine, ojson.ForEachLine},
		{"gjson.ModifierExists", gjson.ModifierExists, ojson.ModifierExists},
		{"gjson.Valid", gjson.Valid, ojson.Valid},
		{"gjson.ValidBytes", gjson.ValidBytes, ojson.ValidBytes},
		{"gjson.Get", gjson.Get, ojson.Get},
		{"gjson.GetBytes", gjson.GetBytes, ojson.GetBytes},
		{"gjson.GetMany", gjson.GetMany, ojson.GetMany},
		{"gjson.GetManyBytes", gjson.GetManyBytes, ojson.GetManyBytes},
		{"gjson.Parse", gjson.Parse, ojson.Parse},
		{"gjson.ParseBytes", gjson.ParseBytes, ojson.ParseBytes},
		{"sjson.Delete", sjson.Delete, ojson.Delete},
		{"sjson.DeleteBytes", sjson.DeleteBytes, ojson.DeleteBytes},
		{"sjson.Set", sjson.Set, ojson.Set},
		{"sjson.SetBytes", sjson.SetBytes, ojson.SetBytes},
		{"sjson.SetBytesOptions", sjson.SetBytesOptions, ojson.SetBytesOptions},
		{"sjson.SetOptions", sjson.SetOptions, ojson.SetOptions},
		{"sjson.SetRaw", sjson.SetRaw, ojson.SetRaw},
		{"sjson.SetRawBytes", sjson.SetRawBytes, ojson.SetRawBytes},
		{"sjson.SetRawBytesOptions", sjson.SetRawBytesOptions, ojson.SetRawBytesOptions},
		{"sjson.SetRawOptions", sjson.SetRawOptions, ojson.SetRawOptions},
	}
	for _, f := range funcs {
		if a, b := normType(reflect.TypeOf(f.ref)), normType(reflect.TypeOf(f.got)); a != b {
			t.Errorf("%s: ojson has %s, want %s", f.name, b, a)
		}
	}
	vars := []struct {
		name     string
		ref, got any
	}{
		{"gjson.DisableModifiers", &gjson.DisableModifiers, &ojson.DisableModifiers},
		{"gjson.DisableEscapeHTML", &gjson.DisableEscapeHTML, &ojson.DisableEscapeHTML},
	}
	for _, v := range vars {
		if a, b := normType(reflect.TypeOf(v.ref)), normType(reflect.TypeOf(v.got)); a != b {
			t.Errorf("var %s: ojson has %s, want %s", v.name, b, a)
		}
		if a, b := reflect.ValueOf(v.ref).Elem().Interface(), reflect.ValueOf(v.got).Elem().Interface(); a != b {
			t.Errorf("var %s: ojson starts as %v, want %v", v.name, b, a)
		}
	}
	consts := []struct {
		ref gjson.Type
		got ojson.Type
	}{
		{gjson.Null, ojson.Null}, {gjson.False, ojson.False}, {gjson.Number, ojson.Number},
		{gjson.String, ojson.String}, {gjson.True, ojson.True}, {gjson.JSON, ojson.JSON},
	}
	for _, c := range consts {
		if int(c.ref) != int(c.got) || c.ref.String() != c.got.String() {
			t.Errorf("const %v: ojson has %d %q", c.ref, int(c.got), c.got.String())
		}
	}
	if a, b := ojson.Type(99).String(), gjson.Type(99).String(); a != b {
		t.Errorf("Type(99).String() = %q, want %q", a, b)
	}
	types := []struct {
		name     string
		ref, got reflect.Type
	}{
		{"gjson.Result", reflect.TypeOf(gjson.Result{}), reflect.TypeOf(ojson.Result{})},
		{"*gjson.Result", reflect.TypeOf(&gjson.Result{}), reflect.TypeOf(&ojson.Result{})},
		{"gjson.Type", reflect.TypeOf(gjson.Type(0)), reflect.TypeOf(ojson.Type(0))},
		{"sjson.Options", reflect.TypeOf(sjson.Options{}), reflect.TypeOf(ojson.Options{})},
		{"*sjson.Options", reflect.TypeOf(&sjson.Options{}), reflect.TypeOf(&ojson.Options{})},
	}
	for _, ty := range types {
		if a, b := ty.ref.Kind(), ty.got.Kind(); a != b {
			t.Errorf("%s: kind %v, want %v", ty.name, b, a)
			continue
		}
		for i := 0; i < ty.ref.NumMethod(); i++ {
			m := ty.ref.Method(i)
			g, ok := ty.got.MethodByName(m.Name)
			if !ok {
				t.Errorf("%s.%s: missing", ty.name, m.Name)
				continue
			}
			if a, b := normType(m.Type), normType(g.Type); a != b {
				t.Errorf("%s.%s: ojson has %s, want %s", ty.name, m.Name, b, a)
			}
		}
		if ty.ref.Kind() != reflect.Struct {
			continue
		}
		if a, b := ty.ref.NumField(), ty.got.NumField(); a != b {
			t.Errorf("%s: %d fields, want %d", ty.name, b, a)
		}
		for i := 0; i < ty.ref.NumField(); i++ {
			f := ty.ref.Field(i)
			g, ok := ty.got.FieldByName(f.Name)
			if !ok || g.Index[0] != i {
				t.Errorf("%s.%s: missing or moved", ty.name, f.Name)
				continue
			}
			if a, b := normType(f.Type), normType(g.Type); a != b {
				t.Errorf("%s.%s: ojson has %s, want %s", ty.name, f.Name, b, a)
			}
		}
	}
}

// show writes a Result with every field, nil and empty Indexes apart.
func show(typ int, raw, str string, num float64, index int, indexes []int) string {
	ix := "nil"
	if indexes != nil {
		ix = fmt.Sprint(indexes)
	}
	return fmt.Sprintf("T%d Raw=%q Str=%q Num=%x Idx=%d Idxs=%s", typ, raw, str, math.Float64bits(num), index, ix)
}

func showG(r gjson.Result) string { return show(int(r.Type), r.Raw, r.Str, r.Num, r.Index, r.Indexes) }
func showO(r ojson.Result) string { return show(int(r.Type), r.Raw, r.Str, r.Num, r.Index, r.Indexes) }

// checker counts mismatches and prints the first few of each kind.
type checker struct {
	t      *testing.T
	n      int
	byKind map[string]int
}

func newChecker(t *testing.T) *checker { return &checker{t: t, byKind: map[string]int{}} }

func (c *checker) eq(kind, input, got, want string) {
	c.n++
	if got == want {
		return
	}
	c.byKind[kind]++
	if c.byKind[kind] <= 5 {
		c.t.Errorf("%s %s\n got  %s\n want %s", kind, input, got, want)
	}
}

func (c *checker) done() {
	total := 0
	for _, n := range c.byKind {
		total += n
	}
	c.t.Logf("%d checks, %d mismatches %v", c.n, total, c.byKind)
}

// gen builds random valid JSON with whitespace, escapes and duplicate keys.
type gen struct{ r *rand.Rand }

var genKeys = []string{"a", "b", "c", "A", "_", "aa", "x y", "", `ab`, `\"`, "é", "a.b", "k1", "<k>", `\\`}

var genStrs = []string{`""`, `"x"`, `"a b"`, `"<b>&"`, `"é\n"`, `"é😀"`, `" "`, `"\\"`, `"\""`,
	`"1"`, `"true"`, `"2006-01-02T15:04:05Z"`, `"2024-02-30T10:00:00+08:00"`, `"{\"a\":[1,2]}"`, `"[1,2"`,
	`"1x"`, `"😀"`, `"ABC"`, `"abc"`, `"ß"`, `"\/"`, `"\u0000"`}

var genNums = []string{"0", "-0", "1", "-1", "12", "1.50", "1E5", "-2.5e-3", "100", "9007199254740993",
	"18446744073709551616", "3.0", "0.1", "7"}

func (g gen) ws() string {
	switch g.r.Intn(8) {
	case 0:
		return " "
	case 1:
		return "\n  "
	case 2:
		return "\t"
	}
	return ""
}

func (g gen) value(depth int) string {
	k := g.r.Intn(10)
	if depth <= 0 && k >= 7 {
		k = g.r.Intn(7)
	}
	switch k {
	case 0, 1:
		return genStrs[g.r.Intn(len(genStrs))]
	case 2, 3:
		return genNums[g.r.Intn(len(genNums))]
	case 4:
		return []string{"true", "false", "null"}[g.r.Intn(3)]
	case 5, 6, 7:
		n := g.r.Intn(5)
		var b strings.Builder
		b.WriteString("[" + g.ws())
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteString(g.ws() + "," + g.ws())
			}
			b.WriteString(g.value(depth - 1))
		}
		b.WriteString(g.ws() + "]")
		return b.String()
	}
	n := g.r.Intn(5)
	var b strings.Builder
	b.WriteString("{" + g.ws())
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(g.ws() + "," + g.ws())
		}
		b.WriteString(`"` + genKeys[g.r.Intn(len(genKeys))] + `"` + g.ws() + ":" + g.ws())
		b.WriteString(g.value(depth - 1))
	}
	b.WriteString(g.ws() + "}")
	return b.String()
}

// paths lists plain paths to values of doc, found through gjson.
func paths(doc string, max int) []string {
	var out []string
	type node struct {
		path string
		v    gjson.Result
	}
	stack := []node{{"", gjson.Parse(doc)}}
	for len(stack) > 0 && len(out) < max {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.path != "" {
			out = append(out, n.path)
		}
		if !n.v.IsObject() && !n.v.IsArray() {
			continue
		}
		i := 0
		n.v.ForEach(func(k, v gjson.Result) bool {
			c := strconv.Itoa(i)
			if n.v.IsObject() {
				c = gjson.Escape(k.Str)
			}
			i++
			p := c
			if n.path != "" {
				p = n.path + "." + c
			}
			stack = append(stack, node{p, v})
			return true
		})
	}
	return out
}

func corpus(n int) []string {
	g := gen{rand.New(rand.NewSource(1))}
	docs := []string{
		`{"name":{"first":"Tom","last":"Anderson"},"age":37,"children":["Sara","Alex","Jack"],"fav.movie":"Deer Hunter","friends":[{"first":"Dale","last":"Murphy","age":44,"nets":["ig","fb","tw"]},{"first":"Roger","last":"Craig","age":68,"nets":["fb","tw"]},{"first":"Jane","last":"Murphy","age":47,"nets":["ig","tw"]}]}`,
		`{"id":["123","456","789"],"val":[2,1]}`, `[[1,2],[3]]`, `[{"a":1},{"b":2}]`, `  [ 1 , 2 ]  `,
		`{"a":[1,[2,[3,[4]]],[]],"b":[[5]]}`, `[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,24,25,26,27,28,29,30]`,
		`{"b":1,"a":2,"\"":3}`, `{"b":1,"a":2,"B":3,"_":4,"aa":5,"a":6}`, `[{}]`, `[[]]`, `[[{}]]`, `{"a":{}}`,
		`"str"`, `12`, `true`, `null`, `{}`, `[]`, ` {"a":1} `,
	}
	for len(docs) < n {
		docs = append(docs, g.ws()+g.value(4)+g.ws())
	}
	return docs
}

var prettyArgs = []string{"", `{}`, `[]`, `x`, `{"width":0}`, `{"width":-1}`, `{"width":1}`, `{"width":3}`,
	`{"width":4}`, `{"width":5}`, `{"width":8}`, `{"width":10}`, `{"width":12}`, `{"width":16}`, `{"width":20}`,
	`{"width":"20"}`, `{"sortKeys":true}`, `{"sortKeys":1}`, `{"sortKeys":"true"}`, `{"indent":"\t"}`,
	`{"indent":""}`, `{"indent":"_"}`, `{"indent":1}`, `{"indent":" x "}`, `{"indent":"\r\n"}`,
	`{"indent":"\f"}`, `{"indent":" "}`, `{"prefix":"> "}`, `{"prefix":"\t"}`, `{"prefix":"ab"}`,
	`{"prefix":"  ","width":12}`, `{"prefix":"\t","indent":"_"}`, `{"width":10,"sortKeys":true,"prefix":"..","indent":"\t"}`,
	`{"width":30,"indent":"    "}`, `{"Width":10}`, `{"width":10,"width":80}`}

// modPaths returns the modifier paths tried on each document.
func modPaths(doc string, r *rand.Rand) []string {
	mods := []string{"@reverse", "@ugly", "@this", "@valid", "@flatten", `@flatten:{"deep":true}`,
		`@flatten:{"deep":1}`, "@join", `@join:{"preserve":true}`, "@keys", "@values", "@tostr", "@fromstr",
		"@group", "@dig", "@dig:a", "@dig:0", "@dig:#", "@dig:b.c", "@dig:a*", "@dig:@this"}
	for _, a := range prettyArgs {
		if a == "" {
			mods = append(mods, "@pretty")
		} else {
			mods = append(mods, "@pretty:"+a)
		}
	}
	var out []string
	out = append(out, mods...)
	ps := paths(doc, 40)
	for i := 0; i < 12 && len(ps) > 0; i++ {
		p := ps[r.Intn(len(ps))]
		m := mods[r.Intn(len(mods))]
		out = append(out, p+"|"+m, p+"."+m)
	}
	return out
}

// TestModifiers compares the built-in modifiers, applied at the root and to
// values inside documents.
func TestModifiers(t *testing.T) {
	c := newChecker(t)
	r := rand.New(rand.NewSource(2))
	for _, html := range []bool{false, true} {
		gjson.DisableEscapeHTML, ojson.DisableEscapeHTML = html, html
		for _, doc := range corpus(1500) {
			for _, p := range modPaths(doc, r) {
				kind := p[strings.LastIndexByte(p, '@'):]
				if i := strings.IndexByte(kind, ':'); i > 0 {
					kind = kind[:i]
				}
				c.eq(kind, fmt.Sprintf("%q %q html=%v", doc, p, html), showO(ojson.Get(doc, p)), showG(gjson.Get(doc, p)))
			}
		}
	}
	gjson.DisableEscapeHTML, ojson.DisableEscapeHTML = false, false
	c.done()
}

// TestCustomModifiers checks AddModifier, ModifierExists and
// DisableModifiers.
func TestCustomModifiers(t *testing.T) {
	c := newChecker(t)
	for _, add := range []func(string, func(string, string) string){gjson.AddModifier, ojson.AddModifier} {
		add("echo", func(json, arg string) string { return " " + arg })
		add("wrap", func(json, arg string) string { return "[" + json + "," + strconv.Quote(arg) + "]" })
		add("empty", func(json, arg string) string { return "" })
	}
	names := []string{"reverse", "ugly", "pretty", "this", "valid", "flatten", "join", "keys", "values", "tostr",
		"fromstr", "group", "dig", "echo", "wrap", "empty", "zz", "", "@reverse", "Reverse"}
	for _, n := range names {
		c.eq("exists", n, fmt.Sprint(ojson.ModifierExists(n, nil)), fmt.Sprint(gjson.ModifierExists(n, nil)))
	}
	docs := []string{`{"a":[1,2],"b":{"c":"x"},"@this":5,"@echo":6}`, `[1,"x",{"a":true}]`, ` "s" `, ``}
	ps := []string{"@echo:hi", "@echo:{\"a\":1}", "@echo:1", "@wrap:x", "a.@wrap:y", "a|@wrap", "@empty", "a.@empty",
		"b.c.@wrap", "@wrap|0", "@wrap.1", "zz.@wrap", "zz|@wrap", "@reverse", "a.@reverse", "@this", "@echo",
		"@keys", "zz.@keys", "zz.@tostr", "zz|@tostr", "zz.@dig:a", "zz.@values", "zz.@pretty", "zz.@group"}
	for _, off := range []bool{false, true} {
		gjson.DisableModifiers, ojson.DisableModifiers = off, off
		for _, d := range docs {
			if !gjson.Valid(d) {
				continue // malformed input only has to be safe
			}
			for _, p := range ps {
				c.eq("custom", fmt.Sprintf("%q %q off=%v", d, p, off), showO(ojson.Get(d, p)), showG(gjson.Get(d, p)))
			}
		}
	}
	gjson.DisableModifiers, ojson.DisableModifiers = false, false
	// Replacing a built-in modifier.
	for _, add := range []func(string, func(string, string) string){gjson.AddModifier, ojson.AddModifier} {
		add("keys", func(json, arg string) string { return `"replaced"` })
	}
	c.eq("replace", "@keys", showO(ojson.Get(`{"a":1}`, "@keys")), showG(gjson.Get(`{"a":1}`, "@keys")))
	c.done()
}

// randText returns a random string with path characters, escapes, UTF-8
// and invalid bytes.
func randText(r *rand.Rand) string {
	pieces := []string{"a", "Z", "0", "_", "-", ":", " ", "\t", "\x00", "\x1f", "\x7f", ".", "*", "?", "|", "#",
		"@", "\\", "!", "[", "]", "{", "}", "(", ")", "=", "<", ">", "%", "~", ",", "\"", "'", "$", "&", "+", "/",
		";", "^", "`", "é", "😀", " ", " ", "\xff", "\xc3", "\xed\xa0\x80", "\xf4\x90\x80\x80", "\n", "\r", "\b", "\f"}
	n := r.Intn(8)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(pieces[r.Intn(len(pieces))])
	}
	return b.String()
}

// TestFuncs compares Escape, AppendJSONString and ForEachLine.
func TestFuncs(t *testing.T) {
	c := newChecker(t)
	r := rand.New(rand.NewSource(3))
	for i := 0; i < 200000; i++ {
		s := randText(r)
		c.eq("Escape", fmt.Sprintf("%q", s), ojson.Escape(s), gjson.Escape(s))
		html := i%2 == 0
		gjson.DisableEscapeHTML, ojson.DisableEscapeHTML = html, html
		c.eq("AppendJSONString", fmt.Sprintf("%q html=%v", s, html),
			string(ojson.AppendJSONString([]byte("x"), s)), string(gjson.AppendJSONString([]byte("x"), s)))
	}
	gjson.DisableEscapeHTML, ojson.DisableEscapeHTML = false, false
	g := gen{r}
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		n := r.Intn(5)
		for j := 0; j < n; j++ {
			b.WriteString([]string{"\n", " ", "\r\n", "\n\n", "\t"}[r.Intn(5)])
			b.WriteString(g.value(2))
		}
		b.WriteString([]string{"", "\n", " "}[r.Intn(3)])
		doc := b.String()
		var got, want []string
		ojson.ForEachLine(doc, func(l ojson.Result) bool { got = append(got, showO(l)); return len(got) < 3 || i%2 == 0 })
		gjson.ForEachLine(doc, func(l gjson.Result) bool { want = append(want, showG(l)); return len(want) < 3 || i%2 == 0 })
		c.eq("ForEachLine", fmt.Sprintf("%q", doc), strings.Join(got, " | "), strings.Join(want, " | "))
	}
	c.done()
}

// methods writes the outputs of every Result method that takes no
// document.
func methodsO(r ojson.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "S=%q B=%v I=%d U=%d F=%x E=%v O=%v A=%v Bo=%v T=%v", r.String(), r.Bool(), r.Int(), r.Uint(),
		math.Float64bits(r.Float()), r.Exists(), r.IsObject(), r.IsArray(), r.IsBool(), r.Time())
	b.WriteString(" FE[")
	r.ForEach(func(k, v ojson.Result) bool { b.WriteString(showO(k) + "=" + showO(v) + ";"); return true })
	b.WriteString("] ALL[")
	for k, v := range r.All() {
		b.WriteString(showO(k) + "=" + showO(v) + ";")
	}
	b.WriteString("] K[")
	for k := range r.Keys() {
		b.WriteString(showO(k) + ";")
	}
	b.WriteString("] V[")
	for v := range r.Values() {
		b.WriteString(showO(v) + ";")
		break
	}
	b.WriteString("] AR[")
	for _, v := range r.Array() {
		b.WriteString(showO(v) + ";")
	}
	b.WriteString("] M[")
	m := r.Map()
	fmt.Fprintf(&b, "%d", len(m))
	fmt.Fprintf(&b, "] VAL=%#v", r.Value())
	return b.String()
}

func methodsG(r gjson.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "S=%q B=%v I=%d U=%d F=%x E=%v O=%v A=%v Bo=%v T=%v", r.String(), r.Bool(), r.Int(), r.Uint(),
		math.Float64bits(r.Float()), r.Exists(), r.IsObject(), r.IsArray(), r.IsBool(), r.Time())
	b.WriteString(" FE[")
	r.ForEach(func(k, v gjson.Result) bool { b.WriteString(showG(k) + "=" + showG(v) + ";"); return true })
	b.WriteString("] ALL[")
	for k, v := range r.All() {
		b.WriteString(showG(k) + "=" + showG(v) + ";")
	}
	b.WriteString("] K[")
	for k := range r.Keys() {
		b.WriteString(showG(k) + ";")
	}
	b.WriteString("] V[")
	for v := range r.Values() {
		b.WriteString(showG(v) + ";")
		break
	}
	b.WriteString("] AR[")
	for _, v := range r.Array() {
		b.WriteString(showG(v) + ";")
	}
	b.WriteString("] M[")
	m := r.Map()
	fmt.Fprintf(&b, "%d", len(m))
	fmt.Fprintf(&b, "] VAL=%#v", r.Value())
	return b.String()
}

// mapO and mapG write Map with sorted keys.
func mapO(r ojson.Result) string {
	m := r.Map()
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sortStrings(ks)
	var b strings.Builder
	for _, k := range ks {
		fmt.Fprintf(&b, "%q:%s;", k, showO(m[k]))
	}
	return b.String()
}

func mapG(r gjson.Result) string {
	m := r.Map()
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sortStrings(ks)
	var b strings.Builder
	for _, k := range ks {
		fmt.Fprintf(&b, "%q:%s;", k, showG(m[k]))
	}
	return b.String()
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// TestResultMethods compares the Result methods on values found by plain
// paths, on Parse results and on missing values.
func TestResultMethods(t *testing.T) {
	c := newChecker(t)
	r := rand.New(rand.NewSource(4))
	queries := []string{"#", "#.a", "#.0", "#(a==1)#", "#(a>0)", "#.#", "a.#.b", "*", "a*", "@this", "a|0", "zz", ""}
	for _, doc := range corpus(3000) {
		ps := append(paths(doc, 30), "", "zz", "0", "@this")
		for _, p := range ps {
			in := fmt.Sprintf("%q %q", doc, p)
			o, g := ojson.Get(doc, p), gjson.Get(doc, p)
			c.eq("get", in, showO(o), showG(g))
			c.eq("methods", in, methodsO(o), methodsG(g))
			c.eq("map", in, mapO(o), mapG(g))
			c.eq("path", in, o.Path(doc), g.Path(doc))
			q := queries[r.Intn(len(queries))]
			if r.Intn(2) == 0 && len(ps) > 0 {
				q = ps[r.Intn(len(ps))]
			}
			c.eq("rget", in+" "+strconv.Quote(q), showO(o.Get(q)), showG(g.Get(q)))
		}
		for _, q := range queries {
			o, g := ojson.Get(doc, q), gjson.Get(doc, q)
			c.eq("paths", fmt.Sprintf("%q %q", doc, q), fmt.Sprintf("%#v %q", o.Paths(doc), o.Path(doc)),
				fmt.Sprintf("%#v %q", g.Paths(doc), g.Path(doc)))
		}
		o, g := ojson.Parse(doc), gjson.Parse(doc)
		c.eq("parse", fmt.Sprintf("%q", doc), showO(o)+methodsO(o), showG(g)+methodsG(g))
	}
	// Less over pairs of values, including missing ones and partial
	// literals.
	vals := append(append(append([]string{"", "nul", "tru", "fals", "{}", "[]", "[2]", "[10]", `{"a":1}`}, genStrs...), genNums...), "true", "false", "null")
	for _, a := range vals {
		for _, b := range vals {
			for _, cs := range []bool{false, true} {
				c.eq("Less", fmt.Sprintf("%q %q %v", a, b, cs),
					fmt.Sprint(ojson.Parse(a).Less(ojson.Parse(b), cs)), fmt.Sprint(gjson.Parse(a).Less(gjson.Parse(b), cs)))
			}
		}
	}
	c.done()
}

// benchDocs are a small and a large document for the modifier benchmarks.
func benchDocs() []struct {
	name, doc string
} {
	small := corpus(1)[0]
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; b.Len() < 64<<10; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"name":"item %d","tags":["a","b","c"],"price":%d.5,"ok":true,"dims":[[1,2],[3,4]]}`, i, i, i)
	}
	b.WriteString(`],"total":1}`)
	items := b.String()
	return []struct{ name, doc string }{{"1KiB", small}, {"64KiB", items}, {"64KiB-indented", gjson.Get(items, "@pretty").Raw}}
}

// BenchmarkModifiers compares the built-in modifiers applied at the root.
func BenchmarkModifiers(b *testing.B) {
	mods := []string{"@this", "@reverse", "@ugly", "@pretty", `@pretty:{"sortKeys":true}`, "@valid", "@keys",
		"@values", "@tostr", "@fromstr", "@group", "items|@flatten", `items.#.dims|@flatten:{"deep":true}`,
		"items|@join", "@dig:name", "friends|@join"}
	for _, d := range benchDocs() {
		for _, m := range mods {
			if !gjson.Get(d.doc, m).Exists() || (d.name == "64KiB-indented" && m != "@ugly" && m != "@pretty") {
				continue
			}
			b.Run(d.name+"/"+m+"/gjson", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					gjson.Get(d.doc, m)
				}
			})
			b.Run(d.name+"/"+m+"/ojson", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					ojson.Get(d.doc, m)
				}
			})
		}
	}
}

// validDocs are documents of different shapes for BenchmarkValid.
func validDocs() []struct{ name, doc string } {
	docs := benchDocs()
	var strs, nums strings.Builder
	strs.WriteString(`{"texts":[`)
	for i := 0; strs.Len() < 64<<10; i++ {
		if i > 0 {
			strs.WriteByte(',')
		}
		fmt.Fprintf(&strs, `"%d: the quick brown fox jumps over the lazy dog, then naps in the warm afternoon sun é"`, i)
	}
	strs.WriteString(`]}`)
	nums.WriteByte('[')
	for i := 0; nums.Len() < 64<<10; i++ {
		if i > 0 {
			nums.WriteByte(',')
		}
		fmt.Fprintf(&nums, `%d.%d,-%de%d`, i*7919, i%1000, i, i%30)
	}
	nums.WriteByte(']')
	return append(docs,
		struct{ name, doc string }{"64KiB-strings", strs.String()},
		struct{ name, doc string }{"64KiB-escaped", string(gjson.AppendJSONString(nil, docs[1].doc))},
		struct{ name, doc string }{"64KiB-numbers", nums.String()},
		struct{ name, doc string }{"deep", strings.Repeat(`{"a":[`, 2000) + "1" + strings.Repeat("]}", 2000)})
}

// BenchmarkValid compares Valid on documents of different shapes.
func BenchmarkValid(b *testing.B) {
	for _, d := range validDocs() {
		if !gjson.Valid(d.doc) || !ojson.Valid(d.doc) {
			b.Fatalf("%s is not valid", d.name)
		}
		b.Run(d.name+"/gjson", func(b *testing.B) {
			b.SetBytes(int64(len(d.doc)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				gjson.Valid(d.doc)
			}
		})
		b.Run(d.name+"/ojson", func(b *testing.B) {
			b.SetBytes(int64(len(d.doc)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				ojson.Valid(d.doc)
			}
		})
	}
}

// BenchmarkResultAPI compares the Result methods and functions added for
// the full gjson API.
func BenchmarkResultAPI(b *testing.B) {
	d := benchDocs()[1].doc
	gr, or := gjson.Get(d, "items.7"), ojson.Get(d, "items.7")
	gi, oi := gjson.Get(d, "items.#.id"), ojson.Get(d, "items.#.id")
	ga, oa := gjson.Get(d, "items"), ojson.Get(d, "items")
	gd, od := gjson.Parse(d), ojson.Parse(d)
	gn, gid, on, oid := gr.Get("name"), gr.Get("id"), or.Get("name"), or.Get("id")
	buf := make([]byte, 0, 4*len(d))
	qd := string(gjson.AppendJSONString(nil, d))
	var wide strings.Builder
	wide.WriteByte('{')
	for i := 0; i < 500; i++ {
		if i > 0 {
			wide.WriteByte(',')
		}
		fmt.Fprintf(&wide, `"k%d":{"id":%d}`, i, i)
	}
	wide.WriteByte('}')
	gw, ow := gjson.Parse(wide.String()), ojson.Parse(wide.String())
	cases := []struct {
		name string
		g, o func()
	}{
		{"Value", func() { gr.Value() }, func() { or.Value() }},
		{"Value-64KiB", func() { gd.Value() }, func() { od.Value() }},
		{"ForEach-64KiB", func() { ga.ForEach(func(k, v gjson.Result) bool { return true }) },
			func() { oa.ForEach(func(k, v ojson.Result) bool { return true }) }},
		{"Array-64KiB", func() { ga.Array() }, func() { oa.Array() }},
		{"Map", func() { gr.Map() }, func() { or.Map() }},
		{"Map-500", func() { gw.Map() }, func() { ow.Map() }},
		{"Array-Indexes", func() { gi.Array() }, func() { oi.Array() }},
		{"Path", func() { gr.Path(d) }, func() { or.Path(d) }},
		{"Paths", func() { gi.Paths(d) }, func() { oi.Paths(d) }},
		{"Escape", func() { gjson.Escape("a.b*c?d") }, func() { ojson.Escape("a.b*c?d") }},
		{"AppendJSONString", func() { gjson.AppendJSONString(nil, `<a href="x">é</a>`) }, func() { ojson.AppendJSONString(nil, `<a href="x">é</a>`) }},
		{"Less", func() { gr.Get("name").Less(gr.Get("id"), false) }, func() { or.Get("name").Less(or.Get("id"), false) }},
		{"Less-only", func() { gn.Less(gid, false) }, func() { on.Less(oid, false) }},
		{"AppendJSONString-64KiB", func() { gjson.AppendJSONString(buf[:0], d) }, func() { ojson.AppendJSONString(buf[:0], d) }},
		{"tostr-parse-64KiB", func() { _ = gjson.Parse(qd).String() }, func() { _ = ojson.Parse(qd).String() }},
		{"All", func() {
			for range gr.All() {
			}
		}, func() {
			for range or.All() {
			}
		}},
	}
	for _, c := range cases {
		b.Run(c.name+"/gjson", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				c.g()
			}
		})
		b.Run(c.name+"/ojson", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				c.o()
			}
		})
	}
}
