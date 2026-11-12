package json

import (
	"reflect"
	"strconv"
	"strings"
	"unsafe"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

var (
	float64Type  = reflect.TypeFor[float64]()
	float64Rtype = rtypeOf(float64Type)
	stringRtype  = rtypeOf(stringType)
)

// anyValue decodes the value at d.i as an interface value: map[string]any,
// []any, string, float64 (or Number with UseNumber), bool or nil. A number
// out of the float64 range gives nil after saving an error.
func (d *decodeState) anyValue() any {
	switch c := d.s[d.i]; c {
	case '{':
		return d.objectAny()
	case '[':
		return d.arrayAny()
	case '"':
		return d.stringAny()
	case 't':
		d.i = d.wordEnd(d.i, "true")
		return true
	case 'f':
		d.i = d.wordEnd(d.i, "false")
		return false
	case 'n':
		d.null()
		return nil
	}
	start := d.i
	if !d.useNumber {
		if f, end, ok := d.floatAt(start, 64); ok {
			d.i = end
			return d.boxFloat(f)
		}
	}
	end := d.numberEnd(start)
	d.i = end
	if d.useNumber {
		return Number(d.copyString(start, end))
	}
	f, ok := d.parseFloatAt(start, end, 64)
	if !ok {
		if jsonv2 {
			d.typeErrorV2("number "+d.s[start:end], float64Type, end, start)
			return d.boxFloat(overflowAny(d.s[start]))
		}
		d.typeError("number "+d.s[start:end], float64Type, end+1)
		return nil
	}
	return d.boxFloat(f)
}

// anyStringMax bounds the string and header that stringAny puts in the
// slab.
const anyStringMax = slabMax

// stringAny reads the string at d.i as an interface value. Unless the
// string views the input or is empty, the string header and its bytes
// share one block, which the interface value keeps alive: the header, in
// memory the collector does not scan, points only into that block. It is
// written as integers, as the block needs no write barrier and may hold
// stale bytes that one would read as a pointer. The block is in the slab,
// or for a long string an allocation of its own.
func (d *decodeState) stringAny() any {
	start := d.i
	end, flags := d.stringScan(start)
	d.i = end + 1
	raw := d.s[start+1 : end]
	if d.noCopy || len(raw) == 0 || flags&spanHole != 0 {
		return d.stringFrom(start, end, flags)
	}
	if 16+len(raw) > min(int(d.slabChunk), anyStringMax) {
		return d.longStringAny(raw, flags)
	}
	if flags != 0 && (flags != jsonsimd.SpanHigh || !validUTF8(raw)) {
		if flags&spanUnquoted == 0 {
			d.scratch = unquote(d.scratch[:0], raw)
		}
		if raw = bytesView(d.scratch); len(raw) == 0 || 16+len(raw) > min(int(d.slabChunk), anyStringMax) {
			return d.ownString(d.scratch)
		}
	}
	if len(raw) <= internMax {
		if b := d.intern(raw); b != nil {
			return internAny(b)
		}
	}
	// The header from the end of the chunk, the bytes from its start:
	// neither needs padding. A new chunk has room for both.
	if 16+len(raw) > d.slabLeft() {
		d.newChunk(16 + len(raw))
	}
	b := d.slabTake(len(raw))
	copy(b, raw)
	return boxString(d.slabAllocNoZero(16), b)
}

// longStringAny returns the value of the string contents raw, with the
// given flags, as an interface value, for raw too long for the slab. The
// header and the bytes take one allocation, the header first, or last for
// a value of copyLongMin bytes or more.
func (d *decodeState) longStringAny(raw string, flags uint32) any {
	v := raw
	switch {
	case flags == 0:
	case flags&spanUnquoted != 0:
		v = bytesView(d.scratch)
	case flags == jsonsimd.SpanHigh && validUTF8(raw):
	case len(raw) >= slabMax:
		// As in decodeString: unescaping only shrinks valid UTF-8, so the
		// value goes right after the header, unless invalid bytes made it
		// grow.
		d.long += len(raw)
		buf := d.bigBlock(16 + len(raw))
		out := unquote(buf[16:16], raw)
		if len(out) > 0 && &out[0] == &buf[16] && cap(out)-len(out) < len(raw)/8 {
			return boxString(unsafe.Pointer(&buf[0]), out)
		}
		v = bytesView(out)
	default:
		d.scratch = unquote(d.scratch[:0], raw)
		v = bytesView(d.scratch)
	}
	if len(v) == 0 {
		return ""
	}
	if 16+len(v) <= min(int(d.slabChunk), anyStringMax) {
		if 16+len(v) > d.slabLeft() {
			d.newChunk(16 + len(v))
		}
		b := d.slabTake(len(v))
		copy(b, v)
		return boxString(d.slabAllocNoZero(16), b)
	}
	if len(v) >= slabMax {
		d.long += len(v)
	}
	if len(v) >= copyLongMin {
		// The header goes after the bytes, which then start the allocation,
		// aligned as the cache-bypassing copy wants them.
		n := (len(v) + 7) &^ 7
		buf := d.bigBlock(n + 16)
		copyLong(buf, v)
		return boxString(unsafe.Pointer(&buf[n]), buf[:len(v):len(v)])
	}
	buf := d.bigBlock(16 + len(v))
	copyLong(buf[16:], v)
	return boxString(unsafe.Pointer(&buf[0]), buf[16:])
}

// boxString returns b as a string in an interface value whose header goes
// to p, in pointer-free memory that b is part of.
func boxString(p unsafe.Pointer, b []byte) any {
	*(*uintptr)(p) = uintptr(unsafe.Pointer(unsafe.SliceData(b)))
	*(*int)(unsafe.Add(p, 8)) = len(b)
	var x any
	*(*eface)(unsafe.Pointer(&x)) = eface{stringRtype, p}
	return x
}

// boxFloat returns f as an interface value whose data comes from the slab.
func (d *decodeState) boxFloat(f float64) any {
	if f == 0 && !signbit(f) {
		return float64(0)
	}
	p := d.slabAlloc(8)
	*(*float64)(p) = f
	var x any
	*(*eface)(unsafe.Pointer(&x)) = eface{float64Rtype, p}
	return x
}

func signbit(f float64) bool { return *(*uint64)(unsafe.Pointer(&f))>>63 != 0 }

// objectAny decodes the JSON object at d.i into a map[string]any.
func (d *decodeState) objectAny() map[string]any {
	n := 0
	if c := d.container(d.i); c != nil {
		n = int(c.N)
		d.ci++
	}
	m := make(map[string]any, n)
	d.enter(true)
	d.i++
	d.ws()
	if d.s[d.i] == '}' {
		d.i++
		d.depth--
		return m
	}
	for {
		k := d.keyString()
		d.colon()
		m[k] = d.anyValue()
		if d.more('}') {
			continue
		}
		d.depth--
		return m
	}
}

// arrayAny decodes the JSON array at d.i into a []any.
func (d *decodeState) arrayAny() []any {
	n := -1
	if c := d.container(d.i); c != nil {
		n = int(c.N)
		d.ci++
	}
	d.enter(false)
	d.i++
	d.ws()
	if d.s[d.i] == ']' {
		d.i++
		d.depth--
		return []any{}
	}
	if n >= 0 {
		a := make([]any, n)
		for k := 0; ; k++ {
			a[k] = d.anyValue()
			if d.more(']') {
				continue
			}
			d.depth--
			return a
		}
	}
	// Without a count, collect the elements on the stack, then copy them.
	// The rest of a longer array is counted once.
	base := len(d.stack)
	for {
		x := d.anyValue()
		d.stack = append(d.stack, x)
		if d.more(']') {
			if k := len(d.stack) - base; k == anyStackMax {
				// As presize does.
				if r := d.countFrom(d.i); r > 0 && (d.checked || !d.fused || uintptr(k+r)*unsafe.Sizeof(x) <= presizeMax) {
					return d.arrayAnyRest(base, r)
				}
			}
			continue
		}
		d.depth--
		a := make([]any, len(d.stack)-base)
		copy(a, d.stack[base:])
		clear(d.stack[base:])
		d.stack = d.stack[:base]
		return a
	}
}

// anyStackMax is the number of elements of an array without a count that
// arrayAny collects before it counts the rest.
const anyStackMax = 64

// arrayAnyRest continues arrayAny for an array of r more elements than
// those on the stack from base.
func (d *decodeState) arrayAnyRest(base, r int) []any {
	k := len(d.stack) - base
	a := make([]any, k, k+r)
	copy(a, d.stack[base:])
	clear(d.stack[base:])
	d.stack = d.stack[:base]
	for {
		a = append(a, d.anyValue())
		if d.more(']') {
			continue
		}
		d.depth--
		return a
	}
}

// keyString reads the member name at d.i as a string of its own.
func (d *decodeState) keyString() string {
	start := d.i
	if d.s[start] != '"' {
		d.bad()
	}
	end, flags := d.stringEnd(start)
	d.i = end + 1
	if flags == 0 {
		return d.copyString(start+1, end)
	}
	return d.decodeString(start+1, end, flags)
}

// mapValue decodes a JSON object into the map at p.
func (d *decodeState) mapValue(pl *decPlan, p unsafe.Pointer) {
	start := d.i
	if pl.keyKind == reflect.Invalid {
		d.typeError("object", pl.typ, start+1)
		d.skip()
		return
	}
	if pl.keyText != nil {
		d.validateAll()
	}
	n := 0
	if c := d.container(start); c != nil {
		n = int(c.N)
		d.ci++
	}
	if *(*unsafe.Pointer)(p) == nil {
		*(*unsafe.Pointer)(p) = reflect.MakeMapWithSize(pl.typ, n).UnsafePointer()
	}
	d.enter(true)
	d.i++
	d.ws()
	if d.s[d.i] == '}' {
		d.i++
		d.depth--
		return
	}
	d.mapDepth++
	defer func() { d.mapDepth-- }()
	switch pl.mapFast {
	case mapStringAny:
		m := *(*map[string]any)(p)
		d.members(func(k string) { m[k] = d.anyValue() })
		return
	case mapStringString:
		m := *(*map[string]string)(p)
		d.members(func(k string) {
			if d.s[d.i] == '"' {
				// As stringInto writes a map element.
				start := d.i
				end, flags := d.stringEnd(start)
				d.i = end + 1
				m[k] = d.stringFrom(start, end, flags)
				return
			}
			var v string
			d.value(pl.elem, unsafe.Pointer(&v))
			m[k] = v
		})
		return
	case mapStringRaw:
		m := *(*map[string]RawMessage)(p)
		if len(d.s) > slabChunkMax && !d.noCopy && d.stream == nil {
			d.rawMembers(m)
			return
		}
		d.members(func(k string) {
			raw, stamp := d.rawChecked()
			if d.noCopy || d.stream != nil && len(raw) >= streamLong {
				raw = raw[:len(raw):len(raw)]
			} else {
				v := d.alloc(len(raw))
				copyLong(v, bytesView(raw))
				raw = v
			}
			d.stampRaw(raw, stamp)
			m[k] = raw
		})
		return
	}
	mv := reflect.NewAt(pl.typ, p).Elem()
	elem := reflect.New(pl.typ.Elem()).Elem()
	ep := elem.Addr().UnsafePointer()
	kt := pl.key
	for {
		kstart := d.i
		if d.s[kstart] != '"' {
			d.bad()
		}
		kend, flags := d.stringEnd(kstart)
		d.i = kend + 1
		item, off := d.s[kstart:kend+1], kstart+1
		if d.stream != nil {
			item, flags = d.streamKey(item, kstart, flags)
			off = int(d.stream.offset(off))
		}
		d.colon()
		if jsonv2 {
			// The key is converted first, and a value whose key can not
			// be is skipped.
			kv, ok := d.mapKey(pl, kt, item, flags, kend+1, kstart)
			if !ok {
				d.skip()
			} else {
				elem.SetZero()
				d.value(pl.elem, ep)
				mv.SetMapIndex(kv, elem)
			}
		} else {
			elem.SetZero()
			d.value(pl.elem, ep)
			if kv, ok := d.mapKey(pl, kt, item, flags, off, 0); ok {
				mv.SetMapIndex(kv, elem)
			}
		}
		if d.more('}') {
			continue
		}
		d.depth--
		return
	}
}

// members calls f for each member of the object whose first key is at d.i,
// with the key as a string of its own and d.i at the value.
func (d *decodeState) members(f func(k string)) {
	for {
		k := d.keyString()
		d.colon()
		f(k)
		if d.more('}') {
			continue
		}
		d.depth--
		return
	}
}

// rawBatch is the number of members of a map of RawMessage values that
// rawMembers reads before it copies them.
const rawBatch = 16

// rawMember is a member read by rawMembers: its key, or the bounds of a
// key to copy from the input when plain is set, and its value, which views
// the input.
type rawMember struct {
	key          string
	kstart, kend int
	raw          []byte
	stamp, plain bool
}

// rawMembers decodes the members of an object at d.i into m. It reads up
// to rawBatch members before it copies their keys and values, so that one
// chunk sized to what they need holds all the short ones, where the rest
// of a long input would size it.
func (d *decodeState) rawMembers(m map[string]RawMessage) {
	var batch [rawBatch]rawMember
	for {
		n, need, done := 0, 0, false
		for n < rawBatch && !done {
			r := &batch[n]
			start := d.i
			if d.s[start] != '"' {
				d.bad()
			}
			end, flags := d.stringEnd(start)
			d.i = end + 1
			r.plain = flags == 0
			if r.plain {
				r.kstart, r.kend = start+1, end
				need += d.slabNeed(end - start - 1)
			} else {
				r.key = d.decodeString(start+1, end, flags)
			}
			d.colon()
			r.raw, r.stamp = d.rawChecked()
			need += d.slabNeed(len(r.raw))
			n++
			done = !d.more('}')
		}
		if need > d.slabLeft() && need <= slabChunkMax {
			d.newChunkFor(need, len(d.s)-d.i+need)
		}
		for k := range n {
			r := &batch[k]
			key := r.key
			if r.plain {
				key = d.copyString(r.kstart, r.kend)
			}
			v := d.alloc(len(r.raw))
			copyLong(v, bytesView(r.raw))
			d.stampRaw(v, r.stamp)
			m[key] = v
		}
		if done {
			d.depth--
			return
		}
	}
}

// slabNeed returns the slab bytes that alloc takes for n bytes.
func (d *decodeState) slabNeed(n int) int {
	if n >= slabMax || n > int(d.slabChunk) {
		return 0
	}
	return n
}

// streamKey returns the key at kstart of the window, with the given token
// and flags, in memory of its own.
func (d *decodeState) streamKey(item string, kstart int, flags uint32) (string, uint32) {
	if flags&spanHole != 0 {
		h := d.stream.strings[flags>>spanHoleShift]
		b := d.holeToken(h)
		end, fl := jsonsimd.ScanString(bytesView(b), 1)
		if end != len(b)-1 {
			d.fail(errInputChanged)
		}
		return bytesView(b), fl
	}
	return strings.Clone(item), flags
}

// mapKey converts the key item, with its quotes, whose string flags are
// flags and whose value starts at offset off, to key type kt of map plan pl.
// On encoding/json/v2, off is the index after the key and kstart that of
// the key.
func (d *decodeState) mapKey(pl *decPlan, kt reflect.Type, item string, flags uint32, off, kstart int) (reflect.Value, bool) {
	if pl.keyText != nil {
		kv := reflect.New(kt)
		pt := kv.Type()
		if implementsPtr(pt, unmarshalerType) {
			if err := kv.Interface().(Unmarshaler).UnmarshalJSON(stringView(item)); err != nil {
				if jsonv2 {
					d.methodError(err)
					return reflect.Value{}, false
				}
				d.fail(err)
			}
			return kv.Elem(), true
		}
		var u textUnmarshaler
		*(*iface)(unsafe.Pointer(&u)) = iface{pl.keyText, kv.UnsafePointer()}
		if err := u.UnmarshalText(unquote(nil, item[1:len(item)-1])); err != nil {
			if jsonv2 {
				d.methodError(err)
				return reflect.Value{}, false
			}
			d.fail(err)
		}
		return kv.Elem(), true
	}
	key := item[1 : len(item)-1]
	if flags != 0 {
		key = string(unquote(nil, key))
	}
	if jsonv2 && key == "null" && pl.keyKind >= reflect.Int && pl.keyKind <= reflect.Float64 {
		// encoding/json/v2 reads the key of a number type as a quoted
		// number, and a quoted null as null: the key is zero.
		return reflect.New(kt).Elem(), true
	}
	switch pl.keyKind {
	case reflect.String:
		kv := reflect.New(kt).Elem()
		if d.noCopy && flags == 0 {
			kv.SetString(key)
		} else {
			kv.SetString(d.ownString(stringView(key)))
		}
		return kv, true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(key, 10, 64)
		if err != nil || reflect.Zero(kt).OverflowInt(n) {
			d.keyError("number "+key, kt, off, kstart)
			return reflect.Value{}, false
		}
		kv := reflect.New(kt).Elem()
		kv.SetInt(n)
		return kv, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		n, err := strconv.ParseUint(key, 10, 64)
		if err != nil || reflect.Zero(kt).OverflowUint(n) {
			d.keyError("number "+key, kt, off, kstart)
			return reflect.Value{}, false
		}
		kv := reflect.New(kt).Elem()
		kv.SetUint(n)
		return kv, true
	case reflect.Float32, reflect.Float64:
		// Only on encoding/json/v2, which takes what strconv takes: "01",
		// "+1", ".5", "0x1p-2", "1_0", "inf" and "NaN" too.
		f, err := strconv.ParseFloat(key, kt.Bits())
		if err != nil {
			d.keyError("number "+key, kt, off, kstart)
			return reflect.Value{}, false
		}
		kv := reflect.New(kt).Elem()
		kv.SetFloat(f)
		return kv, true
	}
	// Other kinds of keys are only decoded on encoding/json/v2.
	d.keyError("string", kt, off, kstart)
	return reflect.Value{}, false
}

// keyError keeps the type error for a map key.
func (d *decodeState) keyError(value string, kt reflect.Type, off, kstart int) {
	if jsonv2 {
		d.typeErrorV2(value, kt, off, kstart)
		return
	}
	d.typeErrorAt(value, kt, int64(off))
}
