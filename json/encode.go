package json

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
	"unsafe"

	"github.com/MoeForge/moego/base64"
	"github.com/MoeForge/moego/internal/jsonsimd"
)

// Marshal returns the JSON encoding of v. It follows the rules of
// encoding/json.Marshal and gives the same bytes and errors, except that a
// value nested more than 1<<20 levels deep returns an
// UnsupportedValueError, where encoding/json overflows the stack.
func Marshal(v any) ([]byte, error) {
	e := getEncodeState(true)
	e.long = 0
	b, err := e.marshal(e.buf[:0], v)
	var out []byte
	if err == nil {
		out = e.result(b)
		if len(out) >= stampMin {
			e.stamp(out, (*eface)(unsafe.Pointer(&v)).typ)
		}
	}
	putEncodeState(e, b)
	return out, err
}

// MarshalIndent is like Marshal but applies Indent to format the output.
// Each JSON element in the output will begin on a new line beginning with
// prefix followed by one or more copies of indent according to the
// indentation nesting.
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	e := getEncodeState(true)
	b, err := e.marshal(e.buf[:0], v)
	if err != nil {
		putEncodeState(e, b)
		return nil, err
	}
	flat := b
	if len(e.refs) > 0 {
		flat = e.result(b)
	}
	out := appendIndentValid(make([]byte, 0, indentedSize(flat, len(prefix), len(indent))), flat, prefix, indent)
	putEncodeState(e, b)
	return out, nil
}

// maxEncodeDepth is the deepest nesting Marshal encodes.
const maxEncodeDepth = 1 << 20

// startDetectingCycles is the number of nested pointers, maps and slices
// after which the encoder looks for cycles.
const startDetectingCycles = 1000

// encodeState holds what an encoding needs besides its output, which the
// encoding functions take and return as b. Long strings and long JSON
// values that are written unchanged are not copied into b: refs records
// them, and result writes them into the output once.
type encodeState struct {
	refs       []strRef
	refBytes   int
	pending    int // refs that view memory user code may still change
	escapeHTML bool
	depth      int
	nest       int // objects and arrays open, on encoding/json/v2
	ptrLevel   int
	ptrSeen    map[ptrKey]struct{}
	keys       []mapEntry
	anyKeys    []anyEntry
	buf        []byte // the output buffer, kept for reuse
	// long counts the bytes of strings of at least stampLong bytes, and
	// notes the outputs of Marshal, for stamps.
	long, notes int
}

// strRef is a string to write at offset at of the output buffer. user
// marks one that views a RawMessage or the result of a MarshalJSON method,
// lazy a string whose bytes result checks as it copies them, and esc a
// string, valid UTF-8 without U+2028 or U+2029, that result escapes as it
// writes it.
type strRef struct {
	at              int
	s               string
	user, lazy, esc bool
}

// refMin is the shortest string or JSON value kept as a reference.
const refMin = 4 << 10

// Strings of at least lazyMin bytes whose first lazyCheck bytes need no
// escape are written by reference before the rest is checked: result
// checks it as it copies it, reading it once.
const (
	lazyMin   = 16 << 10
	lazyCheck = 4 << 10
)

// ptrKey identifies a pointer, map or slice for cycle detection.
type ptrKey struct {
	typ unsafe.Pointer
	ptr unsafe.Pointer
	n   int
}

var (
	mapTag   byte
	sliceTag byte
)

// maxPooledOutput bounds the output buffer kept for the next call.
const maxPooledOutput = 16 << 20

var encodeStates stateCache[encodeState]

func getEncodeState(html bool) *encodeState {
	e := encodeStates.get()
	e.escapeHTML = html
	return e
}

// putEncodeState keeps e for the next call, with b, its output buffer.
func putEncodeState(e *encodeState, b []byte) {
	if cap(b) > maxPooledOutput || cap(e.keys) > 1<<12 || cap(e.anyKeys) > 1<<12 {
		return
	}
	e.buf = b[:0]
	clear(e.refs)
	e.refs = e.refs[:0]
	e.refBytes, e.pending = 0, 0
	e.depth, e.nest, e.ptrLevel = 0, 0, 0
	e.ptrSeen = nil
	held := cap(b) + cap(e.refs)*int(unsafe.Sizeof(strRef{})) +
		cap(e.keys)*int(unsafe.Sizeof(mapEntry{})) + cap(e.anyKeys)*int(unsafe.Sizeof(anyEntry{}))
	encodeStates.put(e, held <= stateCacheMax)
}

// encodeError carries an error out of the encoder.
type encodeError struct{ err error }

func (e *encodeState) fail(err error) { panic(encodeError{err}) }

func (e *encodeState) marshal(b []byte, v any) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			ee, ok := r.(encodeError)
			if !ok {
				panic(r)
			}
			err = ee.err
			clear(e.keys)
			e.keys = e.keys[:0]
			clear(e.anyKeys)
			e.anyKeys = e.anyKeys[:0]
		}
	}()
	return e.anyValue(b, v), nil
}

// result returns the output, b with the references written in, in a new
// slice of its exact size.
func (e *encodeState) result(b []byte) []byte {
	out := makeNoZero(len(b) + e.refBytes)
	if len(e.refs) == 0 {
		copy(out, b)
		return out
	}
	w, from := 0, 0
	for i := range e.refs {
		r := &e.refs[i]
		w += copy(out[w:], b[from:r.at])
		if r.esc {
			w = escapeTo(out, w, r.s, e.escapeHTML)
		} else if r.lazy {
			if !copyPlain(out[w:], r.s, e.escapeHTML) {
				e.escapeRef(r)
				return e.result(b)
			}
			r.lazy = false
			w += len(r.s)
		} else {
			w += copy(out[w:], r.s)
		}
		from = r.at
	}
	copy(out[w:], b[from:])
	return out
}

// copyPlain copies s to dst, which has room for it, and reports whether s
// needs no escape in a JSON string. When it does not, dst holds part of s.
func copyPlain(dst []byte, s string, html bool) bool {
	n := jsonsimd.CopyPlainBlocks(dst, s, html)
	if len(s)-n >= 64 || !plainTail(s[n:], html) {
		return false
	}
	copy(dst[n:], s[n:])
	return true
}

// escapeTo writes s, valid UTF-8 without U+2028 or U+2029, escaped to
// out[w:], which has room for exactly that from w on, and returns the new
// w. The kernel may write past the escape, over bytes result writes
// after it.
func escapeTo(out []byte, w int, s string, html bool) int {
	i := 0
	for len(s)-i >= 32 {
		i2, w2 := jsonsimd.EscapeBlocks(out, s, i, w, html)
		if i2 == i {
			break
		}
		i, w = i2, w2
	}
	for ; i < len(s); i++ {
		c := s[i]
		if c >= utf8.RuneSelf || safeASCII(c, html) {
			out[w] = c
			w++
			continue
		}
		e := escapeShort[c]
		if e == 0 {
			out[w], out[w+1], out[w+2], out[w+3], out[w+4], out[w+5] = '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xF]
			w += 6
			continue
		}
		out[w], out[w+1] = '\\', e
		w += 2
	}
	return w
}

// escapeShort[c] is the second byte of the escape of two bytes of c, or 0.
var escapeShort = [256]byte{'"': '"', '\\': '\\', '\b': 'b', '\f': 'f', '\n': 'n', '\r': 'r', '\t': 't'}

// escapeRef writes the string of r, which needs escapes after all, escaped:
// by result when it is valid UTF-8 without U+2028 or U+2029.
func (e *encodeState) escapeRef(r *strRef) {
	r.lazy = false
	if n := jsonsimd.EscapedLen(r.s, e.escapeHTML); n >= 0 {
		r.esc = true
		e.refBytes += n - len(r.s)
		return
	}
	q := appendStringSlow(nil, r.s, e.escapeHTML)
	q = q[1 : len(q)-1]
	e.refBytes += len(q) - len(r.s)
	r.s, r.lazy = bytesView(q), false
}

// ref writes s by reference at the end of b.
func (e *encodeState) ref(b []byte, s string, user bool) {
	e.refs = append(e.refs, strRef{at: len(b), s: s, user: user})
	e.refBytes += len(s)
	if user {
		e.pending++
	}
}

// pin copies the user references before user code runs, which could change
// the memory they view.
func (e *encodeState) pin() {
	if e.pending == 0 {
		return
	}
	for i := len(e.refs) - 1; e.pending > 0; i-- {
		if r := &e.refs[i]; r.user {
			r.s, r.user = strings.Clone(r.s), false
			e.pending--
		}
	}
}

// anyValue encodes the dynamic value of an interface, which is not
// addressable.
func (e *encodeState) anyValue(b []byte, v any) []byte {
	ef := (*eface)(unsafe.Pointer(&v))
	if ef.typ == nil {
		return append(b, "null"...)
	}
	switch x := v.(type) {
	case string:
		return e.str(b, x)
	case float64:
		return e.float(b, x, 64, nil)
	case bool:
		return appendBool(b, x)
	case map[string]any:
		return e.mapAt(b, encPlanFor(mapStringAnyRtype), *(*unsafe.Pointer)(unsafe.Pointer(&x)), nil)
	case []any:
		return e.sliceAny(b, x)
	}
	pl := encPlanFor(ef.typ)
	switch {
	case pl.kind == ePtr:
		return e.pointer(b, pl, ef.data, nil)
	case pl.kind == eMap:
		return e.mapAt(b, pl, ef.data, nil)
	case pl.direct:
		// The interface holds the value itself in its data word.
		c := ef.data
		return e.value(b, pl, unsafe.Pointer(&c), false)
	}
	return e.value(b, pl, ef.data, false)
}

var (
	mapStringAnyRtype = rtypeOf(reflect.TypeFor[map[string]any]())
	anySliceType      = reflect.TypeFor[[]any]()
)

func (e *encodeState) enter(pl *encPlan) {
	if e.depth++; e.depth > maxEncodeDepth {
		e.tooDeep(pl.typ)
	}
}

// open counts an object or array that starts at index at of the output
// buffer, on encoding/json/v2, which writes maxNestingDepth levels of them.
func (e *encodeState) open(at int) {
	if e.nest++; e.nest > maxNestingDepth {
		e.fail(e.nestingError(at))
	}
}

// outLen is the length of the output up to index at of the output buffer,
// the strings written by reference included.
func (e *encodeState) outLen(at int) int { return at + e.refBytes }

// nestingError is the error for an object or array nested too deep that
// starts at index at of the output buffer.
//
//go:noinline
func (e *encodeState) nestingError(at int) error {
	off := e.outLen(at)
	if go127 {
		off++
	}
	return newSyntaxError("exceeded max depth", int64(off))
}

//go:noinline
func (e *encodeState) tooDeep(via reflect.Type) {
	e.fail(&UnsupportedValueError{Value: reflect.Value{}, Str: "value nested more than 1048576 levels deep via " + via.String()})
}

// value encodes the value of plan pl at p; addr tells whether it is
// addressable, which pointer-receiver methods need.
func (e *encodeState) value(b []byte, pl *encPlan, p unsafe.Pointer, addr bool) []byte {
	if pl.methods && pl.kind != ePtr && pl.kind != eMap {
		if out, ok := e.methods(b, pl, p, addr); ok {
			return out
		}
	}
	switch pl.kind {
	case eString:
		return e.str(b, *(*string)(p))
	case eStruct:
		return e.structValue(b, pl, p, addr)
	case ePtr:
		return e.pointer(b, pl, *(*unsafe.Pointer)(p), p)
	case eRaw:
		return e.raw(b, *(*[]byte)(p), pl)
	case eBool:
		return appendBool(b, *(*bool)(p))
	case eInt:
		return appendInt(b, int64(*(*int)(p)))
	case eInt8:
		return appendInt(b, int64(*(*int8)(p)))
	case eInt16:
		return appendInt(b, int64(*(*int16)(p)))
	case eInt32:
		return appendInt(b, int64(*(*int32)(p)))
	case eInt64:
		return appendInt(b, *(*int64)(p))
	case eUint:
		return appendUint(b, uint64(*(*uint)(p)))
	case eUint8:
		return appendUint(b, uint64(*(*uint8)(p)))
	case eUint16:
		return appendUint(b, uint64(*(*uint16)(p)))
	case eUint32:
		return appendUint(b, uint64(*(*uint32)(p)))
	case eUint64:
		return appendUint(b, *(*uint64)(p))
	case eUintptr:
		return appendUint(b, uint64(*(*uintptr)(p)))
	case eFloat32:
		return e.float(b, float64(*(*float32)(p)), 32, pl.typ)
	case eFloat64:
		return e.float(b, *(*float64)(p), 64, pl.typ)
	case eNumber:
		return e.number(b, *(*string)(p), false)
	case eIface:
		x := *(*any)(p)
		if pl.typ.NumMethod() > 0 {
			x = ifaceToEface(p)
		}
		if x == nil {
			return append(b, "null"...)
		}
		e.enter(pl)
		b = e.anyValue(b, x)
		e.depth--
		return b
	case eSlice:
		return e.slice(b, pl, p)
	case eBytes:
		return appendBase64(b, *(*[]byte)(p))
	case eArray:
		e.enter(pl)
		if jsonv2 {
			e.open(len(b))
		}
		b = e.arrayElements(b, pl, p, addr)
		if jsonv2 {
			e.nest--
		}
		e.depth--
		return b
	case eMap:
		return e.mapAt(b, pl, *(*unsafe.Pointer)(p), p)
	case eMarshalIface:
		x := ifaceToEface(p)
		m, ok := x.(Marshaler)
		if !ok {
			return append(b, "null"...)
		}
		if jsonv2 {
			return e.ifaceMethodV2(b, pl, x)
		}
		return e.callMarshaler(b, m, pl.typ)
	case eTextIface:
		x := ifaceToEface(p)
		m, ok := x.(interface{ MarshalText() ([]byte, error) })
		if !ok {
			return append(b, "null"...)
		}
		if jsonv2 {
			return e.ifaceMethodV2(b, pl, x)
		}
		return e.callText(b, m, pl.typ)
	}
	e.fail(&UnsupportedTypeError{Type: pl.typ})
	return b
}

// slice encodes the slice at p of plan pl.
func (e *encodeState) slice(b []byte, pl *encPlan, p unsafe.Pointer) []byte {
	h := (*sliceHeader)(p)
	if h.data == nil {
		return append(b, "null"...)
	}
	e.enter(pl)
	if jsonv2 {
		e.open(len(b))
	}
	e.ptrLevel++
	if e.ptrLevel > startDetectingCycles {
		e.seen(ptrKey{unsafe.Pointer(&sliceTag), h.data, h.len}, pl, p)
	}
	if el := pl.elem; el.kind == eStruct && !el.methods {
		// Elements written here rather than in elements, which saves two
		// frames for each level of nested slices of structs.
		b = append(b, '[')
		for i := 0; i < h.len; i++ {
			if i > 0 {
				b = append(b, ',')
			}
			b = e.structValue(b, el, unsafe.Add(h.data, uintptr(i)*el.size), true)
		}
		b = append(b, ']')
	} else {
		b = e.elements(b, pl, h.data, h.len)
	}
	if e.ptrLevel > startDetectingCycles {
		e.forget(ptrKey{unsafe.Pointer(&sliceTag), h.data, h.len})
	}
	if jsonv2 {
		e.nest--
	}
	e.ptrLevel--
	e.depth--
	return b
}

func appendBase64(b, v []byte) []byte {
	if v == nil {
		return append(b, "null"...)
	}
	b = append(b, '"')
	b = base64.StdEncoding.AppendEncode(b, v)
	return append(b, '"')
}

// ifaceToEface returns the non-empty interface value at p as an any.
func ifaceToEface(p unsafe.Pointer) any {
	i := (*iface)(p)
	var x any
	if i.tab != nil {
		*(*eface)(unsafe.Pointer(&x)) = eface{*(*unsafe.Pointer)(unsafe.Add(i.tab, unsafe.Sizeof(uintptr(0)))), i.data}
	}
	return x
}

// seen records key k and fails when it is already recorded: a cycle. p
// is the address of the value of plan pl, or nil when the value is the
// pointer k.ptr itself.
//
// It and forget are kept out of line, so that the frames of nested values
// stay small.
//
//go:noinline
func (e *encodeState) seen(k ptrKey, pl *encPlan, p unsafe.Pointer) {
	if e.ptrSeen == nil {
		e.ptrSeen = map[ptrKey]struct{}{}
	}
	if _, ok := e.ptrSeen[k]; ok {
		if p == nil {
			c := k.ptr
			p = unsafe.Pointer(&c)
		}
		// encoding/json on encoding/json/v2 names no value.
		var v reflect.Value
		if !jsonv2 {
			v = reflect.NewAt(pl.typ, p).Elem()
		}
		e.fail(&UnsupportedValueError{Value: v, Str: fmt.Sprintf("encountered a cycle via %s", pl.typ)})
	}
	e.ptrSeen[k] = struct{}{}
}

// forget removes k, recorded by seen.
//
//go:noinline
func (e *encodeState) forget(k ptrKey) { delete(e.ptrSeen, k) }

// pointer encodes the pointer ptr of plan pl, stored at p (nil when it is
// not stored in memory).
func (e *encodeState) pointer(b []byte, pl *encPlan, ptr, p unsafe.Pointer) []byte {
	if pl.methods {
		if out, ok := e.methods(b, pl, ptr, false); ok {
			return out
		}
	}
	if ptr == nil {
		return append(b, "null"...)
	}
	e.enter(pl)
	e.ptrLevel++
	if e.ptrLevel > startDetectingCycles {
		e.seen(ptrKey{pl.rtyp, ptr, 0}, pl, p)
	}
	if el := pl.elem; el.kind == eStruct && !el.methods {
		b = e.structValue(b, el, ptr, true)
	} else {
		b = e.value(b, el, ptr, true)
	}
	if e.ptrLevel > startDetectingCycles {
		e.forget(ptrKey{pl.rtyp, ptr, 0})
	}
	e.ptrLevel--
	e.depth--
	return b
}

// mapAt encodes the map m of plan pl, stored at p (nil when it is not
// stored in memory).
func (e *encodeState) mapAt(b []byte, pl *encPlan, m, p unsafe.Pointer) []byte {
	if pl.methods {
		// A copy of m, so that m itself is not moved to the heap.
		recv := m
		if out, ok := e.methods(b, pl, unsafe.Pointer(&recv), false); ok {
			return out
		}
	}
	if m == nil {
		return append(b, "null"...)
	}
	e.enter(pl)
	if jsonv2 {
		e.open(len(b))
	}
	e.ptrLevel++
	if e.ptrLevel > startDetectingCycles {
		e.seen(ptrKey{unsafe.Pointer(&mapTag), m, 0}, pl, p)
	}
	// The members are written here rather than in mapValue, which saves a
	// frame for each level of nested maps.
	switch pl.mapFast {
	case mapStringAny:
		b = e.mapEntriesAny(b, *(*map[string]any)(unsafe.Pointer(&m)))
	case mapStringString:
		b = e.mapStringString(b, *(*map[string]string)(unsafe.Pointer(&m)))
	case mapStringRaw:
		b = e.mapStringRaw(b, pl, *(*map[string]RawMessage)(unsafe.Pointer(&m)))
	default:
		b = e.mapReflect(b, pl, m)
	}
	if e.ptrLevel > startDetectingCycles {
		e.forget(ptrKey{unsafe.Pointer(&mapTag), m, 0})
	}
	if jsonv2 {
		e.nest--
	}
	e.ptrLevel--
	e.depth--
	return b
}

// methods encodes a value with its MarshalJSON or MarshalText method, the
// receiver being the pointer value for pointer types and else the address
// of the value. It returns false when no method applies.
func (e *encodeState) methods(b []byte, pl *encPlan, recv unsafe.Pointer, addr bool) ([]byte, bool) {
	switch {
	case addr && pl.ptrMarshal != nil:
		return e.callMarshalerTab(b, pl.ptrMarshal, recv, pl.typ), true
	case pl.valMarshal != nil:
		if pl.kind == ePtr && recv == nil {
			return append(b, "null"...), true
		}
		return e.callMarshalerTab(b, pl.valMarshal, recv, pl.typ), true
	case addr && pl.ptrText != nil:
		return e.callTextTab(b, pl.ptrText, recv, pl.typ), true
	case pl.valText != nil:
		if pl.kind == ePtr && recv == nil {
			return append(b, "null"...), true
		}
		return e.callTextTab(b, pl.valText, recv, pl.typ), true
	}
	return b, false
}

func (e *encodeState) callMarshalerTab(b []byte, tab, recv unsafe.Pointer, t reflect.Type) []byte {
	var m Marshaler
	*(*iface)(unsafe.Pointer(&m)) = iface{tab, recv}
	return e.callMarshaler(b, m, t)
}

func (e *encodeState) callMarshaler(b []byte, m Marshaler, t reflect.Type) []byte {
	e.pin()
	out, err := m.MarshalJSON()
	if err == nil {
		b, err = e.appendJSON(b, out, true)
	}
	if err != nil {
		if jsonv2 && (t == timeType || t == timePtrType) {
			// encoding/json/v2 formats times itself.
			err = errors.New(strings.TrimPrefix(err.Error(), "Time.MarshalJSON: "))
		}
		e.fail(newMarshalerError(methodErrType(t), err, "MarshalJSON"))
	}
	return b
}

func (e *encodeState) callTextTab(b []byte, tab, recv unsafe.Pointer, t reflect.Type) []byte {
	var m interface{ MarshalText() ([]byte, error) }
	*(*iface)(unsafe.Pointer(&m)) = iface{tab, recv}
	return e.callText(b, m, t)
}

func (e *encodeState) callText(b []byte, m interface{ MarshalText() ([]byte, error) }, t reflect.Type) []byte {
	e.pin()
	out, err := m.MarshalText()
	if err != nil {
		e.fail(newMarshalerError(methodErrType(t), err, "MarshalText"))
	}
	return appendString(b, bytesView(out), e.escapeHTML)
}

// appendJSON appends the JSON value src, a RawMessage or the output of a
// method when method is set, compacted, with HTML escaping when e has it. A
// long value that needs neither is written by reference.
func (e *encodeState) appendJSON(b, src []byte, method bool) ([]byte, error) {
	s := bytesView(src)
	var ok, spaced, html bool
	if len(src) >= stampMin {
		// A RawMessage value the decoder stamped is compact, but its HTML
		// characters are not escaped.
		var raw bool
		ok, raw = stamped(src)
		html = ok && raw && e.escapeHTML && jsonsimd.HasHTML(s)
	}
	if !ok {
		if method && len(src) >= stampLong {
			// Marshal output that is not stamped is mostly long strings,
			// as far as stamps go.
			e.long += len(src)
		}
		ok, spaced, html = jsonsimd.ValidateSpaced(s, maxNestingDepth, e.escapeHTML)
		if !ok {
			err := compactSyntaxError(src)
			if se, ok := err.(*SyntaxError); ok && jsonv2 {
				// The offset is in the whole output.
				se.Offset += int64(e.outLen(len(b)))
			}
			return b, err
		}
	}
	if jsonv2 && !e.escapeHTML && hasLineSep(src) {
		n := len(b)
		return escapeLineSeps(appendCompactValid(b, src, false), n), nil
	}
	if !spaced && !html {
		if len(src) >= refMin {
			e.ref(b, s, true)
			return b, nil
		}
		return append(b, src...), nil
	}
	return appendCompactValid(b, src, html), nil
}

// raw encodes a RawMessage: null when nil, else its bytes compacted.
func (e *encodeState) raw(b, v []byte, pl *encPlan) []byte {
	if v == nil {
		return append(b, "null"...)
	}
	b, err := e.appendJSON(b, v, false)
	if err != nil {
		// encoding/json on encoding/json/v2 names *RawMessage, whatever
		// holds the value.
		t := pl.typ
		if jsonv2 {
			t = reflect.TypeFor[*RawMessage]()
		}
		e.fail(newMarshalerError(t, err, "MarshalJSON"))
	}
	return b
}

func appendBool(b []byte, v bool) []byte {
	if v {
		return append(b, "true"...)
	}
	return append(b, "false"...)
}

// float encodes f, whose type is t, or float64 when t is nil.
func (e *encodeState) float(b []byte, f float64, bits int, t reflect.Type) []byte {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		// Before Go 1.27, encoding/json on encoding/json/v2 names no value.
		var v reflect.Value
		if !jsonv2 || go127 {
			v = reflect.ValueOf(f)
			if t != nil {
				v = v.Convert(t)
			}
		}
		e.fail(&UnsupportedValueError{Value: v, Str: strconv.FormatFloat(f, 'g', -1, bits)})
	}
	return appendFloat(b, f, bits)
}

// number encodes a Number: its literal, or 0 when empty.
func (e *encodeState) number(b []byte, s string, quoted bool) []byte {
	if s == "" {
		s = "0"
	}
	if !isValidNumber(stringView(s)) {
		if jsonv2 {
			// The text of a quoted Number is shown in its quotes.
			q := s
			if quoted {
				q = `"` + s + `"`
			}
			e.fail(newMarshalerError(reflect.PointerTo(numberType), fmt.Errorf("cannot parse %q as JSON number: %w", q, strconv.ErrSyntax), "MarshalJSONTo"))
		}
		e.fail(fmt.Errorf("json: invalid number literal %q", s))
	}
	if quoted {
		b = append(b, '"')
	}
	b = append(b, s...)
	if quoted {
		b = append(b, '"')
	}
	return b
}

// str encodes a string value, keeping long strings that need no escaping
// as references, and strings with other escapes or bytes of 0x80 and above
// as references that result escapes as it writes them. It counts in e.long
// the long strings written without escapes, which a check of the output
// reads faster than a stamp hashes them.
func (e *encodeState) str(b []byte, s string) []byte {
	if len(s) < stampLong {
		return appendString(b, s, e.escapeHTML)
	}
	html := e.escapeHTML
	if jsonsimd.LazyCopy && len(s) >= lazyMin {
		if n, k := jsonsimd.QuoteCountBlocks(s[:lazyCheck], 0, html); k == 0 && n == lazyCheck {
			e.long += len(s)
			b = append(b, '"')
			e.ref(b, s, false)
			e.refs[len(e.refs)-1].lazy = true
			return append(b, '"')
		}
	} else if n, k := jsonsimd.QuoteCountBlocks(s, 0, html); n == len(s)&^63 && plainTail(s[n:], html) {
		// ASCII whose only escapes are of quotes and backslashes.
		switch {
		case k > 0:
			return appendString(b, s, html)
		case len(s) >= refMin:
			e.long += len(s)
			b = append(b, '"')
			e.ref(b, s, false)
			return append(b, '"')
		}
		e.long += len(s)
		b = append(b, '"')
		b = append(b, s...)
		return append(b, '"')
	}
	if jsonsimd.HaveAVX2 {
		// result escapes it into the output, which it is not copied into
		// again.
		if n := jsonsimd.EscapedLen(s, html); n >= 0 {
			if n == len(s) {
				e.long += len(s)
			}
			b = append(b, '"')
			e.refs = append(e.refs, strRef{at: len(b), s: s, esc: true})
			e.refBytes += n
			return append(b, '"')
		}
	}
	n := len(b)
	b = appendString(b, s, html)
	if len(b)-n == len(s)+2 {
		e.long += len(s)
	}
	return b
}

// plainTail reports whether s needs no escaping and is ASCII.
func plainTail(s string, html bool) bool {
	for i := 0; i < len(s); i++ {
		if !safeASCII(s[i], html) {
			return false
		}
	}
	return true
}

func safeASCII(c byte, html bool) bool {
	if html {
		return htmlSafe[c]
	}
	return plainSafe[c]
}

// plainSafe marks the bytes written as they are without HTML escaping;
// htmlSafe those written as they are with it.
var plainSafe, htmlSafe = func() (p, h [256]bool) {
	for c := 0x20; c < 0x80; c++ {
		p[c] = c != '"' && c != '\\'
		h[c] = p[c] && c != '<' && c != '>' && c != '&'
	}
	return
}()

// elements encodes n slice elements at data as a JSON array.
func (e *encodeState) elements(b []byte, pl *encPlan, data unsafe.Pointer, n int) []byte {
	el := pl.elem
	b = append(b, '[')
	if el.kind == eFloat64 && !el.methods {
		// Vectors of floats, as embeddings are, mostly of fractions with
		// a short decimal form.
		for i := 0; i < n; i++ {
			if i > 0 {
				b = append(b, ',')
			}
			f := *(*float64)(unsafe.Add(data, 8*i))
			if out, ok := appendShortDecimal(b, f); ok {
				b = out
				continue
			}
			b = e.float(b, f, 64, el.typ)
		}
		return append(b, ']')
	}
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = e.value(b, el, unsafe.Add(data, uintptr(i)*el.size), true)
	}
	return append(b, ']')
}

func (e *encodeState) arrayElements(b []byte, pl *encPlan, p unsafe.Pointer, addr bool) []byte {
	el := pl.elem
	b = append(b, '[')
	for i := 0; i < pl.alen; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = e.value(b, el, unsafe.Add(p, uintptr(i)*el.size), addr)
	}
	return append(b, ']')
}

// structValue encodes the struct at p. Each member is written with a
// leading comma, and the first comma becomes the opening brace.
func (e *encodeState) structValue(b []byte, pl *encPlan, p unsafe.Pointer, addr bool) []byte {
	e.enter(pl)
	start := len(b)
	if jsonv2 {
		e.open(start)
	}
	h := 0
	if e.escapeHTML {
		h = 1
	}
	fields := pl.st.fields
	for i := range fields {
		f := &fields[i]
		if f.op == opOther {
			b = e.structField(b, f, p, addr)
			continue
		}
		fp := unsafe.Add(p, f.offset)
		// The omitted fields first, by the word that tells them; the common
		// kinds are written here, where omitzero means omitempty for them,
		// except for RawMessage and slices.
		if k := f.omit; k != omitNone {
			switch {
			case k == omitByte:
				if *(*uint8)(fp) == 0 {
					continue
				}
			case k == omitWide:
				if *(*uint64)(fp) == 0 {
					continue
				}
			case *(*uintptr)(unsafe.Add(fp, f.omitOff)) == 0:
				continue
			}
		}
		key := f.key[h]
		if n := len(b); wordCopy && uint(len(key)) <= keyPad && uint(cap(b)-n) >= keyPad {
			// Three words from the padded key, past the end of b.
			d := unsafe.Add(unsafe.Pointer(unsafe.SliceData(b)), n)
			k := unsafe.Pointer(unsafe.StringData(key))
			*(*uint64)(d) = *(*uint64)(k)
			*(*uint64)(unsafe.Add(d, 8)) = *(*uint64)(unsafe.Add(k, 8))
			*(*uint64)(unsafe.Add(d, 16)) = *(*uint64)(unsafe.Add(k, 16))
			b = b[:n+len(key)]
		} else {
			b = appendKeyBytes(b, key)
		}
		switch f.op {
		case opString, opStringOmit:
			if s := *(*string)(fp); len(s) >= 64 {
				b = e.str(b, s)
			} else if out, ok := appendShortString(b, s, h != 0); ok {
				b = out
			} else {
				b = appendStringSlow(b, s, h != 0)
			}
		case opInt, opIntOmit:
			// Most int fields hold one digit.
			if v, k := *(*int)(fp), len(b); uint(v) < 10 && k < cap(b) {
				b = b[:k+1]
				b[k] = byte('0' + v)
			} else {
				b = appendIntField(b, int64(v))
			}
		case opInt64, opInt64Omit:
			b = appendIntField(b, *(*int64)(fp))
		case opBool, opBoolOmit:
			b = appendBool(b, *(*bool)(fp))
		case opPtrString, opPtrStringOmit:
			ptr := *(*unsafe.Pointer)(fp)
			if ptr == nil {
				b = append(b, "null"...)
				continue
			}
			e.enter(f.plan)
			if s := *(*string)(ptr); len(s) >= 64 {
				b = e.str(b, s)
			} else if out, ok := appendShortString(b, s, h != 0); ok {
				b = out
			} else {
				b = appendStringSlow(b, s, h != 0)
			}
			e.depth--
		case opRaw:
			b = e.raw(b, *(*[]byte)(fp), f.plan)
		case opStruct:
			b = e.structValue(b, f.plan, fp, addr)
		case opPtrStruct, opPtrStructOmit, opPtr, opPtrOmit:
			ptr := *(*unsafe.Pointer)(fp)
			if ptr == nil {
				b = append(b, "null"...)
				continue
			}
			b = e.pointer(b, f.plan, ptr, fp)
		case opSlice, opSliceOmit:
			b = e.slice(b, f.plan, fp)
		case opAny, opAnyOmit:
			x := *(*any)(fp)
			if x == nil {
				b = append(b, "null"...)
				continue
			}
			e.enter(f.plan)
			if s, ok := x.(string); !ok || len(s) >= 64 {
				b = e.anyValue(b, x)
			} else if out, ok := appendShortString(b, s, h != 0); ok {
				b = out
			} else {
				b = appendStringSlow(b, s, h != 0)
			}
			e.depth--
		default:
			b = e.value(b, f.plan, fp, addr)
		}
	}
	if inlineV2 && pl.st.inline != nil {
		b = e.inlineMembers(b, pl.st.inline, p)
	}
	e.depth--
	if len(b) == start {
		return append(b, "{}"...)
	}
	b[start] = '{'
	return append(b, '}')
}

// structField encodes field f of the struct at p, which has options or
// is reached through embedded pointers.
func (e *encodeState) structField(b []byte, f *encField, p unsafe.Pointer, addr bool) []byte {
	fp := f.readPtr(p)
	if fp == nil {
		return b
	}
	if f.path != nil {
		addr = true
	}
	if f.omitEmpty && isEmpty(f.plan, fp) {
		return b
	}
	if f.omitZero && e.isZero(f, fp, addr) {
		return b
	}
	h := 0
	if e.escapeHTML {
		h = 1
	}
	b = appendKeyBytes(b, f.key[h])
	if f.quoted {
		return e.quoted(b, f.plan, fp, addr)
	}
	return e.value(b, f.plan, fp, addr)
}

// appendInt appends n in decimal.
func appendInt(b []byte, n int64) []byte {
	if uint64(n) < 10 {
		return append(b, byte('0'+n))
	}
	u := uint64(n)
	if n < 0 {
		b = append(b, '-')
		u = -u
	}
	return appendUint(b, u)
}

// appendIntField is appendInt with one call for the values of up to eight
// digits that fields mostly hold, when b has room for them.
func appendIntField(b []byte, n int64) []byte {
	if uint64(n) < 1e8 && cap(b)-len(b) >= 8 {
		// The eight digits, less their leading zeros.
		d := digits8(uint64(n))
		z := bits.TrailingZeros64(d^asciiZeros|1<<56) >> 3
		storeWord(b, len(b), d>>(8*z))
		return b[:len(b)+8-z]
	}
	return appendInt(b, n)
}

// appendUint appends u in decimal, eight digits at a time.
func appendUint(b []byte, u uint64) []byte {
	if u < 10 {
		return append(b, byte('0'+u))
	}
	n := len(b)
	if cap(b)-n < 24 {
		b = growBytes(b, 24)
	}
	w := b[n : n+24]
	// The leading digits, without their leading zeros, then whole groups
	// of eight.
	var lead, mid, low uint64
	groups := 0
	switch {
	case u < 1e8:
		lead = u
	case u < 1e16:
		lead, low, groups = u/1e8, u%1e8, 1
	default:
		lead, mid, low, groups = u/1e16, u/1e8%1e8, u%1e8, 2
	}
	d := digits8(lead)
	z := bits.TrailingZeros64(d^asciiZeros) >> 3
	storeWord(w, 0, d>>(8*z))
	k := 8 - z
	if groups == 2 {
		storeWord(w, k, digits8(mid))
		k += 8
	}
	if groups > 0 {
		storeWord(w, k, digits8(low))
		k += 8
	}
	return b[:n+k]
}

// growBytes returns b with room for n more bytes. A new buffer is at least
// twice as large, and the bytes after len(b) are not zeroed.
func growBytes(b []byte, n int) []byte {
	if cap(b)-len(b) >= n {
		return b
	}
	nb := makeNoZero(max(2*cap(b), len(b)+n, 256))
	return nb[:copy(nb, b)]
}

// isEmpty is the omitempty test: false, 0, a nil pointer or interface, an
// empty array, map, slice or string.
func isEmpty(pl *encPlan, p unsafe.Pointer) bool {
	switch pl.kind {
	case eString, eNumber:
		return len(*(*string)(p)) == 0
	case eSlice, eBytes, eRaw:
		return (*sliceHeader)(p).len == 0
	case eMap:
		return len(*(*map[unsafe.Pointer]unsafe.Pointer)(p)) == 0
	case ePtr, eIface, eMarshalIface, eTextIface:
		return *(*unsafe.Pointer)(p) == nil
	case eBool:
		return !*(*bool)(p)
	case eArray:
		return pl.alen == 0
	case eInt, eUint, eUintptr:
		return *(*uint)(p) == 0
	case eInt8, eUint8:
		return *(*uint8)(p) == 0
	case eInt16, eUint16:
		return *(*uint16)(p) == 0
	case eInt32, eUint32:
		return *(*uint32)(p) == 0
	case eInt64, eUint64:
		return *(*uint64)(p) == 0
	case eFloat32:
		return *(*float32)(p) == 0
	case eFloat64:
		return *(*float64)(p) == 0
	case eBad:
		return pl.typ.Kind() == reflect.Map && len(*(*map[unsafe.Pointer]unsafe.Pointer)(p)) == 0
	}
	return false
}

// isZero is the omitzero test: the field's IsZero method when it has one,
// else whether the value is the zero value of its type.
func (e *encodeState) isZero(f *encField, p unsafe.Pointer, addr bool) bool {
	if f.isZero != nil {
		e.pin()
		return f.isZero(p, addr)
	}
	switch f.plan.kind {
	case eString, eNumber, eBool, eInt, eInt8, eInt16, eInt32, eInt64, eUint, eUint8, eUint16, eUint32, eUint64, eUintptr, eFloat32, eFloat64,
		ePtr, eIface, eMarshalIface, eTextIface:
		return isEmpty(f.plan, p)
	case eMap:
		return *(*unsafe.Pointer)(p) == nil
	case eSlice, eBytes, eRaw:
		return (*sliceHeader)(p).data == nil
	}
	return reflect.NewAt(f.plan.typ, p).Elem().IsZero()
}

// quoted encodes a field with the ",string" option: numbers and booleans
// inside quotes, strings encoded twice. Methods and other kinds ignore it.
func (e *encodeState) quoted(b []byte, pl *encPlan, p unsafe.Pointer, addr bool) []byte {
	levels := 0
	for {
		if pl.methods {
			recv := p
			if pl.kind == ePtr {
				recv = *(*unsafe.Pointer)(p)
			}
			if out, ok := e.methods(b, pl, recv, addr); ok {
				e.depth -= levels
				return out
			}
		}
		if pl.kind != ePtr {
			break
		}
		ptr := *(*unsafe.Pointer)(p)
		if ptr == nil {
			e.depth -= levels
			return append(b, "null"...)
		}
		e.enter(pl)
		levels++
		pl, p, addr = pl.elem, ptr, true
	}
	switch pl.kind {
	case eString:
		inner := appendString(nil, *(*string)(p), e.escapeHTML)
		b = appendString(b, bytesView(inner), false)
	case eNumber:
		b = e.number(b, *(*string)(p), true)
	case eBool, eInt, eInt8, eInt16, eInt32, eInt64, eUint, eUint8, eUint16, eUint32, eUint64, eUintptr, eFloat32, eFloat64:
		b = append(b, '"')
		b = e.value(b, pl, p, addr)
		b = append(b, '"')
	default:
		b = e.value(b, pl, p, addr)
	}
	e.depth -= levels
	return b
}

// mapEntry is one map member being sorted: its key and the index of its
// value.
type mapEntry struct {
	key string
	idx int
}

// anyEntry is one member of a map[string]any being sorted.
type anyEntry struct {
	key string
	val any
}

// mapValue encodes the non-nil map m with its keys sorted. Each kind of
// map has its own function, so that nested maps take little stack.
func (e *encodeState) mapValue(b []byte, pl *encPlan, mp unsafe.Pointer) []byte {
	switch pl.mapFast {
	case mapStringAny:
		return e.mapEntriesAny(b, *(*map[string]any)(unsafe.Pointer(&mp)))
	case mapStringString:
		return e.mapStringString(b, *(*map[string]string)(unsafe.Pointer(&mp)))
	case mapStringRaw:
		return e.mapStringRaw(b, pl, *(*map[string]RawMessage)(unsafe.Pointer(&mp)))
	}
	return e.mapReflect(b, pl, mp)
}

func (e *encodeState) mapStringString(b []byte, m map[string]string) []byte {
	base := len(e.keys)
	for k := range m {
		e.keys = append(e.keys, mapEntry{key: k})
	}
	entries := e.keys[base:]
	slices.SortFunc(entries, compareEntries)
	b = append(b, '{')
	for i := range entries {
		if i > 0 {
			b = append(b, ',')
		}
		k := entries[i].key
		b = appendString(b, k, e.escapeHTML)
		b = append(b, ':')
		b = e.str(b, m[k])
	}
	clear(e.keys[base:])
	e.keys = e.keys[:base]
	return append(b, '}')
}

func (e *encodeState) mapStringRaw(b []byte, pl *encPlan, m map[string]RawMessage) []byte {
	base := len(e.keys)
	for k := range m {
		e.keys = append(e.keys, mapEntry{key: k})
	}
	entries := e.keys[base:]
	slices.SortFunc(entries, compareEntries)
	b = append(b, '{')
	for i := range entries {
		if i > 0 {
			b = append(b, ',')
		}
		k := entries[i].key
		b = appendString(b, k, e.escapeHTML)
		b = append(b, ':')
		b = e.raw(b, m[k], pl.elem)
	}
	clear(e.keys[base:])
	e.keys = e.keys[:base]
	return append(b, '}')
}

// mapReflect encodes the other maps through reflect. The map is iterated
// in collectReflect, so that the iterator is not on the stack of every
// nesting level.
func (e *encodeState) mapReflect(b []byte, pl *encPlan, mp unsafe.Pointer) []byte {
	data, base, end := e.collectReflect(pl, mp, b)
	size := pl.elem.size
	b = append(b, '{')
	for i := base; i < end; i++ {
		if i > base {
			b = append(b, ',')
		}
		b = appendString(b, e.keys[i].key, e.escapeHTML)
		b = append(b, ':')
		b = e.value(b, pl.elem, unsafe.Add(data, uintptr(e.keys[i].idx)*size), false)
	}
	clear(e.keys[base:end])
	e.keys = e.keys[:base]
	return append(b, '}')
}

// collectReflect copies the values of the map mp, to be written after the
// output buffer b, into a new array and appends their keys to e.keys,
// sorted. It returns the array and where the keys are.
//
//go:noinline
func (e *encodeState) collectReflect(pl *encPlan, mp unsafe.Pointer, b []byte) (unsafe.Pointer, int, int) {
	mv := reflect.NewAt(pl.typ, unsafe.Pointer(&mp)).Elem()
	n := mv.Len()
	vals := reflect.MakeSlice(reflect.SliceOf(pl.typ.Elem()), n, n)
	kv := reflect.New(pl.typ.Key()).Elem()
	base := len(e.keys)
	it := mv.MapRange()
	for i := 0; it.Next(); i++ {
		kv.SetIterKey(it)
		ks, err := e.resolveKey(kv, pl, b)
		if err != nil {
			if jsonv2 {
				e.fail(newMarshalerError(methodErrType(kv.Type()), err, "MarshalText"))
			}
			e.fail(fmt.Errorf("json: encoding error for type %q: %q", pl.typ.String(), err.Error()))
		}
		vals.Index(i).SetIterValue(it)
		e.keys = append(e.keys, mapEntry{ks, i})
	}
	if jsonv2 && pl.keyText {
		slices.SortFunc(e.keys[base:], compareTextEntries)
	} else {
		slices.SortFunc(e.keys[base:], compareEntries)
	}
	return vals.UnsafePointer(), base, len(e.keys)
}

func compareEntries(a, b mapEntry) int { return strings.Compare(a.key, b.key) }

// compareTextEntries orders MarshalText keys as encoding/json/v2 does: by
// the text with each byte of invalid UTF-8 read as U+FFFD.
func compareTextEntries(a, b mapEntry) int {
	x, y := a.key, b.key
	for len(x) > 0 && len(y) > 0 {
		r, n := utf8.DecodeRuneInString(x)
		q, m := utf8.DecodeRuneInString(y)
		if r != q {
			return int(r - q)
		}
		x, y = x[n:], y[m:]
	}
	return len(x) - len(y)
}

// resolveKey returns the JSON name of map key k, of a map to be written
// after the output buffer b: the string, the MarshalText result, or the
// decimal integer.
func (e *encodeState) resolveKey(k reflect.Value, pl *encPlan, b []byte) (string, error) {
	if pl.keyKind == reflect.String {
		return k.String(), nil
	}
	if pl.keyText {
		if k.Kind() == reflect.Pointer && k.IsNil() {
			return "", nil
		}
		e.pin()
		b, err := k.Interface().(interface{ MarshalText() ([]byte, error) }).MarshalText()
		return string(b), err
	}
	switch pl.keyKind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(k.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(k.Uint(), 10), nil
	}
	return e.keyV2(k, b), nil
}

// mapEntriesAny writes the members of a non-nil map[string]any. The map
// is iterated in collectAny, so that the iterator is not on the stack of
// every nesting level.
func (e *encodeState) mapEntriesAny(b []byte, m map[string]any) []byte {
	if len(m) <= smallMap && e.depth <= smallMapDepth {
		return e.smallMapAny(b, m)
	}
	base, end := e.collectAny(m)
	b = append(b, '{')
	for i := base; i < end; i++ {
		if i > base {
			b = append(b, ',')
		}
		// Nested maps append after these entries and truncate back, so
		// the indexes stay valid.
		b = appendString(b, e.anyKeys[i].key, e.escapeHTML)
		b = append(b, ':')
		b = e.anyValue(b, e.anyKeys[i].val)
	}
	clear(e.anyKeys[base:end])
	e.anyKeys = e.anyKeys[:base]
	return append(b, '}')
}

// smallMap is the most members a map has for smallMapAny, and
// smallMapDepth the deepest it is, so that values nested up to
// maxEncodeDepth levels stay within the default stack limit.
const (
	smallMap      = 8
	smallMapDepth = 1 << 10
)

// smallMapAny is mapEntriesAny for a map of up to smallMap members, which
// it sorts on the stack.
func (e *encodeState) smallMapAny(b []byte, m map[string]any) []byte {
	var sorted [smallMap]anyEntry
	n := 0
	for key, v := range m {
		j := n
		for ; j > 0 && sorted[j-1].key > key; j-- {
			sorted[j] = sorted[j-1]
		}
		sorted[j] = anyEntry{key, v}
		n++
	}
	b = append(b, '{')
	for i := range n {
		if i > 0 {
			b = append(b, ',')
		}
		b = appendString(b, sorted[i].key, e.escapeHTML)
		b = append(b, ':')
		b = e.anyValue(b, sorted[i].val)
	}
	return append(b, '}')
}

// collectAny appends the members of m to e.anyKeys, sorted by key, and
// returns where they are.
//
//go:noinline
func (e *encodeState) collectAny(m map[string]any) (int, int) {
	base := len(e.anyKeys)
	for key, v := range m {
		e.anyKeys = append(e.anyKeys, anyEntry{key, v})
	}
	slices.SortFunc(e.anyKeys[base:], compareAnyEntries)
	return base, len(e.anyKeys)
}

func compareAnyEntries(a, b anyEntry) int { return strings.Compare(a.key, b.key) }

var sliceAnyType = reflect.TypeFor[[]any]()

// sliceAny encodes a []any.
func (e *encodeState) sliceAny(b []byte, a []any) []byte {
	if a == nil {
		return append(b, "null"...)
	}
	e.depth++
	if e.depth > maxEncodeDepth {
		e.tooDeep(anySliceType)
	}
	if jsonv2 {
		e.open(len(b))
	}
	e.ptrLevel++
	if e.ptrLevel > startDetectingCycles {
		// A copy of a, so that a itself is not moved to the heap.
		s := a
		e.seen(ptrKey{unsafe.Pointer(&sliceTag), unsafe.Pointer(unsafe.SliceData(a)), len(a)}, encPlanFor(rtypeOf(sliceAnyType)), unsafe.Pointer(&s))
	}
	b = append(b, '[')
	for i, v := range a {
		if i > 0 {
			b = append(b, ',')
		}
		b = e.anyValue(b, v)
	}
	b = append(b, ']')
	if e.ptrLevel > startDetectingCycles {
		e.forget(ptrKey{unsafe.Pointer(&sliceTag), unsafe.Pointer(unsafe.SliceData(a)), len(a)})
	}
	if jsonv2 {
		e.nest--
	}
	e.ptrLevel--
	e.depth--
	return b
}

// appendFloat appends f as encoding/json writes floats: like ECMAScript,
// in the shortest form that reads back as f, with an exponent below 1e-6
// and from 1e21 on.
func appendFloat(b []byte, f float64, bits int) []byte {
	abs := math.Abs(f)
	fmtc := byte('f')
	if abs != 0 {
		if bits == 64 && (abs < 1e-6 || abs >= 1e21) || bits == 32 && (float32(abs) < 1e-6 || float32(abs) >= 1e21) {
			fmtc = 'e'
		}
	}
	if fmtc == 'f' && bits == 64 {
		if abs < 1e15 && abs == math.Trunc(abs) && (f != 0 || !math.Signbit(f)) {
			return appendInt(b, int64(f))
		}
		if out, ok := appendShortDecimal(b, f); ok {
			return out
		}
	}
	b = strconv.AppendFloat(b, f, fmtc, -1, bits)
	if fmtc == 'e' {
		n := len(b)
		if n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b[n-2] = b[n-1]
			b = b[:n-1]
		}
	}
	return b
}

// asciiZeros is eight '0' bytes.
const asciiZeros = 0x3030303030303030

// digits8 returns the eight decimal digits of x < 1e8 as ASCII in one
// word, the first digit in the lowest byte.
func digits8(x uint64) uint64 {
	y := x/10000 | (x%10000)<<32
	h := (y * 5243) >> 19 & 0x0000007f0000007f
	y = h | (y-h*100)<<16
	t := (y * 103) >> 10 & 0x000f000f000f000f
	return t | (y-t*10)<<8 | asciiZeros
}

// appendShortDecimal appends f in plain notation when it is not an
// integer and its shortest form has at most fifteen significant digits
// and a magnitude from 1e-6 up to 1e15, and reports whether it did. The fifteen-digit decimal nearest f is taken from
// one multiplication and checked with one division, which rounds correctly
// because both operands are exact. No two decimals of up to fifteen digits
// read back as the same float64, so when this one reads back as f, it is
// the shortest form once its trailing zeros are gone.
func appendShortDecimal(dst []byte, f float64) ([]byte, bool) {
	a := math.Abs(f)
	if !(a >= 1e-6 && a < 1e15) {
		return dst, false
	}
	// k makes a*10^k an integer of fifteen digits. The estimate of the
	// decimal exponent from the binary one is exact or one too low.
	k := 14 - (int(math.Float64bits(a)>>52)-1023)*78913>>18
	m := a * pow10[k]
	if m >= 1e15 {
		k--
		m = a * pow10[k]
	}
	u := uint64(m + 0.5)
	if u-1e14 >= 9e14 || float64(u)/pow10[k] != a {
		return dst, false
	}
	hi, lo := digits8(u/1e8)>>8, digits8(u%1e8)
	sig := 0
	if x := lo ^ asciiZeros; x != 0 {
		sig = 15 - bits.LeadingZeros64(x)>>3
	} else {
		sig = 8 - bits.LeadingZeros64(hi^asciiZeros>>8)>>3
	}
	if sig <= 15-k {
		// An integer, which the digits after the point would end in.
		return dst, false
	}
	n := len(dst)
	if cap(dst)-n < 32 {
		dst = growBytes(dst, 32)
	}
	w := dst[n : n+32]
	w[0] = '-'
	at := int(math.Float64bits(f) >> 63)
	if k >= 15 {
		// Below 1: "0." and the zeros, then the digits.
		storeWord(w, at, 0x3030303030302e30)
		at += k - 13
		storeWord(w, at, hi)
		storeWord(w, at+7, lo)
		return dst[:n+at+sig], true
	}
	storeWord(w, at, hi)
	storeWord(w, at+7, lo)
	at += 15 - k
	copy(w[at+1:at+16-(15-k)], w[at:])
	w[at] = '.'
	return dst[:n+at+1+sig-(15-k)], true
}

// appendString appends s as a JSON string: '"', '\\' and control bytes
// escaped (\b, \f, \n, \r and \t in short form), with html also '<', '>'
// and '&'; U+2028 and U+2029 escaped; each byte that is not part of valid
// UTF-8 replaced by invalidUTF8.
func appendString(dst []byte, s string, html bool) []byte {
	if len(s) < 64 {
		if out, ok := appendShortString(dst, s, html); ok {
			return out
		}
	}
	return appendStringSlow(dst, s, html)
}

// appendStringSlow is appendString without the short ASCII case.
func appendStringSlow(dst []byte, s string, html bool) []byte {
	dst = append(dst, '"')
	i := 0
	kernel := jsonsimd.HaveAVX2
	for i < len(s) {
		if len(s)-i >= 64 && jsonsimd.HaveAVX2 {
			n, k := jsonsimd.QuoteCountBlocks(s, i, html)
			if n > i {
				need := len(dst) + (n - i) + k + 136
				if cap(dst) < need {
					dst = growBytes(dst, need-len(dst))
				}
				var w int
				i, w = jsonsimd.QuoteBlocks(dst[:cap(dst)], s, i, len(dst), html)
				dst = dst[:w]
			}
			if kernel && len(s)-i >= 64 {
				// A block with other bytes to escape or bytes of 0x80 and
				// above: when the rest is valid UTF-8 without line
				// separators, the kernel escapes all of it but a short
				// tail, into room for all of it, an eighth more and 256
				// bytes for the kernel's fast loop.
				kernel = false
				if jsonsimd.CheckUTF8(s[i:]) == 0 {
					for len(s)-i >= 32 {
						if n := len(s) - i; cap(dst)-len(dst) < n+256 {
							dst = growBytes(dst, n+n>>3+256)
						}
						var w int
						i, w = jsonsimd.EscapeBlocks(dst[:cap(dst)], s, i, len(dst), html)
						dst = dst[:w]
					}
				}
			}
		}
		// Plain ASCII eight bytes at a time, the tail with a load that
		// overlaps checked bytes.
		start := i
		for i+8 <= len(s) {
			w := load64(s, i)
			if needsEscape(w, html) {
				break
			}
			i += 8
		}
		if i+8 > len(s) && len(s) >= 8 && !needsEscape(load64(s, len(s)-8), html) {
			i = len(s)
		}
		for i < len(s) && safeASCII(s[i], html) {
			i++
		}
		dst = append(dst, s[start:i]...)
		if i >= len(s) {
			break
		}
		c := s[i]
		if c < utf8.RuneSelf {
			switch c {
			case '"', '\\':
				dst = append(dst, '\\', c)
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xF])
			}
			i++
			continue
		}
		// A run of non-ASCII: copy what is valid UTF-8.
		j := i
		for j < len(s) && s[j] >= utf8.RuneSelf {
			r, n := utf8.DecodeRuneInString(s[j:])
			if r == utf8.RuneError && n == 1 {
				dst = append(dst, s[i:j]...)
				dst = append(dst, invalidUTF8...)
				j++
				i = j
				continue
			}
			if r == '\u2028' || r == '\u2029' {
				dst = append(dst, s[i:j]...)
				dst = append(dst, '\\', 'u', '2', '0', '2', hexDigits[r&0xF])
				j += n
				i = j
				continue
			}
			j += n
		}
		dst = append(dst, s[i:j]...)
		i = j
	}
	return append(dst, '"')
}

// appendShortString appends the quoted string s, which is shorter than 64
// bytes, checking it as it is copied, and reports true; or returns dst
// with the same bytes and false when s holds a byte of 0x80 or above.
func appendShortString(dst []byte, s string, html bool) ([]byte, bool) {
	n := len(s)
	if cap(dst)-len(dst) < n+34 {
		dst = growBytes(dst, n+34)
	}
	at := len(dst) + 1
	out := dst[:cap(dst)]
	out[at-1] = '"'
	// As jsonsimd.CopyShort, without a call to it: out has room for 32
	// bytes from at, and the kernel reads 32 bytes from s, which must
	// then be in its page.
	if p := unsafe.StringData(s); jsonsimd.HaveAVX2 && n != 0 && (n >= 32 || uintptr(unsafe.Pointer(p))&4095 <= 4096-32) &&
		jsonsimd.CopyShortAVX2(&out[at], p, n, html) {
		out[at+n] = '"'
		return out[:at+n+1], true
	}
	i := 0
	for ; i+8 <= n; i += 8 {
		w := load64(s, i)
		if needsEscape(w, html) {
			return appendEscaped(dst, s, i, html)
		}
		storeWord(out, at+i, w)
	}
	if i < n {
		if n >= 8 {
			w := load64(s, n-8)
			if needsEscape(w, html) {
				return appendEscaped(dst, s, i, html)
			}
			storeWord(out, at+n-8, w)
		} else {
			for k := i; k < n; k++ {
				c := s[k]
				if !safeASCII(c, html) {
					return appendEscaped(dst, s, i, html)
				}
				out[at+k] = c
			}
		}
	}
	out[at+n] = '"'
	return out[:at+n+1], true
}

// appendEscaped finishes appendShortString from s[i], with s[:i] already
// written after the opening quote, for a string that needs escaping.
func appendEscaped(dst []byte, s string, i int, html bool) ([]byte, bool) {
	n := len(s)
	at := len(dst) + 1
	if cap(dst)-len(dst) < 6*n+2 {
		// Room for every byte escaped as \u00XX.
		nd := growBytes(dst, 6*n+2)
		copy(nd[len(nd):cap(nd)], dst[len(dst):len(dst)+1+i])
		dst = nd
	}
	out := dst[:cap(dst)]
	w := at + i
	for ; i < n; i++ {
		c := s[i]
		if safeASCII(c, html) {
			out[w] = c
			w++
			continue
		}
		switch c {
		case '"', '\\':
			out[w], out[w+1] = '\\', c
			w += 2
		case '\n':
			out[w], out[w+1] = '\\', 'n'
			w += 2
		case '\r':
			out[w], out[w+1] = '\\', 'r'
			w += 2
		case '\t':
			out[w], out[w+1] = '\\', 't'
			w += 2
		case '\b':
			out[w], out[w+1] = '\\', 'b'
			w += 2
		case '\f':
			out[w], out[w+1] = '\\', 'f'
			w += 2
		default:
			if c >= utf8.RuneSelf {
				return dst, false
			}
			out[w], out[w+1], out[w+2], out[w+3], out[w+4], out[w+5] = '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xF]
			w += 6
		}
	}
	out[w] = '"'
	return out[:w+1], true
}

// needsEscape reports whether the eight bytes of w hold one that is not
// written as it is: a control byte, '"', '\\', a byte of 0x80 or above, or
// with html '<', '>' or '&'.
func needsEscape(w uint64, html bool) bool {
	// Bytes below 0x20 or from 0x80 on, and the quote and the backslash;
	// with html also '&', '<' and '>'. Setting bit 2 maps '"' and '&' to
	// '&' only, and setting bit 1 maps '<' and '>' to '>' only.
	m := (w - 0x20*lsb) | w
	if html {
		a := (w | 0x04*lsb) ^ ('&' * lsb)
		g := (w | 0x02*lsb) ^ ('>' * lsb)
		m |= (a-lsb)&^a | (g-lsb)&^g
	} else {
		q := w ^ ('"' * lsb)
		m |= (q - lsb) &^ q
	}
	b := w ^ ('\\' * lsb)
	m |= (b - lsb) &^ b
	return m&msb != 0
}
