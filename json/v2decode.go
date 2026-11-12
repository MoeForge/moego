package json

import (
	stdbase64 "encoding/base64"
	"errors"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/MoeForge/moego/base64"
)

// The decoding rules of encoding/json/v2 that differ from those of the
// classic encoding/json and take more than an offset. They run only when
// jsonv2 is set, and only for values that are not the common case.

var (
	errNotString   = errors.New("JSON value must be string type")
	errEmbeddedPtr = errors.New("cannot set embedded pointer to unexported struct type")
	timeType       = reflect.TypeFor[time.Time]()
	timePtrType    = reflect.TypeFor[*time.Time]()
	timePtrRtype   = rtypeOf(timePtrType)
)

// kindName is the Value of an UnmarshalTypeError for the value starting
// with c.
func kindName(c byte) string {
	switch c {
	case '{':
		return "object"
	case '[':
		return "array"
	}
	return literalName(c)
}

// kindAt returns the kind of the value at d.i and the index of a type
// error for it: after the bracket of an object or array, after the end of
// a literal. The error is kept before skipValue moves past the value,
// which can move the window of a large value.
func (d *decodeState) kindAt() (name string, off int) {
	c := d.s[d.i]
	if c == '{' || c == '[' {
		return kindName(c), d.i + 1
	}
	return literalName(c), d.literalEnd()
}

// skipValue moves past the value at d.i, for which kindAt returned off.
func (d *decodeState) skipValue(off int) {
	if c := d.s[d.i]; c == '{' || c == '[' {
		d.skip()
		return
	}
	d.i = off
}

// typeErrorErr is typeErrorV2 with the error that caused it.
func (d *decodeState) typeErrorErr(value string, t reflect.Type, off, target int, err error) {
	if d.saved == nil {
		e := d.v2TypeError(value, t, off, target)
		setTypeErrorErr(e, err)
		d.saved = e
	}
}

// kindErrorV2 keeps the type error for the value at d.i, which t can not
// hold, and moves past it.
func (d *decodeState) kindErrorV2(t reflect.Type, err error) {
	start := d.i
	name, off := d.kindAt()
	d.typeErrorErr(name, t, off, start, err)
	d.skipValue(off)
}

// ifaceErrorV2 keeps the type error for the value at d.i, which the
// non-empty interface at p of plan pl can not hold, at the start of the
// value, clears the interface and moves past the value.
func (d *decodeState) ifaceErrorV2(pl *decPlan, p unsafe.Pointer) {
	start := d.i
	name, off := d.kindAt()
	d.typeErrorV2(name, pl.typ, start, start)
	*(*iface)(p) = iface{}
	d.skipValue(off)
}

// numberError keeps the error for a value that a Number can not hold. It
// has no offset and no place.
func (d *decodeState) numberError(value string, err error) {
	if d.saved == nil {
		e := &UnmarshalTypeError{Value: value, Type: numberType}
		setTypeErrorErr(e, err)
		d.saved = e
	}
}

// methodError keeps the error that an UnmarshalJSON or UnmarshalText
// method returned, after which decoding goes on.
func (d *decodeState) methodError(err error) {
	if d.saved == nil {
		d.saved = err
	}
}

// isTimeTab reports whether the itab tab is that of *time.Time, which
// takes only a JSON string.
func isTimeTab(tab unsafe.Pointer) bool {
	return *(*unsafe.Pointer)(unsafe.Add(tab, unsafe.Sizeof(uintptr(0)))) == timePtrRtype
}

// overflowAny is the value that encoding/json/v2 stores in an interface for
// a number, starting with c, too large for a float64: 0 from Go 1.27, the
// largest float64 of its sign before.
func overflowAny(c byte) float64 {
	if go127 {
		return 0
	}
	return largestFloat(c, 64)
}

// largestFloat is the largest float of the size bits and of the sign of the
// number starting with c, which encoding/json/v2 before Go 1.27 stores for
// a number too large for the float.
func largestFloat(c byte, bits int) float64 {
	f := math.MaxFloat64
	if bits == 32 {
		f = math.MaxFloat32
	}
	if c == '-' {
		return -f
	}
	return f
}

// base64ValueV2 decodes the base64 text b of the JSON string at start into
// the byte slice at p as encoding/json/v2 does. It appends to the slice cut
// to length zero: into its array when that has room, else into a new array
// of the capacity append gives. An error leaves the slice as it was, and
// its array written as encoding/base64 writes it.
func (d *decodeState) base64ValueV2(pl *decPlan, p unsafe.Pointer, b []byte, start int) {
	s := (*[]byte)(p)
	n := len(b)
	for n > 0 && b[n-1] == '=' {
		n--
	}
	size := n/4*3 + n%4*6/8
	if cap(*s) >= size {
		if _, err := base64.StdEncoding.ValidateString(bytesView(b)); err != nil {
			_, err = stdbase64.StdEncoding.Decode((*s)[:size], b)
			d.typeErrorErr("string", pl.typ, d.i, start, err)
			return
		}
	}
	out := slices.Grow((*s)[:0], size)
	m, err := base64.StdEncoding.Decode(out[:size], b)
	if err != nil {
		d.typeErrorErr("string", pl.typ, d.i, start, err)
		return
	}
	if out == nil {
		out = []byte{}
	}
	*s = out[:m]
}

// quotedValueV2 decodes into p of plan pl, of field type ft with the
// ",string" option. The string holds the literal: a number for numeric
// kinds, true or false, or a JSON string for a string. A value that is not
// a string, or does not hold such a literal, is a type error of the kind
// that pointers lead to, whose pointers are set.
func (d *decodeState) quotedValueV2(pl *decPlan, p unsafe.Pointer, ft reflect.Type) {
	c := d.s[d.i]
	if callsMethod(pl) {
		// The option is for the kinds that have no method.
		d.value(pl, p)
		return
	}
	if c == 'n' {
		d.quotedValue(pl, p, ft)
		return
	}
	base := pl
	for base.kind == dPtr {
		base = base.elem
	}
	start := d.i
	var item []byte
	if c == '"' {
		end := d.literalEnd()
		item = d.stringBytes()
		// Before Go 1.27 a string field takes "null" as a string that
		// does not hold a JSON string.
		if string(item) == "null" && base.kind != dNumber && (go127 || base.kind != dString) {
			d.storeQuoted(item, pl, p, ft, end)
			return
		}
	}
	for pl.kind == dPtr {
		ptr := *(*unsafe.Pointer)(p)
		if ptr == nil {
			ptr = d.newElem(pl)
			*(*unsafe.Pointer)(p) = ptr
		}
		pl, p = pl.elem, ptr
	}
	t := pl.typ
	if c != '"' {
		name, off := d.kindAt()
		if pl.kind == dNumber {
			d.numberError(name, nil)
		} else {
			d.typeErrorV2(name, t, off, start)
		}
		d.skipValue(off)
		return
	}
	end := d.i
	switch pl.kind {
	case dString:
		if msg := quotedStringError(item); msg != "" {
			d.typeErrorErr("string", t, end, start, newSyntaxError(msg, 0))
			return
		}
		s := string(item)
		v := string(unquote(nil, s[1:len(s)-1]))
		if !go127 && v == "null" {
			// Before Go 1.27 a quoted null leaves the string as it is.
			return
		}
		*(*string)(p) = v
	case dNumber:
		if !isValidNumber(item) {
			d.numberError("string "+string(d.tokenBytes(start, end)), strconv.ErrSyntax)
			return
		}
		*(*string)(p) = string(item)
	case dBool:
		switch string(item) {
		case "true", "false":
			*(*bool)(p) = item[0] == 't'
		default:
			d.typeErrorErr("string "+string(d.tokenBytes(start, end)), t, end, start, strconv.ErrSyntax)
		}
	case dInt, dInt8, dInt16, dInt32, dInt64:
		n, err := strconv.ParseInt(string(item), 10, 64)
		if err != nil || overflowInt(pl.kind, n) {
			d.typeErrorV2("number "+string(item), t, end, start)
			return
		}
		storeInt(pl.kind, p, n)
	case dUint, dUint8, dUint16, dUint32, dUint64, dUintptr:
		n, err := strconv.ParseUint(string(item), 10, 64)
		if err != nil || overflowUint(pl.kind, n) {
			d.typeErrorV2("number "+string(item), t, end, start)
			return
		}
		storeUint(pl.kind, p, n)
	case dFloat32, dFloat64:
		bits := 64
		if pl.kind == dFloat32 {
			bits = 32
		}
		f, err := strconv.ParseFloat(string(item), bits)
		if err != nil || overflowFloat(pl.kind, f) {
			d.typeErrorV2("number "+string(item), t, end, start)
			return
		}
		if bits == 32 {
			*(*float32)(p) = float32(f)
		} else {
			*(*float64)(p) = f
		}
	default:
		d.storeQuoted(item, pl, p, ft, end)
	}
}

// quotedStringError returns the message of the syntax error in u, the
// content of a quoted string field, which must be one JSON string and
// nothing else, or "" when it is.
func quotedStringError(u []byte) string {
	if len(u) == 0 {
		return "unexpected end of JSON input"
	}
	if u[0] != '"' {
		q, _ := quoteCharV2(u)
		return "invalid character " + q + " looking for beginning of object key string"
	}
	if Valid(u) {
		if u[len(u)-1] == '"' {
			return ""
		}
		// Whitespace follows the string.
		i := len(u) - 1
		for isSpace[u[i-1]] {
			i--
		}
		return "invalid character " + quoteChar(u[i]) + " after string value"
	}
	msg, _ := firstSyntaxError(u, synUnmarshal)
	const top = "after top-level value"
	if len(msg) > len(top) && msg[len(msg)-len(top):] == top {
		msg = msg[:len(msg)-len(top)] + "after string value"
	}
	return msg
}

// inlineMember decodes the member value at d.i, whose name is not that of
// a field, into the inline map or raw value of the struct at p of type st,
// as encoding/json/v2 does before Go 1.27. name is the member name
// unescaped and raw the name as the input has it. A map entry that exists
// is decoded into; a raw value gets the member appended to its object.
func (d *decodeState) inlineMember(sd *structDec, p unsafe.Pointer, st reflect.Type, name, raw string) {
	f := sd.inline
	var fp unsafe.Pointer
	if f.path == nil {
		fp = unsafe.Add(p, f.offset)
	} else if fp = d.embedded(f, p, st); fp == nil {
		d.skip()
		return
	}
	if f.typ.Kind() != reflect.Map {
		v := d.rawValue()
		x := trimSpaceRight(*(*[]byte)(fp))
		if len(x) > 0 && x[len(x)-1] == '}' {
			x = trimSpaceRight(x[:len(x)-1])
		}
		if len(x) == 0 {
			x = append(x, '{')
		} else if c := x[len(x)-1]; c != '{' && c != ',' {
			x = append(x, ',')
		}
		x = append(append(append(x, raw...), ':'), v...)
		*(*[]byte)(fp) = append(x, '}')
		return
	}
	m := reflect.NewAt(f.typ, fp).Elem()
	if m.IsNil() {
		m.Set(reflect.MakeMap(f.typ))
	}
	k := reflect.ValueOf(strings.Clone(name)).Convert(f.typ.Key())
	v := reflect.New(f.typ.Elem())
	if old := m.MapIndex(k); old.IsValid() {
		v.Elem().Set(old)
	}
	d.mapDepth++
	d.value(sd.inlinePlan, v.UnsafePointer())
	d.mapDepth--
	m.SetMapIndex(k, v.Elem())
}

func trimSpaceRight(b []byte) []byte {
	for len(b) > 0 && isSpaceByte(b[len(b)-1]) {
		b = b[:len(b)-1]
	}
	return b
}
