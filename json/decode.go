package json

import (
	"fmt"
	"math"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/MoeForge/moego/base64"
	"github.com/MoeForge/moego/internal/jsonsimd"
)

// Unmarshal parses the JSON-encoded data and stores the result in the value
// pointed to by v. If v is nil or not a pointer, Unmarshal returns an
// InvalidUnmarshalError. It follows the rules of encoding/json.Unmarshal
// and gives the same results and errors.
//
// The whole input is validated before v is modified, so on a syntax error
// v is unchanged.
func Unmarshal(data []byte, v any) error {
	d := getDecodeState()
	err := d.unmarshal(data, v)
	putDecodeState(d)
	return err
}

// UnmarshalNoCopy is Unmarshal, except that the values it stores may share
// memory with data: a string, map key or Number whose JSON text needs no
// unescaping, and a RawMessage, view data instead of holding a copy. The
// caller must not modify data while any of them is in use.
//
// A RawMessage is set to view data, without reusing the memory it held.
func UnmarshalNoCopy(data []byte, v any) error {
	d := getDecodeState()
	d.noCopy = true
	err := d.unmarshal(data, v)
	d.noCopy = false
	putDecodeState(d)
	return err
}

// decodeState decodes one input into Go values. The input is either
// validated first, which fills the tables, or validated as it is decoded
// (fused): then the tables are empty, and a syntax error stops the decode.
// A state takes at most 3448 bytes on 64-bit platforms, so that with the
// header the allocator puts before it, it fits a block of 3456.
type decodeState struct {
	data []byte
	s    string // data as a string
	i    int    // read position
	// refillAt is the read position from which the second pass of a large
	// value reads more input; otherwise it is never reached.
	refillAt int
	tab      jsonsimd.Tables
	ci       int // container table cursor
	si       int // span table cursor

	stream *streamInput // the second pass of a large value, or nil
	depth  int          // containers open at d.i
	// open has bit k set when the container at depth k+1 is an object,
	// for the first openTrack levels.
	open   [openTrack / 64]uint64
	budget int   // bytes left for counting elements ahead
	stack  []any // elements of the []any being decoded, innermost last

	// slabBuf is the current slab chunk, of which slabBuf[slabLo:slabHi]
	// is free. Taking from it changes only the offsets, so that it writes
	// no pointer.
	slabBuf        []byte
	slabLo, slabHi int32
	slabChunk      int32 // chunk size for this input
	slabSpent      int32 // bytes taken from earlier chunks, at most slabSpentMax

	useNumber       bool
	disallowUnknown bool
	noCopy          bool  // strings may view the input
	fused           bool  // validating while decoding
	checked         bool  // the whole input is known to be valid
	nscr            uint8 // the levels of scr in use

	// After a fused decode validated the rest of a short input at once,
	// d.tab.RestEnds()[restNext:restLen] are the ends, from restBase, of
	// the next arrays and objects that close at depth restDepth, in order.
	restNext, restLen uint8
	restDepth         int32
	// mapDepth > 0 while a map element is decoded into a temporary, whose
	// strings are copied at once.
	mapDepth int32
	restBase int

	saved   error          // the first saved error
	path    []pathStep     // the struct fields being decoded, outermost first
	root    unsafe.Pointer // the type of the pointer decoded into, for type errors on encoding/json/v2
	scratch []byte         // unescaped keys and strings
	memo    *decodeMemo    // kept short strings and slab sizes, or nil

	// pend lists the strings stored as views of the input, which
	// finishStrings copies into one block of pendBytes bytes before the
	// call returns.
	pend      []pendString
	pendBytes int

	// For stamps: the bytes of strings and RawMessage values of at least
	// slabMax bytes, the calls of UnmarshalJSON methods, and the type of
	// the input.
	long, notes int
	stampKind   uint64

	// scr holds the space that arrays of unknown length are decoded into
	// before they are copied to an array of their exact length, one level
	// for each such array being decoded, innermost last: nscr are in use.
	scr [scratchLevels]arrScratch
}

// arrScratch is the space of one level of d.scr: room for cap elements of
// plan el at data, all zero while the level is not in use. While it is,
// the array goes to the slice at h of plan pl; n elements have been
// decoded, and the strings pended from p0 on may be in them.
type arrScratch struct {
	el    *decPlan
	data  unsafe.Pointer
	cap   int
	h     *sliceHeader
	pl    *decPlan
	n, p0 int
}

const (
	scratchLevels = 4
	scratchMax    = 64 << 10 // bytes of one level
)

// pendString is a string at dst that views the input from start for n
// bytes, or a RawMessage when raw is set, which stamp tells stampRaw about.
type pendString struct {
	dst        unsafe.Pointer
	start, n   int
	raw, stamp bool
}

var decodeStates stateCache[decodeState]

func getDecodeState() *decodeState { return decodeStates.get() }

func putDecodeState(d *decodeState) {
	if cap(d.tab.Containers) > 1<<16 || cap(d.tab.Spans) > 1<<12 || cap(d.scratch) > 64<<10 || cap(d.stack) > 1<<12 {
		return
	}
	// Each pointer store costs a write barrier while the collector marks:
	// fields that are usually clear already are only cleared when set.
	d.data, d.s = nil, ""
	if d.slabBuf != nil {
		d.slabBuf = nil
	}
	if d.saved != nil {
		d.saved = nil
	}
	if m := d.memo; m != nil && (m.spare != nil || m.first != 0) {
		m.spare, m.first = nil, 0
	}
	d.tab.Release()
	d.path = d.path[:0]
	clear(d.pend)
	d.pend = d.pend[:0]
	clear(d.stack)
	d.stack = d.stack[:0]
	d.useNumber, d.disallowUnknown = false, false
	held := cap(d.tab.Containers)*int(unsafe.Sizeof(jsonsimd.Container{})) +
		cap(d.tab.Spans)*int(unsafe.Sizeof(jsonsimd.Span{})) + cap(d.scratch) +
		cap(d.stack)*int(unsafe.Sizeof(any(nil))) + cap(d.pend)*int(unsafe.Sizeof(pendString{})) +
		cap(d.path)*int(unsafe.Sizeof(pathStep{}))
	for k := range d.scr {
		if sc := &d.scr[k]; sc.data != nil {
			held += sc.cap * int(sc.el.size)
		}
	}
	decodeStates.put(d, held <= stateCacheMax)
}

// abort carries an error that stops decoding, from an Unmarshaler or
// TextUnmarshaler or a malformed quoted value.
type abort struct{ err error }

// syntaxAbort stops a fused decode at a syntax error.
type syntaxAbort struct{}

// errSyntax is the result of a fused decode that found a syntax error.
var errSyntax error = new(SyntaxError)

// Input into a zero target of up to fusedTargetMax bytes is validated as
// it is decoded: on a syntax error the target is set back to zero, which
// leaves it as it was. Before a method of the caller's types sees part of
// the input, the rest of the input is validated, so methods run only for
// valid input, as when the input is validated first.
const fusedTargetMax = 4 << 10

func (d *decodeState) unmarshal(data []byte, v any) error {
	if jsonv2 {
		// The target is checked before the input.
		if err := checkTarget(v); err != nil {
			return err
		}
	}
	// Input that an UnmarshalJSON method got and passed on as it was is
	// stamped: it is valid.
	valid := len(data) >= stampMin && d.inputStamped(data)
	d.long = 0
	ef := (*eface)(unsafe.Pointer(&v))
	d.root = ef.typ
	inHeap(ef.data)
	// A zero target of a small type is decoded without tables, validating
	// the input on the way unless it is known to be valid. This is one
	// function, so that each call has few frames to return through.
	var pl *decPlan
	if ef.typ != nil && ef.data != nil && kindOfRtype(ef.typ) == reflect.Pointer {
		pl = rootDecPlan(ef.typ)
		if pl.unm != nil {
			// The method gets the whole input, which needs no tables. What
			// stamps learn of the input comes from the decode in it.
			d.stampKind = 0
			if !valid && !jsonsimd.Validate(bytesView(data), maxNestingDepth, nil) {
				return syntaxErrorFor(data)
			}
			valid = true
		} else if size := pl.elem.size; pl.text != nil || size > fusedTargetMax || !isZeroMem(ef.data, size) {
			pl = nil
		}
	}
	var err error
	switch {
	case pl != nil:
		d.start(data, bytesView(data))
		d.tab.Containers, d.tab.Spans = d.tab.Containers[:0], d.tab.Spans[:0]
		d.fused, d.checked = true, valid
		err = d.run(pl, ef.data)
		d.fused = false
		if err == errSyntax && !valid {
			reflect.NewAt(pl.elem.typ, ef.data).Elem().SetZero()
			err = syntaxErrorFor(data)
		}
	case !jsonsimd.Validate(bytesView(data), maxNestingDepth, &d.tab):
		err = syntaxErrorFor(data)
	default:
		err = d.decodeValid(data, v)
	}
	if d.stampKind != 0 {
		d.stampLearn(len(data))
	}
	return err
}

// heapSink is where inHeap would store a pointer if heapSinkOn were set,
// which it never is: storing it makes the compiler place what it points to
// in the heap, at no cost when the call runs.
var (
	heapSink   unsafe.Pointer
	heapSinkOn bool
)

// inHeap makes the value at p, the target of a decode, escape to the
// heap. Decoded strings view the input until the call returns, and d.pend
// points into the target meanwhile, which a stack that moves would not
// allow.
func inHeap(p unsafe.Pointer) {
	if heapSinkOn {
		heapSink = p
	}
}

// isZeroMem reports whether the n bytes at p are all zero. It reads only
// them: the target was usually just cleared, and comparing it with zeroed
// memory elsewhere would read that too.
func isZeroMem(p unsafe.Pointer, n uintptr) bool {
	if jsonsimd.HaveAVX2 && n >= 32 {
		return jsonsimd.ZeroAVX2(p, n)
	}
	var x uint64
	i := uintptr(0)
	if uintptr(p)%8 == 0 {
		for ; i+32 <= n; i += 32 {
			q := unsafe.Add(p, i)
			x |= *(*uint64)(q) | *(*uint64)(unsafe.Add(q, 8)) | *(*uint64)(unsafe.Add(q, 16)) | *(*uint64)(unsafe.Add(q, 24))
		}
		for ; i+8 <= n; i += 8 {
			x |= *(*uint64)(unsafe.Add(p, i))
		}
	}
	for ; i < n; i++ {
		x |= uint64(*(*byte)(unsafe.Add(p, i)))
	}
	return x == 0
}

// decodeValid decodes data, which is valid and whose tables are in d.tab,
// into v.
func (d *decodeState) decodeValid(data []byte, v any) error {
	s := bytesView(data)
	ef := (*eface)(unsafe.Pointer(&v))
	if err := checkTarget(v); err != nil {
		return err
	}
	inHeap(ef.data)
	d.root = ef.typ
	d.start(data, s)
	return d.run(rootDecPlan(ef.typ), ef.data)
}

// checkTarget returns the error for a value that can not be decoded into.
func checkTarget(v any) error {
	ef := (*eface)(unsafe.Pointer(&v))
	if ef.typ == nil {
		return &InvalidUnmarshalError{Type: nil}
	}
	if ef.data == nil || reflect.TypeOf(v).Kind() != reflect.Pointer {
		return &InvalidUnmarshalError{Type: reflect.TypeOf(v)}
	}
	return nil
}

// decodeLargeInto decodes the value of the second pass d.stream into v,
// which checkTarget accepts.
func (d *decodeState) decodeLargeInto(si *streamInput, v any) error {
	ef := (*eface)(unsafe.Pointer(&v))
	inHeap(ef.data)
	d.root = ef.typ
	d.start(si.win[:0], "")
	d.fused, d.checked = true, true
	d.mapDepth = 1
	d.budget = math.MaxInt / 2
	d.stream = si
	d.tab.Containers, d.tab.Spans = d.tab.Containers[:0], d.tab.Spans[:0]
	err := d.run(rootDecPlan(ef.typ), ef.data)
	d.fused, d.stream = false, nil
	return err
}

func (d *decodeState) start(data []byte, s string) {
	d.data, d.s, d.i, d.ci, d.si = data, s, 0, 0, 0
	d.refillAt = math.MaxInt
	d.checked, d.depth = false, 0
	d.long = 0
	d.pend = d.pend[:0]
	d.pendBytes, d.mapDepth = 0, 0
	d.budget = 4*len(data) + 256
	if d.saved != nil {
		d.saved = nil
	}
	d.path = d.path[:0]
	if d.slabBuf != nil {
		d.slabBuf = nil
	}
	d.slabLo, d.slabHi, d.slabSpent = 0, 0, 0
	d.restNext, d.restLen = 0, 0
	d.slabChunk = int32(max(slabChunkMin, min(len(data), slabChunkMax)))
	if len(data) > slabChunkMax {
		d.slabChunk = slabChunkFirst
	}
}

// run decodes the input into the value at ptr of root plan pl, and returns
// the error that stopped it, or the first saved error. A fused decode
// checks that nothing follows the value, and returns errSyntax for a
// syntax error.
func (d *decodeState) run(pl *decPlan, ptr unsafe.Pointer) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = d.recovered(r)
		}
	}()
	if d.stream != nil {
		d.fillWindow(0)
	} else if m := d.memo; m != nil && m.sures != 0 {
		m.spareFor(d.root)
	}
	d.ws()
	if el := pl.elem; pl.unm == nil && pl.text == nil && el.kind == dStruct && d.s[d.i] == '{' {
		d.object(el, ptr)
	} else {
		d.pointee(pl, ptr, pl.typ)
	}
	if d.fused {
		d.ws()
		if d.i != len(d.s) {
			d.bad()
		}
	}
	if m := d.memo; m != nil && d.stream == nil {
		m.noteSlab(d.root, d.slabTaken())
	}
	d.finishStrings()
	return d.saved
}

// recovered returns the error for the panic r that stopped a decode. In a
// fused decode, a syntax error anywhere in the input comes first, as the
// input is validated before decoding otherwise; reading past the end of
// a truncated input is one too.
func (d *decodeState) recovered(r any) error {
	if d.stream != nil {
		switch r.(type) {
		case syntaxAbort, runtime.Error:
			return errInputChanged
		}
	}
	if d.fused && !d.checked {
		switch r.(type) {
		case syntaxAbort:
			d.endScratch(false)
			d.dropStrings()
			return errSyntax
		case abort, runtime.Error:
			if !jsonsimd.Validate(d.s, maxNestingDepth, nil) {
				d.endScratch(false)
				d.dropStrings()
				return errSyntax
			}
		}
	}
	d.endScratch(true)
	d.finishStrings()
	a, ok := r.(abort)
	if !ok {
		panic(r)
	}
	return d.withContext(a.err)
}

// stringInto stores the string at d.i into the string at p. A short one
// without escapes that is not interned is first stored as a view of the
// input, and copied with the others into one block when the decode ends.
func (d *decodeState) stringInto(p unsafe.Pointer) {
	start := d.i
	end, flags := d.stringScan(start)
	d.i = end + 1
	n := end - start - 1
	if flags != 0 || n >= slabMax || d.mapDepth > 0 {
		*(*string)(p) = d.stringFrom(start, end, flags)
		return
	}
	if n == 0 {
		*(*string)(p) = ""
		return
	}
	if d.noCopy {
		*(*string)(p) = d.s[start+1 : end]
		return
	}
	if n <= internMax {
		if b := d.intern(d.s[start+1 : end]); b != nil {
			*(*string)(p) = *(*string)(b)
			return
		}
	}
	*(*string)(p) = d.s[start+1 : end]
	d.pend = append(d.pend, pendString{dst: p, start: start + 1, n: n})
	d.pendBytes += n
}

// finishStrings copies the strings that view the input into one block. A
// string that was replaced since is left alone.
func (d *decodeState) finishStrings() {
	if len(d.pend) == 0 {
		return
	}
	var buf []byte
	if n := d.pendBytes; n <= d.slabLeft() {
		// They fit in what is left of the chunk.
		buf = d.slabTake(n)
	} else {
		buf = makeNoZero(n)
	}
	off := 0
	base := unsafe.StringData(d.s)
	for _, e := range d.pend {
		at := (*byte)(unsafe.Add(unsafe.Pointer(base), e.start))
		b := buf[off : off+e.n : off+e.n]
		if e.raw {
			rp := (*[]byte)(e.dst)
			if len(*rp) != e.n || unsafe.SliceData(*rp) != at {
				continue
			}
			copy(b, *rp)
			*rp = b
			d.stampRaw(b, e.stamp)
		} else {
			sp := (*string)(e.dst)
			if len(*sp) != e.n || unsafe.StringData(*sp) != at {
				continue
			}
			copy(b, *sp)
			*sp = bytesView(b)
		}
		off += e.n
	}
	clear(buf[off:])
	d.dropStrings()
}

// views reports whether b is part of the input, as a RawMessage not yet
// copied is.
func (d *decodeState) views(b []byte) bool {
	return uintptr(unsafe.Pointer(unsafe.SliceData(b)))-uintptr(unsafe.Pointer(unsafe.SliceData(d.data))) < uintptr(len(d.data))
}

func (d *decodeState) dropStrings() {
	clear(d.pend)
	d.pend, d.pendBytes = d.pend[:0], 0
}

// movedStrings updates the pending strings in the n elements of size
// bytes at old, which were copied to new.
func (d *decodeState) movedStrings(old, new unsafe.Pointer, n int, size uintptr) {
	lo := uintptr(old)
	hi := lo + uintptr(n)*size
	for k := range d.pend {
		if a := uintptr(d.pend[k].dst); a >= lo && a < hi {
			d.pend[k].dst = unsafe.Add(new, a-lo)
		}
	}
}

func (d *decodeState) fail(err error) { panic(abort{err}) }

// bad stops a fused decode at a syntax error.
func (d *decodeState) bad() { panic(syntaxAbort{}) }

// restMax is the longest rest of an input that a fused decode validates
// at once at a RawMessage, rather than value by value.
const restMax = 2 << 10

// validateShortRest validates the rest of a short input of a fused decode
// at once when an array or object that a RawMessage field holds starts at
// d.i, and the member that followed it in an earlier input held one too:
// that costs less than validating each such value on its own. It keeps
// the ends of the values at this depth, which skip reads instead of the
// input.
func (d *decodeState) validateShortRest() {
	if c := d.s[d.i]; c != '{' && c != '[' || !jsonsimd.HaveAVX2 || uint(len(d.s)-d.i-64) > restMax-64 || d.depth == 0 || d.depth > openTrack || len(d.path) == 0 {
		return
	}
	st := d.path[len(d.path)-1]
	sd := (*structDecs.Load())[st.sd]
	if sd.plans[st.fi].kind != dRaw || sd.plans[sd.next[st.fi].Load()].kind != dRaw {
		return
	}
	ok, ends, _ := jsonsimd.ValidateRestEnds(d.s[d.i:], d.open[:], d.depth, maxNestingDepth, &d.tab)
	if !ok {
		d.bad()
	}
	d.checked = true
	d.restNext, d.restLen, d.restBase, d.restDepth = 0, uint8(len(ends)), d.i, int32(d.depth)
}

// restEnd returns the end of the array or object at i, at depth
// d.restDepth, from the ends kept for it, or 0 when it is not there.
func (d *decodeState) restEnd(i int) int {
	e := d.tab.RestEnds()[:d.restLen]
	k := int(d.restNext)
	for k < len(e) && d.restBase+int(e[k]) <= i {
		k++
	}
	d.restNext = uint8(k)
	if k == len(e) {
		return 0
	}
	return d.restBase + int(e[k])
}

// validateAll validates the input of a fused decode from d.i, where a
// value starts, to the end, before a method of the caller's types sees
// part of it. The input before d.i is valid so far.
func (d *decodeState) validateAll() {
	if !d.fused || d.checked {
		return
	}
	if len(d.s)-d.i >= tablesMin && d.stream == nil {
		// Validate all of the input with tables, which the rest of the
		// decode reads instead of the input: the ends of the containers
		// and of the long strings.
		if !jsonsimd.Validate(d.s, maxNestingDepth, &d.tab) {
			d.bad()
		}
		d.ci, d.si = 0, 0
	} else if !d.validRest() {
		d.bad()
	}
	d.checked = true
}

// tablesMin is the shortest rest of the input that validateAll validates
// with tables.
const tablesMin = 64 << 10

// openTrack is the depth up to which the decoder records the kinds of the
// open containers, so that the rest of the input can be validated alone.
const openTrack = 256

// validRest reports whether the input from d.i, where a value must
// follow, validly continues the open containers. It keeps the ends of the
// values at this depth, as validateShortRest does.
func (d *decodeState) validRest() bool {
	if d.depth > openTrack {
		return jsonsimd.Validate(d.s, maxNestingDepth, nil)
	}
	ok, ends, _ := jsonsimd.ValidateRestEnds(d.s[d.i:], d.open[:], d.depth, maxNestingDepth, &d.tab)
	d.restNext, d.restLen, d.restBase, d.restDepth = 0, uint8(len(ends)), d.i, int32(d.depth)
	return ok
}

// enter counts a container entered at d.i, an object when obj is set.
func (d *decodeState) enter(obj bool) {
	if k := d.depth; k < openTrack {
		w, bit := &d.open[k>>6], uint64(1)<<(k&63)
		if obj {
			*w |= bit
		} else {
			*w &^= bit
		}
	}
	if d.depth++; d.depth > maxNestingDepth {
		d.bad()
	}
	if d.i >= d.refillAt {
		d.refill()
	}
}

// countNumbers is countFrom for an array of numbers: the commas up to the
// first closing bracket. An element of another kind only makes the count
// wrong, and the array then grows as it needs to.
func (d *decodeState) countNumbers(i int) int {
	if d.budget <= 0 {
		return -1
	}
	s := d.s[i:min(len(d.s), i+d.budget)]
	end := strings.IndexByte(s, ']')
	if end < 0 {
		d.budget -= len(s)
		return -1
	}
	d.budget -= end
	return strings.Count(s[:end], ",") + 1
}

// countLongest is the longest string countFrom reads through. The decode
// reads a longer one anyway, and the count would add a second read.
const countLongest = 64 << 10

// countFrom returns the number of elements of the array from the one that
// starts at d.s[i] to its end, by a light scan, or -1 when the scan would
// read more than the bytes left in the budget or the input is not valid
// there.
func (d *decodeState) countFrom(i int) int {
	if d.budget <= 0 {
		return -1
	}
	s := d.s[:min(len(d.s), i+d.budget)]
	depth, n := 0, 1
	j := i
	// Past 64 bytes, a long array: count in blocks.
	blocks := math.MaxInt
	if jsonsimd.HaveAVX2 {
		blocks = i + 64
	}
	for ; j < len(s); j++ {
		if j >= blocks && len(s)-j >= 64 {
			k, end := jsonsimd.CountElements(s[j:], depth, countLongest)
			d.budget -= j + end - i
			if k < 0 {
				return -1
			}
			return n + k - 1
		}
		switch s[j] {
		case '"':
			lim := min(len(s), j+1+countLongest)
			if j = jsonsimd.StringEnd(s[:lim], j+1); j < 0 {
				d.budget -= lim - i
				return -1
			}
		case '[', '{':
			depth++
		case ']', '}':
			if depth == 0 {
				d.budget -= j - i
				return n
			}
			depth--
		case ',':
			if depth == 0 {
				n++
			}
		}
	}
	d.budget -= j - i
	return -1
}

// saveError keeps the first error, with the field context.
func (d *decodeState) saveError(err error) {
	if d.saved == nil {
		d.saved = d.withContext(err)
	}
}

// withContext adds to an UnmarshalTypeError the struct and the path of
// the field being decoded. On encoding/json/v2, type errors have their
// place when they are made, and errors of methods are returned as they are.
func (d *decodeState) withContext(err error) error {
	if jsonv2 {
		return err
	}
	e, ok := err.(*UnmarshalTypeError)
	if !ok || len(d.path) == 0 {
		return err
	}
	decs := *structDecs.Load()
	last := d.path[len(d.path)-1]
	e.Struct = decs[last.sd].typ.Name()
	var b []byte
	for _, st := range d.path {
		sd := decs[st.sd]
		b = sd.fields[st.fi].appendContext(b, sd.typ)
	}
	if e.Field != "" {
		if len(b) > 0 {
			b = append(b, '.')
		}
		b = append(b, e.Field...)
	}
	e.Field = string(b)
	return err
}

// pathStep is a struct field being decoded: the id of its struct's
// decoder and its index there. It holds no pointers, so that pushing it
// costs no write barrier.
type pathStep struct {
	sd, fi int32
}

func (d *decodeState) typeError(value string, t reflect.Type, off int) {
	if jsonv2 {
		d.typeErrorV2(value, t, off, off-1)
		return
	}
	o := int64(off)
	if d.stream != nil {
		o = d.stream.offset(off)
	}
	d.typeErrorAt(value, t, o)
}

// typeErrorAt is typeError with the offset in the value.
func (d *decodeState) typeErrorAt(value string, t reflect.Type, off int64) {
	d.saveError(&UnmarshalTypeError{Value: value, Type: t, Offset: off})
}

// typeErrorV2 keeps the type error of encoding/json/v2 for the value or
// member name at index target of the input, with offset off.
func (d *decodeState) typeErrorV2(value string, t reflect.Type, off, target int) {
	if d.saved == nil {
		d.saved = d.v2TypeError(value, t, off, target)
	}
}

// ws skips whitespace. Any other byte stops it, and the check that follows
// rejects the control bytes.
func (d *decodeState) ws() {
	s, i := d.s, d.i
	for i < len(s) && isSpace[s[i]] {
		i++
	}
	d.i = i
}

var isSpace = [256]bool{' ': true, '\t': true, '\n': true, '\r': true}

// container returns the table entry of the container whose bracket is at
// off, or nil.
func (d *decodeState) container(off int) *jsonsimd.Container {
	c := d.tab.Containers
	k := d.ci
	for k < len(c) && int(c[k].Off) < off {
		k++
	}
	d.ci = k
	if k < len(c) && int(c[k].Off) == off {
		return &c[k]
	}
	return nil
}

// span returns the table entry of the long string whose opening quote is
// at off, or nil.
func (d *decodeState) span(off int) *jsonsimd.Span {
	sp := d.tab.Spans
	k := d.si
	for k < len(sp) && int(sp[k].Start) < off {
		k++
	}
	d.si = k
	if k < len(sp) && int(sp[k].Start) == off {
		return &sp[k]
	}
	return nil
}

// skip moves past the value at d.i.
func (d *decodeState) skip() {
	s, i := d.s, d.i
	switch s[i] {
	case '{', '[':
		if d.stream != nil {
			d.skipStream()
			return
		}
		if c := d.container(i); c != nil {
			d.i = int(c.End) + 1
			d.ci = int(c.Next)
			return
		}
		if d.fused && !d.checked {
			d.skipChecked()
			return
		}
		if d.restNext < d.restLen && d.depth == int(d.restDepth) {
			if end := d.restEnd(i); end > 0 {
				d.i = end
				return
			}
		}
		if d.fused && d.depth == 0 {
			// The input is valid, and its root value ends where the
			// whitespace after it starts.
			d.i = len(strings.TrimRight(s, " \t\r\n"))
			return
		}
		// Counting reads a block of 64 bytes, from a copy for a last one:
		// that costs less than reading byte by byte, unless little input
		// is left.
		if jsonsimd.HaveAVX2 && len(s)-i >= skipCountMin {
			_, end := jsonsimd.CountElements(s[i+1:], 0, 0)
			d.i = i + end + 2
			return
		}
		d.i = skipContainer(s, i)
	default:
		d.i = d.literalEnd()
	}
}

// skipCountMin is the rest of a valid input from which skip finds the end
// of an array or object by counting.
const skipCountMin = 24

// skipChecked moves past the value at d.i, validating it.
func (d *decodeState) skipChecked() {
	if c := d.s[d.i]; (c == '{' || c == '[') && jsonsimd.HaveAVX2 && len(d.s)-d.i >= 64 {
		end := jsonsimd.ValidateValue(d.s[d.i:], maxNestingDepth-d.depth, &d.tab)
		if end < 0 {
			d.bad()
		}
		d.i += end
		return
	}
	switch d.s[d.i] {
	case '{':
		d.enter(true)
		d.i++
		d.ws()
		if d.s[d.i] == '}' {
			d.i++
		} else {
			for {
				if d.s[d.i] != '"' {
					d.bad()
				}
				end, _ := d.stringEnd(d.i)
				d.i = end + 1
				d.colon()
				d.skipChecked()
				if !d.more('}') {
					break
				}
			}
		}
		d.depth--
	case '[':
		d.enter(false)
		d.i++
		d.ws()
		if d.s[d.i] == ']' {
			d.i++
		} else {
			for {
				d.skipChecked()
				if !d.more(']') {
					break
				}
			}
		}
		d.depth--
	default:
		d.i = d.literalEnd()
	}
}

// skipContainer returns the index after the container at s[i], which is
// valid.
func skipContainer(s string, i int) int {
	depth := 0
	for ; i < len(s); i++ {
		switch s[i] {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return i + 1
			}
		case '"':
			i = jsonsimd.StringEnd(s, i+1)
		}
	}
	return i
}

// numberEnd returns the index after the number at d.s[i], or stops a
// fused decode when it is malformed.
func (d *decodeState) numberEnd(i int) int {
	s := d.s
	if i < len(s) && s[i] == '-' {
		i++
	}
	j := digitsEnd(s, i)
	if j == i || s[i] == '0' && j > i+1 {
		d.bad()
	}
	if j < len(s) && s[j] == '.' {
		k := digitsEnd(s, j+1)
		if k == j+1 {
			d.bad()
		}
		j = k
	}
	if j < len(s) && s[j]|0x20 == 'e' {
		j++
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		k := digitsEnd(s, j)
		if k == j {
			d.bad()
		}
		j = k
	}
	return j
}

func digitsEnd(s string, i int) int {
	for i < len(s) && s[i]-'0' < 10 {
		i++
	}
	return i
}

// literalEnd returns the index after the literal (string, number, true,
// false or null) at d.i, or stops a fused decode at anything else.
func (d *decodeState) literalEnd() int {
	i := d.i
	switch d.s[i] {
	case '"':
		end, _ := d.stringEnd(i)
		return end + 1
	case 't':
		return d.wordEnd(i, "true")
	case 'n':
		return d.wordEnd(i, "null")
	case 'f':
		return d.wordEnd(i, "false")
	}
	return d.numberEnd(i)
}

// wordEnd returns the index after the literal w at d.s[i], or stops a
// fused decode when another word is there.
func (d *decodeState) wordEnd(i int, w string) int {
	if len(d.s)-i < len(w) || d.s[i:i+len(w)] != w {
		d.bad()
	}
	return i + len(w)
}

// null moves past the null at d.i.
func (d *decodeState) null() { d.i = d.wordEnd(d.i, "null") }

// rawValue returns the bytes of the value at d.i and moves past it.
func (d *decodeState) rawValue() []byte {
	if d.stream != nil {
		if b := d.rawLarge(); b != nil {
			return b
		}
		// The value is short and in the window, which stays as it is.
		at := d.refillAt
		d.refillAt = math.MaxInt
		start := d.i
		d.skip()
		d.refillAt = at
		return d.data[start:d.i]
	}
	start := d.i
	d.skip()
	return d.data[start:d.i]
}

// rawChecked is rawValue for a RawMessage. While the decoder stamps such
// values, it also reports whether it checked the value itself and found it
// compact and not mostly long strings, so that its copy may be stamped.
func (d *decodeState) rawChecked() (raw []byte, stamp bool) {
	if encStamps.kinds[rawKind].Load() != stampDense || d.stream != nil || !d.fused || d.checked {
		if d.fused && !d.checked && d.stream == nil {
			d.validateShortRest()
		}
		return d.rawValue(), false
	}
	if c := d.s[d.i]; c != '{' && c != '[' || !jsonsimd.HaveAVX2 || len(d.s)-d.i < 64 {
		return d.rawValue(), false
	}
	start := d.i
	end, spaced, long := jsonsimd.ValidateValueInfo(d.s[start:], maxNestingDepth-d.depth, &d.tab)
	if end < 0 {
		d.bad()
	}
	d.i += end
	return d.data[start:d.i], !spaced && (end-long)*4 >= end
}

// tokenBytes returns the bytes of the string token at start, which ends at
// end.
func (d *decodeState) tokenBytes(start, end int) []byte {
	if d.stream != nil && end == start+2 {
		for k := range d.tab.Spans {
			if sp := &d.tab.Spans[k]; int(sp.Start) == start && sp.Flags&spanHole != 0 {
				return d.holeToken(d.stream.strings[sp.Flags>>spanHoleShift])
			}
		}
	}
	return d.data[start:end]
}

// literalName is the Value of an UnmarshalTypeError for the literal
// starting with c.
func literalName(c byte) string {
	switch c {
	case '"':
		return "string"
	case 't', 'f':
		return "bool"
	case 'n':
		return "null"
	}
	return "number"
}

// pointee decodes into the value that pointer ptr of the dPtr plan pl
// points to, checking the methods of the pointer type first. outer is the
// type reported when a TextUnmarshaler gets a value other than a string.
func (d *decodeState) pointee(pl *decPlan, ptr unsafe.Pointer, outer reflect.Type) {
	if pl.unm != nil {
		d.callUnmarshaler(pl.unm, ptr)
		return
	}
	c := d.s[d.i]
	if pl.text != nil && c != 'n' {
		d.callText(pl.text, ptr, outer)
		return
	}
	el := pl.elem
	switch el.kind {
	case dPtr:
		d.ptrChain(el, ptr, outer)
	case dAny, dIface:
		d.iface(el, ptr, outer)
	case dStruct:
		if c == '{' {
			d.object(el, ptr)
			return
		}
		d.value(el, ptr)
	default:
		d.value(el, ptr)
	}
}

// ptrChain decodes into the pointer at p, of the dPtr plan pl.
func (d *decodeState) ptrChain(pl *decPlan, p unsafe.Pointer, outer reflect.Type) {
	if d.s[d.i] == 'n' {
		d.null()
		*(*unsafe.Pointer)(p) = nil
		return
	}
	ptr := *(*unsafe.Pointer)(p)
	if ptr == nil {
		ptr = d.newElem(pl)
		*(*unsafe.Pointer)(p) = ptr
	} else if pl.elem.kind == dAny || pl.elem.kind == dIface {
		// A pointer to an interface that holds this pointer: decode into
		// the interface instead of following it around.
		ef := (*eface)(ptr)
		if ef.data == ptr && ef.typ != nil && dynType(pl.elem, ptr) == pl.rtyp {
			d.ifaceNoFollow(pl.elem, ptr)
			return
		}
	}
	d.pointee(pl, ptr, outer)
}

// newElem allocates the zero element of pointer plan pl.
func (d *decodeState) newElem(pl *decPlan) unsafe.Pointer {
	if pl.elemSlab {
		return d.slabAlloc(int(pl.elem.size))
	}
	return newObject(pl.elem.typ, pl.elem.rtyp, pl.elem.size)
}

// callUnmarshaler calls UnmarshalJSON on the pointer ptr with itab tab,
// with the bytes of the value at d.i.
func (d *decodeState) callUnmarshaler(tab, ptr unsafe.Pointer) {
	if jsonv2 && d.s[d.i] != '"' && d.s[d.i] != 'n' && isTimeTab(tab) {
		d.kindErrorV2(timeType, nil)
		return
	}
	d.validateAll()
	raw := d.rawValue()
	if len(raw) >= stampMin {
		d.stampInput(raw, tab)
	}
	var u Unmarshaler
	*(*iface)(unsafe.Pointer(&u)) = iface{tab, ptr}
	if jsonv2 && isTimeTab(tab) {
		// encoding/json/v2 parses times itself, from the text the string
		// holds, and keeps the old one when it fails.
		if r := bytesView(raw); r[0] == '"' && (strings.IndexByte(r, '\\') >= 0 || !validUTF8(r)) {
			u := unquote([]byte{'"'}, bytesView(raw[1:len(raw)-1]))
			raw = append(u, '"')
		}
		old := *(*time.Time)(ptr)
		if err := u.UnmarshalJSON(raw); err != nil {
			*(*time.Time)(ptr) = old
			d.methodError(err)
		}
		return
	}
	if err := u.UnmarshalJSON(raw); err != nil {
		if jsonv2 {
			d.methodError(err)
			return
		}
		d.fail(err)
	}
}

// callText calls UnmarshalText on the pointer ptr with itab tab for the
// value at d.i, which is not null.
func (d *decodeState) callText(tab, ptr unsafe.Pointer, outer reflect.Type) {
	c := d.s[d.i]
	if jsonv2 && c != '"' {
		for outer.Kind() == reflect.Pointer {
			outer = outer.Elem()
		}
		d.kindErrorV2(outer, errNotString)
		return
	}
	switch c {
	case '{':
		d.typeError("object", outer, d.i+1)
		d.skip()
		return
	case '[':
		d.typeError("array", outer, d.i+1)
		d.skip()
		return
	case '"':
		d.validateAll()
		b := d.stringBytes()
		var u textUnmarshaler
		*(*iface)(unsafe.Pointer(&u)) = iface{tab, ptr}
		if err := u.UnmarshalText(b); err != nil {
			if jsonv2 {
				d.methodError(err)
				return
			}
			d.fail(err)
		}
		return
	}
	end := d.literalEnd()
	d.typeError(literalName(c), outer, end)
	d.i = end
}

type textUnmarshaler interface{ UnmarshalText([]byte) error }

// dynType returns the dynamic type of the interface at p.
func dynType(pl *decPlan, p unsafe.Pointer) unsafe.Pointer {
	if pl.kind == dAny {
		return (*eface)(p).typ
	}
	tab := (*iface)(p).tab
	if tab == nil {
		return nil
	}
	// The itab holds the interface type first, then the dynamic type.
	return *(*unsafe.Pointer)(unsafe.Add(tab, unsafe.Sizeof(uintptr(0))))
}

// iface decodes into the interface at p of plan pl. An interface holding a
// non-nil pointer is decoded through the pointer, except that null needs
// the pointer to point to another pointer for that.
func (d *decodeState) iface(pl *decPlan, p unsafe.Pointer, outer reflect.Type) {
	data := (*eface)(p).data
	if rt := dynType(pl, p); rt != nil && data != nil && kindOfRtype(rt) == reflect.Pointer {
		dp := rootDecPlan(rt)
		if d.s[d.i] != 'n' || dp.elem.kind == dPtr {
			if dp.elem.kind == dAny || dp.elem.kind == dIface {
				ef := (*eface)(data)
				if ef.data == data && dynType(dp.elem, data) == rt {
					d.ifaceNoFollow(dp.elem, data)
					return
				}
			}
			if jsonv2 {
				outer = typeOfRtype(rt)
			}
			d.pointee(dp, data, outer)
			return
		}
	}
	d.ifaceNoFollow(pl, p)
}

func kindOfRtype(rt unsafe.Pointer) reflect.Kind { return typeOfRtype(rt).Kind() }

// ifaceNoFollow decodes into the interface at p, replacing its value.
func (d *decodeState) ifaceNoFollow(pl *decPlan, p unsafe.Pointer) {
	c := d.s[d.i]
	switch c {
	case 'n':
		d.null()
		if pl.kind == dAny {
			*(*any)(p) = nil
		} else {
			*(*iface)(p) = iface{}
		}
		return
	case '{', '[':
		if pl.kind == dAny {
			*(*any)(p) = d.anyValue()
			return
		}
		if jsonv2 {
			d.ifaceErrorV2(pl, p)
			return
		}
		name := "object"
		if c == '[' {
			name = "array"
		}
		d.typeError(name, pl.typ, d.i+1)
		d.skip()
		return
	case '"', 't', 'f':
		if pl.kind == dAny {
			*(*any)(p) = d.anyValue()
			return
		}
		if jsonv2 {
			d.ifaceErrorV2(pl, p)
			return
		}
		end := d.literalEnd()
		d.typeError(literalName(c), pl.typ, end)
		d.i = end
		return
	}
	// A number is converted before the type is checked, except on
	// encoding/json/v2.
	if jsonv2 && pl.kind != dAny {
		d.ifaceErrorV2(pl, p)
		return
	}
	start := d.i
	end := d.numberEnd(start)
	d.i = end
	var x any
	if d.useNumber {
		x = Number(d.copyString(start, end))
	} else {
		f, ok := d.parseFloatAt(start, end, 64)
		if !ok {
			if jsonv2 {
				d.typeErrorV2("number "+d.s[start:end], float64Type, end, start)
				*(*any)(p) = d.boxFloat(overflowAny(d.s[start]))
				return
			}
			d.typeError("number "+d.s[start:end], float64Type, end+1)
			return
		}
		x = d.boxFloat(f)
	}
	if pl.kind != dAny {
		d.typeError("number", pl.typ, end)
		return
	}
	*(*any)(p) = x
}

// rawInto decodes the value at d.i into the RawMessage at p.
func (d *decodeState) rawInto(p unsafe.Pointer) {
	start := d.i
	raw, stamp := d.rawChecked()
	b := (*[]byte)(p)
	switch {
	case d.noCopy || d.stream != nil && len(raw) >= streamLong:
		*b = raw[:len(raw):len(raw)]
	case cap(*b) >= len(raw) && !d.views(*b):
		*b = append((*b)[:0], raw...)
	case len(raw) < slabMax && len(d.data) > slabChunkMax && d.mapDepth == 0 && d.stream == nil:
		// In a long input, where the first chunk is small, a short one
		// is copied with the strings stringInto keeps when the decode
		// ends.
		*b = raw[:len(raw):len(raw)]
		d.pend = append(d.pend, pendString{p, start, len(raw), true, stamp})
		d.pendBytes += len(raw)
		return
	default:
		nb := d.alloc(len(raw))
		copyLong(nb, bytesView(raw))
		*b = nb
	}
	d.stampRaw(*b, stamp)
}

// value decodes the value at d.i, after whitespace, into p of plan pl.
func (d *decodeState) value(pl *decPlan, p unsafe.Pointer) {
	c := d.s[d.i]
	switch pl.kind {
	case dString:
		if c == '"' {
			d.stringInto(p)
			return
		}
	case dPtr:
		d.ptrChain(pl, p, pl.typ)
		return
	case dStruct:
		if c == '{' {
			d.object(pl, p)
			return
		}
	case dRaw:
		d.rawInto(p)
		return
	case dAny, dIface:
		d.iface(pl, p, pl.typ)
		return
	case dBool:
		if c == 't' {
			d.i = d.wordEnd(d.i, "true")
			*(*bool)(p) = true
			return
		}
		if c == 'f' {
			d.i = d.wordEnd(d.i, "false")
			*(*bool)(p) = false
			return
		}
	case dInt, dInt8, dInt16, dInt32, dInt64, dUint, dUint8, dUint16, dUint32, dUint64, dUintptr:
		if c == '-' || c-'0' < 10 {
			d.intValue(pl, p)
			return
		}
	case dFloat64:
		if c == '-' || c-'0' < 10 {
			if f, end, ok := d.floatAt(d.i, 64); ok {
				*(*float64)(p) = f
				d.i = end
				return
			}
			end := d.numberEnd(d.i)
			if f, ok := d.parseFloatAt(d.i, end, 64); ok {
				*(*float64)(p) = f
			} else {
				if jsonv2 && !go127 {
					*(*float64)(p) = float64(largestFloat(d.s[d.i], 64))
				}
				d.typeError("number "+d.s[d.i:end], pl.typ, end)
			}
			d.i = end
			return
		}
	case dFloat32:
		if c == '-' || c-'0' < 10 {
			if f, end, ok := d.floatAt(d.i, 32); ok {
				*(*float32)(p) = float32(f)
				d.i = end
				return
			}
			end := d.numberEnd(d.i)
			if f, ok := d.parseFloatAt(d.i, end, 32); ok {
				*(*float32)(p) = float32(f)
			} else {
				if jsonv2 && !go127 {
					*(*float32)(p) = float32(largestFloat(d.s[d.i], 32))
				}
				d.typeError("number "+d.s[d.i:end], pl.typ, end)
			}
			d.i = end
			return
		}
	case dNumber:
		if c == '-' || c-'0' < 10 {
			end := d.numberEnd(d.i)
			*(*string)(p) = d.copyString(d.i, end)
			d.i = end
			return
		}
		if c == '"' {
			start := d.i
			b := d.stringBytes()
			if !isValidNumber(b) {
				if jsonv2 {
					d.numberError("string "+string(d.tokenBytes(start, d.i)), strconv.ErrSyntax)
					return
				}
				d.fail(fmt.Errorf("json: invalid number literal, trying to unmarshal %q into Number", d.tokenBytes(start, d.i)))
			}
			*(*string)(p) = d.ownString(b)
			return
		}
	case dSlice:
		if c == '[' {
			d.slice(pl, p)
			return
		}
	case dBytes:
		if c == '"' {
			d.base64Value(pl, p)
			return
		}
		if c == '[' {
			d.slice(pl, p)
			return
		}
	case dArray:
		if c == '[' {
			d.array(pl, p)
			return
		}
	case dMap:
		if c == '{' {
			d.mapValue(pl, p)
			return
		}
	case dUnm:
		d.callUnmarshaler(pl.itab, p)
		return
	case dText:
		if c != 'n' {
			d.callText(pl.itab, p, pl.typ)
			return
		}
		d.value(pl.elem, p)
		return
	}
	d.mismatch(pl, c, p)
}

// mismatch handles a JSON value that does not fit plan pl: null sets
// pointers, interfaces, maps and slices to nil and leaves other values
// alone; anything else is a type error.
func (d *decodeState) mismatch(pl *decPlan, c byte, p unsafe.Pointer) {
	if jsonv2 && pl.kind == dNumber && c != 'n' {
		name, off := d.kindAt()
		d.numberError(name, nil)
		d.skipValue(off)
		return
	}
	switch c {
	case 'n':
		d.null()
		switch pl.kind {
		case dPtr, dMap:
			*(*unsafe.Pointer)(p) = nil
		case dSlice, dBytes, dRaw:
			*(*[]byte)(p) = nil
		}
	case '{':
		d.typeError("object", pl.typ, d.i+1)
		d.skip()
	case '[':
		d.typeError("array", pl.typ, d.i+1)
		d.skip()
	default:
		end := d.literalEnd()
		d.typeError(literalName(c), pl.typ, end)
		d.i = end
	}
}

// object decodes a JSON object into the struct at p. The read position
// is kept in i between calls.
func (d *decodeState) object(pl *decPlan, p unsafe.Pointer) {
	sd := pl.st
	d.enter(true)
	s := d.s
	i := d.i + 1
	for isSpace[s[i]] {
		i++
	}
	if s[i] == '}' {
		d.i = i + 1
		d.depth--
		return
	}
	prev := len(sd.fields)
	guess := int(sd.next[prev].Load())
	for {
		// Members usually come in the order of an earlier input, at first
		// field order: try the field that followed the last one first,
		// comparing the input with its quoted name.
		fi := -1
		var key string
		kstart := i
		nm := &sd.names[guess]
		hit := false
		if uint(len(nm.q)-1) < 16 && len(s)-i > 16 {
			hit = s[i] == '"' && (jsonsimd.Load64(s, i+1)^nm.w[0])&nm.m[0]|(jsonsimd.Load64(s, i+9)^nm.w[1])&nm.m[1] == 0
		} else {
			hit = s[i] == '"' && nm.at(s, i+1)
		}
		if hit {
			fi = guess
			i += 1 + len(nm.q)
		} else {
			n := -1
			if len(s)-i > 16 && s[i] == '"' {
				n, fi = sd.findShort(s, i)
			}
			if n >= 0 {
				key = s[i+1 : i+1+n]
				i += n + 2
			} else {
				d.i = i
				key = d.key()
				fi = sd.lookup(key)
				i = d.i
			}
		}
		kend := i
		for isSpace[s[i]] {
			i++
		}
		if s[i] != ':' {
			d.i = i
			d.bad()
		}
		i++
		for isSpace[s[i]] {
			i++
		}
		d.i = i
		if fi >= 0 {
			if fi != guess {
				sd.next[prev].Store(int32(fi))
			}
			prev, guess = fi, int(sd.next[fi].Load())
			f := &sd.fields[fi]
			var fp unsafe.Pointer
			if f.path == nil {
				fp = unsafe.Add(p, f.offset)
			} else {
				fp = d.embedded(f, p, pl.typ)
			}
			if fp == nil {
				d.skip()
			} else {
				n := len(d.path)
				d.path = append(d.path, pathStep{sd.id, int32(fi)})
				// The common kinds are decoded here rather than in value,
				// which saves frames for each level of nesting.
				switch fpl, c := sd.plans[fi], s[i]; {
				case f.quoted && jsonv2:
					d.quotedValueV2(fpl, fp, f.typ)
				case f.quoted:
					d.quotedValue(fpl, fp, f.typ)
				case fpl.kind == dString && c == '"':
					d.stringInto(fp)
				case fpl.kind == dRaw:
					d.rawInto(fp)
				case fpl.kind == dStruct && c == '{':
					d.object(fpl, fp)
				case fpl.kind == dSlice && c == '[':
					d.slice(fpl, fp)
				case (fpl.kind == dInt || fpl.kind == dInt64) && (c == '-' || c-'0' < 10):
					d.intValue(fpl, fp)
				case fpl.kind == dPtr && fpl.elem.kind == dStruct && fpl.unm == nil && fpl.text == nil && c == '{':
					ptr := *(*unsafe.Pointer)(fp)
					if ptr == nil {
						ptr = d.newElem(fpl)
						*(*unsafe.Pointer)(fp) = ptr
					}
					d.object(fpl.elem, ptr)
				case fpl.kind == dAny && (*eface)(fp).typ == nil && (c == '{' || c == '[' || c == '"'):
					*(*any)(fp) = d.anyValue()
				default:
					d.value(fpl, fp)
				}
				d.path = d.path[:n]
			}
		} else if inlineV2 && sd.inline != nil {
			d.inlineMember(sd, p, pl.typ, key, s[kstart:kend])
		} else {
			if d.disallowUnknown {
				d.saveError(fmt.Errorf("json: unknown field %q", key))
			}
			d.skip()
		}
		if d.i >= d.refillAt {
			d.refill()
		}
		s = d.s
		i = d.i
		for isSpace[s[i]] {
			i++
		}
		switch s[i] {
		case ',':
			i++
			for isSpace[s[i]] {
				i++
			}
			continue
		case '}':
			d.i = i + 1
			d.depth--
			return
		}
		d.i = i
		d.bad()
	}
}

// colon moves past the colon after a member name and the whitespace
// around it.
func (d *decodeState) colon() {
	d.ws()
	if d.s[d.i] != ':' {
		d.bad()
	}
	d.i++
	d.ws()
}

// more moves past the whitespace and comma after a member or element and
// reports true, or past the whitespace and the closing bracket and
// reports false.
func (d *decodeState) more(close byte) bool {
	if d.i >= d.refillAt {
		d.refill()
	}
	d.ws()
	switch d.s[d.i] {
	case ',':
		d.i++
		d.ws()
		return true
	case close:
		d.i++
		return false
	}
	d.bad()
	return false
}

// embedded returns the address of field f of the struct at p, allocating
// the embedded structs on its path, or nil after saving an error when one
// of them cannot be set.
func (d *decodeState) embedded(f *field, p unsafe.Pointer, st reflect.Type) unsafe.Pointer {
	for _, s := range f.path {
		pp := (*unsafe.Pointer)(unsafe.Add(p, s.off))
		if *pp == nil {
			if !s.exported {
				if jsonv2 {
					d.typeErrorErr(kindName(d.s[d.i]), st, d.i, d.i, errEmbeddedPtr)
					return nil
				}
				d.saveError(fmt.Errorf("json: cannot set embedded pointer to unexported struct: %v", s.elem))
				return nil
			}
			*pp = newObject(s.elem, rtypeOf(s.elem), s.elem.Size())
		}
		p = *pp
	}
	return unsafe.Add(p, f.offset)
}

// key reads the member name at d.i and returns it, unescaped. The result
// may view the input or d.scratch and is valid until the next key.
func (d *decodeState) key() string {
	start := d.i
	if d.s[start] != '"' {
		d.bad()
	}
	end, flags := d.stringEnd(start)
	d.i = end + 1
	if flags&spanHole != 0 {
		return d.holeString(flags)
	}
	raw := d.s[start+1 : end]
	if flags == 0 || flags == jsonsimd.SpanHigh && validUTF8(raw) {
		return raw
	}
	d.scratch = unquote(d.scratch[:0], raw)
	return bytesView(d.scratch)
}

// slice decodes a JSON array into the slice at p.
func (d *decodeState) slice(pl *decPlan, p unsafe.Pointer) {
	h := (*sliceHeader)(p)
	start := d.i
	n := -1
	if c := d.container(start); c != nil {
		n = int(c.N)
		d.ci++
	}
	d.enter(false)
	d.i++
	d.ws()
	if d.s[d.i] == ']' {
		d.i++
		d.depth--
		*h = sliceHeader{data: zeroBase, len: 0, cap: 0}
		return
	}
	el := pl.elem
	size := el.size
	if n > h.cap {
		d.growSlice(pl, h, n)
	}
	p0 := len(d.pend)
	for k := 0; ; k++ {
		if k >= h.cap {
			// Without a table entry, room for one element of a large type
			// or four of a small one. A longer array goes on in scratch
			// space, or its rest is counted once.
			c := max(2*h.cap, 4)
			if size > 64 && k < 4 {
				// A large element type starts from the length of the last
				// arrays of the plan, when they had the same, short one.
				c = max(2*h.cap, 1, pl.lastLen())
			}
			// In a short input, two elements of a large type grow in place:
			// through scratch space, they would be moved, copied and cleared
			// again.
			if n < 0 && k > 0 && k <= 4 && (k > 1 || size <= 64 || len(d.data) > slabChunkMax) && pl.scratch && d.nscr < scratchLevels && d.stream == nil {
				if k = d.sliceScratch(pl, h, k, p0); k < 0 {
					if size > 64 {
						pl.noteLen(h.len)
					}
					return
				}
				n = 0
			} else {
				if n < 0 && k >= 4 {
					if r := d.presize(k, el); r > 0 {
						c = k + r
					}
					n = 0
				}
				d.growSlice(pl, h, c)
			}
		}
		if k >= h.len {
			h.len = k + 1
		}
		if ep := unsafe.Add(h.data, uintptr(k)*size); el.kind == dStruct && d.s[d.i] == '{' {
			d.object(el, ep)
		} else {
			d.value(el, ep)
		}
		if d.more(']') {
			continue
		}
		d.depth--
		if k+1 < h.len {
			h.len = k + 1
		}
		if size > 64 {
			pl.noteLen(k + 1)
		}
		return
	}
}

type sliceHeader struct {
	data     unsafe.Pointer
	len, cap int
}

// sliceScratch decodes the rest of the array at d.i, whose length is not
// known, from its k-th element, into the next level of d.scr, and then
// stores all of it in the slice h of plan pl as an array of its exact
// length. The first k elements, which are in h, may hold the strings
// pended from p0 on. sliceScratch returns -1, or, when the level would
// grow past scratchMax bytes, the number of elements decoded so far:
// those are then in h, which has room for the rest of the array or twice
// as many elements.
func (d *decodeState) sliceScratch(pl *decPlan, h *sliceHeader, k, p0 int) int {
	el := pl.elem
	size := el.size
	sc := d.scratchFor(el)
	d.nscr++
	sc.h, sc.pl, sc.p0 = h, pl, p0
	if sc.cap <= k {
		sc.data, sc.cap = newArray(el.typ, el.rtyp, size, 8), 8
	}
	d.scratchMove(sc, sc.data, h.data, k)
	k0, i0, long0 := k, d.i, d.long
	for ; ; k++ {
		if k >= sc.cap {
			c := min(2*sc.cap, int(scratchMax/size))
			if c <= k {
				var r int
				if n := d.i - i0; k > k0 && (d.long-long0)*2 >= n {
					// Elements mostly of long strings, which a count would
					// read a second time: the rest of the array is taken
					// to fill the input, at the bytes per element so far,
					// and a quarter more.
					r = (len(d.s) - d.i) / max(n/(k-k0), 1)
					r = min(r+r/4+1, k)
				} else {
					r = d.presize(k, el)
				}
				if r > 0 {
					c = k + r
				} else {
					c = 2 * k
				}
				d.scratchDone(sc, c)
				return k
			}
			data := newArray(el.typ, el.rtyp, size, c)
			d.scratchMove(sc, data, sc.data, k)
			sc.data, sc.cap = data, c
		}
		sc.n = k + 1
		if ep := unsafe.Add(sc.data, uintptr(k)*size); el.kind == dStruct && d.s[d.i] == '{' {
			d.object(el, ep)
		} else {
			d.value(el, ep)
		}
		if d.more(']') {
			continue
		}
		d.depth--
		d.scratchDone(sc, k+1)
		return -1
	}
}

// scratchFor returns the next level of d.scr, holding the space of a
// free level for elements of plan el, or no space when there is none.
func (d *decodeState) scratchFor(el *decPlan) *arrScratch {
	sc := &d.scr[d.nscr]
	if sc.el == el {
		return sc
	}
	free := d.nscr
	for k := d.nscr + 1; k < scratchLevels; k++ {
		if d.scr[k].el == el {
			*sc, d.scr[k] = d.scr[k], *sc
			return sc
		}
		if d.scr[k].data == nil {
			free = k
		}
	}
	// Keep the space of this level in a free one.
	*sc, d.scr[free] = d.scr[free], *sc
	sc.el, sc.data, sc.cap = el, nil, 0
	return sc
}

// scratchDone stores the elements of the scratch level sc in its slice,
// in a new array of capacity c, and ends the level, which is the
// innermost in use.
func (d *decodeState) scratchDone(sc *arrScratch, c int) {
	el := sc.el
	data := newArray(el.typ, el.rtyp, el.size, c)
	d.scratchMove(sc, data, sc.data, sc.n)
	*sc.h = sliceHeader{data, sc.n, c}
	clearElems(sc.pl, sc.data, sc.n)
	sc.h, sc.pl = nil, nil
	d.nscr--
}

// scratchMove copies n elements of the slice of the scratch level sc from
// src to dst, with the strings pended in them.
func (d *decodeState) scratchMove(sc *arrScratch, dst, src unsafe.Pointer, n int) {
	if n == 0 {
		return
	}
	if !sc.pl.elemNoPtr && len(d.pend) > sc.p0 {
		lo := uintptr(src)
		hi := lo + uintptr(n)*sc.el.size
		for k := sc.p0; k < len(d.pend); k++ {
			if a := uintptr(d.pend[k].dst); a >= lo && a < hi {
				d.pend[k].dst = unsafe.Add(dst, a-lo)
			}
		}
	}
	copyElems(sc.pl, dst, src, n)
}

// endScratch ends the scratch levels in use after a decode stopped. With
// keep, each stores the elements decoded so far in its slice, as a
// decode that does not stop would have.
func (d *decodeState) endScratch(keep bool) {
	for d.nscr > 0 {
		sc := &d.scr[d.nscr-1]
		if keep {
			d.scratchDone(sc, sc.n)
			continue
		}
		clearElems(sc.pl, sc.data, sc.n)
		sc.h, sc.pl = nil, nil
		d.nscr--
	}
}

// presizeMax bounds the backing array allocated from a count of elements
// that a syntax error may still void.
const presizeMax = 1 << 20

// presize returns the number of elements of plan el from the one at d.i,
// which is the k-th, to the end of the array, or -1 when they are not
// counted.
func (d *decodeState) presize(k int, el *decPlan) int {
	size := el.size
	var r int
	if el.kind >= dInt && el.kind <= dFloat64 {
		r = d.countNumbers(d.i)
	} else {
		r = d.countFrom(d.i)
	}
	if r > 0 && d.fused && !d.checked && uintptr(k+r)*size > presizeMax {
		return -1
	}
	return r
}

// zeroBase is the data pointer of empty slices.
var zeroBase = unsafe.Pointer(&zeroArray)

var zeroArray [0]uint64

// growSlice gives the slice h of plan pl a new backing array of capacity
// n, larger than its own, holding all the elements of its array: those
// past its length are decoded into again, as encoding/json does.
func (d *decodeState) growSlice(pl *decPlan, h *sliceHeader, n int) {
	el := pl.elem
	data := newArray(el.typ, el.rtyp, el.size, n)
	if h.cap > 0 && len(d.pend) > 0 && !pl.elemNoPtr {
		d.movedStrings(h.data, data, h.cap, el.size)
	}
	if h.cap > 0 {
		copyElems(pl, data, h.data, h.cap)
	}
	h.data, h.cap = data, n
}

// array decodes a JSON array into the Go array at p.
func (d *decodeState) array(pl *decPlan, p unsafe.Pointer) {
	if c := d.container(d.i); c != nil {
		d.ci++
	}
	d.enter(false)
	d.i++
	d.ws()
	el := pl.elem
	k := 0
	if d.s[d.i] == ']' {
		d.i++
	} else {
		for {
			if k < pl.alen {
				d.value(el, unsafe.Add(p, uintptr(k)*el.size))
			} else {
				d.skip()
			}
			k++
			if !d.more(']') {
				break
			}
		}
	}
	d.depth--
	for ; k < pl.alen; k++ {
		reflect.NewAt(el.typ, unsafe.Add(p, uintptr(k)*el.size)).Elem().SetZero()
	}
}

// base64Value decodes the JSON string at d.i as base64 into the byte slice
// at p.
func (d *decodeState) base64Value(pl *decPlan, p unsafe.Pointer) {
	start := d.i
	b := d.stringBytes()
	if jsonv2 {
		d.base64ValueV2(pl, p, b, start)
		return
	}
	out := make([]byte, base64.StdEncoding.DecodedLen(len(b)))
	n, err := base64.StdEncoding.Decode(out, b)
	if err != nil {
		d.saveError(err)
		return
	}
	*(*[]byte)(p) = out[:n]
}

// quotedValue decodes into p of plan pl, of field type ft with the
// ",string" option: the value is a JSON string holding the literal.
func (d *decodeState) quotedValue(pl *decPlan, p unsafe.Pointer, ft reflect.Type) {
	c := d.s[d.i]
	switch c {
	case 'n':
		d.value(pl, p)
		return
	case '"':
		if callsMethod(pl) {
			d.validateAll()
		}
		end := d.literalEnd()
		item := d.stringBytes()
		d.storeQuoted(item, pl, p, ft, end)
		return
	case '{', '[':
		d.skip()
	default:
		if c != 't' && c != 'f' && !d.useNumber {
			end := d.numberEnd(d.i)
			if _, ok := d.parseFloatAt(d.i, end, 64); !ok {
				// The number is out of range for a float64, which is an
				// error, and it decodes as null.
				d.typeError("number "+string(d.s[d.i:end]), reflect.TypeFor[float64](), end+1)
				d.i = end
				d.storeQuoted([]byte("null"), pl, p, ft, end)
				return
			}
		}
		d.i = d.literalEnd()
	}
	d.saveError(fmt.Errorf("json: invalid use of ,string struct tag, trying to unmarshal unquoted value into %v", ft))
}

// callsMethod reports whether storing a quoted value into plan pl may call
// UnmarshalJSON or UnmarshalText.
func callsMethod(pl *decPlan) bool {
	for pl.kind == dPtr {
		if pl.unm != nil || pl.text != nil {
			return true
		}
		pl = pl.elem
	}
	return pl.kind == dUnm || pl.kind == dText
}

func invalidQuoted(item []byte, t reflect.Type) error {
	return fmt.Errorf("json: invalid use of ,string struct tag, trying to unmarshal %q into %v", item, t)
}

// storeQuoted stores the literal item, read from a quoted field value that
// ends at end, into p of plan pl and field type ft.
func (d *decodeState) storeQuoted(item []byte, pl *decPlan, p unsafe.Pointer, ft reflect.Type, end int) {
	if len(item) == 0 {
		d.saveError(invalidQuoted(item, ft))
		return
	}
	isNull := item[0] == 'n'
	// Follow pointers, allocating them, as for any other value.
	for pl.kind == dPtr {
		if isNull {
			if string(item) != "null" {
				d.saveError(invalidQuoted(item, ft))
				return
			}
			*(*unsafe.Pointer)(p) = nil
			return
		}
		ptr := *(*unsafe.Pointer)(p)
		if ptr == nil {
			ptr = d.newElem(pl)
			*(*unsafe.Pointer)(p) = ptr
		}
		if pl.unm != nil {
			var u Unmarshaler
			*(*iface)(unsafe.Pointer(&u)) = iface{pl.unm, ptr}
			if err := u.UnmarshalJSON(item); err != nil {
				d.fail(err)
			}
			return
		}
		if pl.text != nil {
			d.storeQuotedText(item, pl.text, ptr, ft)
			return
		}
		pl, p = pl.elem, ptr
	}
	switch pl.kind {
	case dUnm:
		var u Unmarshaler
		*(*iface)(unsafe.Pointer(&u)) = iface{pl.itab, p}
		if err := u.UnmarshalJSON(item); err != nil {
			d.fail(err)
		}
		return
	case dText:
		if !isNull {
			d.storeQuotedText(item, pl.itab, p, ft)
			return
		}
		pl = pl.elem
	}
	t := pl.typ
	switch c := item[0]; {
	case c == 'n':
		if string(item) != "null" {
			d.saveError(invalidQuoted(item, t))
			return
		}
		switch pl.kind {
		case dMap:
			*(*unsafe.Pointer)(p) = nil
		case dSlice, dBytes, dRaw:
			*(*[]byte)(p) = nil
		case dAny:
			*(*any)(p) = nil
		case dIface:
			*(*iface)(p) = iface{}
		}
	case c == 't' || c == 'f':
		if string(item) != "true" && string(item) != "false" {
			d.saveError(invalidQuoted(item, t))
			return
		}
		switch pl.kind {
		case dBool:
			*(*bool)(p) = c == 't'
		case dAny:
			*(*any)(p) = c == 't'
		default:
			d.saveError(invalidQuoted(item, t))
		}
	case c == '"':
		s := string(item)
		if jsonsimd.StringEnd(s, 1) != len(s)-1 {
			d.fail(invalidQuoted(item, t))
		}
		u := unquote(nil, s[1:len(s)-1])
		switch pl.kind {
		case dString:
			*(*string)(p) = string(u)
		case dNumber:
			if !isValidNumber(u) {
				d.fail(fmt.Errorf("json: invalid number literal, trying to unmarshal %q into Number", item))
			}
			*(*string)(p) = string(u)
		case dAny:
			*(*any)(p) = string(u)
		case dBytes:
			out := make([]byte, base64.StdEncoding.DecodedLen(len(u)))
			n, err := base64.StdEncoding.Decode(out, u)
			if err != nil {
				d.saveError(err)
				return
			}
			*(*[]byte)(p) = out[:n]
		default:
			d.typeError("string", t, end)
		}
	default:
		if c != '-' && (c < '0' || c > '9') {
			d.fail(invalidQuoted(item, t))
		}
		s := string(item)
		switch pl.kind {
		case dNumber:
			*(*string)(p) = s
		case dAny, dIface:
			f, err := strconv.ParseFloat(s, 64)
			if d.useNumber {
				if pl.kind == dAny {
					*(*any)(p) = Number(s)
					return
				}
			} else if err != nil {
				d.typeError("number "+s, reflect.TypeFor[float64](), end+1)
				return
			}
			if pl.kind == dIface {
				d.typeError("number", t, end)
				return
			}
			*(*any)(p) = f
		case dInt, dInt8, dInt16, dInt32, dInt64:
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil || overflowInt(pl.kind, n) {
				d.typeError("number "+s, t, end)
				return
			}
			storeInt(pl.kind, p, n)
		case dUint, dUint8, dUint16, dUint32, dUint64, dUintptr:
			n, err := strconv.ParseUint(s, 10, 64)
			if err != nil || overflowUint(pl.kind, n) {
				d.typeError("number "+s, t, end)
				return
			}
			storeUint(pl.kind, p, n)
		case dFloat32, dFloat64:
			bits := 64
			if pl.kind == dFloat32 {
				bits = 32
			}
			f, err := strconv.ParseFloat(s, bits)
			if err != nil || overflowFloat(pl.kind, f) {
				d.typeError("number "+s, t, end)
				return
			}
			if bits == 32 {
				*(*float32)(p) = float32(f)
			} else {
				*(*float64)(p) = f
			}
		default:
			d.fail(invalidQuoted(item, t))
		}
	}
}

func (d *decodeState) storeQuotedText(item []byte, tab, ptr unsafe.Pointer, ft reflect.Type) {
	if item[0] != '"' {
		d.saveError(invalidQuoted(item, ft))
		return
	}
	s := string(item)
	if jsonsimd.StringEnd(s, 1) != len(s)-1 {
		d.fail(invalidQuoted(item, ft))
	}
	var u textUnmarshaler
	*(*iface)(unsafe.Pointer(&u)) = iface{tab, ptr}
	if err := u.UnmarshalText(unquote(nil, s[1:len(s)-1])); err != nil {
		d.fail(err)
	}
}
