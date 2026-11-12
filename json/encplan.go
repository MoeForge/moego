package json

import (
	"encoding"
	"reflect"
	"unsafe"
)

// Field ops: the kinds of fields that structValue writes itself, without
// options other than omitempty, or omitzero with no IsZero method; the
// ...Omit ops are those with one of the options, which means omitempty for
// them, except for RawMessage. opValue fields of a struct reached directly
// go through value; opOther fields, with options or behind embedded
// pointers, through structField.
const (
	opValue uint8 = iota
	opOther
	opString
	opStringOmit
	opInt
	opIntOmit
	opInt64
	opInt64Omit
	opBool
	opBoolOmit
	opPtrString // a pointer to a string
	opPtrStringOmit
	opRaw
	opStruct    // a struct without methods
	opPtrStruct // a pointer to one, without methods itself
	opPtrStructOmit
	opPtr // another pointer without methods
	opPtrOmit
	opSlice // a slice without methods, other than []byte
	opSliceOmit
	opAny // an empty interface
	opAnyOmit
)

// fieldOp returns the op of field f, whose plans are complete.
func fieldOp(f *encField) uint8 {
	pl := f.plan
	if f.path != nil {
		return opOther
	}
	plain := opValue
	if f.omitEmpty || f.omitZero || f.quoted {
		plain = opOther
	}
	if f.quoted || f.isZero != nil || pl.methods {
		return plain
	}
	omit := f.omitEmpty || f.omitZero
	pick := func(op uint8) uint8 {
		if omit {
			return op + 1
		}
		return op
	}
	switch pl.kind {
	case eString:
		return pick(opString)
	case eInt:
		return pick(opInt)
	case eInt64:
		return pick(opInt64)
	case eBool:
		return pick(opBool)
	case eRaw:
		return opRaw
	case eStruct:
		if !omit {
			return opStruct
		}
	case ePtr:
		if el := pl.elem; el.kind == eString && !el.methods {
			return pick(opPtrString)
		}
		if el := pl.elem; el.kind == eStruct && !el.methods {
			return pick(opPtrStruct)
		}
		return pick(opPtr)
	case eSlice:
		return pick(opSlice)
	case eIface:
		if pl.typ.NumMethod() == 0 {
			return pick(opAny)
		}
	}
	return plain
}

// How structValue finds a field omitted, as encField.omit tells.
const (
	omitNone = iota
	omitByte
	omitWide
	omitWord
)

// fieldOmit returns the omit and omitOff of a field of op f.op.
func fieldOmit(f *encField) (uint8, uintptr) {
	const word = unsafe.Sizeof(uintptr(0))
	switch f.op {
	case opStringOmit:
		return omitWord, word // the length
	case opIntOmit, opPtrStringOmit, opPtrStructOmit, opPtrOmit, opAnyOmit:
		return omitWord, 0 // the int, the pointer or the type word
	case opInt64Omit:
		return omitWide, 0
	case opBoolOmit:
		return omitByte, 0
	case opRaw, opSliceOmit:
		switch {
		case f.omitEmpty:
			return omitWord, word // the length
		case f.omitZero:
			return omitWord, 0 // the pointer
		}
	}
	return omitNone, 0
}

// Encode plan kinds.
const (
	eBool uint8 = iota
	eInt
	eInt8
	eInt16
	eInt32
	eInt64
	eUint
	eUint8
	eUint16
	eUint32
	eUint64
	eUintptr
	eFloat32
	eFloat64
	eString
	eNumber
	eBytes // base64
	eRaw   // RawMessage
	eIface
	ePtr
	eSlice
	eArray
	eMap
	eStruct
	eMarshalIface // an interface type that implements Marshaler
	eTextIface    // an interface type that implements encoding.TextMarshaler
	eBad          // chan, func, complex, unsafe.Pointer
)

// An encPlan says how to encode one Go type.
type encPlan struct {
	kind uint8
	// direct means an interface holds values of the type in its data word.
	direct bool
	typ    reflect.Type
	rtyp   unsafe.Pointer
	size   uintptr
	elem   *encPlan
	alen   int
	st     *structEnc

	// Methods, as itabs of the pointer to the type: ptrMarshal and ptrText
	// when only the pointer implements the interface, so they are used
	// for addressable values only; valMarshal and valText when the type
	// itself does. ptrShaped means the type is a pointer, whose value is
	// the receiver.
	ptrMarshal, valMarshal unsafe.Pointer
	ptrText, valText       unsafe.Pointer
	methods                bool

	// Maps.
	keyKind reflect.Kind
	keyText bool // keys are resolved with MarshalText
	mapFast uint8
}

// structEnc is the encode side of a struct plan.
type structEnc struct {
	fields []encField
	inline *encField // with inlineV2, the map or raw value of other members
}

type encField struct {
	// The hot fields of the field's description, copied here.
	offset uintptr
	plan   *encPlan
	op     uint8 // how structValue writes the field
	// omit tells how structValue finds a field of an ...Omit op, or a
	// RawMessage field with an option, omitted: when the byte at the field
	// is 0, the uint64 at the field is 0, or the uintptr omitOff bytes into
	// the field is 0.
	omit      uint8
	omitOff   uintptr
	omitEmpty bool
	omitZero  bool
	quoted    bool
	path      []embedStep
	// key holds `,"name":` without HTML escaping and with it, each
	// followed in memory by keyPad bytes of padding.
	key [2]string
	// isZero, when the field type has an IsZero method, reports whether
	// the field value is zero for omitzero.
	isZero func(p unsafe.Pointer, addr bool) bool
	*field
}

var (
	marshalerType     = reflect.TypeFor[Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	isZeroerType      = reflect.TypeFor[interface{ IsZero() bool }]()
)

var encPlans planCache[encPlan]

type encBuilder struct {
	plans map[unsafe.Pointer]*encPlan
}

func encPlanFor(rt unsafe.Pointer) *encPlan {
	if p := encPlans.load(rt); p != nil {
		return p
	}
	planMu.Lock()
	defer planMu.Unlock()
	if p := encPlans.load(rt); p != nil {
		return p
	}
	b := &encBuilder{plans: map[unsafe.Pointer]*encPlan{}}
	p := b.plan(typeOfRtype(rt))
	// Field ops look at the plans of field types, which recursive types
	// complete only now.
	for _, q := range b.plans {
		if q.st != nil {
			for i := range q.st.fields {
				f := &q.st.fields[i]
				f.op = fieldOp(f)
				f.omit, f.omitOff = fieldOmit(f)
			}
		}
	}
	encPlans.publish(b.plans)
	return p
}

func (b *encBuilder) plan(t reflect.Type) *encPlan {
	rt := rtypeOf(t)
	if p := b.plans[rt]; p != nil {
		return p
	}
	if p := encPlans.load(rt); p != nil {
		return p
	}
	p := &encPlan{typ: t, rtyp: rt, size: t.Size(), direct: isDirect(t)}
	b.plans[rt] = p
	pt := reflect.PointerTo(t)
	if t.Kind() != reflect.Pointer && t.Kind() != reflect.Interface {
		if pt.Implements(marshalerType) {
			if t.Implements(marshalerType) {
				p.valMarshal = itabOf(pt, marshalerType)
			} else {
				p.ptrMarshal = itabOf(pt, marshalerType)
			}
		}
		if pt.Implements(textMarshalerType) {
			if t.Implements(textMarshalerType) {
				p.valText = itabOf(pt, textMarshalerType)
			} else {
				p.ptrText = itabOf(pt, textMarshalerType)
			}
		}
	} else if t.Kind() == reflect.Pointer {
		// The pointer value itself is the receiver.
		if t.Implements(marshalerType) {
			p.valMarshal = itabOf(t, marshalerType)
		}
		if t.Implements(textMarshalerType) {
			p.valText = itabOf(t, textMarshalerType)
		}
	}
	p.methods = p.ptrMarshal != nil || p.valMarshal != nil || p.ptrText != nil || p.valText != nil
	if t == rawMessageType {
		p.kind = eRaw
		p.methods = false
		return p
	}
	switch t.Kind() {
	case reflect.Bool:
		p.kind = eBool
	case reflect.Int:
		p.kind = eInt
	case reflect.Int8:
		p.kind = eInt8
	case reflect.Int16:
		p.kind = eInt16
	case reflect.Int32:
		p.kind = eInt32
	case reflect.Int64:
		p.kind = eInt64
	case reflect.Uint:
		p.kind = eUint
	case reflect.Uint8:
		p.kind = eUint8
	case reflect.Uint16:
		p.kind = eUint16
	case reflect.Uint32:
		p.kind = eUint32
	case reflect.Uint64:
		p.kind = eUint64
	case reflect.Uintptr:
		p.kind = eUintptr
	case reflect.Float32:
		p.kind = eFloat32
	case reflect.Float64:
		p.kind = eFloat64
	case reflect.String:
		p.kind = eString
		if t == numberType {
			p.kind = eNumber
		}
	case reflect.Interface:
		p.kind = eIface
		if t.Implements(marshalerType) {
			p.kind = eMarshalIface
		} else if t.Implements(textMarshalerType) {
			p.kind = eTextIface
		}
	case reflect.Pointer:
		p.kind = ePtr
		p.elem = b.plan(t.Elem())
	case reflect.Slice:
		p.kind = eSlice
		et := t.Elem()
		if et.Kind() == reflect.Uint8 {
			ept := reflect.PointerTo(et)
			if !ept.Implements(marshalerType) && !ept.Implements(textMarshalerType) {
				p.kind = eBytes
			}
		}
		p.elem = b.plan(et)
	case reflect.Array:
		p.kind = eArray
		p.alen = t.Len()
		p.elem = b.plan(t.Elem())
	case reflect.Map:
		p.kind = eMap
		kt := t.Key()
		p.keyKind = kt.Kind()
		switch p.keyKind {
		case reflect.String,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		default:
			if !kt.Implements(textMarshalerType) && !(jsonv2 && v2KeyKind(p.keyKind)) {
				p.kind = eBad
			}
		}
		p.keyText = p.keyKind != reflect.String && kt.Implements(textMarshalerType)
		if kt == stringType {
			switch t.Elem() {
			case anyType:
				p.mapFast = mapStringAny
			case stringType:
				p.mapFast = mapStringString
			case rawMessageType:
				p.mapFast = mapStringRaw
			}
		}
		if p.kind == eMap {
			p.elem = b.plan(t.Elem())
		}
	case reflect.Struct:
		p.kind = eStruct
		p.st = b.structEnc(t)
	default:
		p.kind = eBad
	}
	return p
}

func (b *encBuilder) structEnc(t reflect.Type) *structEnc {
	fs, in := structFields(t)
	se := &structEnc{fields: make([]encField, len(fs))}
	if in != nil {
		se.inline = &encField{offset: in.offset, path: in.path, plan: b.plan(in.typ), field: in}
	}
	// The keys share one block, each followed by padding, so that they can
	// be copied a word at a time.
	var keys []byte
	var at [][2][2]int
	for i := range fs {
		f := &fs[i]
		ef := &se.fields[i]
		ef.field = f
		ef.offset, ef.path = f.offset, f.path
		ef.omitEmpty, ef.omitZero, ef.quoted = f.omitEmpty, f.omitZero, f.quoted
		ef.plan = b.plan(f.typ)
		var pos [2][2]int
		for h, name := range [2]string{f.name, string(appendHTMLEscape(nil, f.name))} {
			start := len(keys)
			keys = append(append(append(keys, ',', '"'), name...), '"', ':')
			pos[h] = [2]int{start, len(keys)}
			keys = append(keys, make([]byte, keyPad)...)
		}
		at = append(at, pos)
		if f.omitZero {
			ef.isZero = isZeroFunc(f.typ)
		}
	}
	block := string(keys)
	for i := range se.fields {
		for h := range 2 {
			se.fields[i].key[h] = block[at[i][h][0]:at[i][h][1]]
		}
	}
	return se
}

// isDirect reports whether an interface holds values of type t in its data
// word. Only pointer-shaped types qualify; for structs and arrays the rule
// of the toolchain decides, so it is observed by boxing a probe value.
func isDirect(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return true
	case reflect.Struct, reflect.Array:
		if t.Size() != unsafe.Sizeof(uintptr(0)) || !pointerShaped(t) {
			return false
		}
		v := reflect.New(t)
		*(*unsafe.Pointer)(v.UnsafePointer()) = unsafe.Pointer(&directProbe)
		x := v.Elem().Interface()
		return (*eface)(unsafe.Pointer(&x)).data == unsafe.Pointer(&directProbe)
	}
	return false
}

var directProbe byte

// pointerShaped reports whether t is one pointer, alone or wrapped in
// structs and arrays whose other elements have no size.
func pointerShaped(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return true
	case reflect.Struct:
		var only reflect.Type
		for i := 0; i < t.NumField(); i++ {
			ft := t.Field(i).Type
			if ft.Size() == 0 {
				continue
			}
			if only != nil {
				return false
			}
			only = ft
		}
		return only != nil && pointerShaped(only)
	case reflect.Array:
		return t.Len() == 1 && pointerShaped(t.Elem())
	}
	return false
}

// appendHTMLEscape appends s with '<', '>', '&', U+2028 and U+2029 escaped.
func appendHTMLEscape(dst []byte, s string) []byte {
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '<' || c == '>' || c == '&' {
			dst = append(dst, s[start:i]...)
			dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xF])
			start = i + 1
		}
		if c == 0xE2 && i+2 < len(s) && s[i+1] == 0x80 && s[i+2]&^1 == 0xA8 {
			dst = append(dst, s[start:i]...)
			dst = append(dst, '\\', 'u', '2', '0', '2', hexDigits[s[i+2]&0xF])
			start = i + 3
		}
	}
	return append(dst, s[start:]...)
}

const hexDigits = "0123456789abcdef"

// isZeroFunc returns the IsZero check of type t for omitzero, or nil when t
// has no IsZero method and reflect.Value.IsZero applies.
func isZeroFunc(t reflect.Type) func(p unsafe.Pointer, addr bool) bool {
	switch {
	case t.Kind() == reflect.Interface && t.Implements(isZeroerType):
		return func(p unsafe.Pointer, _ bool) bool {
			v := reflect.NewAt(t, p).Elem()
			return v.IsNil() ||
				(v.Elem().Kind() == reflect.Pointer && v.Elem().IsNil()) ||
				v.Interface().(interface{ IsZero() bool }).IsZero()
		}
	case t.Kind() == reflect.Pointer && t.Implements(isZeroerType):
		return func(p unsafe.Pointer, _ bool) bool {
			v := reflect.NewAt(t, p).Elem()
			if v.IsNil() {
				return true
			}
			return v.Interface().(interface{ IsZero() bool }).IsZero()
		}
	case t.Implements(isZeroerType):
		return func(p unsafe.Pointer, _ bool) bool {
			return reflect.NewAt(t, p).Elem().Interface().(interface{ IsZero() bool }).IsZero()
		}
	case reflect.PointerTo(t).Implements(isZeroerType):
		return func(p unsafe.Pointer, addr bool) bool {
			if !addr {
				// The method gets a pointer to a copy, as for a value that
				// is not addressable.
				c := reflect.New(t)
				c.Elem().Set(reflect.NewAt(t, p).Elem())
				return c.Interface().(interface{ IsZero() bool }).IsZero()
			}
			return reflect.NewAt(t, p).Interface().(interface{ IsZero() bool }).IsZero()
		}
	}
	return nil
}
