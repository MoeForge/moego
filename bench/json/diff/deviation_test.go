package diff

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Deviation rule: sjson can turn a valid document into malformed JSON (a
// complex path whose matches do not map to input bytes, or an escaped key
// it mishandles). ojson never does: it writes the correct edit when the
// target is well defined and otherwise leaves the document unchanged with
// a nil error.

// encodeValue is the JSON text sjson writes for v.
func encodeValue(v any) string {
	s, err := sjson.Set(`{"x":0}`, "x", v)
	if err != nil {
		return ""
	}
	return gjson.Get(s, "x").Raw
}

// complexSetPath reports a path sjson does not resolve key by key: one
// with '|', '#', '@', '*' or '?' outside an escape.
func complexSetPath(p string) bool {
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '\\':
			i++
		case '|', '#', '@', '*', '?':
			return true
		}
	}
	return false
}

// mapSpans returns the input spans of the values path matches, and false
// when a match does not map to input bytes.
func mapSpans(in, path string) ([][2]int, bool) {
	if modifierOutput(path) {
		return nil, false
	}
	r := gjson.Get(in, path)
	if !r.Exists() {
		return nil, true
	}
	// A match maps to input bytes only where a value of the document
	// starts and ends: text inside a string value is not addressable.
	vs := valueSpans(in)
	at := func(i int, raw string) ([2]int, bool) {
		sp := [2]int{i, i + len(raw)}
		return sp, i > 0 && i+len(raw) <= len(in) && in[i:i+len(raw)] == raw && vs[sp]
	}
	var spans [][2]int
	if r.Indexes != nil {
		es := r.Array()
		if len(es) != len(r.Indexes) {
			return nil, false
		}
		for k, e := range es {
			sp, ok := at(r.Indexes[k], e.Raw)
			if !ok {
				return nil, false
			}
			spans = append(spans, sp)
		}
	} else {
		sp, ok := at(r.Index, r.Raw)
		if !ok {
			return nil, false
		}
		spans = append(spans, sp)
	}
	sort.Slice(spans, func(a, b int) bool { return spans[a][0] < spans[b][0] })
	for k := 1; k < len(spans); k++ {
		if spans[k][0] < spans[k-1][1] {
			return nil, false
		}
	}
	return spans, true
}

// valueSpans lists the span of every value (not member name) of the
// valid document in.
func valueSpans(in string) map[[2]int]bool {
	spans := map[[2]int]bool{}
	var open []int
	for i := 0; i < len(in); i++ {
		switch in[i] {
		case '{', '[':
			open = append(open, i)
		case '}', ']':
			spans[[2]int{open[len(open)-1], i + 1}] = true
			open = open[:len(open)-1]
		case '"':
			j := i + 1
			for ; in[j] != '"'; j++ {
				if in[j] == '\\' {
					j++
				}
			}
			k := j + 1
			for k < len(in) && strings.IndexByte(" \t\r\n", in[k]) >= 0 {
				k++
			}
			if k >= len(in) || in[k] != ':' {
				spans[[2]int{i, j + 1}] = true
			}
			i = j
		case ' ', '\t', '\r', '\n', ',', ':':
		default:
			j := i
			for j < len(in) && strings.IndexByte(" \t\r\n,]}", in[j]) < 0 {
				j++
			}
			spans[[2]int{i, j}] = true
			i = j - 1
		}
	}
	return spans
}

// sep separates the key from the raw value in flatten's entries; it is
// not valid UTF-8, so decoded member names do not contain it.
const sep = "\xff\xfe\xfd"

// modifierOutput reports a path whose result comes out of a modifier (one
// used outside a query's parentheses): never addressable, even where its
// bytes happen to equal the input's.
func modifierOutput(path string) bool {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(path); i++ {
		switch c := path[i]; {
		case c == '\\':
			i++
		case c == '(':
			depth++
		case c == ')' && depth > 0:
			depth--
		case depth == 0:
			b.WriteByte(c)
		}
	}
	for _, m := range reMod.FindAllStringSubmatch(b.String(), -1) {
		if isMod(m[1]) {
			return true
		}
	}
	return false
}

// flattenDepth caps the nesting flatten descends into; deeper values are
// listed whole.
const flattenDepth = 64

// flatten lists key+sep+raw for every scalar and empty container of a
// valid document, in document order. A key is the normalized path with a
// leading dot ("" for the root), so an empty member name stays distinct. ForEach on a scalar yields the
// scalar itself, so only containers are descended into.
func flatten(doc string) []string {
	var out []string
	var walk func(p string, r gjson.Result, depth int)
	walk = func(p string, r gjson.Result, depth int) {
		if !r.IsObject() && !r.IsArray() || depth >= flattenDepth {
			out = append(out, p+sep+r.Raw)
			return
		}
		n := 0
		r.ForEach(func(k, v gjson.Result) bool {
			c := k.Raw
			if r.IsObject() {
				c = gjson.Escape(k.Str)
			} else {
				c = fmt.Sprint(n)
			}
			n++
			walk(p+"."+c, v, depth+1)
			return true
		})
		if n == 0 {
			out = append(out, p+sep+r.Raw)
		}
	}
	walk("", gjson.Parse(doc), 0)
	return out
}

// without drops the entries at or under path p, and those of its
// ancestors (a parent may become or stop being empty, or a scalar parent
// may be replaced by a container).
func without(es []string, p string) []string {
	var out []string
	for _, e := range es {
		k, _, _ := strings.Cut(e, sep)
		if k == p || strings.HasPrefix(k, p+".") {
			continue
		}
		if k == "" || strings.HasPrefix(p, k+".") {
			continue
		}
		out = append(out, e)
	}
	return out
}

// normPath rewrites a plain Set path in flatten's form: components
// unescaped, without sjson's ':' force-key prefix, then escaped with
// gjson.Escape.
func normPath(p string) string { return sjsonPath("", p) }

// setComp is one component of a plain Set path: as written, and its key
// (unescaped, without the ':' force-key prefix).
type setComp struct{ text, key string }

func setComps(p string) []setComp {
	var cs []setComp
	var b strings.Builder
	start := 0
	for i := 0; i < len(p); i++ {
		switch {
		case p[i] == ':' && b.Len() == 0 && (i == 0 || p[i-1] == '.'):
		case p[i] == '\\':
			// A dangling final backslash escapes nothing.
			if i++; i < len(p) {
				b.WriteByte(p[i])
			}
		case p[i] == '.':
			cs = append(cs, setComp{p[start:i], b.String()})
			b.Reset()
			start = i + 1
		default:
			b.WriteByte(p[i])
		}
	}
	return append(cs, setComp{p[start:], b.String()})
}

// sjsonPath is normPath with sjson's reading of a component that starts
// with '[', '{' or '!': sjson looks it up with gjson.Get on the container,
// and a hit (Index > 0) is the member or element whose value starts
// there (`[v` can name member ""). Otherwise the component is a plain key.
func sjsonPath(doc, p string) string {
	cs := setComps(p)
	out := make([]string, len(cs))
	cur := gjson.Parse(doc)
	for k, c := range cs {
		out[k] = gjson.Escape(c.key)
		if !cur.IsObject() && !cur.IsArray() {
			cur = gjson.Result{}
			continue
		}
		if c.text != "" && strings.IndexByte("[{!", c.text[0]) >= 0 {
			if r := gjson.Get(cur.Raw, c.text); r.Index > 0 {
				if name, at, ok := memberAt(cur.Raw, r.Index); ok {
					out[k], cur = name, gjson.Parse(cur.Raw[at:])
					continue
				}
			}
		}
		cur = step(cur, out[k])
	}
	return strings.Join(out, ".")
}

// resolvedPath is path as sjson reads it without its lookup bug: each
// component whose key starts with '[', '{' or '!' (also after the ':'
// force-key prefix, where sjson misses the member too) is replaced by the
// escaped name of the member or element it addresses (the plain key when
// the lookup misses); the other components stay as written, so sjson's
// own reading of them (`\0` appends to an existing array) holds.
func resolvedPath(doc, p string) string {
	cs := setComps(p)
	names := splitPath(sjsonPath(doc, p))
	if len(names) != len(cs) {
		return p
	}
	out := make([]string, len(cs))
	for k, c := range cs {
		out[k] = c.text
		if c.key != "" && strings.IndexByte("[{!", c.key[0]) >= 0 {
			out[k] = names[k]
		}
	}
	return strings.Join(out, ".")
}

// memberAt names the member (escaped key) or element (index) of the valid
// container raw whose value starts at offset off, and returns off.
func memberAt(raw string, off int) (string, int, bool) {
	obj := strings.TrimLeft(raw, " \t\r\n")[0] == '{'
	name := func(key string, idx int) string {
		if obj {
			return gjson.Escape(key)
		}
		return strconv.Itoa(idx)
	}
	depth, idx, key := 0, 0, ""
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if depth == 1 && i == off && strings.IndexByte(" \t\r\n,:}]", c) < 0 {
			if c != '"' || !obj || !isKey(raw, i) {
				return name(key, idx), off, true
			}
		}
		switch c {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		case ',':
			if depth == 1 {
				idx++
			}
		case '"':
			j := i + 1
			for ; raw[j] != '"'; j++ {
				if raw[j] == '\\' {
					j++
				}
			}
			if depth == 1 && obj && isKey(raw, i) {
				key = gjson.Parse(raw[i : j+1]).Str
			}
			i = j
		}
	}
	return "", 0, false
}

// isKey reports whether the string starting at raw[i] is a member name.
func isKey(raw string, i int) bool {
	j := i + 1
	for ; raw[j] != '"'; j++ {
		if raw[j] == '\\' {
			j++
		}
	}
	for j++; j < len(raw) && strings.IndexByte(" \t\r\n", raw[j]) >= 0; j++ {
	}
	return j < len(raw) && raw[j] == ':'
}

// deviationKind classifies a Set-family case where sjson's result breaks the
// deviation rule, or returns "": "malformed" (sjson made malformed JSON),
// "unmapped" (a complex path whose matches do not map to input bytes, yet
// sjson changed the document), "string" (a plain path that sjson followed
// into the text of a string value) or "dupkey" (sjson appended a member
// whose name the parent object already has, instead of replacing it).
func deviationKind(in, path, ws string, we error, val string, del bool) string {
	switch {
	case !gjson.Valid(in) || !del && !gjson.Valid(val):
		return ""
	case we != nil:
		// sjson fails on a plain path where it reads a string's text as
		// JSON ("cannot set array element for non-numeric key"): the
		// string deviation. Other errors stay exact.
		if !complexSetPath(path) && intoString(in, sjsonPath(in, path)) {
			return "string"
		}
		return ""
	case !gjson.Valid(ws):
		return "malformed"
	case complexSetPath(path):
		if _, ok := mapSpans(in, path); !ok && ws != in {
			return "unmapped"
		}
	default:
		t := sjsonPath(in, path)
		if intoString(in, t) || stringEdit(in, ws, "."+canonPath(ws, t)) {
			return "string"
		}
		if !del {
			// A duplicate made at the final component or at any
			// intermediate one.
			comps := splitPath(t)
			for k := 1; k <= len(comps); k++ {
				p := strings.Join(comps[:k], ".")
				if n := keyCount(in, p); n > 0 && keyCount(ws, p) > n {
					return "dupkey"
				}
			}
		}
	}
	return ""
}

// intoString reports a plain target that goes on past a string value of
// in, or whose document is a string.
func intoString(in, t string) bool {
	cut := cutPoint(in, t)
	return cut != t && walk(in, cut).Type == gjson.String || gjson.Parse(in).Type == gjson.String
}

// canonPath writes the components of a normalized target that index an
// array of doc as flatten does: "00" reads as index 0, and "-1" (sjson:
// the last element, appended by Set) as the last index.
func canonPath(doc, target string) string {
	comps := splitPath(target)
	cur := gjson.Parse(doc)
	for k, c := range comps {
		if !cur.Exists() {
			break
		}
		if cur.IsArray() && c != "" && strings.Trim(c, "0123456789") == "" {
			if n, err := strconv.Atoi(c); err == nil {
				comps[k] = strconv.Itoa(n)
			}
		}
		if n := len(cur.Array()); cur.IsArray() && c == "-1" && n > 0 {
			comps[k] = strconv.Itoa(n - 1)
		}
		cur = step(cur, comps[k])
	}
	return strings.Join(comps, ".")
}

// fffd replaces each byte of s that is not valid UTF-8 with U+FFFD.
func fffd(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 {
			b.WriteString("\uFFFD")
		} else {
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}

// walk resolves a normalized path component by component: the first
// member of a name, or an array index. Unlike a gjson path it reaches
// names such as "" ("a..b" is not a JSON Lines path here). "@this" is the
// root.
func walk(doc, target string) gjson.Result {
	cur := gjson.Parse(doc)
	if target == "@this" {
		return cur
	}
	for _, c := range splitPath(target) {
		if cur = step(cur, c); !cur.Exists() {
			break
		}
	}
	return cur
}

// step is one component of walk.
func step(cur gjson.Result, c string) gjson.Result {
	var next gjson.Result
	switch {
	case cur.IsObject():
		key := unescape(c)
		cur.ForEach(func(k, v gjson.Result) bool {
			if k.Str == key {
				next = v
				return false
			}
			return true
		})
	case cur.IsArray():
		if n, err := strconv.Atoi(c); err == nil && n >= 0 {
			if es := cur.Array(); n < len(es) {
				next = es[n]
			}
		}
	}
	return next
}

// stringEdit reports whether ws differs from in only inside the text of
// string values other than the target tkey: the same members and elements,
// and every changed value a string in both. sjson does this when it
// follows a path into a string's characters.
func stringEdit(in, ws, tkey string) bool {
	a, b := flatten(in), flatten(ws)
	if len(a) != len(b) || in == ws {
		return false
	}
	for i := range a {
		ka, va, _ := strings.Cut(a[i], sep)
		kb, vb, _ := strings.Cut(b[i], sep)
		if ka != kb {
			return false
		}
		if va != vb && (ka == tkey || !strings.HasPrefix(va, `"`) || !strings.HasPrefix(vb, `"`)) {
			return false
		}
	}
	return true
}

// keyCount counts the members of target's parent object named by its last
// component, or returns -1 when the parent is not an object.
func keyCount(doc, target string) int {
	comps := splitPath(target)
	parent := walk(doc, parentOf(target))
	if !parent.IsObject() {
		return -1
	}
	key := unescape(comps[len(comps)-1])
	n := 0
	parent.ForEach(func(k, _ gjson.Result) bool {
		if k.Str == key {
			n++
		}
		return true
	})
	return n
}

// unescape drops the escaping backslashes of a path component.
func unescape(c string) string {
	var b strings.Builder
	for i := 0; i < len(c); i++ {
		if c[i] == '\\' && i+1 < len(c) {
			i++
		}
		b.WriteByte(c[i])
	}
	return b.String()
}

// deviationCheck holds ojson's result gs, ge for a deviation case to the rule;
// it returns why the result breaks it, or "".
func deviationCheck(in, path, gs string, ge error, val string, del bool) string {
	if ge != nil {
		return "error " + ge.Error()
	}
	if !gjson.Valid(gs) {
		return "ojson output is not valid JSON"
	}
	if complexSetPath(path) {
		spans, ok := mapSpans(in, path)
		if !ok {
			if gs != in {
				return "unverified: changed a target that does not map to input bytes"
			}
			return ""
		}
		var b strings.Builder
		last := 0
		for _, sp := range spans {
			b.WriteString(in[last:sp[0]] + val)
			last = sp[1]
		}
		b.WriteString(in[last:])
		if exp := b.String(); gs != exp {
			return fmt.Sprintf("not the edit at the matched positions %q", exp)
		}
		return ""
	}
	// A plain path is well defined: a Set must be made, a Delete must
	// remove the value if it exists, and nothing outside the target may
	// change. sjson's own edit on the resolved path also holds: there it
	// may append a member whose new name, written with U+FFFD for bytes
	// that are not valid UTF-8, equals an existing one.
	why := plainCheck(in, path, gs, val, del)
	if why != "" {
		var alt string
		var err error
		if rp := resolvedPath(in, path); del {
			alt, err = sjson.Delete(in, rp)
		} else {
			alt, err = sjson.SetRaw(in, rp, val)
		}
		if err == nil && alt == gs && gjson.Valid(alt) {
			return ""
		}
	}
	return why
}

// plainCheck is deviationCheck for a plain path.
func plainCheck(in, path, gs, val string, del bool) string {
	if dupKeys(in) {
		return ""
	}
	if dupKeys(gs) {
		return "created a duplicate key"
	}
	target := sjsonPath(in, path)
	if del {
		target = canonPath(in, target)
	} else {
		target = canonPath(gs, target)
	}
	tkey := "." + target
	fin, fout := flatten(in), flatten(gs)
	if del && len(under(fin, tkey)) == 0 {
		if gs != in {
			return "deleted a value that does not exist"
		}
		return ""
	}
	// An empty component (`a.` or `a..b`) appends to an array under
	// sjson's reading, so its final index is not known here: the value
	// check is skipped and only the rest of the rule is checked. An
	// escaped dot (`a\.`) is part of a key, not a separator.
	emptyComp := false
	for _, c := range splitPath(target) {
		emptyComp = emptyComp || c == ""
	}
	if del {
		if gs == in {
			return "unchanged although the value exists"
		}
		if parent := walk(in, parentOf(target)); parent.IsArray() {
			if n := len(walk(gs, parentOf(target)).Array()); n != len(parent.Array())-1 {
				return "array did not shrink by one"
			}
			return ""
		}
		if len(under(fout, tkey)) != 0 {
			return "value still present after delete"
		}
	} else {
		if len(under(fout, tkey)) == 0 && !utf8.ValidString(target) {
			// A new member name that is not valid UTF-8 is written with
			// U+FFFD for each bad byte, as gjson's AppendJSONString does.
			target = canonPath(gs, fffd(target))
			tkey = "." + target
		}
		want := flatten(val)
		for i, e := range want {
			want[i] = tkey + e
		}
		if got := under(fout, tkey); !emptyComp && strings.Join(got, "\n") != strings.Join(want, "\n") {
			return "the target does not hold the value"
		}
		if gs == in {
			return "" // the target already held the value
		}
	}
	if root := gjson.Parse(in); !root.IsObject() && !root.IsArray() {
		return "" // a scalar root is replaced as a whole
	}
	cut := cutPoint(in, target)
	if emptyComp {
		comps := splitPath(target)
		k := 0
		for comps[k] != "" {
			k++
		}
		if k == 0 {
			return ""
		}
		if k < len(splitPath(cut)) {
			cut = strings.Join(comps[:k], ".")
		}
	}
	a, b := without(fin, "."+cut), without(fout, "."+cut)
	if parent := walk(in, parentOf(cut)); parent.IsArray() {
		// Padding of an array grown up to the index.
		pk := "." + parentOf(cut)
		if pk == ".@this" {
			pk = ""
		}
		b = withoutFrom(b, pk, len(parent.Array()))
	}
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		return "values outside the target changed"
	}
	return ""
}

// under lists the entries at or under path p.
func under(es []string, p string) []string {
	var out []string
	for _, e := range es {
		if k, _, _ := strings.Cut(e, sep); k == p || strings.HasPrefix(k, p+".") {
			out = append(out, e)
		}
	}
	return out
}

// splitPath splits a normalized path at unescaped dots.
func splitPath(p string) []string {
	var comps []string
	last := 0
	for i := 0; i < len(p); i++ {
		if p[i] == '\\' {
			i++
		} else if p[i] == '.' {
			comps = append(comps, p[last:i])
			last = i + 1
		}
	}
	return append(comps, p[last:])
}

// cutPoint is the shortest prefix of the normalized target that is not a
// container in in: everything under it may be replaced by the edit.
func cutPoint(in, target string) string {
	comps := splitPath(target)
	for k := 1; k <= len(comps); k++ {
		p := strings.Join(comps[:k], ".")
		if r := walk(in, p); !r.IsObject() && !r.IsArray() {
			return p
		}
	}
	return target
}

// withoutFrom drops the elements of array p from index n on.
func withoutFrom(es []string, p string, n int) []string {
	var out []string
	for _, e := range es {
		k, _, _ := strings.Cut(e, sep)
		if rest, ok := strings.CutPrefix(k, p+"."); ok {
			c, _, _ := strings.Cut(rest, ".")
			if i, err := strconv.Atoi(c); err == nil && i >= n {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// parentOf drops the last component of a normalized path ("@this" for a
// single component). Components are split as splitPath does, so `\\.`
// is an escaped backslash before a separator.
func parentOf(p string) string {
	comps := splitPath(p)
	if len(comps) == 1 {
		return "@this"
	}
	return strings.Join(comps[:len(comps)-1], ".")
}
