package ojson

import (
	"math/bits"
	"strconv"
	"strings"
	"unicode/utf8"
	"unsafe"
)

// Get searches json for path and returns the value it finds. A missing
// value returns a Result whose Exists method reports false.
func Get(json, path string) Result {
	if path != "" && path[0] == '@' {
		return getComplex(json, path) // a modifier first: nothing to walk
	}
	vs, ve, esc := getSpan(json, path)
	if vs >= 0 && ve >= 0 {
		return resultAt(json, vs, ve, esc)
	}
	return getMissing(json, path, vs, ve)
}

// getMissing is Get for a path that getSpan did not resolve: vs and ve are
// what getSpan returned.
func getMissing(json, path string, vs, ve int) Result {
	if vs <= -2 {
		return getQuery(json, path, vs, ve)
	}
	if vs == -1 && (path == "" || parsePipe(path) || scalarRoot(json) || skipSpace(json, 0) == len(json)) {
		return getComplex(json, path)
	}
	return Result{}
}

// parsePipe reports whether path has a pipe: a '|', or a dot before '[',
// '{' or '@'. gjson sets a pipe when it reads the component before it,
// even when no key matches that component, and applies it to the missing
// value, so find's miss is not the answer for such paths.
func parsePipe(path string) bool {
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '\\':
			i++
		case '|':
			return true
		case '.':
			if i+1 < len(path) && (path[i+1] == '[' || path[i+1] == '{' || path[i+1] == '@') {
				return true
			}
		}
	}
	return false
}

// scalarRoot reports whether json holds something other than an object or
// an array. gjson reads paths from the first '{' or '[' in the document,
// even inside a string, so such documents go to getComplex, as do empty
// ones, for paths such as modifiers that need no document.
func scalarRoot(json string) bool {
	i := skipSpace(json, 0)
	return i < len(json) && json[i] != '{' && json[i] != '['
}

// GetBytes is Get for a byte slice. The result does not share memory with
// json.
func GetBytes(json []byte, path string) Result {
	s := bytesView(json)
	if path != "" && path[0] == '@' {
		return detach1(json, getComplex(s, path))
	}
	vs, ve, esc := getSpan(s, path)
	if vs < 0 || ve < 0 {
		return detach1(json, getMissing(s, path, vs, ve))
	}
	if !esc && ve-vs >= 2 && s[vs] == '"' {
		b := makeNoZero(ve - vs)
		copy(b, s[vs:ve])
		raw := bytesView(b)
		return Result{Type: String, Raw: raw, Str: raw[1 : len(raw)-1], Index: vs}
	}
	return detach1(json, resultAt(s, vs, ve, esc))
}

// GetBytesNoCopy is GetBytes without the copy. The result shares memory with
// json, so it is valid only while json is not modified.
func GetBytesNoCopy(json []byte, path string) Result { return Get(bytesView(json), path) }

// getSpan finds the value path names in json, as find does. A path that
// does not resolve may still hold query bytes past the components walked:
// callers check it with getMissing before reporting a missing value.
func getSpan(json, path string) (vs, ve int, esc bool) {
	if path == "" {
		return -1, -1, false
	}
	if queryStart(path) {
		return -2, -2, false
	}
	i := skipSpace(json, 0)
	if i == len(json) {
		return -1, -1, false
	}
	return find(json, i, path, 0, pathDots(path))
}

// GetMany returns the values for several paths, in order.
func GetMany(json string, path ...string) []Result {
	out := make([]Result, len(path))
	if direct(path) {
		var spans [3]span
		directSpans(json, path, &spans)
		for k, p := range path {
			spanResult(&out[k], json, p, spans[k])
		}
		return out
	}
	getMany(json, path, out)
	return out
}

// direct reports whether to resolve paths one at a time. For a few paths a
// walk per path costs less than the shared walk, which also stores more
// into the results. At worst, when every path is missing from a large
// document, the document is read once per path, as GetMany in gjson does.
func direct(paths []string) bool { return len(paths) <= 3 }

// GetManyBytes is GetMany for a byte slice. The results do not share memory
// with json.
func GetManyBytes(json []byte, path ...string) []Result {
	out := make([]Result, len(path))
	if direct(path) {
		getDirect(json, path, out)
		return out
	}
	getMany(bytesView(json), path, out)
	detach(json, out)
	return out
}

// getDirect resolves up to three paths with directSpans and copies the
// values into one buffer.
func getDirect(json []byte, paths []string, out []Result) {
	s := bytesView(json)
	var spans [3]span
	directSpans(s, paths, &spans)
	n := 0
	for k := range paths {
		if sp := spans[k]; sp.vs >= 0 && sp.ve >= 0 && !isLiteral(s[sp.vs:sp.ve]) {
			n += sp.ve - sp.vs
		}
	}
	var buf []byte
	if n > 0 {
		buf = makeNoZero(n)[:0]
	}
	for k, p := range paths {
		sp := spans[k]
		if sp.vs < 0 || sp.ve < 0 {
			out[k] = detach1(json, getMissing(s, p, sp.vs, sp.ve))
			continue
		}
		if raw := s[sp.vs:sp.ve]; isLiteral(raw) {
			r := resultAt(s, sp.vs, sp.ve, false)
			r.Raw = literal(raw)
			out[k] = r
			continue
		}
		at := len(buf)
		buf = append(buf, s[sp.vs:sp.ve]...)
		t := bytesView(buf[at:])
		if r := &out[k]; !sp.esc && len(t) >= 2 && t[0] == '"' {
			// resultAt for a string, written in place.
			r.Type, r.Raw, r.Str, r.Index = String, t, t[1:len(t)-1], sp.vs
			continue
		}
		r := resultAt(t, 0, len(t), sp.esc)
		if r.Raw != "" {
			r.Index = sp.vs
		}
		out[k] = r
	}
}

// GetManyBytesNoCopy is GetManyBytes without copies. It stores the results in
// dst, reusing its memory when it has room, and returns them. The results
// share memory with json, so they are valid only while json is not modified.
func GetManyBytesNoCopy(dst []Result, json []byte, path ...string) []Result {
	if cap(dst) < len(path) {
		dst = make([]Result, len(path))
	} else {
		dst = dst[:len(path)]
		clear(dst)
	}
	if direct(path) {
		s := bytesView(json)
		var spans [3]span
		directSpans(s, path, &spans)
		for k, p := range path {
			spanResult(&dst[k], s, p, spans[k])
		}
		return dst
	}
	getMany(bytesView(json), path, dst)
	return dst
}

// Parse parses json and returns its root value. It does not validate json.
func Parse(json string) Result {
	i := skipSpace(json, 0)
	if i == len(json) {
		return Result{}
	}
	var r Result
	switch c := json[i]; {
	case c == '{' || c == '[':
		r = Result{Type: JSON, Raw: json[i:]}
	case c == '"':
		end := stringEnd(json, i+1)
		if end < 0 {
			r = stringResult(json, i, len(json), false)
		} else {
			r = stringResult(json, i, end, true)
		}
	case c == 't':
		r = Result{Type: True, Raw: json[i:min(len(json), i+4)]}
	case c == 'f' || c == 'n' || c == '-' || (c >= '0' && c <= '9'):
		r, _ = valueAt(json, i)
	default:
		return Result{}
	}
	r.Index = i
	return r
}

// ParseBytes is Parse for a byte slice.
func ParseBytes(json []byte) Result {
	return detach1(json, Parse(bytesView(json)))
}

// detach copies the strings of rs that point into json, all into one
// buffer. Str shares the copy of Raw when it lies inside Raw. true, false and
// null become constants, and strings elsewhere are already copies.
func detach(json []byte, rs []Result) {
	src := bytesView(json)
	n := 0
	for i := range rs {
		r := &rs[i]
		if within(r.Raw, src) && !isLiteral(r.Raw) {
			n += len(r.Raw)
		}
		if within(r.Str, src) && !within(r.Str, r.Raw) {
			n += len(r.Str)
		}
	}
	var buf []byte
	if n > 0 {
		buf = makeNoZero(n)[:0]
	}
	for i := range rs {
		r := &rs[i]
		if within(r.Str, src) && !within(r.Str, r.Raw) {
			at := len(buf)
			buf = append(buf, r.Str...)
			r.Str = bytesView(buf[at:])
		}
		switch {
		case isLiteral(r.Raw):
			r.Raw = literal(r.Raw)
		case within(r.Raw, src):
			at := len(buf)
			buf = append(buf, r.Raw...)
			raw := bytesView(buf[at:])
			if within(r.Str, r.Raw) {
				off := int(uintptr(unsafe.Pointer(unsafe.StringData(r.Str))) - uintptr(unsafe.Pointer(unsafe.StringData(r.Raw))))
				r.Str = raw[off : off+len(r.Str)]
			}
			r.Raw = raw
		}
	}
}

// detach1 is detach for one result.
func detach1(json []byte, r Result) Result {
	src := bytesView(json)
	switch {
	case r.Raw == "":
	case isLiteral(r.Raw):
		r.Raw = literal(r.Raw)
	case within(r.Raw, src):
		b := makeNoZero(len(r.Raw))
		copy(b, r.Raw)
		raw := bytesView(b)
		if within(r.Str, r.Raw) {
			off := int(uintptr(unsafe.Pointer(unsafe.StringData(r.Str))) - uintptr(unsafe.Pointer(unsafe.StringData(r.Raw))))
			r.Str = raw[off : off+len(r.Str)]
			r.Raw = raw
			return r
		}
		r.Raw = raw
	}
	if within(r.Str, src) {
		r.Str = string(r.Str)
	}
	return r
}

func isLiteral(s string) bool { return s == "true" || s == "false" || s == "null" }

// within reports whether the non-empty s lies inside the memory of t.
func within(s, t string) bool {
	if s == "" || t == "" {
		return false
	}
	p := uintptr(unsafe.Pointer(unsafe.StringData(s)))
	q := uintptr(unsafe.Pointer(unsafe.StringData(t)))
	return p >= q && p+uintptr(len(s)) <= q+uintptr(len(t))
}

// literal returns s, or the constant with the same text when s is true,
// false or null.
func literal(s string) string {
	switch s {
	case "true":
		return "true"
	case "false":
		return "false"
	}
	return "null"
}

// find resolves path[pi:] inside the value at json[i] and returns the span of
// the value it names, and for a string whether it may hold escapes. vs is -1
// when there is no such value. When a component makes the path a query, vs
// is -2-pi and ve is the start of the container that component applies to,
// so that getQuery can go on from there; ve is -2 when it cannot. Like gjson, a key that matches but whose remaining path fails does
// not stop the search: later members with the same key are tried.
//
// dots is pathDots(path): when it is not 0, the components end at its set
// bits.
func find(json string, i int, path string, pi int, dots uint64) (vs, ve int, esc bool) {
	var key string
	var kw, km uint64
	next := -1
	if dots != 0 {
		// pathDots leaves paths with '[' or '{' to getComp, which finds
		// the multipaths among them.
		e := pi + bits.TrailingZeros64(dots>>uint(pi))
		key = path[pi:e]
		if e < len(path) {
			next = e + 1
		}
	} else {
		raw, n, kesc := getComp(path, pi, pi)
		if n == -2 {
			return findQuery(json, i, path, pi)
		}
		key, next = compKey(raw, kesc), n
		if kesc {
			pi = -1 - pi // escaped: no keyWord, and never an index
		}
	}
	switch json[i] {
	case '{':
		if pi >= 0 {
			kw, km = keyWord(path, pi, pi+len(key))
		}
		i++
		for {
			v := memberValue(json, i, key, kw, km)
			if v < 0 {
				return -1, -1, false
			}
			if next < 0 {
				ve, esc := valueSpan(json, v)
				return v, ve, esc
			}
			if c := json[v]; c == '{' || c == '[' {
				if fs, fe, fesc := find(json, v, path, next, dots); fs != -1 {
					return fs, fe, fesc
				}
			}
			if i = skipValue(json, v); i < 0 {
				return -1, -1, false
			}
		}
	case '[':
		n, ok := arrayIndex(key)
		if !ok || pi < 0 {
			if pi < 0 {
				return findEscIndex(i, key, -1-pi)
			}
			return -1, -1, false
		}
		v := elementAt(json, i+1, n)
		switch {
		case v < 0:
		case next < 0:
			ve, esc := valueSpan(json, v)
			return v, ve, esc
		case json[v] == '{' || json[v] == '[':
			return find(json, v, path, next, dots)
		}
	}
	return -1, -1, false
}

// findQuery is find for a component at path[pi] that makes the path a
// query.
func findQuery(json string, i int, path string, pi int) (vs, ve int, esc bool) {
	star := wildScan(path, pi)
	if star >= 0 {
		return findPrefix(json, i, path, pi, star)
	}
	if star == -1 {
		if oc := readObjComp(path[pi:]); oc.wild && !oc.piped {
			if json[i] == '[' {
				// An array reads the component as an index, which an
				// escaped '.' or '|' ends, as for an escaped key: the
				// only '.' or '|' in the component are escaped ones.
				end := len(path) - len(oc.rest)
				if oc.more {
					end--
				}
				return findEscIndex(i, path[pi:end], pi)
			}
			return findWild(json, i, path, &oc)
		}
	}
	if pi > 0 && path[pi] != '[' && path[pi] != '{' && path[pi] != '@' {
		return -2 - pi, i, false
	}
	return -2, -2, false
}

// findEscIndex is find for an escaped component key, at path[at], in the
// array at json[i]: it never names an element. gjson ends an array
// component at the first '.' or '|', escaped or not, and goes on from the
// missing element.
func findEscIndex(i int, key string, at int) (vs, ve int, esc bool) {
	if !strings.ContainsAny(key, ".|") {
		return -1, -1, false
	}
	if at > 0 {
		return -2 - at, i, false
	}
	return -2, -2, false
}

// wildScan reads the component at path[pi] for findQuery. Components with
// a '|', '#' or '@', or followed by a dot and a '[', '{' or '@', are not
// wildcard components: it returns -2. When the only wildcard byte is a '*'
// that ends the component, and the bytes before it are ASCII without
// escapes, it returns the index of that '*', for findPrefix: comparing
// those bytes is then the same as globMatch's comparison of runes. It
// returns -1 for the rest, which readObjComp parses for findWild.
func wildScan(path string, pi int) int {
	if pi > 0 && (path[pi] == '[' || path[pi] == '{') {
		return -2
	}
	star, plain := -1, true
	for j := pi; j < len(path); j++ {
		c := path[j]
		if pathClass[c] == 0 {
			if c >= utf8.RuneSelf {
				plain = false
			}
			continue
		}
		switch c {
		case '\\':
			plain = false
			j++
		case '?':
			plain = false
		case '*':
			if plain && (j+1 == len(path) || path[j+1] == '.') {
				star = j
			}
			plain = false
		case '|', '#', '@':
			return -2
		case '.':
			if j+1 < len(path) && (path[j+1] == '[' || path[j+1] == '{' || path[j+1] == '@') {
				return -2
			}
			return star
		}
	}
	return star
}

// findWild is find for a wildcard component oc: in an object, the members
// whose keys match it are tried in order until the rest of the path
// resolves. A wildcard never matches an array element.
func findWild(json string, i int, path string, oc *objComp) (vs, ve int, esc bool) {
	if json[i] != '{' {
		return -1, -1, false
	}
	next := len(path) - len(oc.rest)
	for j := i + 1; ; {
		ks, ke, v, kesc, ok := nextMember(json, j)
		if !ok {
			return -1, -1, false
		}
		if keyMatch(json[ks+1:ke-1], kesc, oc) {
			if !oc.more {
				ve, esc := valueSpan(json, v)
				return v, ve, esc
			}
			if c := json[v]; c == '{' || c == '[' {
				if fs, fe, fesc := find(json, v, path, next, 0); fs != -1 {
					return fs, fe, fesc
				}
			}
		}
		if j = skipValue(json, v); j < 0 {
			return -1, -1, false
		}
	}
}

// memberValue reads the members of an object from json[i], just after the
// opening brace or a comma, and returns the start of the value of the first
// whose key is key, or -1 when no later member has it or the object is
// malformed. When km is not 0, kw and km are keyWord(key).
//
// This loop and the one in elementAt are the hot path of every read, so the
// common cases of nextMember and skipValue are written out in them: a call
// costs more than scanning a short key or value.
func memberValue(json string, i int, key string, kw, km uint64) int {
	if !haveAVX2 {
		v, _ := memberScalar(json, i, key, kw, km, -1)
		return v
	}
	v, at := membersFast(json, i, key)
	if v == membersSlow {
		v, _ = memberScalar(json, at, key, kw, km, -1)
	}
	return v
}

// memberScalar is memberValue a byte or a word at a time, for at most n
// members when n > 0. It returns membersSlow and the start of the next
// member when it stops there.
func memberScalar(json string, i int, key string, kw, km uint64, n int) (v, at int) {
	for ; ; n-- {
		if n == 0 {
			return membersSlow, i
		}
		if i >= len(json) || json[i] != '"' {
			i = skipSpace(json, i)
			if i >= len(json) || json[i] != '"' {
				return -1, 0
			}
		}
		ks := i + 1
		var match bool
		if i = quickEnd(json, ks); i > 0 {
			if km != 0 {
				match = (load64(json, ks)^kw)&km == 0
			} else {
				match = json[ks:i-1] == key
			}
		} else {
			var e bool
			if i, e = scanString(json, ks); i < 0 {
				return -1, 0
			}
			match = keyIs(json[ks:i-1], e, key)
		}
		if i >= len(json) || json[i] != ':' {
			i = skipSpace(json, i)
			if i >= len(json) || json[i] != ':' {
				return -1, 0
			}
		}
		i++
		if i < len(json) && json[i] == ' ' {
			i++
		}
		if i >= len(json) || json[i] <= ' ' {
			i = skipSpace(json, i)
			if i >= len(json) {
				return -1, 0
			}
		}
		if match {
			return i, 0
		}
		switch json[i] {
		case '"':
			if e := quickEnd(json, i+1); e > 0 {
				i = e
			} else {
				i, _ = scanString(json, i+1)
			}
		case '{', '[':
			i = containerEnd(json, i)
		default:
			for i < len(json) && !delim[json[i]] {
				i++
			}
		}
		if i < 0 {
			return -1, 0
		}
		if i >= len(json) || json[i] != ',' {
			i = skipSpace(json, i)
			if i >= len(json) || json[i] != ',' {
				return -1, 0
			}
		}
		i++
	}
}

// keyWord returns, for a key of up to seven bytes that is path[pi:e], the
// key followed by its closing quote as a little-endian word, and the mask of
// those bytes. Comparing that many bytes of a member key with it tells
// whether the key matches. km is 0 for longer keys and keys holding a
// quote.
func keyWord(path string, pi, e int) (kw, km uint64) {
	n := e - pi
	if n >= 8 {
		return 0, 0
	}
	var w uint64
	switch {
	case pi+8 <= len(path):
		w = load64(path, pi)
	case len(path) >= 8:
		w = load64(path, len(path)-8) >> (8 * uint(pi+8-len(path)))
	default:
		for j := e - 1; j >= pi; j-- {
			w = w<<8 | uint64(path[j])
		}
	}
	low := uint64(1)<<(8*uint(n)) - 1
	w &= low
	if zeroBytes(w^lsb*'"')&low != 0 {
		return 0, 0
	}
	return w | '"'<<(8*uint(n)), low<<8 | 0xff
}

// elementAt returns the start of element n of the array whose elements start
// at json[i], just after the opening bracket, or -1 when the array has no
// such element or is malformed.
func elementAt(json string, i, n int) int {
	for ; ; n-- {
		if i < len(json) && json[i] == ' ' {
			i++
		}
		if i >= len(json) || json[i] <= ' ' {
			i = skipSpace(json, i)
		}
		if i >= len(json) || json[i] == ']' {
			return -1
		}
		if n == 0 {
			return i
		}
		switch json[i] {
		case '"':
			if e := quickEnd(json, i+1); e > 0 {
				i = e
			} else {
				i, _ = scanString(json, i+1)
			}
		case '{', '[':
			i = containerEnd(json, i)
		default:
			for i < len(json) && !delim[json[i]] {
				i++
			}
		}
		if i < 0 {
			return -1
		}
		if i >= len(json) || json[i] != ',' {
			i = skipSpace(json, i)
			if i >= len(json) || json[i] != ',' {
				return -1
			}
		}
		i++
	}
}

// valueSpan returns the end of the value at json[i], or -1 when it is
// unterminated, and for a string whether it may hold escapes.
func valueSpan(json string, i int) (int, bool) {
	switch json[i] {
	case '"':
		if e := quickEnd(json, i+1); e > 0 {
			return e, false
		}
		return scanString(json, i+1)
	case '{', '[':
		return containerEnd(json, i), false
	}
	for i < len(json) && !delim[json[i]] {
		i++
	}
	return i, false
}

// skipValue skips the value at json[i] and the comma after it. It returns
// the index after the comma, or -1 when no comma follows or the value is
// malformed.
func skipValue(json string, i int) int {
	switch json[i] {
	case '"':
		if e := quickEnd(json, i+1); e > 0 {
			i = e
		} else {
			i, _ = scanString(json, i+1)
		}
	case '{', '[':
		i = containerEnd(json, i)
	default:
		for i < len(json) && !delim[json[i]] {
			i++
		}
	}
	if i < 0 {
		return -1
	}
	return afterValue(json, i)
}

// nextMember reads the object member that starts at or after json[i]. It
// returns the key span including quotes, the start of the value, and whether
// the key may hold escapes.
func nextMember(json string, i int) (ks, ke, vs int, esc, ok bool) {
	if i >= len(json) || json[i] != '"' {
		i = skipSpace(json, i)
		if i >= len(json) || json[i] != '"' {
			return 0, 0, 0, false, false
		}
	}
	ks = i
	if ke = quickEnd(json, i+1); ke == 0 {
		if ke, esc = scanString(json, i+1); ke < 0 {
			return 0, 0, 0, false, false
		}
	}
	i = ke
	if i >= len(json) || json[i] != ':' {
		i = skipSpace(json, i)
		if i >= len(json) || json[i] != ':' {
			return 0, 0, 0, false, false
		}
	}
	i++
	if i >= len(json) || json[i] <= ' ' {
		i = skipSpace(json, i)
		if i >= len(json) {
			return 0, 0, 0, false, false
		}
	}
	return ks, ke, i, esc, true
}

// afterValue skips the separator after a member value. It returns the index
// after a comma, or -1 at the end of the container.
func afterValue(json string, i int) int {
	if i < len(json) && json[i] == ',' {
		return i + 1
	}
	i = skipSpace(json, i)
	if i < len(json) && json[i] == ',' {
		return i + 1
	}
	return -1
}

// valueAt returns the value starting at json[i] and the index after it.
func valueAt(json string, i int) (Result, int) {
	end := valueEnd(json, i)
	if end < 0 {
		return Result{}, -1
	}
	return valueResult(json, i, end)
}

// valueResult builds the Result for the value json[vs:ve].
func valueResult(json string, vs, ve int) (Result, int) {
	if ve < 0 {
		return Result{}, -1
	}
	if ve == vs {
		return Result{}, ve
	}
	raw := json[vs:ve]
	var r Result
	switch c := raw[0]; {
	case c == '"':
		r = stringResult(json, vs, ve, true)
	case c == '{' || c == '[':
		r = Result{Type: JSON, Raw: raw}
	case c == 't':
		r = Result{Type: True, Raw: raw}
	case c == 'f':
		r = Result{Type: False, Raw: raw}
	case c == 'n':
		r = Result{Type: Null, Raw: raw}
	case c == '-' || (c >= '0' && c <= '9'):
		r = Result{Type: Number, Raw: raw, Num: parseNum(raw)}
	default:
		return Result{}, ve
	}
	r.Index = vs
	return r, ve
}

// resultAt is valueResult for a value whose end is known. esc reports
// whether a string may hold escapes; false skips the search for them.
//
// Strings without escapes, containers and literals are built here rather
// than in valueResult, whose larger frame costs more than they do.
func resultAt(json string, vs, ve int, esc bool) Result {
	if ve > vs {
		switch json[vs] {
		case '"':
			if !esc && ve-vs >= 2 {
				return Result{Type: String, Raw: json[vs:ve], Str: json[vs+1 : ve-1], Index: vs}
			}
		case '{', '[':
			return Result{Type: JSON, Raw: json[vs:ve], Index: vs}
		case 't':
			return Result{Type: True, Raw: json[vs:ve], Index: vs}
		case 'f':
			return Result{Type: False, Raw: json[vs:ve], Index: vs}
		case 'n':
			return Result{Type: Null, Raw: json[vs:ve], Index: vs}
		default:
			// Outside the switch, which keeps it a short binary search.
			if c := json[vs]; c == '-' || c-'0' <= 9 {
				return Result{Type: Number, Raw: json[vs:ve], Num: parseNum(json[vs:ve]), Index: vs}
			}
		}
	}
	r, _ := valueResult(json, vs, ve)
	return r
}

// stringResult builds the Result for the string json[i:end]. closed reports
// whether json[end-1] is the closing quote.
func stringResult(json string, i, end int, closed bool) Result {
	raw := json[i:end]
	inner := raw[1:]
	if closed {
		inner = inner[:len(inner)-1]
	}
	r := Result{Type: String, Raw: raw, Index: i}
	if strings.IndexByte(inner, '\\') < 0 {
		r.Str = inner
	} else {
		b, _ := unescape(makeNoZero(len(inner))[:0], inner)
		r.Str = bytesView(b)
	}
	return r
}

// manyChunk is the number of paths getMany resolves in one walk.
const manyChunk = 8

// getMany fills out with the values of paths, with the same result as Get
// for each path. The paths are resolved in one walk, so the members they
// share are read once.
func getMany(json string, paths []string, out []Result) {
	for len(paths) > manyChunk {
		getMany(json, paths[:manyChunk], out[:manyChunk])
		paths, out = paths[manyChunk:], out[manyChunk:]
	}
	i := skipSpace(json, 0)
	if i == len(json) || json[i] != '{' && json[i] != '[' {
		for k, p := range paths {
			out[k] = Get(json, p)
		}
		return
	}
	w := walker{json: json, paths: paths, out: out}
	var buf [manyChunk]pend
	todo := buf[:0]
	for k, p := range paths {
		switch {
		case p == "" || queryStart(p):
			w.query |= 1 << k
		default:
			if t, ok := w.pend(k, 0); ok {
				todo = append(todo, t)
			}
		}
	}
	if len(todo) > 0 {
		w.walk(i, todo)
	}
	for k, p := range paths {
		bit := uint(1) << k
		if w.query&bit != 0 || w.fin&bit == 0 && w.parsed&bit == 0 && p != "" && !plainComps(p) {
			out[k] = getComplex(json, p)
		}
	}
}

// A walker resolves several paths in one walk over json. The bit sets have
// one bit per path: fin marks paths whose search is over, query paths that
// the full path engine evaluates, and parsed paths whose components were
// all read, so that a missing value needs no check for query bytes.
type walker struct {
	json               string
	paths              []string
	out                []Result
	fin, query, parsed uint
}

// A pend is a path being resolved at one level of the walk: k indexes paths
// and out, key is the component to match there and next the start of the
// following component, or -1.
type pend struct {
	k, next int
	key     string
}

// pend reads the component of path k that starts at pi. It returns false
// when the component makes the path a query.
func (w *walker) pend(k, pi int) (pend, bool) {
	p := w.paths[k]
	j := pi
	for j+8 <= len(p) && plainWord(load64(p, j)) {
		j += 8
	}
	raw, next, esc := getComp(p, pi, j)
	if next == -2 {
		w.query |= 1 << k
		w.fin |= 1 << k
		return pend{}, false
	}
	key := raw
	if esc {
		if key = unescapePath(raw); isDigits(key) {
			// Escaped digits name an object key but never an array
			// element, which a pend cannot tell: leave the path to
			// getComplex.
			w.query |= 1 << k
			w.fin |= 1 << k
			return pend{}, false
		}
	}
	if next < 0 {
		w.parsed |= 1 << k
	}
	return pend{k: k, next: next, key: key}, true
}

// walk resolves the paths in todo inside the container at json[i], the way
// find resolves one. Paths it does not finish continue at the parent object.
func (w *walker) walk(i int, todo []pend) {
	json := w.json
	var buf [manyChunk]pend
	if json[i] == '[' {
		w.walkArray(i, todo, &buf)
		return
	}
	i++
	for {
		ks, ke, v, kesc, ok := nextMember(json, i)
		if !ok {
			return
		}
		key := json[ks+1 : ke-1]
		sub := buf[:0]
		ve, vesc := -2, false
		for m := 0; m < len(todo); {
			t := &todo[m]
			if !keyIs(key, kesc, t.key) {
				m++
				continue
			}
			if t.next < 0 {
				if ve == -2 {
					ve, vesc = valueSpan(json, v)
				}
				if ve >= 0 {
					w.out[t.k] = resultAt(json, v, ve, vesc)
				}
				w.fin |= 1 << t.k
				todo = drop(todo, m)
				continue
			}
			if c := json[v]; c == '{' || c == '[' {
				s, ok := w.pend(t.k, t.next)
				if !ok {
					todo = drop(todo, m)
					continue
				}
				sub = append(sub, s)
			}
			m++
		}
		if len(sub) > 0 {
			w.walk(v, sub)
			for m := 0; m < len(todo); {
				if w.fin&(1<<todo[m].k) != 0 {
					todo = drop(todo, m)
				} else {
					m++
				}
			}
		}
		if len(todo) == 0 {
			return
		}
		switch {
		case ve == -2:
			i = skipValue(json, v)
		case ve < 0:
			return
		default:
			i = afterValue(json, ve)
		}
		if i < 0 {
			return
		}
	}
}

// walkArray is walk for the array at json[i]. As with find, a path whose
// index matches ends its search in this array, found or not.
func (w *walker) walkArray(i int, todo []pend, buf *[manyChunk]pend) {
	json := w.json
	var idx [manyChunk]int
	n := 0
	for _, t := range todo {
		if x, ok := arrayIndex(t.key); ok {
			todo[n], idx[n] = t, x
			n++
		} else if strings.ContainsAny(t.key, ".|") {
			// An escaped '.' or '|': gjson ends an array component there,
			// so the path goes on from the missing element.
			w.query |= 1 << t.k
			w.fin |= 1 << t.k
		}
	}
	todo = todo[:n]
	i++
	for k := 0; len(todo) > 0; k++ {
		if i >= len(json) || json[i] <= ' ' {
			i = skipSpace(json, i)
		}
		if i >= len(json) || json[i] == ']' {
			return
		}
		sub := buf[:0]
		ve, vesc := -2, false
		for m := 0; m < len(todo); {
			if idx[m] != k {
				m++
				continue
			}
			t := todo[m]
			if t.next < 0 {
				if ve == -2 {
					ve, vesc = valueSpan(json, i)
				}
				if ve >= 0 {
					w.out[t.k] = resultAt(json, i, ve, vesc)
				}
				w.fin |= 1 << t.k
			} else if c := json[i]; c == '{' || c == '[' {
				if s, ok := w.pend(t.k, t.next); ok {
					sub = append(sub, s)
				}
			}
			last := len(todo) - 1
			todo[m], idx[m] = todo[last], idx[last]
			todo = todo[:last]
		}
		if len(sub) > 0 {
			w.walk(i, sub)
		}
		if len(todo) == 0 {
			return
		}
		switch {
		case ve == -2:
			i = skipValue(json, i)
		case ve < 0:
			return
		default:
			i = afterValue(json, ve)
		}
		if i < 0 {
			return
		}
	}
}

// drop removes todo[n], moving the last entry into its place.
func drop(todo []pend, n int) []pend {
	last := len(todo) - 1
	todo[n] = todo[last]
	return todo[:last]
}

// span is what getSpan returns for a path.
type span struct {
	vs, ve int
	esc    bool
}

// directSpans sets spans to the getSpan results of up to three paths.
// Paths that begin with the same plain components look up the value those
// components name once and search on from it. Only a path that does not
// resolve there gets a full search, which also tries later members with the
// same keys. Paths that share nothing, such as model and group, find their
// first keys in one pass over the root members (rootSpans).
func directSpans(json string, paths []string, spans *[3]span) {
	var pre, at [3]int
	shared := false
	for k := 1; k < len(paths); k++ {
		for j := 0; j < k; j++ {
			n := sharedPrefix(paths[j], paths[k])
			pre[j], pre[k] = max(pre[j], n), max(pre[k], n)
			shared = shared || n > 0
		}
	}
	if len(paths) > 1 && !shared && rootSpans(json, paths, spans[:len(paths)]) {
		return
	}
	for k, p := range paths {
		sp := &spans[k]
		if n := pre[k]; n > 0 {
			i := -2
			for j := 0; j < k; j++ {
				if pre[j] == n && paths[j][:n] == p[:n] {
					i = at[j]
					break
				}
			}
			if i == -2 {
				i, _, _ = getSpan(json, p[:n])
			}
			at[k] = i
			if i >= 0 {
				if sp.vs, sp.ve, sp.esc = find(json, i, p, n+1, pathDots(p)); sp.vs >= 0 {
					continue
				}
			}
		}
		sp.vs, sp.ve, sp.esc = getSpan(json, p)
	}
}

// sharedPrefix returns the length of the longest run of whole components
// that starts both p and q and is followed by a dot in both, when those
// components are plain keys or indexes, and 0 when there is none.
func sharedPrefix(p, q string) int {
	if p == "" || q == "" || queryStart(p) || p[0] == '.' {
		return 0
	}
	n := 0
	for i := 0; i < len(p) && i < len(q) && p[i] == q[i]; i++ {
		switch pathClass[p[i]] {
		case 0, pathOpen:
		case pathDot:
			if p[i-1] == '.' {
				return n
			}
			n = i
		default:
			return n
		}
	}
	return n
}

// spanResult sets *r, which holds the zero Result, to Get(json, path) for a
// path whose getSpan result is sp.
func spanResult(r *Result, json, path string, sp span) {
	if sp.vs >= 0 && sp.ve >= 0 {
		if vs, ve := sp.vs, sp.ve; !sp.esc && ve-vs >= 2 && json[vs] == '"' {
			// resultAt for a string, written in place.
			r.Type, r.Raw, r.Str, r.Index = String, json[vs:ve], json[vs+1:ve-1], vs
			return
		}
		*r = resultAt(json, sp.vs, sp.ve, sp.esc)
	} else {
		*r = getMissing(json, path, sp.vs, sp.ve)
	}
}

// parseNum is strconv.ParseFloat(raw, 64) without the error. Integers of up
// to 15 digits, which a float64 holds exactly, are converted directly.
func parseNum(raw string) float64 {
	d := raw
	if d != "" && d[0] == '-' {
		d = d[1:]
	}
	if len(d) > 0 && len(d) <= 15 {
		var u uint64
		i := 0
		for ; i < len(d) && d[i]-'0' <= 9; i++ {
			u = u*10 + uint64(d[i]-'0')
		}
		if i == len(d) {
			f := float64(u)
			if len(d) < len(raw) {
				f = -f
			}
			return f
		}
	}
	f, _ := strconv.ParseFloat(raw, 64)
	return f
}

// rootSpans is directSpans for paths whose first components are plain keys
// of a root object. It reads the root members once for all of those keys,
// then searches on from the values found; a path that does not resolve
// there gets a full search, as in directSpans. It reports false, setting
// nothing, when there are more paths than rootKeys holds or than spans has
// room for, or when the paths or the document do not have that shape.
func rootSpans(json string, paths []string, spans []span) bool {
	var r rootKeys
	if len(paths) > len(r.key) || len(paths) > len(spans) {
		return false
	}
	for k, p := range paths {
		if p == "" || queryStart(p) || p[0] == '.' {
			return false
		}
		raw, next, esc := getComp(p, 0, 0)
		if next == -2 || esc || raw == "" {
			return false
		}
		r.key[k], r.next[k], r.at[k] = raw, next, -1
	}
	i := skipSpace(json, 0)
	if i >= len(json) || json[i] != '{' {
		return false
	}
	r.n = len(paths)
	r.scan(json, i+1)
	for k, p := range paths {
		sp, v := &spans[k], r.at[k]
		switch {
		case v < 0:
			*sp = span{-1, -1, false}
		case r.next[k] < 0:
			*sp = span{v, r.end[k], r.esc[k]}
		default:
			if c := json[v]; c == '{' || c == '[' {
				if sp.vs, sp.ve, sp.esc = find(json, v, p, r.next[k], pathDots(p)); sp.vs >= 0 {
					continue
				}
			}
			sp.vs, sp.ve, sp.esc = getSpan(json, p)
		}
	}
	return true
}

// rootScan is how many members rootKeys.scan reads for all keys at once.
// Keys not seen by then are looked for one at a time, which the member
// loop of memberValue does faster over many members.
const rootScan = 8

// rootKeys holds the first keys of up to eight paths and the index of the
// rest of each path, -1 when there is none. scan sets at to the start of the
// value of the first member with each key, or -1, and for a path with no
// rest end and esc to what valueSpan returns for that value.
type rootKeys struct {
	key  [8]string
	next [8]int
	at   [8]int
	end  [8]int
	esc  [8]bool
	n    int
}

// scan reads the members of the object from json[i], just after the opening
// brace, until every key is found. It reads them as memberScalar does and
// stops where memberValue would, so a key it does not find is one that
// memberValue does not find either.
func (r *rootKeys) scan(json string, i int) {
	left := r.n
	for m := 0; ; m++ {
		if m == rootScan {
			for k := 0; k < r.n; k++ {
				if r.at[k] < 0 {
					v := memberValue(json, i, r.key[k], 0, 0)
					if r.at[k] = v; v >= 0 && r.next[k] < 0 {
						r.end[k], r.esc[k] = valueSpan(json, v)
					}
				}
			}
			return
		}
		if i >= len(json) || json[i] != '"' {
			i = skipSpace(json, i)
			if i >= len(json) || json[i] != '"' {
				return
			}
		}
		ks := i + 1
		var esc bool
		if i = quickEnd(json, ks); i == 0 {
			if i, esc = scanString(json, ks); i < 0 {
				return
			}
		}
		key := json[ks : i-1]
		if i >= len(json) || json[i] != ':' {
			i = skipSpace(json, i)
			if i >= len(json) || json[i] != ':' {
				return
			}
		}
		i++
		if i < len(json) && json[i] == ' ' {
			i++
		}
		if i >= len(json) || json[i] <= ' ' {
			i = skipSpace(json, i)
			if i >= len(json) {
				return
			}
		}
		hit := 0
		for k := 0; k < r.n; k++ {
			if r.at[k] < 0 && (len(key) == len(r.key[k]) && key == r.key[k] || esc && keyEqual(key, r.key[k])) {
				r.at[k] = i
				hit |= 1 << k
				left--
			}
		}
		if hit != 0 {
			e, vesc := valueSpan(json, i)
			for k := 0; k < r.n; k++ {
				if hit&(1<<k) != 0 {
					r.end[k], r.esc[k] = e, vesc
				}
			}
			if left == 0 {
				return
			}
			i = e
		} else {
			switch json[i] {
			case '"':
				if e := quickEnd(json, i+1); e > 0 {
					i = e
				} else {
					i, _ = scanString(json, i+1)
				}
			case '{', '[':
				i = containerEnd(json, i)
			default:
				for i < len(json) && !delim[json[i]] {
					i++
				}
			}
		}
		if i < 0 {
			return
		}
		if i >= len(json) || json[i] != ',' {
			i = skipSpace(json, i)
			if i >= len(json) || json[i] != ',' {
				return
			}
		}
		i++
	}
}
