package ojson

import (
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
	"unsafe"
)

// ectx is the state of one evaluation of a path against a document: the
// value found so far and the pipe to run on it at the end. As in gjson, a
// component that ends in a pipe sets the pipe when it is read, before any
// key matches it.
type ectx struct {
	json  string
	value Result
	shift int // offset the piped value adds to the indexes of the pipe result
	pipe  string
	piped bool
	// done is 1 + the offset of a container whose evaluation with the rest
	// of the path doneRest getQuery has already made, or 0: it is not made
	// again.
	done     int
	doneRest string
}

// getComplex evaluates paths with queries, wildcards, modifiers, pipes,
// multipaths, literals or JSON Lines.
func getComplex(json, path string) Result {
	if len(path) > 1 {
		if path[0] == '@' && !DisableModifiers || path[0] == '!' {
			var rest, out string
			var ok bool
			if path[0] == '@' {
				rest, out, ok = callModifier(json, path)
			} else {
				rest, out, ok = literalAt(path)
			}
			if ok {
				if rest != "" && (rest[0] == '|' || rest[0] == '.') {
					r := Get(out, rest[1:])
					r.Index, r.Indexes = 0, nil
					return r
				}
				return parseOut(out)
			}
		}
		if path[0] == '[' || path[0] == '{' {
			r, rest, ok := multipath(json, path)
			if ok {
				return r
			}
			// What follows a multipath that is not a pipe or a dot is
			// read as a path, from the root.
			path = rest
		}
	}
	c := ectx{json: json}
	return c.walk(path)
}

// walk evaluates path from the root of c.json.
func (c *ectx) walk(path string) Result {
	json := c.json
	if len(path) >= 2 && path[0] == '.' && path[1] == '.' {
		c.array(0, path[2:], true)
	} else if i := rootOpen(json); i >= 0 {
		if json[i] == '{' {
			c.object(i+1, path)
		} else {
			c.array(i+1, path, false)
		}
	}
	return c.result()
}

// enter evaluates rest on the container at json[vs].
func (c *ectx) enter(vs int, rest string) bool {
	if vs+1 == c.done && rest == c.doneRest {
		return false
	}
	if c.json[vs] == '{' {
		return c.object(vs+1, rest)
	}
	return c.array(vs+1, rest, false)
}

// result is the value found, with the pipe applied.
func (c *ectx) result() Result {
	if c.piped {
		v := c.value
		v.Index = c.shift
		r := getRel(v, c.pipe)
		r.Index = 0
		return r
	}
	return c.value
}

// rootOpen returns the index of the first '{' or '[' in json, where gjson
// starts every path, or -1.
func rootOpen(json string) int {
	for i := 0; i < len(json); i++ {
		if json[i] == '{' || json[i] == '[' {
			return i
		}
	}
	return -1
}

// getRel is Result.Get: the indexes of the result count from the start of
// the document t came from.
func getRel(t Result, path string) Result { return getAt(t.Raw, t.Index, path) }

// offsetIn returns the offset of s inside json, or 0 when s does not point
// into json.
func offsetIn(s, json string) int {
	if !within(s, json) {
		return 0
	}
	return int(uintptr(unsafe.Pointer(unsafe.StringData(s))) - uintptr(unsafe.Pointer(unsafe.StringData(json))))
}

// object evaluates path against the members of the object whose first
// member starts at or after c.json[i]. It reports whether the path resolved
// here; on false, the caller goes on with later members of its own.
func (c *ectx) object(i int, path string) bool {
	oc := readObjComp(path)
	if !oc.more && oc.piped {
		c.pipe, c.piped = oc.pipe, true
	}
	json := c.json
	if !oc.wild {
		// A plain key: the member loop of plain paths finds it.
		kw, km := keyWord(oc.key, 0, len(oc.key))
		for {
			vs := memberValue(json, i, oc.key, kw, km)
			if vs < 0 {
				return false
			}
			if c.member(vs, &oc) {
				return true
			}
			if i = skipValue(json, vs); i < 0 {
				return false
			}
		}
	}
	for {
		ks, ke, vs, kesc, ok := nextMember(json, i)
		if !ok {
			return false
		}
		if keyMatch(json[ks+1:ke-1], kesc, &oc) && c.member(vs, &oc) {
			return true
		}
		if i = skipValue(json, vs); i < 0 {
			return false
		}
	}
}

// member evaluates the rest of the component oc against the value of a
// member whose key matched it, at c.json[vs].
func (c *ectx) member(vs int, oc *objComp) bool {
	json := c.json
	if !oc.more {
		ve, vesc := valueSpan(json, vs)
		if ve < 0 {
			return false
		}
		c.value, c.shift = resultAt(json, vs, ve, vesc), 0
		return true
	}
	if k := json[vs]; k == '{' || k == '[' {
		return c.enter(vs, oc.rest)
	}
	return false
}

// keyMatch reports whether the member key raw, without quotes, matches the
// component.
func keyMatch(raw string, esc bool, oc *objComp) bool {
	if !oc.wild {
		return keyIs(raw, esc, oc.key)
	}
	if esc {
		b, _ := unescape(nil, raw)
		raw = bytesView(b)
	}
	return globMatch(raw, oc.key)
}

// array evaluates path against the elements of the array that start at or
// after c.json[i]. lines reads the elements as JSON Lines, up to the end of
// the document.
func (c *ectx) array(i int, path string, lines bool) bool {
	ac := readArrComp(path)
	idx := -1
	if !ac.hash {
		if n, ok := arrayIndex(ac.part); ok {
			idx = n
		}
	}
	if !ac.more && ac.piped {
		c.pipe, c.piped = ac.pipe, true
	}
	alogKey := ac.alogKey
	if ac.alog {
		if l, r, ok := pipeSplit(alogKey); ok {
			alogKey = l
			c.pipe, c.piped = r, true
		}
	}
	json := c.json
	var multi, alog []byte
	var ixs indexList
	split := false
	h := 0
	for {
		for i < len(json) && (json[i] <= ' ' || json[i] == ',') {
			i++
		}
		if i >= len(json) || json[i] == ']' {
			break
		}
		vs := i
		ve, vesc := valueSpan(json, vs)
		if ve <= vs {
			break
		}
		i = ve
		if h == idx {
			if !ac.more {
				c.value, c.shift = resultAt(json, vs, ve, vesc), 0
				return true
			}
			if k := json[vs]; k == '{' || k == '[' {
				return c.enter(vs, ac.rest)
			}
			return false
		}
		h++
		if ac.alog {
			r := Get(json[vs:ve], alogKey)
			if r.Exists() {
				if alog == nil {
					alog = append(make([]byte, 0, 64), '[')
				} else {
					alog = append(alog, ',')
				}
				alog = appendRaw(alog, r)
				at := r.Index
				switch {
				case r.Indexes != nil:
					at = 0
				case json[vs] == '{' || json[vs] == '[':
					at += vs
				}
				ixs.add(at)
			}
			continue
		}
		if !ac.q.on {
			continue
		}
		ev := resultAt(json, vs, ve, vesc)
		qv := ev
		if ev.Type == JSON {
			qv = Get(ev.Raw, ac.q.path)
		} else if ac.q.path != "" {
			continue // a key path never matches a scalar
		}
		if !queryMatch(&ac.q, qv) {
			continue
		}
		if ac.more && !split {
			split = true
			if l, r, ok := pipeSplit(ac.rest); ok {
				ac.rest = l
				c.pipe, c.piped = r, true
			}
		}
		res := ev
		res.Index = 0
		if ac.more {
			res = Get(ev.Raw, ac.rest)
		}
		if !ac.q.all {
			c.value, c.shift = res, res.Index
			c.value.Index = 0
			if !ac.more || !copiesTail(ac.rest) {
				c.value.Index = offsetIn(res.Raw, json)
			}
			return true
		}
		if res.Exists() {
			if multi == nil {
				multi = append(make([]byte, 0, 64), '[')
			} else {
				multi = append(multi, ',')
			}
			multi = appendRaw(multi, res)
			ixs.add(vs + res.Index)
		}
	}
	if ac.part == "#" {
		if ac.alog {
			c.value = Result{Type: JSON, Raw: "[]", Indexes: []int{}}
			if ixs.len() > 0 {
				c.value.Raw, c.value.Indexes = ixs.result(append(alog, ']'))
			}
		} else {
			c.value = Result{Type: Number, Raw: countText(h), Num: float64(h)}
		}
		c.shift = 0
		return true
	}
	if !c.value.Exists() {
		if ixs.len() > 0 {
			c.value = Result{Type: JSON}
			c.value.Raw, c.value.Indexes = ixs.result(append(multi, ']'))
		} else if ac.q.all {
			c.value = Result{Type: JSON, Raw: "[]"}
		}
		c.shift = 0
	}
	return false
}

// indexList gathers the indexes of a '#.' or '#(...)#' result: up to 16
// on the stack, stored with the text of the result in one allocation
// (withIndexes); past that, in a slice of their own, so a large result never
// copies its text.
type indexList struct {
	n     int
	small [16]int
	big   []int
}

func (l *indexList) add(i int) {
	if l.big == nil && l.n < len(l.small) {
		l.small[l.n] = i
		l.n++
		return
	}
	if l.big == nil {
		l.big = append(make([]int, 0, 4*len(l.small)), l.small[:]...)
	}
	l.big = append(l.big, i)
}

func (l *indexList) len() int {
	if l.big != nil {
		return len(l.big)
	}
	return l.n
}

// result returns the text in buf as a string, with the indexes.
func (l *indexList) result(buf []byte) (string, []int) {
	if l.big != nil {
		return bytesView(buf), l.big[:len(l.big):len(l.big)]
	}
	return withIndexes(buf, l.small[:l.n])
}

// withIndexes returns the text in buf as a string, and a copy of idx stored
// in the same allocation after the text. Neither holds pointers, so one
// object can carry both; buf grows once when its spare room is too small.
// idx must not be empty.
func withIndexes(buf []byte, idx []int) (string, []int) {
	const size = int(unsafe.Sizeof(idx[0]))
	n := len(buf)
	if need := size - 1 + len(idx)*size; cap(buf)-n < need {
		buf = slices.Grow(buf, need)
	}
	// Align the indexes in memory, whatever the alignment of buf.
	end := uintptr(unsafe.Pointer(unsafe.SliceData(buf))) + uintptr(n)
	at := n + (size-int(end%uintptr(size)))%size
	ix := unsafe.Slice((*int)(unsafe.Pointer(&buf[:cap(buf)][at])), len(idx))
	copy(ix, idx)
	return bytesView(buf), ix
}

// countText is strconv.Itoa(n), cut for counts below 1000 from text made
// once, so that '#' allocates nothing (strconv itself does not below 100).
func countText(n int) string {
	if n >= 100 && n < 1000 {
		i := (n - 100) * 3
		return countDigits()[i : i+3]
	}
	return strconv.Itoa(n)
}

var countDigits = sync.OnceValue(func() string {
	b := make([]byte, 0, 900*3)
	for n := 100; n < 1000; n++ {
		b = strconv.AppendInt(b, int64(n), 10)
	}
	return string(b)
})

// copiesTail reports whether path applies, outside queries and multipaths,
// a modifier that gjson always gives new text but ojson may answer with its
// input: @ugly of compact JSON. Such a result is not the input's text, so it
// has no Index there.
func copiesTail(path string) bool {
	depth := 0
	for i := 0; i < len(path); i++ {
		switch c := path[i]; c {
		case '\\':
			i++
		case '"':
			if depth > 0 {
				if e := stringEnd(path, i+1); e > i {
					i = e - 1
				}
			}
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		case '@':
			if depth == 0 && (i == 0 || path[i-1] == '.' || path[i-1] == '|') && strings.HasPrefix(path[i+1:], "ugly") {
				if j := i + 5; j == len(path) || path[j] == '.' || path[j] == '|' || path[j] == ':' {
					return true
				}
			}
		}
	}
	return false
}

// appendRaw appends the raw text of r, or its string form when it has none.
func appendRaw(dst []byte, r Result) []byte {
	if r.Raw != "" {
		return append(dst, r.Raw...)
	}
	return append(dst, r.String()...)
}

// queryMatch reports whether the value v satisfies the query.
func queryMatch(q *query, v Result) bool {
	val := q.val
	if len(val) > 0 && val[0] == '~' {
		var ish bool
		switch val[1:] {
		case "*":
			ish = v.Exists()
		case "null":
			ish = v.Type == Null
		case "true":
			ish = truthy(v)
		case "false":
			ish = falsy(v)
		default:
			return false
		}
		val = "true"
		v = Result{Type: False}
		if ish {
			v.Type = True
		}
	}
	if !v.Exists() {
		return false
	}
	if q.op == "" {
		return true
	}
	switch v.Type {
	case String:
		switch q.op {
		case "=":
			return v.Str == val
		case "!=":
			return v.Str != val
		case "<":
			return v.Str < val
		case "<=":
			return v.Str <= val
		case ">":
			return v.Str > val
		case ">=":
			return v.Str >= val
		case "%":
			return globMatch(v.Str, val)
		case "!%":
			return !globMatch(v.Str, val)
		}
	case Number:
		n, _ := strconv.ParseFloat(val, 64)
		switch q.op {
		case "=":
			return v.Num == n
		case "!=":
			return v.Num != n
		case "<":
			return v.Num < n
		case "<=":
			return v.Num <= n
		case ">":
			return v.Num > n
		case ">=":
			return v.Num >= n
		}
	case True:
		switch q.op {
		case "=":
			return val == "true"
		case "!=":
			return val != "true"
		case ">":
			return val == "false"
		case ">=":
			return true
		}
	case False:
		switch q.op {
		case "=":
			return val == "false"
		case "!=":
			return val != "false"
		case "<":
			return val == "true"
		case "<=":
			return true
		}
	}
	return false
}

// truthy reports whether a "~true" query sees v as true: true, a nonzero
// number or a string that parses as true.
func truthy(v Result) bool {
	switch v.Type {
	case True:
		return true
	case String:
		b, err := strconv.ParseBool(v.Str)
		return err == nil && b
	case Number:
		return v.Num != 0
	}
	return false
}

// falsy reports whether a "~false" query sees v as false: missing, null,
// false, zero or a string that parses as false.
func falsy(v Result) bool {
	switch v.Type {
	case Null, False:
		return true
	case String:
		b, err := strconv.ParseBool(v.Str)
		return err == nil && !b
	case Number:
		return v.Num == 0
	}
	return false
}

// globMatch reports whether s matches pattern, where '*' matches any run of
// characters, '?' any one character and '\' makes the next character
// literal. As in gjson, characters compare as runes, and each byte that is
// not part of valid UTF-8 reads as U+FFFD. It backtracks only to the last
// '*', so it runs in O(len(s)*len(pattern)).
func globMatch(s, pattern string) bool {
	si, pi := 0, 0
	starP, starS := -1, 0
	for {
		if pi < len(pattern) {
			switch pattern[pi] {
			case '*':
				starP, starS = pi, si
				pi++
				continue
			case '?':
				if si < len(s) {
					_, n := utf8.DecodeRuneInString(s[si:])
					si += n
					pi++
					continue
				}
			case '\\':
				if pi+1 < len(pattern) && si < len(s) {
					if n, m := sameRune(s, si, pattern, pi+1); n > 0 {
						si += n
						pi += 1 + m
						continue
					}
				}
			default:
				if si < len(s) {
					if n, m := sameRune(s, si, pattern, pi); n > 0 {
						si += n
						pi += m
						continue
					}
				}
			}
		} else if si == len(s) {
			return true
		}
		if starP < 0 || starS >= len(s) {
			return false
		}
		_, n := utf8.DecodeRuneInString(s[starS:])
		starS += n
		si, pi = starS, starP+1
	}
}

// sameRune returns the widths of the characters at s[i] and p[j] when they
// are equal as globMatch compares them, or 0, 0.
func sameRune(s string, i int, p string, j int) (n, m int) {
	if c, d := s[i], p[j]; c < utf8.RuneSelf || d < utf8.RuneSelf {
		if c == d {
			return 1, 1
		}
		return 0, 0
	}
	r, n := utf8.DecodeRuneInString(s[i:])
	q, m := utf8.DecodeRuneInString(p[j:])
	if r != q {
		return 0, 0
	}
	return n, m
}

// parseOut reads the value that starts json, after any leading whitespace,
// as gjson's Parse does for the output of modifiers and literals: numbers
// may start with '+', 'i', 'I', 'N' or "na", and nothing is validated.
func parseOut(json string) Result {
	for i := 0; i < len(json); i++ {
		c := json[i]
		if c == '{' || c == '[' {
			return Result{Type: JSON, Raw: json[i:], Index: i}
		}
		if c <= ' ' {
			continue
		}
		var r Result
		switch c {
		case '+', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', 'i', 'I', 'N':
			r = numberOut(json[i:])
		case 'n':
			if i+1 < len(json) && json[i+1] != 'u' {
				r = numberOut(json[i:])
			} else {
				r = Result{Type: Null, Raw: wordOut(json[i:])}
			}
		case 't':
			r = Result{Type: True, Raw: wordOut(json[i:])}
		case 'f':
			r = Result{Type: False, Raw: wordOut(json[i:])}
		case '"':
			if end := stringEnd(json, i+1); end < 0 {
				r = stringResult(json, i, len(json), false)
			} else {
				r = stringResult(json, i, end, true)
			}
		default:
			return Result{}
		}
		r.Index = i
		return r
	}
	return Result{}
}

// numberOut reads a number up to whitespace, ',', ']' or '}'.
func numberOut(s string) Result {
	i := 1
	for i < len(s) && s[i] > ' ' && s[i] != ',' && s[i] != ']' && s[i] != '}' {
		i++
	}
	n, _ := strconv.ParseFloat(s[:i], 64)
	return Result{Type: Number, Raw: s[:i], Num: n}
}

// wordOut reads a literal up to the first byte that is not a lowercase
// letter.
func wordOut(s string) string {
	i := 1
	for i < len(s) && s[i] >= 'a' && s[i] <= 'z' {
		i++
	}
	return s[:i]
}

// callModifier runs the modifier that path starts with on json. It returns
// the path after the modifier and its argument, and the output.
func callModifier(json, path string) (rest, out string, ok bool) {
	name := path[1:]
	hasArg := false
scan:
	for i := 1; i < len(path); i++ {
		switch path[i] {
		case ':':
			name, rest = path[1:i], path[i+1:]
			hasArg = rest != ""
			break scan
		case '|', '.':
			name, rest = path[1:i], path[i:]
			break scan
		}
	}
	if !modifierKnown(name) {
		return rest, "", false
	}
	var arg string
	if hasArg {
		n := 0
		switch rest[0] {
		case '{', '[', '"':
			if parseOut(rest).Exists() {
				n = len(squashText(rest, false))
			}
		}
		if n == 0 {
			for n < len(rest) && rest[n] != '|' {
				switch rest[n] {
				case '{', '[', '"', '(':
					n += len(squashText(rest[n:], false)) - 1
				}
				n++
			}
		}
		arg, rest = rest[:n], rest[n:]
	}
	out, ok = applyModifier(name, json, arg)
	return rest, out, ok
}

// squashText returns the bracketed value or string that s starts with,
// skipping nested brackets and strings, or all of s when it does not end.
// open treats the first byte as an opening bracket whatever it is, as
// literals do.
func squashText(s string, open bool) string {
	i, depth := 0, 0
	if open || s[0] != '"' {
		i, depth = 1, 1
	}
	for ; i < len(s); i++ {
		switch s[i] {
		case '"':
			e := quotedEnd(s, i)
			if e < 0 {
				return s
			}
			if depth == 0 {
				return s[:e]
			}
			i = e - 1
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			if depth--; depth == 0 {
				return s[:i+1]
			}
		}
	}
	return s
}

// literalAt reads the literal that path starts with: "!" and a JSON value.
// It returns the path after the literal and the literal text.
func literalAt(path string) (rest, out string, ok bool) {
	name := path[1:]
	switch name[0] {
	case '{', '[', '"', '+', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		out = squashText(name, true)
		return name[len(out):], out, true
	}
	for i := 1; i < len(path); i++ {
		if path[i] == '|' || path[i] == '.' {
			name, rest = path[1:i], path[i:]
			break
		}
	}
	switch strings.ToLower(name) {
	case "true", "false", "null", "nan", "inf":
		return rest, name, true
	}
	return rest, "", false
}

// multipath evaluates the multipath that path starts with. When path does
// not start with one followed by nothing, a '|' or a '.', it reports false
// and returns the path after the multipath, or "" when it is not closed.
func multipath(json, path string) (Result, string, bool) {
	var sbuf [8]subSel
	sels, rest, ok := readSubs(path, sbuf[:0])
	if !ok || rest != "" && rest[0] != '|' && rest[0] != '.' {
		return Result{}, rest, false
	}
	// The values and names first, so that the output is allocated once.
	obj := path[0] == '{'
	var vbuf, nbuf [8]string
	var qbuf [8]bool
	var pbuf [8]string
	var spbuf [8]span
	vals, names, quoted := vbuf[:0], nbuf[:0], qbuf[:0]
	hasSpans := false
	if len(sels) > 1 && len(sels) <= len(pbuf) && !scalarRoot(json) {
		paths := pbuf[:0]
		for _, s := range sels {
			paths = append(paths, s.path)
		}
		hasSpans = rootSpans(json, paths, spbuf[:len(sels)])
	}
	size := 2
	for idx, s := range sels {
		var r Result
		if hasSpans {
			sp := spbuf[idx]
			if sp.vs >= 0 && sp.ve >= 0 {
				r = resultAt(json, sp.vs, sp.ve, sp.esc)
			} else if sp.vs != -1 || !plainComps(s.path) {
				r = Get(json, s.path)
			}
		} else {
			r = Get(json, s.path)
		}
		if !r.Exists() {
			continue
		}
		raw := r.Raw
		if raw == "" {
			if raw = r.String(); raw == "" {
				raw = "null"
			}
		}
		vals = append(vals, raw)
		size += len(raw) + 1
		if obj {
			// A key given as a JSON string is written as it is.
			name, q := s.name, s.name != "" && s.name[0] == '"' && Valid(s.name)
			if name == "" {
				if name = lastComp(s.path); !simpleName(name) {
					name = "_"
				}
			}
			names, quoted = append(names, name), append(quoted, q)
			size += len(name) + 3
		}
	}
	b := makeNoZero(size)[:0]
	b = append(b, path[0])
	for k, raw := range vals {
		if k > 0 {
			b = append(b, ',')
		}
		if obj {
			if quoted[k] {
				b = append(b, names[k]...)
			} else {
				b = AppendJSONString(b, names[k])
			}
			b = append(b, ':')
		}
		b = append(b, raw...)
	}
	b = append(b, path[0]+2)
	res := Result{Type: JSON, Raw: bytesView(b)}
	if rest != "" {
		res = getRel(res, rest[1:])
	}
	res.Index = 0
	return res, "", true
}

// getPipe is Get for a path "left|right" whose left part is a plain path:
// right is read from the value left names, as the pipe does, and the
// result has Index 0. The pipe applies to a missing value too, once the
// component before it has been read, so a missing single key in an object
// or array reads right from nothing. getPipe reports false for other
// paths, and for a left part of several keys that is missing, which the
// full evaluation decides.
func getPipe(json, path string) (Result, bool) {
	k, dot := 0, false
	for ; k < len(path) && path[k] != '|'; k++ {
		switch path[k] {
		case '\\':
			k++
		case '.':
			dot = true
		}
	}
	if k == 0 || k >= len(path) {
		return Result{}, false
	}
	left := path[:k]
	if queryStart(left) || !plainComps(left) {
		return Result{}, false
	}
	vs, ve, _ := getSpan(json, left)
	if vs < 0 || ve < 0 {
		if vs != -1 || dot || scalarRoot(json) || skipSpace(json, 0) == len(json) {
			return Result{}, false
		}
		vs, ve = 0, 0
	}
	r := Get(json[vs:ve], path[k+1:])
	r.Index = 0
	return r, true
}

// getQuery is Get for a path that find found to be a query: vs is -2-pi
// for the component pi where it stopped, and ve the start of the container
// that component applies to. find took the first member with each key on
// the way there, as the full evaluation does, so when the rest of the path
// resolves there the result is the same. Otherwise later members with the
// same keys may matter, and the path is evaluated from the root.
func getQuery(json, path string, vs, ve int) Result {
	if r, ok := getPipe(json, path); ok {
		return r
	}
	if pi := -2 - vs; pi > 0 && ve >= 0 {
		c := ectx{json: json}
		var hit bool
		if json[ve] == '{' {
			hit = c.object(ve+1, path[pi:])
		} else {
			hit = c.array(ve+1, path[pi:], false)
		}
		if hit {
			return c.result()
		}
		// Get goes on to later members with the keys before pi. The walk
		// from the root reaches this container first, with the same rest:
		// there it keeps what was found here instead of looking again.
		c.done, c.doneRest = ve+1, path[pi:]
		return c.walk(path)
	}
	return getComplex(json, path)
}
