package json

import (
	"bytes"
	"encoding"
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// The encoding rules of encoding/json/v2 that differ from those of the
// classic encoding/json, beyond the encoding of invalid UTF-8.

// methodErrType is the type a MarshalerError names for a method of type t.
// encoding/json/v2 names the pointer type, through which it calls methods.
func methodErrType(t reflect.Type) reflect.Type {
	if jsonv2 && t.Kind() != reflect.Pointer {
		return reflect.PointerTo(t)
	}
	return t
}

// ifaceMethodV2 encodes x, the value of an interface of plan pl that has a
// MarshalJSON or MarshalText method, as encoding/json/v2 does: as the value
// it holds, whose type a MarshalerError names, except that the method of
// the interface is called on a nil pointer. The error then names a pointer
// to a struct that embeds the interface.
func (e *encodeState) ifaceMethodV2(b []byte, pl *encPlan, x any) []byte {
	if reflect.TypeOf(x).Kind() != reflect.Pointer || (*eface)(unsafe.Pointer(&x)).data != nil {
		e.enter(pl)
		b = e.anyValue(b, x)
		e.depth--
		return b
	}
	t := embedderOf(pl.typ)
	if pl.kind == eMarshalIface {
		return e.callMarshaler(b, x.(Marshaler), t)
	}
	return e.callText(b, x.(interface{ MarshalText() ([]byte, error) }), t)
}

// embedderOf returns a pointer to a struct that embeds the named interface
// type t, or t when there is none.
func embedderOf(t reflect.Type) (p reflect.Type) {
	if t.Name() == "" {
		return t
	}
	defer func() {
		if recover() != nil {
			p = t
		}
	}()
	f := reflect.StructField{Name: t.Name(), Type: t, Anonymous: true}
	if r, _ := utf8.DecodeRuneInString(f.Name); !unicode.IsUpper(r) {
		f.PkgPath = t.PkgPath()
	}
	return reflect.PointerTo(reflect.StructOf([]reflect.StructField{f}))
}

// v2KeyKind reports whether encoding/json/v2 encodes maps with keys of
// kind k, which the classic encoding/json does not: the keys are checked
// as they are written.
func v2KeyKind(k reflect.Kind) bool {
	switch k {
	case reflect.Float32, reflect.Float64, reflect.Pointer, reflect.Interface,
		reflect.Bool, reflect.Array, reflect.Struct, reflect.Complex64, reflect.Complex128:
		return true
	}
	return false
}

// keyV2 returns the member name of map key k, which is not a string or an
// integer, on encoding/json/v2: the number for a float, the name of the
// value a pointer or interface holds. The map is written after the output
// buffer b.
func (e *encodeState) keyV2(k reflect.Value, b []byte) string {
	for {
		switch k.Kind() {
		case reflect.String:
			return k.String()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return strconv.FormatInt(k.Int(), 10)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			return strconv.FormatUint(k.Uint(), 10)
		case reflect.Float32, reflect.Float64:
			bits := k.Type().Bits()
			f := k.Float()
			if math.IsInf(f, 0) || math.IsNaN(f) {
				e.float(nil, f, bits, k.Type())
			}
			return string(appendFloat(nil, f, bits))
		case reflect.Complex64, reflect.Complex128:
			e.fail(&UnsupportedTypeError{Type: k.Type()})
		case reflect.Pointer, reflect.Interface:
			if !k.IsNil() {
				k = k.Elem()
				if m, ok := k.Interface().(encoding.TextMarshaler); ok && k.Kind() != reflect.String {
					e.pin()
					b, err := m.MarshalText()
					if err != nil {
						e.fail(newMarshalerError(methodErrType(k.Type()), err, "MarshalText"))
					}
					return string(b)
				}
				continue
			}
		}
		msg := "jsontext: object member name must be a string"
		if p := formatPath(first(pathTo(&pathInput{s: bytesView(b)}, int64(len(b)))), '/', true); p != "" {
			msg += " within " + strconv.Quote(p)
		}
		off := e.outLen(len(b)) + 1
		if go127 {
			off++
		}
		e.fail(&UnsupportedValueError{Str: msg + " after offset " + strconv.Itoa(off)})
	}
}

// hasLineSep reports whether src holds U+2028 or U+2029.
func hasLineSep(src []byte) bool {
	for i := 0; ; i++ {
		k := bytes.IndexByte(src[i:], 0xE2)
		if k < 0 || i+k+2 >= len(src) {
			return false
		}
		i += k
		if src[i+1] == 0x80 && src[i+2]&^1 == 0xA8 {
			return true
		}
	}
}

// escapeLineSeps escapes U+2028 and U+2029 in b[from:], valid JSON, where
// they can only be in strings: encoding/json/v2 escapes them in the JSON it
// writes, with or without HTML escaping.
func escapeLineSeps(b []byte, from int) []byte {
	tail := append([]byte(nil), b[from:]...)
	b = b[:from]
	start := 0
	for i := 0; i+2 < len(tail); i++ {
		if tail[i] == 0xE2 && tail[i+1] == 0x80 && tail[i+2]&^1 == 0xA8 {
			b = append(b, tail[start:i]...)
			b = append(b, `\u202`...)
			b = append(b, '8'+tail[i+2]-0xA8)
			start = i + 3
			i += 2
		}
	}
	return append(b, tail[start:]...)
}

func first[T, U any](t T, _ U) T { return t }

// inlineMembers writes, after the fields of the struct at p, the members
// of its inline map or raw value f, as encoding/json/v2 does before Go
// 1.27: the map entries sorted by key, or the members of the raw object.
func (e *encodeState) inlineMembers(b []byte, f *encField, p unsafe.Pointer) []byte {
	fp := f.readPtr(p)
	if fp == nil {
		return b
	}
	if f.typ.Kind() != reflect.Map {
		return e.inlineRaw(b, *(*[]byte)(fp))
	}
	mp := *(*unsafe.Pointer)(fp)
	if mp == nil {
		return b
	}
	pl := f.plan
	data, base, end := e.collectReflect(pl, mp, b)
	for i := base; i < end; i++ {
		b = append(b, ',')
		b = appendString(b, e.keys[i].key, e.escapeHTML)
		b = append(b, ':')
		b = e.value(b, pl.elem, unsafe.Add(data, uintptr(e.keys[i].idx)*pl.elem.size), false)
	}
	clear(e.keys[base:end])
	e.keys = e.keys[:base]
	return b
}

// inlineRaw writes the members of the inline raw value v, which must be
// empty or a JSON object.
func (e *encodeState) inlineRaw(b, v []byte) []byte {
	if len(v) == 0 {
		return b
	}
	fail := func(msg string) {
		e.fail(&UnsupportedValueError{Str: msg})
	}
	k := 0
	for k < len(v) && isSpaceByte(v[k]) {
		k++
	}
	switch {
	case k == len(v):
		fail("unexpected EOF")
	case v[k] != '{' && startsValue(v[k]):
		fail("inlined raw value must be a JSON object")
	case v[k] != '{':
		q, _ := quoteCharV2(v[k:])
		fail("jsontext: invalid character " + q + " at start of value")
	}
	if ok, _, _ := jsonsimd.ValidateSpaced(bytesView(v), maxNestingDepth, false); !ok {
		fail(inlineRawError(v))
	}
	c := appendCompactValid(nil, v, e.escapeHTML)
	if !e.escapeHTML && hasLineSep(c) {
		c = escapeLineSeps(c, 0)
	}
	if len(c) == 2 {
		return b
	}
	return append(append(b, ','), c[1:len(c)-1]...)
}

// inlineRawError is the message for the inline raw value v, an object that
// is not valid JSON, as encoding/json/v2 gives it before Go 1.27: what is
// wrong in the words of encoding/json/jsontext, the JSON Pointer of the
// member it is in, and its offset.
func inlineRawError(v []byte) string {
	var sc scanner
	sc.reset()
	var path []pathLevel
	key := -1   // the start of the member name being read
	comma := -1 // the last comma
	msg, at := "unexpected EOF", len(v)
	for i, c := range v {
		before := sc.state
		r := sc.step(c)
		if sc.state == stError {
			msg, at = inlineRawMessage(v[i:], errState(sc.errState), sc.stack), i
			switch {
			case c == '}' && sc.errState == stBeginString:
				// A comma before the end of an object is wrong itself.
				msg, at = "invalid character ',' at start of value", comma
			case c == ']' && sc.errState == stBeginValue && sc.stack[len(sc.stack)-1] == inArr:
				msg = "invalid character ']' after object value (expecting ',' or '}')"
			}
			break
		}
		switch r {
		case scanBeginObject:
			path = append(path, pathLevel{obj: true, wantKey: true})
		case scanBeginArray:
			path = append(path, pathLevel{index: -1})
		case scanEndObject, scanEndArray:
			path = path[:len(path)-1]
		case scanObjectValue:
			path[len(path)-1].wantKey = true
			comma = i
		case scanArrayValue:
			path[len(path)-1].index = -1
			comma = i
		case scanBeginLiteral:
			if before == stBeginStringOrEmpty || before == stBeginString {
				key = i
			} else if l := &path; len(*l) > 0 && !(*l)[len(*l)-1].obj {
				(*l)[len(*l)-1].index = arrayIndex(v[:i])
			}
		}
		if key >= 0 && before == stInString && sc.state == stEndValue {
			l := &path[len(path)-1]
			l.key, l.wantKey = string(unquote(nil, string(v[key+1:i]))), false
			key = -1
		}
		if c == '[' || c == '{' {
			if n := len(path); n > 1 && !path[n-2].obj && path[n-2].index < 0 {
				path[n-2].index = arrayIndex(v[:i])
			}
		}
	}
	// The member or element the error is in: none of the innermost
	// container after a member or element.
	st := errState(sc.errState)
	if at == len(v) {
		st = sc.state
	}
	after := st == stEndValue && len(sc.stack) > 0 && sc.stack[len(sc.stack)-1] != inObjKey
	var in []pathLevel
	for k, l := range path {
		last := k == len(path)-1
		if l.obj && (l.wantKey || last && after) || !l.obj && (l.index < 0 || last && after) {
			break
		}
		in = append(in, l)
	}
	if st != stEndTop {
		if p := formatPath(in, '/', true); p != "" {
			msg += " within " + strconv.Quote(p)
		}
	}
	return "jsontext: " + msg + " after offset " + strconv.Itoa(at)
}

// arrayIndex returns the index of the element that starts at the end of
// b in the innermost array open there.
func arrayIndex(b []byte) int {
	var sc scanner
	sc.reset()
	var counts []int
	for _, c := range b {
		switch sc.step(c) {
		case scanBeginArray:
			counts = append(counts, 0)
		case scanBeginObject:
			counts = append(counts, -1)
		case scanEndArray, scanEndObject:
			counts = counts[:len(counts)-1]
		case scanArrayValue:
			counts[len(counts)-1]++
		}
	}
	return counts[len(counts)-1]
}

// inlineRawMessage words the syntax error at the start of b, met in scanner
// state st with the containers open, as encoding/json/jsontext does.
func inlineRawMessage(b []byte, st uint8, stack []uint8) string {
	q, _ := quoteCharV2(b)
	c := "invalid character " + q
	switch st {
	case stBeginString, stBeginStringOrEmpty, stBeginValue, stBeginValueOrEmpty:
		return c + " at start of value"
	case stEndTop:
		return c + " after top-level value"
	case stEndValue:
		switch stack[len(stack)-1] {
		case inObjKey:
			return c + " after object name (expecting ':')"
		case inObjVal:
			return c + " after object value (expecting ',' or '}')"
		}
		return c + " after array value (expecting ',' or ']')"
	case stInString:
		return c + " in string (expecting non-control character)"
	case stNeg, stDot, stE, stESign:
		return c + " in number (expecting digit)"
	case stT, stTr, stTru, stF, stFa, stFal, stFals, stN, stNu, stNul:
		var sc scanner
		sc.state = st
		sc.step(b[0])
		return c + " " + strings.TrimPrefix(sc.err, "invalid character "+quoteChar(b[0])+" ")
	}
	return c
}

// errState is the scanner state st of an error, with the states inside a
// number that a byte ends taken as after the value.
func errState(st uint8) uint8 {
	switch st {
	case st0, st1, stDot0, stE0:
		return stEndValue
	}
	return st
}
