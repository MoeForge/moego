package ojson

import (
	"iter"
	"slices"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

// Type is the type of a JSON value.
type Type int

const (
	// Null is a null JSON value, or a missing one.
	Null Type = iota
	// False is the JSON false literal.
	False
	// Number is a JSON number.
	Number
	// String is a JSON string.
	String
	// True is the JSON true literal.
	True
	// JSON is a raw object or array.
	JSON
)

// String returns the type name.
func (t Type) String() string {
	switch t {
	case Null:
		return "Null"
	case False:
		return "False"
	case Number:
		return "Number"
	case String:
		return "String"
	case True:
		return "True"
	case JSON:
		return "JSON"
	}
	return ""
}

// Result is a value found in a JSON document.
type Result struct {
	// Type is the JSON type.
	Type Type
	// Raw is the raw JSON text of the value.
	Raw string
	// Str is the decoded value of a String.
	Str string
	// Num is the value of a Number.
	Num float64
	// Index is the byte offset of Raw in the original document, or 0 when
	// unknown.
	Index int
	// Indexes holds the offsets of the elements produced by a '#' query.
	Indexes []int
}

// Exists reports whether the value exists in the document.
func (t Result) Exists() bool { return t.Type != Null || len(t.Raw) != 0 }

// IsObject reports whether the value is a JSON object.
func (t Result) IsObject() bool { return t.Type == JSON && len(t.Raw) > 0 && t.Raw[0] == '{' }

// IsArray reports whether the value is a JSON array.
func (t Result) IsArray() bool { return t.Type == JSON && len(t.Raw) > 0 && t.Raw[0] == '[' }

// IsBool reports whether the value is true or false.
func (t Result) IsBool() bool { return t.Type == True || t.Type == False }

// String returns a string form of the value.
func (t Result) String() string {
	switch t.Type {
	case False:
		return "false"
	case True:
		return "true"
	case Number:
		if intRaw(t.Raw) {
			return t.Raw
		}
		return strconv.FormatFloat(t.Num, 'f', -1, 64)
	case String:
		return t.Str
	case JSON:
		return t.Raw
	}
	return ""
}

// Bool returns a boolean form of the value.
func (t Result) Bool() bool {
	switch t.Type {
	case True:
		return true
	case String:
		// As strconv.ParseBool on the lower-case text.
		s := t.Str
		return s == "1" || (len(s) == 1 && s[0]|0x20 == 't') ||
			(len(s) == 4 && s[0]|0x20 == 't' && s[1]|0x20 == 'r' && s[2]|0x20 == 'u' && s[3]|0x20 == 'e')
	case Number:
		return t.Num != 0
	}
	return false
}

// Int returns an integer form of the value.
func (t Result) Int() int64 {
	switch t.Type {
	case True:
		return 1
	case String:
		if integerText(t.Str, true) {
			return wrapInt(t.Str)
		}
		return 0
	case Number:
		// Num first while it is exact, then the integer text of Raw.
		if t.Num > -1<<53 && t.Num < 1<<53 {
			return int64(t.Num)
		}
		if integerText(t.Raw, true) {
			return wrapInt(t.Raw)
		}
		return int64(t.Num)
	}
	return 0
}

// Uint returns an unsigned integer form of the value.
func (t Result) Uint() uint64 {
	switch t.Type {
	case True:
		return 1
	case String:
		if integerText(t.Str, false) {
			return wrapUint(t.Str)
		}
		return 0
	case Number:
		if t.Num > -1<<53 && t.Num < 1<<53 && int64(t.Num) >= 0 {
			return uint64(int64(t.Num))
		}
		if integerText(t.Raw, false) {
			return wrapUint(t.Raw)
		}
		return uint64(t.Num)
	}
	return 0
}

// Float returns a float64 form of the value.
func (t Result) Float() float64 {
	switch t.Type {
	case True:
		return 1
	case String:
		f, _ := strconv.ParseFloat(t.Str, 64)
		return f
	case Number:
		return t.Num
	}
	return 0
}

// integerText reports whether s is digits, optionally with a leading '-'.
func integerText(s string, signed bool) bool {
	if signed && len(s) > 0 && s[0] == '-' {
		s = s[1:]
	}
	return isDigits(s)
}

// intRaw reports whether s is non-empty, an optional '-' and then
// only digits.
func intRaw(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '-' {
		s = s[1:]
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// wrapUint parses digits with uint64 wraparound.
func wrapUint(s string) uint64 {
	var n uint64
	for i := 0; i < len(s); i++ {
		n = n*10 + uint64(s[i]-'0')
	}
	return n
}

// wrapInt parses an optionally negative integer with wraparound.
func wrapInt(s string) int64 {
	if s[0] == '-' {
		return -int64(wrapUint(s[1:]))
	}
	return int64(wrapUint(s))
}

// Array returns the elements of an array. A non-array value is returned as
// a one-element slice, and a null value as an empty one.
func (t Result) Array() []Result {
	if t.Type == Null {
		return []Result{}
	}
	if !t.IsArray() {
		return []Result{t}
	}
	s := t.Raw
	var sbuf [128]int
	spans := sbuf[:0]
	var bp *[]byte
	for i := 1; ; {
		if i < len(s) && s[i] == ',' {
			i++
		}
		if i < len(s) && (s[i] <= ' ' || s[i] == ',') {
			for i < len(s) && (isSpace(s[i]) || s[i] == ',') {
				i++
			}
		}
		if i >= len(s) || s[i] == '}' || s[i] == ']' {
			break
		}
		end, esc := valueSpan(s, i)
		if end < 0 {
			break
		}
		if esc {
			end = -end
		}
		if len(spans) == cap(spans) {
			spans, bp = growSpans(spans, bp)
		}
		spans = append(spans, i, end)
		if end < 0 {
			end = -end
		}
		i = end
	}
	n := len(spans) >> 1
	if n == 0 {
		if bp != nil {
			scratchPool.Put(bp)
		}
		return []Result{}
	}
	out := make([]Result, n)
	base := t.Index
	// The elements take their offsets from Indexes only when there is
	// one for each; otherwise they are unknown.
	hasIdx := t.Indexes != nil && len(t.Indexes) == n
	noIdx := t.Indexes != nil && len(t.Indexes) != n
	for k := 0; k < n; k++ {
		i, end := spans[2*k], spans[2*k+1]
		esc := end < 0
		if esc {
			end = -end
		}
		r := &out[k]
		idx := i + base
		if hasIdx {
			idx = t.Indexes[k]
		} else if noIdx {
			idx = 0
		}
		if raw := s[i:end]; raw != "" {
			switch c := raw[0]; {
			case c == '"':
				if esc || len(raw) < 2 {
					*r = stringResult(s, i, end, true)
					if hasIdx || noIdx {
						r.Index = idx
					} else {
						r.Index += base
					}
					continue
				}
				r.Type, r.Raw, r.Str, r.Index = String, raw, raw[1:len(raw)-1], idx
			case c == '{' || c == '[':
				r.Type, r.Raw, r.Index = JSON, raw, idx
			case c == 't':
				r.Type, r.Raw, r.Index = True, raw, idx
			case c == 'f':
				r.Type, r.Raw, r.Index = False, raw, idx
			case c == 'n':
				r.Type, r.Raw, r.Index = Null, raw, idx
			case c == '-' || (c >= '0' && c <= '9'):
				r.Type, r.Raw, r.Num, r.Index = Number, raw, parseNum(raw), idx
			default:
				if !hasIdx && !noIdx {
					r.Index = base
				} else {
					r.Index = idx
				}
			}
		} else if !hasIdx && !noIdx {
			r.Index = base
		} else {
			r.Index = idx
		}
	}
	if bp != nil {
		scratchPool.Put(bp)
	}
	return out
}

// growSpans doubles the capacity of spans using scratchPool up to 1 MiB.
func growSpans(spans []int, bp *[]byte) ([]int, *[]byte) {
	const intSize = int(unsafe.Sizeof(int(0)))
	need := max(2*cap(spans), 512)
	if need*intSize > 1<<20 {
		dst := make([]int, len(spans), need)
		copy(dst, spans)
		if bp != nil {
			scratchPool.Put(bp)
		}
		return dst, nil
	}
	if bp == nil {
		bp = scratchPool.Get().(*[]byte)
	}
	if cap(*bp) < need*intSize || uintptr(unsafe.Pointer(unsafe.SliceData(*bp)))%uintptr(intSize) != 0 {
		buf := make([]int, need)
		*bp = unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(buf))), need*intSize)
	}
	dst := unsafe.Slice((*int)(unsafe.Pointer(unsafe.SliceData(*bp))), cap(*bp)/intSize)
	copy(dst, spans)
	return dst[:len(spans)], bp
}

// ForEach calls iterator for each member of an object or element of an
// array, in document order, until it returns false. The key of an array
// element is a Number holding its position. For any other existing value it
// calls iterator once with an empty key.
func (t Result) ForEach(iterator func(key, value Result) bool) {
	if !t.Exists() {
		return
	}
	if t.Type != JSON {
		iterator(Result{}, t)
		return
	}
	s := t.Raw
	i := skipSpace(s, 0)
	if i == len(s) || (s[i] != '{' && s[i] != '[') {
		return
	}
	obj := s[i] == '{'
	i++
	// With Indexes, as from a query, value k is at Indexes[k], or unknown
	// past them.
	for n, k := 0, 0; ; n++ {
		if i < len(s) && s[i] == ',' {
			i++
		}
		if i < len(s) && (s[i] <= ' ' || s[i] == ',') {
			for i < len(s) && (isSpace(s[i]) || s[i] == ',') {
				i++
			}
		}
		if i >= len(s) || s[i] == '}' || s[i] == ']' {
			return
		}
		// The results are built here rather than returned by resultAt,
		// which would copy them, with the same fields.
		var key Result
		if obj {
			ks, ke, vs, esc, ok := nextMember(s, i)
			if !ok {
				return
			}
			if esc {
				key = stringResult(s, ks, ke, true)
				key.Index += t.Index
			} else {
				key = Result{Type: String, Raw: s[ks:ke], Str: s[ks+1 : ke-1], Index: ks + t.Index}
			}
			i = vs
		} else {
			key = Result{Type: Number, Num: float64(n)}
		}
		end, esc := valueSpan(s, i)
		if end < 0 {
			return
		}
		v := Result{Index: t.Index}
		if raw := s[i:end]; raw != "" {
			switch c := raw[0]; {
			case c == '"':
				if esc || len(raw) < 2 {
					v = stringResult(s, i, end, true)
					v.Index += t.Index
				} else {
					v = Result{Type: String, Raw: raw, Str: raw[1 : len(raw)-1], Index: i + t.Index}
				}
			case c == '{' || c == '[':
				v = Result{Type: JSON, Raw: raw, Index: i + t.Index}
			case c == 't':
				v = Result{Type: True, Raw: raw, Index: i + t.Index}
			case c == 'f':
				v = Result{Type: False, Raw: raw, Index: i + t.Index}
			case c == 'n':
				v = Result{Type: Null, Raw: raw, Index: i + t.Index}
			case c == '-' || (c >= '0' && c <= '9'):
				v = Result{Type: Number, Raw: raw, Num: parseNum(raw), Index: i + t.Index}
			}
		}
		if t.Indexes != nil {
			v.Index = 0
			if k < len(t.Indexes) {
				v.Index = t.Indexes[k]
			}
			k++
		}
		if !iterator(key, v) {
			return
		}
		i = end
	}
}

// mapCountMin is the shortest object Map counts to size its map: 9
// members take at least 45 bytes, and counting a short object costs more
// than the map growth it saves.
const mapCountMin = 256

// count returns how many times ForEach calls an iterator that returns
// true, from the same walk without building the results.
func (t Result) count() int {
	if !t.Exists() {
		return 0
	}
	if t.Type != JSON {
		return 1
	}
	s := t.Raw
	i := skipSpace(s, 0)
	if i == len(s) || (s[i] != '{' && s[i] != '[') {
		return 0
	}
	obj := s[i] == '{'
	n := 0
	for i++; ; {
		if i < len(s) && s[i] == ',' {
			i++
		}
		if i < len(s) && (s[i] <= ' ' || s[i] == ',') {
			for i < len(s) && (isSpace(s[i]) || s[i] == ',') {
				i++
			}
		}
		if i >= len(s) || s[i] == '}' || s[i] == ']' {
			return n
		}
		if obj {
			_, _, vs, _, ok := nextMember(s, i)
			if !ok {
				return n
			}
			i = vs
		}
		end, _ := valueSpan(s, i)
		if end < 0 {
			return n
		}
		n, i = n+1, end
	}
}

// Get searches the value with path, as package-level Get does. Index and
// Indexes are offsets in the document t came from: like gjson, Index is
// shifted even when nothing is found.
func (t Result) Get(path string) Result { return getAt(t.Raw, t.Index, path) }

// getAt is Result.Get for a value raw at offset index. It takes the two
// fields only, so that Result.Get inlines and its callers copy no Result.
func getAt(raw string, index int, path string) Result {
	// Get, written out to save a call and a copy of the Result.
	var r Result
	if path != "" && path[0] == '@' {
		r = getComplex(raw, path)
	} else if vs, ve, esc := getSpan(raw, path); vs >= 0 && ve >= 0 {
		r = resultAt(raw, vs, ve, esc)
	} else {
		r = getMissing(raw, path, vs, ve)
	}
	if r.Indexes != nil {
		for i := range r.Indexes {
			r.Indexes[i] += index
		}
	} else {
		r.Index += index
	}
	return r
}

// Map returns the members of an object. Duplicate keys keep the first value.
func (t Result) Map() map[string]Result {
	if !t.IsObject() {
		return map[string]Result{}
	}
	// A map of up to 8 members is one group whatever its size hint, so
	// only an object long enough to hold more is counted first.
	var m map[string]Result
	if len(t.Raw) >= mapCountMin {
		m = make(map[string]Result, t.count())
	} else {
		m = map[string]Result{}
	}
	// The values keep their offsets in Raw even when t has Indexes.
	t.Indexes = nil
	// Of duplicate keys the first wins. They are rare, so each member is
	// stored with one map write, and only an object that has them is read
	// again with a lookup before each write.
	n, dup := 0, false
	t.ForEach(func(k, v Result) bool {
		m[k.Str] = v
		n++
		dup = len(m) != n
		return !dup
	})
	if dup {
		clear(m)
		t.ForEach(func(k, v Result) bool {
			if _, ok := m[k.Str]; !ok {
				m[k.Str] = v
			}
			return true
		})
	}
	return m
}

// Value returns the value as a Go value: map[string]interface{},
// []interface{}, float64, string, bool or nil. Of duplicate keys, the first
// wins.
func (t Result) Value() interface{} {
	if t.Type != JSON {
		return scalarValue(t)
	}
	s := t.Raw
	i := skipSpace(s, 0)
	if i == len(s) || (s[i] != '{' && s[i] != '[') {
		return nil
	}
	// An explicit stack of open containers keeps deep input off the
	// goroutine stack. The members of the open containers wait in keys and
	// vals, so each map and slice is made once at its final size.
	type open struct {
		it   members
		base int // the first of the members in keys and vals
	}
	var (
		stackBuf [8]open
		keyBuf   [32]string
		valBuf   [32]interface{}
	)
	stack := append(stackBuf[:0], open{membersOf(s, i), 0})
	keys, vals := keyBuf[:0], valBuf[:0]
	for {
		top := &stack[len(stack)-1]
		ks, ke, vs, ok := top.it.start()
		if ok {
			key := ""
			if top.it.obj {
				key = decodedKey(s, ks, ke)
			}
			keys = append(keys, key)
			if c := s[vs]; c == '{' || c == '[' {
				// The container's place in vals is filled when it closes.
				vals = append(vals, nil)
				stack = append(stack, open{membersOf(s, vs), len(vals)})
				continue
			}
			ve, esc := valueSpan(s, vs)
			vals = append(vals, scalarValue(resultAt(s, vs, ve, esc)))
			top.it.finish(ve)
			continue
		}
		var v interface{}
		k, n := top.base, len(vals)-top.base
		if top.it.obj {
			m := make(map[string]interface{}, n)
			for ; k < len(vals); k++ {
				if _, dup := m[keys[k]]; !dup {
					m[keys[k]] = vals[k]
				}
			}
			v = m
		} else {
			a := make([]interface{}, n)
			copy(a, vals[k:])
			v = a
		}
		keys, vals = keys[:top.base], vals[:top.base]
		end := top.it.end
		stack = stack[:len(stack)-1]
		if len(stack) == 0 {
			return v
		}
		vals[len(vals)-1] = v
		stack[len(stack)-1].it.finish(end)
	}
}

// scalarValue is Value for a value that is not an object or array.
func scalarValue(t Result) interface{} {
	switch t.Type {
	case String:
		return t.Str
	case Number:
		return t.Num
	case True:
		return true
	case False:
		return false
	}
	return nil
}

// All returns an iterator over the members of an object or the elements of
// an array, as ForEach visits them.
func (t Result) All() iter.Seq2[Result, Result] {
	return func(yield func(key, value Result) bool) { t.ForEach(yield) }
}

// Keys returns an iterator over the keys that ForEach visits.
func (t Result) Keys() iter.Seq[Result] {
	return func(yield func(key Result) bool) {
		t.ForEach(func(k, _ Result) bool { return yield(k) })
	}
}

// Values returns an iterator over the values that ForEach visits.
func (t Result) Values() iter.Seq[Result] {
	return func(yield func(value Result) bool) {
		t.ForEach(func(_, v Result) bool { return yield(v) })
	}
}

// Less reports whether t sorts before token. Values of different types
// sort as Null < False < Number < String < True < JSON. Strings compare by
// bytes, or with ASCII letters folded to lower case when caseSensitive is
// false; numbers compare by value, and other values by their raw text.
func (t Result) Less(token Result, caseSensitive bool) bool {
	if t.Type != token.Type {
		return t.Type < token.Type
	}
	return lessSameType(&t, &token, caseSensitive)
}

// lessSameType is Less for results of the same type, apart so that Less
// inlines.
func lessSameType(t, token *Result, caseSensitive bool) bool {
	switch t.Type {
	case String:
		if caseSensitive {
			return t.Str < token.Str
		}
		return lessFold(t.Str, token.Str)
	case Number:
		return t.Num < token.Num
	}
	return t.Raw < token.Raw
}

// lessFold compares a and b with ASCII upper-case letters read as lower
// case.
func lessFold(a, b string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return ca < cb
		}
	}
	return len(a) < len(b)
}

// Time parses the string form of the value as an RFC 3339 time. It returns
// the zero time when that fails.
func (t Result) Time() time.Time {
	tm, _ := time.Parse(time.RFC3339, t.String())
	return tm
}

// Paths returns, for a result with Indexes such as one from
// "friends.#.first", the path of each element in json, the document the
// result came from. An element whose path cannot be found gets "". The
// result is nil when t has no Indexes.
func (t Result) Paths(json string) []string {
	if t.Indexes == nil {
		return nil
	}
	// Each element needs its index: otherwise there are no paths.
	paths := make([]string, 0, len(t.Indexes))
	var order []int // the elements whose Raw is at their index
	n := 0
	t.ForEach(func(_, v Result) bool {
		if n++; n > len(t.Indexes) {
			return false
		}
		k := len(paths)
		if i := t.Indexes[k]; i >= 0 && i <= len(json) && strings.HasPrefix(json[i:], v.Raw) {
			order = append(order, k)
		}
		paths = append(paths, "")
		return true
	})
	if n != len(t.Indexes) {
		return nil
	}
	if !slices.IsSortedFunc(order, func(a, b int) int { return t.Indexes[a] - t.Indexes[b] }) {
		slices.SortStableFunc(order, func(a, b int) int { return t.Indexes[a] - t.Indexes[b] })
	}
	findPaths(json, t.Indexes, order, paths)
	return paths
}

// Path returns a path that finds t in json, the document t came from, with
// every key escaped, or "" when Raw is not at Index in json. The root value
// has the path "@this".
func (t Result) Path(json string) string {
	if t.Index < 0 || t.Index > len(json) || !strings.HasPrefix(json[t.Index:], t.Raw) {
		return ""
	}
	var path [1]string
	findPaths(json, []int{t.Index}, []int{0}, path[:])
	return path[0]
}

// findPaths sets paths[k] to the path of the value at json[idx[k]], for the
// k in order, whose indexes ascend. It walks json forward from the root and
// reads only up to each index: a member that ends before it is skipped by
// containerEnd on json cut at the index, and a member that does not end
// there holds it. An index that is not at the start of a member value, and
// every index from malformed JSON on, is left to pathBack.
func findPaths(json string, idx, order []int, paths []string) {
	for ti := walkPaths(json, idx, order, paths); ti < len(order); ti++ {
		paths[order[ti]] = pathBack(json, idx[order[ti]])
	}
}

// walkPaths is the forward walk of findPaths. It returns how many targets
// of order it has set.
func walkPaths(json string, idx, order []int, paths []string) int {
	ti := 0
	r := skipSpace(json, 0)
	for ; ti < len(order) && idx[order[ti]] <= r; ti++ {
		if idx[order[ti]] == r {
			paths[order[ti]] = rootPath()
		} else {
			paths[order[ti]] = pathBack(json, idx[order[ti]])
		}
	}
	if ti == len(order) || r >= len(json) || (json[r] != '{' && json[r] != '[') {
		return ti
	}
	// buf holds the components of the open containers, each followed by a
	// dot.
	type frame struct {
		m    members
		n    int // the index of the next element
		plen int // len(buf) before this container's component
	}
	var (
		stackBuf [8]frame
		pathBuf  [128]byte
	)
	stack := append(stackBuf[:0], frame{m: membersOf(json, r)})
	buf := pathBuf[:0]
	for len(stack) > 0 && ti < len(order) {
		f := &stack[len(stack)-1]
		ks, ke, vs, ok := f.m.start()
		if !ok {
			end := f.m.end
			if end < 0 {
				return ti
			}
			buf = buf[:f.plen]
			if stack = stack[:len(stack)-1]; len(stack) > 0 {
				stack[len(stack)-1].m.finish(end)
			}
			continue
		}
		n := f.n
		f.n++
		target := idx[order[ti]]
		for target < vs {
			paths[order[ti]] = pathBack(json, target)
			if ti++; ti == len(order) {
				return ti
			}
			target = idx[order[ti]]
		}
		// Find where the value ends, reading no further than target.
		var ve int
		if c := json[vs]; c == '{' || c == '[' {
			ve = -1
			if vs < target {
				ve = containerEnd(json[:target], vs)
			}
		} else if ve, _ = valueSpan(json, vs); ve <= vs {
			return ti
		}
		if ve >= 0 && ve <= target {
			f.m.finish(ve)
			continue
		}
		// The value holds target or starts at it.
		plen := len(buf)
		if f.m.obj {
			buf = appendEscape(buf, decodedKey(json, ks, ke))
		} else {
			buf = strconv.AppendInt(buf, int64(n), 10)
		}
		for ti < len(order) && idx[order[ti]] == vs {
			paths[order[ti]] = string(buf)
			ti++
		}
		if c := json[vs]; c == '{' || c == '[' {
			// The container may hold the next target. Its members tell
			// where it ends, and the walk goes on from there.
			if ti == len(order) {
				return ti
			}
			buf = append(buf, '.')
			stack = append(stack, frame{m: membersOf(json, vs), plen: plen})
			continue
		}
		buf = buf[:plen]
		for ti < len(order) && idx[order[ti]] < ve {
			paths[order[ti]] = pathBack(json, idx[order[ti]])
			ti++
		}
		f.m.finish(ve)
	}
	return ti
}

// rootPath is the path of the root value: "@this", or "" when modifiers
// are disabled and the path would not work.
func rootPath() string {
	if DisableModifiers {
		return ""
	}
	return "@this"
}

// appendEscape appends Escape(comp) to dst.
func appendEscape(dst []byte, comp string) []byte {
	for i := 0; i < len(comp); i++ {
		if !safeKeyByte(comp[i]) {
			dst = append(dst, '\\')
		}
		dst = append(dst, comp[i])
	}
	return dst
}

// pathBack is Path for a value at json[i], found by walking back to the
// root. It works on any input, but each step back reads one byte.
func pathBack(json string, i int) string {
	// Walk back from the value to the root, collecting the components in
	// reverse.
	var comps []string
	for i--; i >= 0; {
		for i >= 0 && isSpace(json[i]) {
			i--
		}
		if i < 0 {
			break
		}
		switch json[i] {
		case ':':
			i--
			for i >= 0 && isSpace(json[i]) {
				i--
			}
			if i < 0 || json[i] != '"' {
				return ""
			}
			ks := stringStartBack(json, i)
			if ks < 0 {
				return ""
			}
			comps = append(comps, Escape(decodedKey(json, ks, i+1)))
			// Without the opening brace, as in JSON Lines whose earlier
			// values have strings with commas or colons, the walk ends
			// with the components it has.
			if i, _ = openBack(json, ks-1); i < 0 {
				break
			}
			i--
		case ',', '[':
			var n int
			if i, n = arrayBack(json, i); i == -2 {
				return ""
			} else if i < 0 {
				break
			}
			comps = append(comps, strconv.Itoa(n))
			i--
		case '{':
			// An object that is not open around the value: an earlier
			// value of JSON Lines.
			return ""
		default:
			i--
		}
	}
	if len(comps) == 0 {
		return rootPath()
	}
	n := len(comps) - 1
	for _, c := range comps {
		n += len(c)
	}
	b := make([]byte, 0, n)
	for k := len(comps) - 1; k >= 0; k-- {
		b = append(b, comps[k]...)
		if k > 0 {
			b = append(b, '.')
		}
	}
	return bytesView(b)
}

// stringStartBack returns the index of the opening quote of the string
// whose closing quote is json[i], or -1.
func stringStartBack(json string, i int) int {
	for j := i - 1; j >= 0; j-- {
		if json[j] != '"' {
			continue
		}
		k := j - 1
		for k >= 0 && json[k] == '\\' {
			k--
		}
		if (j-k)%2 == 1 {
			return j
		}
	}
	return -1
}

// openBack walks back from json[i] to the bracket that opens the enclosing
// container and returns its index, or -1, and the number of commas between
// them at that depth.
func openBack(json string, i int) (int, int) {
	depth, commas := 0, 0
	for ; i >= 0; i-- {
		switch json[i] {
		case '"':
			if i = stringStartBack(json, i); i < 0 {
				return -1, 0
			}
		case '}', ']':
			depth++
		case '{', '[':
			if depth == 0 {
				return i, commas
			}
			depth--
		case ',':
			if depth == 0 {
				commas++
			}
		}
	}
	return -1, 0
}

// arrayBack walks back from json[i] to the '[' that opens the enclosing
// array and returns its index, or -1, and the number of commas between them
// at that depth. A '{' there opens no array and is passed over, and at a
// ':' there the value is in no array: arrayBack returns -2.
func arrayBack(json string, i int) (int, int) {
	depth, commas := 0, 0
	for ; i >= 0; i-- {
		switch json[i] {
		case '"':
			if i = stringStartBack(json, i); i < 0 {
				return -1, 0
			}
		case '}', ']':
			depth++
		case '{':
			if depth > 0 {
				depth--
			}
		case '[':
			if depth == 0 {
				return i, commas
			}
			depth--
		case ',':
			if depth == 0 {
				commas++
			}
		case ':':
			if depth == 0 {
				return -2, 0
			}
		}
	}
	return -1, 0
}
