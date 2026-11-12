package ojson

import (
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// readerWindow is the size of the reads. Tests make it small.
var readerWindow = 64 << 10

// ValidReader reports whether the bytes r returns until io.EOF are valid
// JSON, as Valid does for the same bytes, and returns an error other than
// io.EOF that r returned. It reads through a fixed buffer and stops at the
// first byte that makes the input invalid, so it uses memory for the
// nesting of the input and not for its size, unless a single number or
// literal is longer than the buffer.
func ValidReader(r io.Reader) (bool, error) {
	var c jsonsimd.StreamCheck
	buf := make([]byte, readerWindow)
	n := 0
	ended := false
	for {
		k, err := r.Read(buf[n:])
		n += k
		if err != nil && err != io.EOF {
			return false, err
		}
		eof := err == io.EOF
		b := buf[:n]
		if !ended {
			used, st := c.Feed(b, eof)
			switch st {
			case jsonsimd.StreamInvalid:
				return false, nil
			case jsonsimd.StreamEnd:
				ended = true
			}
			b = b[used:]
		}
		if ended {
			for _, c := range b {
				if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
					return false, nil
				}
			}
			b = nil
		}
		if eof {
			return ended, nil
		}
		n = copy(buf, b)
		if n == len(buf) {
			// One number or literal fills the buffer.
			buf = append(buf, make([]byte, len(buf))...)
		}
	}
}

// GetManyReaderAt is GetMany for the size bytes at the start of r: the
// results are the ones GetMany returns for those bytes, and they do not
// share memory with r.
//
// When each path is a chain of member names and array indexes, made of
// letters, digits, '_' and '-', GetManyReaderAt reads r through a fixed
// buffer, stops once every path is resolved, and keeps only the values that
// the paths name. Any other path, and malformed JSON that it meets on the
// way, make it read all size bytes into memory.
func GetManyReaderAt(r io.ReaderAt, size int64, paths ...string) ([]Result, error) {
	if size < 0 {
		return nil, errors.New("ojson: negative size")
	}
	root := outlinePaths(paths)
	if root == nil {
		return getManyWhole(r, size, paths)
	}
	o := &outline{r: r, size: size}
	o.value(root, true)
	if o.err != nil {
		return nil, o.err
	}
	if o.bad {
		return getManyWhole(r, size, paths)
	}
	res := GetMany(bytesView(o.out), paths...)
	for k := range res {
		if err := o.place(&res[k]); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func getManyWhole(r io.ReaderAt, size int64, paths []string) ([]Result, error) {
	b := makeNoZero(int(size))
	if _, err := io.ReadFull(io.NewSectionReader(r, 0, size), b); err != nil {
		return nil, err
	}
	return GetMany(bytesView(b), paths...), nil
}

// outlineNode is a position in the paths: what they name below a value.
type outlineNode struct {
	leaf     bool // a path ends here
	hit      bool // its value was read
	members  map[string]*outlineNode
	elems    map[int]*outlineNode
	maxIndex int
}

// outlinePaths returns the tree of the paths, or nil when one of them is not
// a plain chain of names and indexes.
func outlinePaths(paths []string) *outlineNode {
	root := &outlineNode{maxIndex: -1}
	for _, p := range paths {
		if p == "" {
			return nil
		}
		n := root
		for len(p) >= 0 {
			comp, rest, more := strings.Cut(p, ".")
			if comp == "" || len(comp) > 256 {
				return nil
			}
			for i := 0; i < len(comp); i++ {
				if c := comp[i]; !(c-'0' < 10 || (c|0x20)-'a' < 26 || c == '_' || c == '-') {
					return nil
				}
			}
			child := n.members[comp]
			if child == nil {
				child = &outlineNode{maxIndex: -1}
				if n.members == nil {
					n.members = map[string]*outlineNode{}
				}
				n.members[comp] = child
				if comp[0]-'0' < 10 {
					k, err := strconv.Atoi(comp)
					if err != nil || len(comp) > 9 || strconv.Itoa(k) != comp {
						return nil
					}
					if n.elems == nil {
						n.elems = map[int]*outlineNode{}
					}
					n.elems[k] = child
					n.maxIndex = max(n.maxIndex, k)
				}
			}
			n = child
			if !more {
				n.leaf = true
				break
			}
			p = rest
		}
	}
	return root
}

// countLeaves returns the paths not yet resolved below n.
func (n *outlineNode) countLeaves() int {
	c := 0
	if n.leaf && !n.hit {
		c = 1
	}
	for _, m := range n.members {
		c += m.countLeaves()
	}
	return c
}

// resolve marks the paths at and below n as resolved, and returns how many
// were not.
func (n *outlineNode) resolve() int {
	c := 0
	if n.leaf && !n.hit {
		n.hit = true
		c = 1
	}
	for _, m := range n.members {
		c += m.resolve()
	}
	return c
}

// outlineLong is the length from which a string is left out of an outline.
// A path can not name a key that long: the components of the paths are at
// most 256 bytes, or six times that with every byte escaped.
const outlineLong = 4 << 10

// An outline is a copy of a document that holds only what the paths reach,
// so that GetMany on it finds the same values: the members the paths name,
// with other members left out, zeros for the elements before an index, and
// empty strings for long strings. seg maps it back to the document.
type outline struct {
	r    io.ReaderAt
	size int64
	win  []byte
	off  int64 // where win is in the document
	pos  int64 // the next byte to read
	out  []byte
	segs []outlineSeg
	left int  // paths not resolved
	init bool // left is set
	bad  bool // not the JSON a walk can follow
	err  error
}

// outlineSeg says that n bytes of the outline from at are the docN bytes of
// the document from doc. They differ for a long string.
type outlineSeg struct {
	at, n     int
	doc, docN int64
}

// fill moves the window to o.pos, and reports whether a byte is there.
func (o *outline) fill() bool {
	if o.pos >= o.size || o.err != nil {
		return false
	}
	if o.pos >= o.off && o.pos < o.off+int64(len(o.win)) {
		return true
	}
	if o.win == nil {
		o.win = make([]byte, readerWindow)
	}
	n := int(min(int64(cap(o.win)), o.size-o.pos))
	o.win = o.win[:n]
	if k, err := o.r.ReadAt(o.win, o.pos); k < n {
		if err == nil || err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		o.err = err
		return false
	}
	o.off = o.pos
	return true
}

// peek returns the byte at o.pos, or marks the document bad at its end.
func (o *outline) peek() byte {
	if !o.fill() {
		o.bad = true
		return 0
	}
	return o.win[o.pos-o.off]
}

func (o *outline) space() {
	for o.fill() {
		c := o.win[o.pos-o.off]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return
		}
		o.pos++
	}
}

// add appends the n bytes at o.pos to the outline and moves past them.
func (o *outline) add(b []byte) {
	if k := len(o.segs) - 1; k >= 0 {
		if sg := &o.segs[k]; sg.at+sg.n == len(o.out) && sg.doc+sg.docN == o.pos && int64(sg.n) == sg.docN {
			sg.n += len(b)
			sg.docN += int64(len(b))
			o.out = append(o.out, b...)
			o.pos += int64(len(b))
			return
		}
	}
	o.segs = append(o.segs, outlineSeg{len(o.out), len(b), o.pos, int64(len(b))})
	o.out = append(o.out, b...)
	o.pos += int64(len(b))
}

// stringToken moves past the string at o.pos, with its opening quote, and
// returns where it starts in the outline. With keep, the outline holds it,
// or an empty string when it is long.
func (o *outline) stringToken(keep bool) {
	start := o.pos
	mark, nsegs := len(o.out), len(o.segs)
	long := false
	// from is where the contents go on after the part of the string in the
	// last window: after the opening quote, or after an escaped byte.
	from := 1
	for {
		if !o.fill() {
			o.bad = true
			return
		}
		w := bytesView(o.win[o.pos-o.off:])
		i := from
		from = 0
		for {
			if i >= len(w) {
				from = i - len(w)
				break
			}
			i = jsonsimd.PlainEnd(w, i)
			if i >= len(w) {
				break
			}
			c := w[i]
			if c == '\\' {
				i += 2
				continue
			}
			if c != '"' {
				i++ // a control byte: the walk does not judge it
				continue
			}
			i++
			if keep && !long {
				o.add(o.win[o.pos-o.off : o.pos-o.off+int64(i)])
			} else {
				o.pos += int64(i)
			}
			if keep && long {
				o.segs = append(o.segs, outlineSeg{len(o.out), 2, start, o.pos - start})
				o.out = append(o.out, '"', '"')
			}
			return
		}
		i = min(i, len(w))
		if keep && !long {
			if o.pos-start+int64(i) >= outlineLong {
				long = true
				o.out, o.segs = o.out[:mark], o.segs[:nsegs]
				o.pos += int64(i)
			} else {
				o.add(o.win[o.pos-o.off : o.pos-o.off+int64(i)])
			}
		} else {
			o.pos += int64(i)
		}
	}
}

// skip moves past the value at o.pos. With keep, the outline holds it, with
// empty strings for its long strings.
func (o *outline) skip(keep bool) {
	depth, start := 0, o.pos
	for {
		if !o.fill() {
			o.bad = true
			return
		}
		w := o.win[o.pos-o.off:]
		switch c := w[0]; c {
		case '"':
			o.stringToken(keep)
		case '{', '[':
			depth++
			o.one(keep, w)
		case '}', ']':
			depth--
			if depth < 0 {
				o.bad = true
				return
			}
			o.one(keep, w)
		case ' ', '\t', '\n', '\r', ',', ':':
			if depth == 0 {
				// No value, or the end of one.
				o.bad = o.pos == start
				return
			}
			o.one(keep, w)
		default:
			i := 1
			for i < len(w) && !literalEnd(w[i]) {
				i++
			}
			if keep {
				o.add(w[:i])
			} else {
				o.pos += int64(i)
			}
		}
		if o.bad || o.err != nil {
			return
		}
		if depth == 0 {
			// A literal may go on in the next window.
			if o.fill() {
				if c := o.win[o.pos-o.off]; !literalEnd(c) {
					continue
				}
			}
			return
		}
	}
}

func (o *outline) one(keep bool, w []byte) {
	if keep {
		o.add(w[:1])
	} else {
		o.pos++
	}
}

func literalEnd(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', ',', ':', '{', '}', '[', ']', '"':
		return true
	}
	return false
}

// done notes that the paths at and below n were read.
func (o *outline) done(n *outlineNode) { o.left -= n.resolve() }

// value walks the value at o.pos for the paths below n.
func (o *outline) value(n *outlineNode, root bool) {
	if !o.init {
		o.init = true
		o.left = n.countLeaves()
	}
	o.space()
	if !o.fill() {
		if o.err == nil && !root {
			o.bad = true
		}
		return
	}
	if n.leaf {
		o.skip(true)
		o.done(n)
		return
	}
	switch o.peek() {
	case '{':
		o.object(n)
	case '[':
		o.array(n)
	default:
		o.skip(true)
	}
}

// object walks the object at o.pos for the paths below n.
func (o *outline) object(n *outlineNode) {
	o.out = append(o.out, '{')
	o.pos++
	if !o.open('}') {
		return
	}
	wrote := false
	for {
		if o.peek() != '"' {
			o.bad = true
			return
		}
		child := o.member(n, wrote)
		if o.bad || o.err != nil {
			return
		}
		o.space()
		if o.peek() != ':' {
			o.bad = true
			return
		}
		o.pos++
		if child == nil {
			o.space()
			o.skip(false)
		} else {
			o.out = append(o.out, ':')
			wrote = true
			o.value(child, false)
		}
		if o.bad || o.err != nil {
			return
		}
		if o.left == 0 {
			o.out = append(o.out, '}')
			return
		}
		if !o.next('}') {
			return
		}
	}
}

// open moves past the space after an opening bracket and reports whether
// a value follows. Otherwise the outline holds the closing bracket end.
func (o *outline) open(end byte) bool {
	o.space()
	if c := o.peek(); o.bad || o.err != nil || c != end {
		return !o.bad && o.err == nil
	}
	o.pos++
	o.out = append(o.out, end)
	return false
}

// next moves past the comma after a value and the space after it and
// reports whether a value follows. Otherwise the outline holds the closing
// bracket end, or the document is bad.
func (o *outline) next(end byte) bool {
	o.space()
	switch o.peek() {
	case ',':
		o.pos++
		o.space()
		return true
	case end:
		o.pos++
		o.out = append(o.out, end)
	default:
		o.bad = true
	}
	return false
}

// member reads the key at o.pos and returns the node it names. Then the
// outline holds the key, after a comma when wrote is set.
func (o *outline) member(n *outlineNode, wrote bool) *outlineNode {
	mark, nsegs, start := len(o.out), len(o.segs), o.pos
	if wrote {
		o.out = append(o.out, ',')
	}
	at := len(o.out)
	o.stringToken(true)
	if o.bad || o.err != nil {
		return nil
	}
	key := bytesView(o.out[at:])
	var child *outlineNode
	if o.pos-start < outlineLong {
		name := key[1 : len(key)-1]
		if strings.IndexByte(name, '\\') >= 0 {
			name = Parse(key).Str
		}
		child = n.members[name]
	}
	if child == nil {
		o.out, o.segs = o.out[:mark], o.segs[:nsegs]
	}
	return child
}

// array walks the array at o.pos for the paths below n.
func (o *outline) array(n *outlineNode) {
	o.out = append(o.out, '[')
	o.pos++
	if !o.open(']') {
		return
	}
	for k := 0; ; k++ {
		if k > 0 && k <= n.maxIndex {
			o.out = append(o.out, ',')
		}
		if child := n.elems[k]; child != nil {
			o.value(child, false)
		} else {
			if k <= n.maxIndex {
				o.out = append(o.out, '0')
			}
			o.skip(false)
		}
		if o.bad || o.err != nil {
			return
		}
		if o.left == 0 {
			o.out = append(o.out, ']')
			return
		}
		if !o.next(']') {
			return
		}
	}
}

// place makes r, a result on the outline, the result on the document.
func (o *outline) place(r *Result) error {
	if r.Raw == "" || r.Index <= 0 {
		return nil
	}
	end := r.Index + len(r.Raw)
	sg := o.segAt(r.Index)
	eg := o.segAt(end - 1)
	if sg == nil || eg == nil {
		return nil
	}
	vs := sg.doc + int64(r.Index-sg.at)
	ve := eg.doc + int64(end-eg.at)
	if eg.n != int(eg.docN) {
		ve = eg.doc + eg.docN
	}
	n := int(ve - vs)
	if n == len(r.Raw) {
		r.Index = int(vs)
		return nil
	}
	b := makeNoZero(n)
	if k, err := o.r.ReadAt(b, vs); k < n {
		if err == nil || err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return err
	}
	raw := bytesView(b)
	*r = resultAt(raw, 0, n, strings.IndexByte(raw, '\\') >= 0)
	r.Index = int(vs)
	return nil
}

// segAt returns the segment that holds byte i of the outline.
func (o *outline) segAt(i int) *outlineSeg {
	k := sort.Search(len(o.segs), func(k int) bool { return o.segs[k].at > i }) - 1
	if k < 0 || i >= o.segs[k].at+o.segs[k].n {
		return nil
	}
	return &o.segs[k]
}
