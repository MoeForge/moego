package ojson

import "strings"

// Options matches sjson.Options. On valid JSON the options never change
// the document an edit produces, only how fast and where it is written.
type Options struct {
	// Optimistic is a hint that the value likely exists. The edit first
	// looks for it the way Get does, though taking the first of duplicate
	// keys as Set does, and replaces it when found.
	Optimistic bool
	// ReplaceInPlace asks SetBytesOptions and SetRawBytesOptions to write
	// the result into the input's memory when its capacity holds it, after
	// which the input must not be used. As in sjson, it needs Optimistic.
	ReplaceInPlace bool
}

// splice describes an edit as "replace json[lo:hi] with pre, key, val and
// post". key, when hasKey is set, is an object key written as a JSON string
// with a colon. When trim is set, the output keeps only json[from:to]
// around the edit, which drops the whitespace around the root value.
type splice struct {
	lo, hi    int
	pre, post string
	key       string
	hasKey    bool
	val       piece
	trim      bool
	from, to  int
}

// insLen returns the length of the text the splice inserts.
func (sp *splice) insLen() int {
	n := len(sp.pre) + sp.val.len() + len(sp.post)
	if sp.hasKey {
		if needsEscape(sp.key) {
			n += len(appendString(nil, sp.key)) + 1
		} else {
			n += len(sp.key) + 3
		}
	}
	return n
}

// appendIns appends the text the splice inserts.
func (sp *splice) appendIns(dst []byte) []byte {
	dst = append(dst, sp.pre...)
	if sp.hasKey {
		dst = append(appendQuoted(dst, sp.key), ':')
	}
	dst = sp.val.appendTo(dst)
	return append(dst, sp.post...)
}

func (sp *splice) size(n int) int {
	if sp.trim {
		n = sp.to - sp.from
	}
	return n - (sp.hi - sp.lo) + sp.insLen()
}

func (sp *splice) appendTo(dst []byte, json string) []byte {
	from, to := 0, len(json)
	if sp.trim {
		from, to = sp.from, sp.to
	}
	dst = append(dst, json[from:sp.lo]...)
	dst = sp.appendIns(dst)
	return append(dst, json[sp.hi:to]...)
}

func (sp *splice) bytes(json string) []byte {
	return sp.appendTo(makeNoZero(sp.size(len(json)))[:0], json)
}

// inPlace is bytes for the ReplaceInPlace option: the result is written into
// json's own memory when its capacity holds it. The text the splice inserts
// must not lie in that memory, as a raw value cut from json would, since
// moving the rest of the document could overwrite it first.
func (sp *splice) inPlace(json []byte) []byte {
	from, to := 0, len(json)
	if sp.trim {
		from, to = sp.from, sp.to
	}
	n := sp.insLen()
	size := sp.lo - from + n + to - sp.hi
	if size > cap(json) || sp.insertsFrom(json[:cap(json)]) {
		return sp.bytes(bytesView(json))
	}
	buf := json[:cap(json)]
	at := sp.lo - from
	if from > 0 {
		copy(buf, buf[from:sp.lo])
	}
	if at+n != sp.hi {
		copy(buf[at+n:], buf[sp.hi:to])
	}
	sp.appendIns(buf[at:at])
	return buf[:size]
}

// insertsFrom reports whether any text the splice inserts lies in mem.
func (sp *splice) insertsFrom(mem []byte) bool {
	m := bytesView(mem)
	if within(sp.pre, m) || within(sp.post, m) || within(sp.key, m) {
		return true
	}
	for _, s := range sp.val {
		if within(s, m) {
			return true
		}
	}
	return false
}

// inPlace reports whether opts asks for the result in the input's memory,
// which sjson does only when Optimistic is set too.
func inPlace(opts *Options) bool {
	return opts != nil && opts.Optimistic && opts.ReplaceInPlace
}

// Set sets the value at path, creating missing objects and arrays.
func Set(json, path string, value interface{}) (string, error) {
	return SetOptions(json, path, value, nil)
}

// SetOptions is Set with options.
func SetOptions(json, path string, value interface{}, opts *Options) (string, error) {
	var buf [32]byte
	v, err := encodeValue(value, buf[:0])
	if err != nil {
		return "", err
	}
	sp, err := planSetOpts(json, path, v, opts)
	if err != nil {
		if err == errQuery {
			return bytesView(setQuery(json, path, v)), nil
		}
		return json, err
	}
	return bytesView(sp.bytes(json)), nil
}

// SetBytes is Set for a byte slice. The input is not modified.
func SetBytes(json []byte, path string, value interface{}) ([]byte, error) {
	return SetBytesOptions(json, path, value, nil)
}

// SetBytesOptions is SetBytes with options. With Optimistic and
// ReplaceInPlace both set, the result is written into json's memory when
// its capacity allows, and json must not be used afterwards.
func SetBytesOptions(json []byte, path string, value interface{}, opts *Options) ([]byte, error) {
	var buf [32]byte
	v, err := encodeValue(value, buf[:0])
	if err != nil {
		return nil, err
	}
	s := bytesView(json)
	sp, err := planSetOpts(s, path, v, opts)
	if err != nil {
		if err == errQuery {
			return setQueryBytes(json, path, v, opts), nil
		}
		return cloneBytes(json), err
	}
	if inPlace(opts) {
		return sp.inPlace(json), nil
	}
	return sp.bytes(s), nil
}

// SetRaw sets the raw JSON text value at path. value is not validated.
func SetRaw(json, path, value string) (string, error) {
	return SetRawOptions(json, path, value, nil)
}

// SetRawOptions is SetRaw with options.
func SetRawOptions(json, path, value string, opts *Options) (string, error) {
	sp, err := planSetOpts(json, path, textPiece(value), opts)
	if err != nil {
		if err == errQuery {
			return bytesView(setQuery(json, path, textPiece(value))), nil
		}
		return json, err
	}
	return bytesView(sp.bytes(json)), nil
}

// SetRawBytes is SetRaw for byte slices.
func SetRawBytes(json []byte, path string, value []byte) ([]byte, error) {
	return SetRawBytesOptions(json, path, value, nil)
}

// SetRawBytesOptions is SetRawBytes with options, which it treats as
// SetBytesOptions does.
func SetRawBytesOptions(json []byte, path string, value []byte, opts *Options) ([]byte, error) {
	s := bytesView(json)
	sp, err := planSetOpts(s, path, textPiece(bytesView(value)), opts)
	if err != nil {
		if err == errQuery {
			return setQueryBytes(json, path, textPiece(bytesView(value)), opts), nil
		}
		return cloneBytes(json), err
	}
	if inPlace(opts) {
		return sp.inPlace(json), nil
	}
	return sp.bytes(s), nil
}

// Delete removes the value at path. A missing value is not an error.
func Delete(json, path string) (string, error) {
	sp, ok, err := planDelete(json, path, nil)
	if err != nil || !ok {
		return json, err
	}
	return bytesView(sp.bytes(json)), nil
}

// DeleteBytes is Delete for a byte slice. When nothing is deleted the input
// slice is returned.
func DeleteBytes(json []byte, path string) ([]byte, error) {
	s := bytesView(json)
	sp, ok, err := planDelete(s, path, nil)
	if err != nil {
		return cloneBytes(json), err
	}
	if !ok {
		return json, nil
	}
	return sp.bytes(s), nil
}

func cloneBytes(b []byte) []byte {
	out := makeNoZero(len(b))
	copy(out, b)
	return out
}

// planSet locates path in json and returns the splice that sets it to v.
// planSetOpts is planSet with options. With Optimistic it first looks for
// the value the way Get does, which costs less than walking the path the
// way planSet must to create what is missing, and replaces it when found.
func planSetOpts(json, path string, v piece, opts *Options) (splice, error) {
	if opts != nil && opts.Optimistic && optimisticPath(path) {
		if vs, ve := optimisticSpan(json, path); vs > 0 {
			return splice{lo: vs, hi: ve, val: v}, nil
		}
	}
	return planSet(json, path, v, nil)
}

// optimisticSpan returns the span of the value Get finds at path, or -1
// when there is none or Get's result is not a value of json. When the root
// is not an object or array, Get reads from the first bracket even inside a
// string; Set replaces the root then instead of editing the string, unless
// Get found the root itself (a JSON Lines path).
func optimisticSpan(json, path string) (vs, ve int) {
	vs, ve, _ = getSpan(json, path)
	if vs >= 0 && ve >= 0 {
		return vs, ve
	}
	r := getMissing(json, path, vs, ve)
	if vs, ve = r.Index, r.Index+len(r.Raw); vs > 0 && r.Raw != "" && valueAtSpan(json, vs, ve, r.Raw) && (!scalarRoot(json) || vs == skipSpace(json, 0)) {
		return vs, ve
	}
	return -1, -1
}

// optimisticPath reports whether sjson's Optimistic option looks path up
// as Get does before it sets it as usual: only paths made of '.', '/',
// digits, letters and the bytes "[\]^_`". Paths with query bytes are set
// by what Get finds in any case.
func optimisticPath(path string) bool {
	if path == "" {
		return false
	}
	for i := 0; i < len(path); i++ {
		if b := path[i]; b < '.' || b > 'z' || b > '9' && b < 'A' {
			return false
		}
	}
	return true
}

// planSet returns the splice that sets the value at path to v. comps are the
// components of path when the caller has parsed it already, or nil.
func planSet(json, path string, v piece, comps []setComp) (sp splice, err error) {
	if comps == nil {
		if path == "" {
			return splice{}, errEmptyPath
		}
		// Most query paths have a '#'. An unescaped one, before which
		// there is no backslash, is found without parsing the path.
		if k := strings.IndexByte(path, '#'); k >= 0 && strings.IndexByte(path[:k], '\\') < 0 {
			return splice{}, errQuery
		}
		var buf [8]setComp
		var kind int
		if comps, kind = parseSetPath(path, buf[:]); kind == setComplex {
			return splice{}, errQuery
		}
	}
	lo, hi := trimSpace(json)
	if lo == hi || (json[lo] != '{' && json[lo] != '[') {
		// Not a container: the whole input is replaced by a new one.
		pre, post, err := build(comps, true)
		if err != nil {
			return splice{}, err
		}
		return splice{lo: 0, hi: len(json), val: wrap(v, pre, post)}, nil
	}
	sp, err = setIn(json, lo, comps, true)
	sp.val[1], sp.val[2], sp.val[3] = v[1], v[2], v[3]
	return sp, err
}

// setIn applies comps to the container at json[i]. The splice it returns
// holds only what goes around the value, in val[0] and val[4]: the caller
// puts the value in. Passing the value down would make the compiler move a
// value encoded on the caller's stack to the heap. A component that names
// an existing container goes on in the same loop, so the splice is
// returned once instead of being copied out of every level.
func setIn(json string, i int, comps []setComp, root bool) (splice, error) {
	for {
		c, last := &comps[0], len(comps) == 1
		key, vs, ve := c.key, -1, -1
		if c.look {
			if vs = c.lookup(json, i); vs >= 0 {
				ve = valueEnd(json, vs)
			} else {
				key = c.name()
			}
		}
		switch {
		case vs >= 0:
		case json[i] == '{':
			// A member that a lookup component does not find but its plain
			// key names is replaced; sjson adds a second member with that
			// key.
			var close int
			var empty bool
			if vs, ve, close, empty = findKey(json, i, key); vs >= 0 {
				break
			}
			if close < 0 {
				return rebuildRoot(json, comps)
			}
			pre, post, err := build(comps[1:], false)
			if err != nil {
				return splice{}, err
			}
			sep := ","
			if empty {
				sep = ""
			}
			sp := splice{lo: close, hi: close, pre: sep, key: key, hasKey: true, val: wrap(piece{}, pre, post)}
			if root {
				sp = trimOuter(json, sp)
			}
			return sp, nil
		default:
			// Array.
			idx, kind := c.findIndex()
			if kind == idxNone {
				return splice{}, nonNumericErr(key)
			}
			elems := scanArray(json, i, idx, kind)
			if elems.bad {
				return rebuildRoot(json, comps)
			}
			if (kind == idxNumber || kind == idxForced) && idx < elems.n {
				vs, ve = elems.vs, elems.ve
				break
			}
			if kind == idxForced {
				return splice{}, nonNumericErr(key)
			}
			return addElement(json, i, comps, elems, idx, kind, root)
		}
		// json[vs:ve] is the value c names.
		if last {
			return splice{lo: vs, hi: ve}, nil
		}
		if k := json[vs]; k == '{' || k == '[' {
			i, comps, root = vs, comps[1:], false
			continue
		}
		pre, post, err := build(comps[1:], false)
		if err != nil {
			return splice{}, err
		}
		return splice{lo: vs, hi: ve, val: wrap(piece{}, pre, post)}, nil
	}
}

// addElement is setIn for an array component that names no element: the
// value goes after the last element, or the array is rewritten compactly
// with null padding up to the index.
func addElement(json string, i int, comps []setComp, elems arrayScan, idx, kind int, root bool) (splice, error) {
	pre, post, err := build(comps[1:], false)
	if err != nil {
		return splice{}, err
	}
	if kind == idxAppend {
		sep := ","
		if elems.n == 0 {
			sep = ""
		}
		sp := splice{lo: elems.close, hi: elems.close, pre: sep, val: wrap(piece{}, pre, post)}
		if root {
			sp = trimOuter(json, sp)
		}
		return sp, nil
	}
	pad := idx - elems.n
	if kind == idxEmpty || pad < 0 {
		pad = 0
	}
	if pad > maxPad {
		return splice{}, errHugeIndex
	}
	sp := splice{lo: i, hi: elems.close + 1, pre: compactArray(json, i, elems.n, pad), post: "]", val: wrap(piece{}, pre, post)}
	if root {
		sp.lo, sp.hi = 0, len(json)
	}
	return sp, nil
}

// lookup returns the start of the member or element of the container at
// json[i] that gjson's reading of the lookup component c finds there, or
// -1.
func (c *setComp) lookup(json string, i int) int {
	r := Get(json[i:], c.key)
	if r.Index <= 0 || r.Raw == "" || !childAt(json, i, i+r.Index) {
		return -1
	}
	return i + r.Index
}

// childAt reports whether a member or element of the container at json[i]
// starts at p.
func childAt(json string, i, p int) bool {
	obj := json[i] == '{'
	for j := i + 1; ; j++ {
		vs := skipSpace(json, j)
		if obj {
			_, _, v, _, ok := nextMember(json, j)
			if !ok {
				return false
			}
			vs = v
		} else if vs >= len(json) || json[vs] == ']' {
			return false
		}
		if vs >= p {
			return vs == p
		}
		ve := valueEnd(json, vs)
		if ve < 0 {
			return false
		}
		if j = skipSpace(json, ve); j >= len(json) || json[j] != ',' {
			return false
		}
	}
}

// trimOuter widens a root-level splice so whitespace around the root value
// is dropped, as sjson does when it adds a member or element to the root.
func trimOuter(json string, sp splice) splice {
	sp.trim = true
	sp.from, sp.to = trimSpace(json)
	return sp
}

// rebuildRoot handles inputs whose container cannot be scanned to its end.
func rebuildRoot(json string, comps []setComp) (splice, error) {
	return splice{}, errUnsupported
}

// findKey looks for key directly inside the object at json[i]. It returns
// the value span when found. Otherwise close is the index of the closing
// brace, or -1 when the object is malformed, and empty reports whether the
// object has no members.
func findKey(json string, i int, key string) (vs, ve, close int, empty bool) {
	j := i + 1
	empty = true
	for {
		k := skipSpace(json, j)
		if k < len(json) && json[k] == '}' {
			return -1, -1, k, empty
		}
		ks, ke, s, esc, ok := nextMember(json, j)
		if !ok {
			return -1, -1, -1, empty
		}
		empty = false
		e := valueEnd(json, s)
		if e < 0 {
			return -1, -1, -1, false
		}
		if keyIs(json[ks+1:ke-1], esc, key) {
			return s, e, 0, false
		}
		j = skipSpace(json, e)
		if j < len(json) && json[j] == ',' {
			j++
			continue
		}
		if j < len(json) && json[j] == '}' {
			return -1, -1, j, false
		}
		return -1, -1, -1, false
	}
}

type arrayScan struct {
	n      int // elements seen (all of them unless the target was found)
	vs, ve int // span of the target element
	close  int // index of ']'
	bad    bool
}

// scanArray walks the array at json[i]. With a numeric index it stops at that
// element; otherwise it counts all elements and finds the closing bracket.
func scanArray(json string, i, idx, kind int) arrayScan {
	var a arrayScan
	j := skipSpace(json, i+1)
	if j < len(json) && json[j] == ']' {
		a.close = j
		return a
	}
	for {
		j = skipSpace(json, j)
		if j >= len(json) {
			a.bad = true
			return a
		}
		e := valueEnd(json, j)
		if e < 0 {
			a.bad = true
			return a
		}
		if (kind == idxNumber || kind == idxForced) && a.n == idx {
			a.vs, a.ve = j, e
			a.n++
			return a
		}
		a.n++
		j = skipSpace(json, e)
		if j < len(json) && json[j] == ',' {
			j++
			continue
		}
		if j < len(json) && json[j] == ']' {
			a.close = j
			return a
		}
		a.bad = true
		return a
	}
}

// compactArray returns "[" followed by the elements of the array at json[i]
// without surrounding whitespace, joined by commas, then pad nulls, ready for
// one more element.
func compactArray(json string, i, n, pad int) string {
	b := []byte{'['}
	j := i + 1
	for k := 0; k < n; k++ {
		j = skipSpace(json, j)
		e := valueEnd(json, j)
		b = append(append(b, json[j:e]...), ',')
		j = skipSpace(json, e) + 1
	}
	for k := 0; k < pad; k++ {
		b = append(b, "null,"...)
	}
	return bytesView(b)
}

// build returns the text placed before and after a value to create the
// missing components comps. At the root of a new document a "-1" component
// is an object key; elsewhere it starts an array.
func build(comps []setComp, root bool) (pre, post string, err error) {
	if len(comps) == 0 {
		return "", "", nil
	}
	var p, q []byte
	for k, c := range comps {
		idx, kind := c.setIndex()
		if root && k == 0 && kind == idxAppend {
			kind = idxNone
		}
		switch kind {
		case idxNumber:
			if idx > maxPad {
				return "", "", errHugeIndex
			}
			p = append(p, '[')
			for n := 0; n < idx; n++ {
				p = append(p, "null,"...)
			}
			q = append(q, ']')
		case idxAppend, idxEmpty:
			p = append(p, '[')
			q = append(q, ']')
		default:
			p = append(appendQuoted(append(p, '{'), c.name()), ':')
			q = append(q, '}')
		}
	}
	for l, r := 0, len(q)-1; l < r; l, r = l+1, r-1 {
		q[l], q[r] = q[r], q[l]
	}
	return bytesView(p), bytesView(q), nil
}

// planDelete locates path and returns the splice that removes it. ok is false
// when the value does not exist. comps are as for planSet.
func planDelete(json, path string, comps []setComp) (splice, bool, error) {
	if comps == nil {
		if path == "" {
			return splice{}, false, errEmptyPath
		}
		var buf [8]setComp
		var kind int
		if comps, kind = parseSetPath(path, buf[:]); kind == setComplex {
			return splice{}, false, errComplexDel
		}
	}
	lo, hi := trimSpace(json)
	if lo == hi || (json[lo] != '{' && json[lo] != '[') {
		return splice{}, false, nil
	}
	i := lo
	for len(comps) > 1 {
		vs := locate(json, i, comps[0])
		if vs < 0 || (json[vs] != '{' && json[vs] != '[') {
			return splice{}, false, nil
		}
		i = vs
		comps = comps[1:]
	}
	return deleteIn(json, i, comps[0])
}

// quoteSplice returns sjson's deletion of a member whose key text begins
// with an escaped quote; ke is the end of the key, ve the end of the value
// and next the first byte after it that is not space. sjson finds the start
// of a key by scanning back from the value, and skips a quote after a
// backslash together with the byte before the backslash, so for such a key
// it passes the opening quote and stops at an earlier comma, which it
// removes, or brace, after which it removes through a comma that follows
// the value. That place can be in an earlier member or element. ok is false
// when sjson's result is malformed: the member alone is removed then.
func quoteSplice(json string, ke, ve, next int) (sp splice, ok bool) {
	p := -1
	for k := ke - 2; k >= 0; k-- {
		if json[k] != '"' {
			continue
		}
		if k--; k >= 0 && json[k] == '\\' {
			k--
			continue
		}
		for ; k >= 0; k-- {
			if json[k] == '{' || json[k] == ',' {
				p = k
				break
			}
		}
		break
	}
	switch {
	case p < 0:
		return splice{}, false
	case json[p] == ',':
		sp = splice{lo: p, hi: ve}
	case next < len(json) && json[next] == ',':
		sp = splice{lo: p + 1, hi: next + 1}
	default:
		sp = splice{lo: p + 1, hi: ve}
	}
	return sp, Valid(json[:sp.lo] + json[sp.hi:])
}

// locate returns the start of the member or element named by c inside the
// container at json[i], or -1.
func locate(json string, i int, c setComp) int {
	if c.look {
		return c.lookup(json, i)
	}
	if json[i] == '{' {
		vs, _, _, _ := findKey(json, i, c.key)
		return vs
	}
	idx, kind := c.findIndex()
	if kind == idxAppend {
		a := scanArray(json, i, 0, idxAppend)
		if a.bad || a.n == 0 {
			return -1
		}
		a = scanArray(json, i, a.n-1, idxNumber)
		return a.vs
	}
	if kind != idxNumber && kind != idxForced {
		return -1
	}
	a := scanArray(json, i, idx, idxNumber)
	if a.bad || a.n <= idx {
		return -1
	}
	return a.vs
}

// deleteIn removes the member or element named by c from the container at
// json[i]. A later member is removed from the comma before it through its
// value. A first member followed by a comma is removed through that comma;
// for objects the whitespace before it goes too, for arrays it stays.
func deleteIn(json string, i int, c setComp) (splice, bool, error) {
	obj := json[i] == '{'
	idx, kind := 0, idxNumber
	at := -1
	if c.look {
		if at = c.lookup(json, i); at < 0 {
			return splice{}, false, nil
		}
	} else if !obj {
		idx, kind = c.findIndex()
		if kind == idxAppend {
			a := scanArray(json, i, 0, idxAppend)
			if a.bad || a.n == 0 {
				return splice{}, false, nil
			}
			idx, kind = a.n-1, idxNumber
		}
		if kind != idxNumber && kind != idxForced {
			return splice{}, false, nil
		}
	}
	j := i + 1
	comma := -1
	for k := 0; ; k++ {
		start, vs, ks, ke := j, 0, 0, 0
		match := false
		if obj {
			var esc, ok bool
			ks, ke, vs, esc, ok = nextMember(json, j)
			if !ok {
				return splice{}, false, nil
			}
			match = at < 0 && keyIs(json[ks+1:ke-1], esc, c.key)
		} else {
			vs = skipSpace(json, j)
			if vs >= len(json) || json[vs] == ']' {
				return splice{}, false, nil
			}
			start, match = vs, at < 0 && k == idx
		}
		match = match || vs == at
		ve := valueEnd(json, vs)
		if ve < 0 {
			return splice{}, false, nil
		}
		next := skipSpace(json, ve)
		if match {
			if obj && strings.HasPrefix(json[ks+1:ke], `\"`) {
				if sp, ok := quoteSplice(json, ke, ve, next); ok {
					return sp, true, nil
				}
			}
			switch {
			case comma >= 0:
				return splice{lo: comma, hi: ve}, true, nil
			case next < len(json) && json[next] == ',':
				return splice{lo: start, hi: next + 1}, true, nil
			}
			return splice{lo: start, hi: ve}, true, nil
		}
		if next >= len(json) || json[next] != ',' {
			return splice{}, false, nil
		}
		comma = next
		j = next + 1
	}
}
