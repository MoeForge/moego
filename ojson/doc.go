package ojson

import (
	"encoding/json"
	"io"
	"sync"
)

// Doc is a JSON document for a batch of edits. The first edit is kept as one
// splice of the input, so a single edit costs what SetBytes does. From the
// second edit on, the objects and arrays that edits and reads walk through
// are split into members, front to back and only as far as each lookup
// needs; everything else stays a slice of the input, and new text goes into
// blocks the Doc owns. Set and Delete give the same document, byte for byte,
// as SetBytes and DeleteBytes called in sequence, and the output is built
// once.
//
// A Doc reads its input without copying it, so the caller must not modify
// the input while the Doc is in use.
type Doc struct {
	base  string // the input, or the document after an edit applied flat
	t     *tree  // the split document, from the second edit on
	pend  pending
	size  int
	arena []byte // new text; see room
	small [48]byte
}

// pending is the first edit, a splice of base: the document is
// base[:lo] + ins + base[hi:], without the whitespace around the root value
// when trim is set. path and val replay it on the tree.
type pending struct {
	ok, del, trim bool
	lo, hi        int
	ins           string
	path, val     string
}

func (p *pending) appendTo(dst []byte, base string) []byte {
	from, to := 0, len(base)
	if p.trim {
		from, to = trimSpace(base)
	}
	dst = append(dst, base[from:p.lo]...)
	dst = append(dst, p.ins...)
	return append(dst, base[p.hi:to]...)
}

// NewDoc returns a Doc for json.
func NewDoc(json []byte) *Doc {
	d := &Doc{}
	d.reset(bytesView(json))
	return d
}

// NewDocString returns a Doc for json.
func NewDocString(json string) *Doc {
	d := &Doc{}
	d.reset(json)
	return d
}

func (d *Doc) reset(s string) {
	d.base, d.t, d.pend, d.size = s, nil, pending{}, len(s)
	if d.arena == nil {
		d.arena = d.small[:0]
	}
}

// room returns the arena with space for n more bytes. Text is only ever
// appended, and a full block is left as it is for a new one, so strings
// that view the arena stay valid.
func (d *Doc) room(n int) []byte {
	if cap(d.arena)-len(d.arena) >= n {
		return d.arena
	}
	c := 2 * cap(d.arena)
	if c < 512 {
		c = 512
	} else if c > 64<<10 {
		c = 64 << 10
	}
	if c < n {
		c = n
	}
	d.arena = makeNoZero(c)[:0]
	return d.arena
}

// join returns a+b, copying only when both are non-empty.
func (d *Doc) join(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	buf := d.room(len(a) + len(b))
	s := len(buf)
	buf = append(append(buf, a...), b...)
	d.arena = buf
	return bytesView(buf[s:])
}

// encode returns the text of value, encoded like encodeValue does.
func (d *Doc) encode(value interface{}) (string, error) {
	var s string
	switch v := value.(type) {
	case string:
		s = v
	case []byte:
		s = bytesView(v)
	default:
		b := d.room(24)
		n := len(b)
		b, ok := appendScalar(b, value)
		if !ok {
			out, err := json.Marshal(value)
			if err != nil {
				return "", err
			}
			return bytesView(out), nil
		}
		d.arena = b
		return bytesView(b[n:]), nil
	}
	b := d.room(len(s) + 2)
	n := len(b)
	b = appendQuoted(b, s)
	d.arena = b
	return bytesView(b[n:]), nil
}

// tree is a document whose root value is an object or array.
type tree struct {
	lead, trail string // whitespace around the root value
	root        val
}

// val is a value in a tree: text, from the input or new, or a container
// being split into members.
type val struct {
	s string
	c *container
}

func (v *val) len() int {
	if v.c != nil {
		return v.c.size
	}
	return len(v.s)
}

func (v *val) isContainer() bool {
	return v.c != nil || v.s != "" && (v.s[0] == '{' || v.s[0] == '[')
}

// open returns the container that v holds.
func (v *val) open() *container {
	if v.c == nil {
		v.c = newContainer(v.s)
		v.s = ""
	}
	return v.c
}

func (v *val) appendTo(dst []byte) []byte {
	if v.c != nil {
		return v.c.appendTo(dst)
	}
	return append(dst, v.s...)
}

// text returns the value as one string.
func (v *val) text() string {
	if v.c == nil {
		return v.s
	}
	return bytesView(v.c.appendTo(makeNoZero(v.c.size)[:0]))
}

// container is an object or array split into members from the front. Its
// text is the opening bracket, the gap and value of each member, then tail.
type container struct {
	items []member
	// tail is the text after the last split value: the whitespace after
	// it, then a comma and the members not split yet, or the closing
	// bracket. Before the first member is split it is everything after the
	// opening bracket.
	tail string
	size int // length of the text
	obj  bool
	done bool // every member is split
}

// member is a member or element. gap is the text from the end of the
// previous value, or from the opening bracket, to the start of this value:
// the whitespace after the previous value and the comma, the whitespace
// before this member, and an object member's key and colon.
type member struct {
	gap string
	v   val
	// An object member's key, without quotes, is gap[ks:ks+kn]. A negative
	// kn is -len for a key with escapes, and slowKey a key past the range
	// of int32 that nameIs finds by scanning the gap.
	ks, kn int32
}

const slowKey = -1 << 31

// setKey records that the key of m, quoted, starts at gap[q].
func (m *member) setKey(q, n int, esc bool) {
	if q+1+n > 1<<31-1 {
		m.ks, m.kn = 0, slowKey
		return
	}
	m.ks, m.kn = int32(q+1), int32(n)
	if esc {
		m.kn = -m.kn
	}
}

// shiftKey records that the gap of m gained d bytes at its start.
func (m *member) shiftKey(d int) {
	if m.kn == slowKey || int(m.ks)+d > 1<<31-1-int(abs32(m.kn)) {
		m.ks, m.kn = 0, slowKey
		return
	}
	m.ks += int32(d)
}

func abs32(n int32) int32 {
	if n < 0 {
		return -n
	}
	return n
}

func newContainer(s string) *container {
	c := &container{obj: s[0] == '{', tail: s[1:], size: len(s)}
	if k := skipSpace(s, 1); k == len(s)-1 && s[k] == c.closing() {
		c.done = true
	}
	return c
}

func (c *container) closing() byte {
	if c.obj {
		return '}'
	}
	return ']'
}

// splitOne splits the next member off tail. It reports false when the text
// there is not a member followed by a comma or by the closing bracket that
// ends the container.
func (c *container) splitOne() bool {
	t := c.tail
	j := 0
	if len(c.items) > 0 {
		j = skipSpace(t, 0) + 1 // the comma
	}
	var m member
	vs := 0
	if c.obj {
		ks, ke, v, esc, ok := nextMember(t, j)
		if !ok {
			return false
		}
		m.setKey(ks, ke-ks-2, esc)
		vs = v
	} else if vs = skipSpace(t, j); vs >= len(t) {
		return false
	}
	ve := valueEnd(t, vs)
	if ve <= vs {
		return false
	}
	k := skipSpace(t, ve)
	if k >= len(t) {
		return false
	}
	if t[k] != ',' {
		if t[k] != c.closing() || k != len(t)-1 {
			return false
		}
		c.done = true
	}
	if c.items == nil {
		c.items = make([]member, 0, 4)
	}
	m.gap, m.v.s = t[:vs], t[vs:ve]
	c.items = append(c.items, m)
	c.tail = t[ve:]
	return true
}

// splitTo splits members until member n exists or none are left.
func (c *container) splitTo(n int) bool {
	for len(c.items) <= n && !c.done {
		if !c.splitOne() {
			return false
		}
	}
	return true
}

func (c *container) splitAll() bool { return c.splitTo(maxInt) }

// nameIs reports whether the key of member i decodes to key.
func (c *container) nameIs(i int, key string) bool {
	m := &c.items[i]
	switch {
	case m.kn >= 0:
		return int(m.kn) == len(key) && m.gap[m.ks:int(m.ks)+len(key)] == key
	case m.kn != slowKey:
		return keyEqual(m.gap[m.ks:m.ks-m.kn], key)
	}
	g := m.gap
	j := skipSpace(g, 0)
	if i > 0 {
		j = skipSpace(g, j+1)
	}
	esc := false
	e := quickEnd(g, j+1)
	if e == 0 {
		e, esc = scanString(g, j+1)
	}
	return keyIs(g[j+1:e-1], esc, key)
}

// find returns the first member named key at or after member i, splitting
// as far as needed. It returns -1 when there is none and -2 when the text
// cannot be split.
func (c *container) find(key string, i int) int {
	for ; ; i++ {
		if i == len(c.items) {
			if c.done {
				return -1
			}
			if !c.splitOne() {
				return -2
			}
		}
		// A key without escapes matches only one of the same length.
		if m := &c.items[i]; int(m.kn) == len(key) {
			if m.gap[m.ks:int(m.ks)+len(key)] == key {
				return i
			}
		} else if m.kn < 0 && c.nameIs(i, key) {
			return i
		}
	}
}

// index returns the element an array component names, -1 when there is
// none, or -2 when the text cannot be split.
func (c *container) index(k setComp) int {
	idx, kind := k.findIndex()
	switch kind {
	case idxAppend:
		if !c.splitAll() {
			return -2
		}
		return len(c.items) - 1
	case idxNumber, idxForced:
		if !c.splitTo(idx) {
			return -2
		}
		if idx < len(c.items) {
			return idx
		}
	}
	return -1
}

// locate returns the member named by k, like locate in set.go.
func (c *container) locate(k setComp) int {
	if c.obj {
		return c.find(k.key, 0)
	}
	return c.index(k)
}

func (c *container) measure() int {
	n := 1 + len(c.tail)
	for i := range c.items {
		n += len(c.items[i].gap) + c.items[i].v.len()
	}
	return n
}

func (c *container) appendTo(dst []byte) []byte {
	open := byte('[')
	if c.obj {
		open = '{'
	}
	dst = append(dst, open)
	for i := range c.items {
		m := &c.items[i]
		dst = append(dst, m.gap...)
		if m.v.c != nil {
			dst = m.v.c.appendTo(dst)
		} else {
			dst = append(dst, m.v.s...)
		}
	}
	return append(dst, c.tail...)
}

// errFallback makes an edit go through the flat edit functions.
var errFallback = &pathError{"ojson: fallback"}

// Set sets the value at path, like SetBytes.
func (d *Doc) Set(path string, value interface{}) error {
	v, err := d.encode(value)
	if err != nil {
		return err
	}
	return d.set(path, v)
}

// SetRaw sets the raw JSON text value at path, like SetRawBytes. value is
// not validated.
func (d *Doc) SetRaw(path, value string) error {
	if !cleanValue(value) {
		return d.flatSet(path, value)
	}
	return d.set(path, value)
}

// SetRawBytes is SetRaw for a byte slice. The bytes are copied.
func (d *Doc) SetRawBytes(path string, value []byte) error {
	return d.SetRaw(path, string(value))
}

// cleanValue reports whether t is one value with nothing around it, so that
// scanning the edited document finds exactly t.
func cleanValue(t string) bool {
	return t != "" && !isSpace(t[0]) && valueEnd(t, 0) == len(t)
}

func (d *Doc) set(path, v string) error {
	if path == "" {
		return errEmptyPath
	}
	var buf [8]setComp
	comps, kind := parseSetPath(path, buf[:])
	if kind != setPlain {
		return d.flatSet(path, v)
	}
	if d.t == nil && d.pend.ok {
		d.replay()
	}
	if d.t == nil && !d.pend.ok {
		sp, err := planSet(d.base, path, textPiece(v), comps)
		if err != nil {
			return err
		}
		d.keep(&sp, path, v, false)
		return nil
	}
	return d.treeSet(path, comps, v)
}

// keep makes sp the pending edit.
func (d *Doc) keep(sp *splice, path, v string, del bool) {
	ins := sp.val[1]
	if sp.pre != "" || sp.post != "" || sp.hasKey || sp.val[0] != "" || sp.val[2] != "" || sp.val[3] != "" || sp.val[4] != "" {
		b := d.room(sp.insLen())
		s := len(b)
		b = sp.appendIns(b)
		d.arena = b
		ins = bytesView(b[s:])
	}
	d.pend = pending{ok: true, del: del, trim: sp.trim, lo: sp.lo, hi: sp.hi, ins: ins, path: path, val: v}
	d.size = sp.size(len(d.base))
}

// replay moves the pending edit onto a tree, or applies it flat when the
// root is not an object or array.
func (d *Doc) replay() {
	p := d.pend
	lo, hi := trimSpace(d.base)
	if lo == hi || d.base[lo] != '{' && d.base[lo] != '[' {
		d.reset(bytesView(p.appendTo(makeNoZero(d.size)[:0], d.base)))
		return
	}
	d.pend = pending{}
	d.t = &tree{lead: d.base[:lo], trail: d.base[hi:], root: val{s: d.base[lo:hi]}}
	d.size = len(d.base)
	// The edit succeeded on the same text, so it succeeds again.
	var buf [8]setComp
	comps, _ := parseSetPath(p.path, buf[:])
	if p.del {
		d.treeDelete(p.path, comps)
	} else {
		d.treeSet(p.path, comps, p.val)
	}
}

func (d *Doc) treeSet(path string, comps []setComp, v string) error {
	_, err := d.setIn(d.t.root.open(), comps, v, true)
	if err == errFallback {
		return d.flatSet(path, v)
	}
	d.size = len(d.t.lead) + d.t.root.len() + len(d.t.trail)
	return err
}

// setIn applies comps to c and returns the change in c's length. The rules
// follow setIn in set.go. Nothing changes when it returns an error.
func (d *Doc) setIn(c *container, comps []setComp, v string, root bool) (int, error) {
	k, rest := comps[0], comps[1:]
	if c.obj {
		i := c.find(k.key, 0)
		if i >= 0 {
			return d.setVal(c, i, rest, v)
		}
		if i == -2 {
			return 0, errFallback
		}
		w, err := d.wrap(rest, v)
		if err != nil {
			return 0, err
		}
		return d.add(c, k.key, w, root), nil
	}
	idx, kind := k.findIndex()
	if kind == idxNone {
		return 0, nonNumericErr(k.key)
	}
	if kind == idxNumber || kind == idxForced {
		if !c.splitTo(idx) {
			return 0, errFallback
		}
		if idx < len(c.items) {
			return d.setVal(c, idx, rest, v)
		}
		if kind == idxForced {
			return 0, nonNumericErr(k.key)
		}
	}
	if !c.splitAll() {
		return 0, errFallback
	}
	w, err := d.wrap(rest, v)
	if err != nil {
		return 0, err
	}
	if kind == idxAppend {
		return d.add(c, "", w, root), nil
	}
	pad := 0
	if kind == idxNumber || kind == idxPad {
		pad = max(idx-len(c.items), 0)
	}
	if pad > maxPad {
		return 0, errHugeIndex
	}
	if pad > 1<<12 {
		return 0, errFallback // written flat, without a member per null
	}
	// The array is rewritten compactly, padded with nulls.
	old := c.size
	for i := range c.items {
		c.items[i].gap = ","
	}
	if c.items == nil {
		c.items = make([]member, 0, pad+1)
	}
	for ; pad > 0; pad-- {
		c.items = append(c.items, member{gap: ",", v: val{s: "null"}})
	}
	c.items = append(c.items, member{gap: ",", v: val{s: w}})
	c.items[0].gap = ""
	c.tail = "]"
	c.size = c.measure()
	if root {
		d.t.lead, d.t.trail = "", ""
	}
	return c.size - old, nil
}

// setVal applies rest to the value of member i of c.
func (d *Doc) setVal(c *container, i int, rest []setComp, v string) (int, error) {
	m := &c.items[i]
	if len(rest) > 0 {
		if m.v.isContainer() {
			delta, err := d.setIn(m.v.open(), rest, v, false)
			c.size += delta
			return delta, err
		}
		w, err := d.wrap(rest, v)
		if err != nil {
			return 0, err
		}
		v = w
	}
	delta := len(v) - m.v.len()
	m.v = val{s: v}
	c.size += delta
	return delta, nil
}

// add appends a member with value v, and key when c is an object, before
// the closing bracket of the fully split c. Adding to the root container
// drops the whitespace around the document.
func (d *Doc) add(c *container, key, v string, root bool) int {
	ws := c.tail[:len(c.tail)-1]
	b := d.room(len(ws) + len(key) + 4)
	s := len(b)
	b = append(b, ws...)
	if len(c.items) > 0 {
		b = append(b, ',')
	}
	var m member
	if c.obj {
		q := len(b) - s
		b = appendQuoted(b, key)
		n := len(b) - s - q - 2
		m.setKey(q, n, n != len(key))
		b = append(b, ':')
	}
	d.arena = b
	m.gap, m.v.s = bytesView(b[s:]), v
	if c.items == nil {
		c.items = make([]member, 0, 4)
	}
	c.items = append(c.items, m)
	c.tail = c.tail[len(ws):]
	delta := len(m.gap) + len(v) - len(ws)
	c.size += delta
	if root && len(d.t.lead)+len(d.t.trail) > 0 {
		d.t.lead, d.t.trail = "", ""
	}
	return delta
}

// wrap returns v inside new objects and arrays for the missing components
// rest, as build does.
func (d *Doc) wrap(rest []setComp, v string) (string, error) {
	if len(rest) == 0 {
		return v, nil
	}
	n := len(v)
	for _, c := range rest {
		switch idx, kind := c.setIndex(); kind {
		case idxNumber:
			if idx > maxPad {
				return "", errHugeIndex
			}
			n += 2 + 5*idx
		case idxAppend, idxEmpty:
			n += 2
		default:
			n += len(c.key) + 5
		}
	}
	b := d.room(n)
	s := len(b)
	for _, c := range rest {
		switch idx, kind := c.setIndex(); kind {
		case idxNumber:
			b = append(b, '[')
			for ; idx > 0; idx-- {
				b = append(b, "null,"...)
			}
		case idxAppend, idxEmpty:
			b = append(b, '[')
		default:
			b = append(appendQuoted(append(b, '{'), c.key), ':')
		}
	}
	b = append(b, v...)
	for k := len(rest) - 1; k >= 0; k-- {
		switch _, kind := rest[k].setIndex(); kind {
		case idxNumber, idxAppend, idxEmpty:
			b = append(b, ']')
		default:
			b = append(b, '}')
		}
	}
	d.arena = b
	return bytesView(b[s:]), nil
}

func (d *Doc) flatSet(path, v string) error {
	s := d.flat()
	sp, err := planSet(s, path, textPiece(v), nil)
	if err == errQuery {
		d.reset(bytesView(setQuery(s, path, textPiece(v))))
		return nil
	}
	if err != nil {
		return err
	}
	d.reset(bytesView(sp.bytes(s)))
	return nil
}

// Delete removes the value at path, like DeleteBytes.
func (d *Doc) Delete(path string) error {
	if path == "" {
		return errEmptyPath
	}
	var buf [8]setComp
	comps, kind := parseSetPath(path, buf[:])
	switch kind {
	case setComplex:
		return errComplexDel
	case setLookup:
		return d.flatDelete(path)
	}
	if quoteKeyPath(path) {
		return d.flatDelete(path)
	}
	if d.t == nil && d.pend.ok {
		d.replay()
	}
	if d.t == nil && !d.pend.ok {
		sp, ok, err := planDelete(d.base, path, comps)
		if err != nil || !ok {
			return err
		}
		d.keep(&sp, path, "", true)
		return nil
	}
	return d.treeDelete(path, comps)
}

func (d *Doc) treeDelete(path string, comps []setComp) error {
	c := d.t.root.open()
	var stack [16]*container
	walked := stack[:0]
	for ; len(comps) > 1; comps = comps[1:] {
		i := c.locate(comps[0])
		if i == -2 {
			return d.flatDelete(path)
		}
		if i < 0 || !c.items[i].v.isContainer() {
			return nil
		}
		walked = append(walked, c)
		c = c.items[i].v.open()
	}
	delta, ok := d.remove(c, comps[0])
	if !ok {
		return d.flatDelete(path)
	}
	for _, p := range walked {
		p.size += delta
	}
	d.size += delta
	return nil
}

// remove deletes the member named by k with the whitespace rules of
// deleteIn in set.go, and returns the change in c's length. It reports
// false when the text cannot be split.
func (d *Doc) remove(c *container, k setComp) (int, bool) {
	i := c.locate(k)
	if i == -2 {
		return 0, false
	}
	if i < 0 {
		return 0, true
	}
	if i == 0 && len(c.items) == 1 && !c.done && !c.splitOne() {
		return 0, false
	}
	m := c.items[i]
	switch {
	case i > 0:
		// From the comma before the member through its value: the
		// whitespace after the previous value stays.
		g := m.gap
		ws := g[:skipSpace(g, 0)]
		if i+1 < len(c.items) {
			n := &c.items[i+1]
			n.gap = d.join(ws, n.gap)
			n.shiftKey(len(ws))
		} else {
			c.tail = d.join(ws, c.tail)
		}
	case len(c.items) > 1:
		// Through the comma after it; an array keeps the whitespace before.
		n := &c.items[1]
		cut := skipSpace(n.gap, 0) + 1
		n.gap = n.gap[cut:]
		if c.obj {
			n.shiftKey(-cut)
		} else {
			n.gap = d.join(m.gap, n.gap)
		}
	case !c.obj:
		c.tail = d.join(m.gap, c.tail)
	}
	old := c.size
	c.items = append(c.items[:i], c.items[i+1:]...)
	c.size = c.measure()
	return c.size - old, true
}

func (d *Doc) flatDelete(path string) error {
	s := d.flat()
	sp, ok, err := planDelete(s, path, nil)
	if err != nil || !ok {
		return err
	}
	d.reset(bytesView(sp.bytes(s)))
	return nil
}

// Get returns the value at path in the current document, as Get on String
// would. The result may share memory with the Doc's input.
func (d *Doc) Get(path string) Result {
	if d.t == nil && d.pend.ok {
		d.replay()
	}
	if d.t == nil {
		return Get(d.base, path)
	}
	if path == "" || queryStart(path) || !plainComps(path) {
		return Get(d.flat(), path)
	}
	r, ok, bad := getIn(&d.t.root, path, 0, len(d.t.lead))
	if bad || !ok && d.t.root.c == nil && scalarRoot(d.t.root.s) {
		return Get(d.flat(), path)
	}
	return r
}

// flat returns the current document as one string, and makes it the base
// of the Doc so that later reads and query edits use it without copying
// the document again.
func (d *Doc) flat() string {
	if d.t == nil && !d.pend.ok {
		return d.base
	}
	s := d.String()
	d.reset(s)
	return s
}

// getIn resolves path[pi:] in v, whose text starts at offset at of the
// document. It follows find in get.go. bad reports text that cannot be
// split.
func getIn(v *val, path string, pi, at int) (r Result, ok, bad bool) {
	if v.c == nil {
		t := v.s
		fs, fe, esc := find(t, 0, path, pi, 0)
		if fs < 0 || fe < 0 {
			return Result{}, false, false
		}
		r := resultAt(t, fs, fe, esc)
		if r.Raw != "" {
			r.Index += at
		}
		return r, true, false
	}
	c := v.c
	raw, next, esc := nextComp(path, pi)
	key := compKey(raw, esc)
	if c.obj {
		for i := 0; ; i++ {
			if i = c.find(key, i); i < 0 {
				return Result{}, false, i == -2
			}
			m := &c.items[i]
			vs := c.offset(i, at)
			if next < 0 {
				return valResult(&m.v, vs), true, false
			}
			if m.v.isContainer() {
				if r, ok, bad := getIn(&m.v, path, next, vs); ok || bad {
					return r, ok, bad
				}
			}
		}
	}
	n, ok := arrayIndex(key)
	if !ok || esc {
		return Result{}, false, false
	}
	if !c.splitTo(n) {
		return Result{}, false, true
	}
	if n >= len(c.items) {
		return Result{}, false, false
	}
	m := &c.items[n]
	vs := c.offset(n, at)
	if next < 0 {
		return valResult(&m.v, vs), true, false
	}
	if m.v.isContainer() {
		return getIn(&m.v, path, next, vs)
	}
	return Result{}, false, false
}

// offset returns where the value of member i starts, for a container that
// starts at offset at.
func (c *container) offset(i, at int) int {
	at++
	for j := 0; j < i; j++ {
		at += len(c.items[j].gap) + c.items[j].v.len()
	}
	return at + len(c.items[i].gap)
}

func valResult(v *val, at int) Result {
	t := v.text()
	r, _ := valueResult(t, 0, len(t))
	if r.Raw != "" {
		r.Index = at
	}
	return r
}

// Len returns the length of the current document in bytes.
func (d *Doc) Len() int { return d.size }

// AppendTo appends the current document to dst.
func (d *Doc) AppendTo(dst []byte) []byte {
	switch {
	case d.t != nil:
		dst = append(dst, d.t.lead...)
		dst = d.t.root.appendTo(dst)
		return append(dst, d.t.trail...)
	case d.pend.ok:
		return d.pend.appendTo(dst, d.base)
	}
	return append(dst, d.base...)
}

// Bytes returns the current document in a new slice.
func (d *Doc) Bytes() []byte { return d.AppendTo(makeNoZero(d.size)[:0]) }

// String returns the current document.
func (d *Doc) String() string { return bytesView(d.Bytes()) }

// segBufs holds the buffers WriteTo gathers short pieces in, so that it
// does not allocate one per call.
var segBufs = sync.Pool{New: func() any { return new([4 << 10]byte) }}

// WriteTo writes the current document to w. Short pieces are gathered in a
// buffer; long runs of the input are written to w directly.
func (d *Doc) WriteTo(w io.Writer) (int64, error) {
	buf := segBufs.Get().(*[4 << 10]byte)
	defer segBufs.Put(buf)
	sw := segWriter{w: w, buf: buf[:0]}
	switch {
	case d.t != nil:
		sw.write(d.t.lead)
		sw.val(&d.t.root)
		sw.write(d.t.trail)
	case d.pend.ok:
		from, to := 0, len(d.base)
		if d.pend.trim {
			from, to = trimSpace(d.base)
		}
		sw.write(d.base[from:d.pend.lo])
		sw.write(d.pend.ins)
		sw.write(d.base[d.pend.hi:to])
	default:
		sw.write(d.base)
	}
	sw.flush()
	return sw.n, sw.err
}

type segWriter struct {
	w   io.Writer
	buf []byte
	n   int64
	err error
}

func (sw *segWriter) write(s string) {
	if len(s) <= cap(sw.buf)-len(sw.buf) {
		sw.buf = append(sw.buf, s...)
		return
	}
	sw.flush()
	if len(s) < cap(sw.buf)/2 {
		sw.buf = append(sw.buf, s...)
		return
	}
	if sw.err == nil {
		n, err := sw.w.Write(stringBytes(s))
		sw.n += int64(n)
		sw.err = err
	}
}

func (sw *segWriter) flush() {
	if len(sw.buf) > 0 && sw.err == nil {
		n, err := sw.w.Write(sw.buf)
		sw.n += int64(n)
		sw.err = err
	}
	sw.buf = sw.buf[:0]
}

func (sw *segWriter) val(v *val) {
	if v.c == nil {
		sw.write(v.s)
		return
	}
	c := v.c
	if c.obj {
		sw.write("{")
	} else {
		sw.write("[")
	}
	for i := range c.items {
		sw.write(c.items[i].gap)
		sw.val(&c.items[i].v)
	}
	sw.write(c.tail)
}
