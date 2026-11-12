package json

import (
	"encoding"
	"math/bits"
	"reflect"
	"sync"
	"sync/atomic"
	"unicode/utf8"
	"unsafe"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// Decode plan kinds.
const (
	dBool uint8 = iota
	dInt
	dInt8
	dInt16
	dInt32
	dInt64
	dUint
	dUint8
	dUint16
	dUint32
	dUint64
	dUintptr
	dFloat32
	dFloat64
	dString
	dNumber // json.Number
	dBytes  // a slice of bytes, from base64
	dRaw    // RawMessage
	dAny    // an interface without methods
	dIface  // an interface with methods
	dPtr
	dSlice
	dArray
	dMap
	dStruct
	dUnm  // the pointer to the type implements Unmarshaler
	dText // the pointer to the type implements encoding.TextUnmarshaler
	dBad  // a kind JSON does not decode into: chan, func, complex, unsafe.Pointer
)

// A decPlan says how to decode into one Go type.
type decPlan struct {
	kind uint8
	typ  reflect.Type
	rtyp unsafe.Pointer // the runtime type, for allocation
	size uintptr
	// elem is the plan of the element of a pointer, slice, array or map,
	// or, for dUnm and dText, the plan of the type without its methods.
	elem *decPlan
	alen int // array length
	st   *structDec
	// itab is the Unmarshaler (dUnm) or TextUnmarshaler (dText) itab of the
	// pointer to the type. For dPtr, unm and text are the itabs of the
	// pointer type itself, when it implements those interfaces.
	itab      unsafe.Pointer
	unm, text unsafe.Pointer
	// For maps: the key kind and, when the pointer to the key type
	// implements encoding.TextUnmarshaler, its itab.
	key     reflect.Type
	keyKind reflect.Kind
	keyText unsafe.Pointer
	mapFast uint8
	// elemSlab: a pointer whose element is small and pointer-free; it is
	// allocated from the call's slab.
	elemSlab  bool
	elemNoPtr bool // the element type holds no pointers
	// scratch: a slice whose elements may be decoded into scratch space
	// and then copied, as no method sees their addresses.
	scratch bool
	// last: for a slice of a large element type, the length of the last
	// array decoded into one, up to 5, and lastSame when the one before
	// had the same.
	last int32
}

const lastSame = 8

// lastLen returns the length of the last two arrays decoded into a slice
// of plan pl when they had the same, of at most 4, or else 0. A longer
// array is counted or goes through scratch space.
func (pl *decPlan) lastLen() int {
	if v := atomic.LoadInt32(&pl.last); v&lastSame != 0 && v&^lastSame <= 4 {
		return int(v &^ lastSame)
	}
	return 0
}

// noteLen notes n as the length of the last array decoded into a slice of
// plan pl.
func (pl *decPlan) noteLen(n int) {
	v := atomic.LoadInt32(&pl.last)
	w := int32(min(n, 5))
	if v&^lastSame == w {
		w |= lastSame
	}
	if v != w {
		atomic.StoreInt32(&pl.last, w)
	}
}

// Specialized map types.
const (
	mapGeneric uint8 = iota
	mapStringAny
	mapStringString
	mapStringRaw
)

// structDec is the decode side of a struct plan.
type structDec struct {
	typ    reflect.Type
	id     int32 // the index in structDecs
	fields []field
	plans  []*decPlan
	// table is an open-addressing hash of the exact names: entries are
	// field indexes plus 1, 0 for empty; seed and mask pick the slot.
	table []uint16
	seed  uint64
	mask  uint64
	// fold is an open-addressing hash like table, of foldSize entries and
	// with seed, of the names folded by appendFold, for case-insensitive
	// matches. It is made when first needed.
	fold atomic.Pointer[uint16]
	// names holds the member name of each field as it appears in the
	// input. It has at least one entry.
	names []memberName
	// next holds, for each field and then for the start of an object, the
	// field whose member came next the last time it differed from the
	// guess, so that an input order that repeats is guessed.
	next []atomic.Int32
	// With inlineV2, the map or raw value that takes the other members,
	// and for a map the plan of its element.
	inline     *field
	inlinePlan *decPlan
}

// memberName is a field name followed by a quote, as a member name without
// escapes ends in the input, or "" when the name holds a byte a JSON
// string must escape. Its first 16 bytes are also kept as two words, with
// masks of the bytes in use.
type memberName struct {
	q    string
	w, m [2]uint64
}

// newMemberName returns the memberName of name, whose q is quoted, the
// name followed by a quote.
func newMemberName(name, quoted string) memberName {
	if !plainName(name) {
		return memberName{}
	}
	n := memberName{q: quoted}
	var pad [16]byte
	k := copy(pad[:], n.q)
	for j := range n.w {
		n.w[j] = jsonsimd.Load64(string(pad[:]), 8*j)
	}
	for b := 0; b < k; b++ {
		n.m[b/8] |= 0xff << (8 * (b % 8))
	}
	return n
}

// at reports whether the member name in s from i, after its opening
// quote, is n. object compares the words itself when it can.
func (n *memberName) at(s string, i int) bool {
	return n.q != "" && len(s)-i >= len(n.q) && s[i:i+len(n.q)] == n.q
}

var (
	unmarshalerType     = reflect.TypeFor[Unmarshaler]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
	rawMessageType      = reflect.TypeFor[RawMessage]()
	numberType          = reflect.TypeFor[Number]()
	anyType             = reflect.TypeFor[any]()
	stringType          = reflect.TypeFor[string]()
)

// eface is the layout of an empty interface value; iface that of an
// interface with methods.
type eface struct {
	typ, data unsafe.Pointer
}

type iface struct {
	tab, data unsafe.Pointer
}

func rtypeOf(t reflect.Type) unsafe.Pointer {
	return (*iface)(unsafe.Pointer(&t)).data
}

// typeOfRtype returns the reflect.Type of a runtime type pointer.
func typeOfRtype(rt unsafe.Pointer) reflect.Type {
	var x any
	(*eface)(unsafe.Pointer(&x)).typ = rt
	return reflect.TypeOf(x)
}

// itabOf returns the itab of the method set of pointer type pt for the
// interface type it, which pt implements.
func itabOf(pt, it reflect.Type) unsafe.Pointer {
	i := reflect.New(it).Elem()
	i.Set(reflect.Zero(pt))
	return (*iface)(i.Addr().UnsafePointer()).tab
}

// implementsPtr reports whether pointer type pt has methods and implements
// interface it, as the decoder checks before calling methods.
func implementsPtr(pt, it reflect.Type) bool {
	return pt.NumMethod() > 0 && pt.Implements(it)
}

// planCache maps rtypes to plans. Lookups try a small direct-mapped table
// of recent types first, then the map of all plans, which is replaced as
// a whole when plans are added.
type planCache[P any] struct {
	m     atomic.Pointer[map[unsafe.Pointer]*planEntry[P]]
	front [256]atomic.Pointer[planEntry[P]]
}

type planEntry[P any] struct {
	rt   unsafe.Pointer
	plan *P
}

func frontIndex(rt unsafe.Pointer) uintptr {
	h := uintptr(rt)
	return (h>>5 ^ h>>13) & 255
}

func (c *planCache[P]) load(rt unsafe.Pointer) *P {
	f := &c.front[frontIndex(rt)]
	if e := f.Load(); e != nil && e.rt == rt {
		return e.plan
	}
	if m := c.m.Load(); m != nil {
		if e := (*m)[rt]; e != nil {
			f.Store(e)
			return e.plan
		}
	}
	return nil
}

func (c *planCache[P]) publish(add map[unsafe.Pointer]*P) {
	old := c.m.Load()
	n := len(add)
	if old != nil {
		n += len(*old)
	}
	m := make(map[unsafe.Pointer]*planEntry[P], n)
	if old != nil {
		for k, v := range *old {
			m[k] = v
		}
	}
	for k, v := range add {
		m[k] = &planEntry[P]{k, v}
	}
	c.m.Store(&m)
}

var (
	planMu    sync.Mutex
	decPlans  planCache[decPlan] // with the method check
	decBases  planCache[decPlan] // without it
	rootPlans planCache[decPlan] // pointer types passed to Unmarshal
)

// structDecs holds every struct decoder by id, for error contexts. It only
// grows, under planMu, and is nil before the first one.
var structDecs atomic.Pointer[[]*structDec]

// registerStructDec gives sd its id. planMu must be held.
func registerStructDec(sd *structDec) {
	var old []*structDec
	if p := structDecs.Load(); p != nil {
		old = *p
	}
	sd.id = int32(len(old))
	// Readers never look past their length, so appending in place is safe.
	all := append(old, sd)
	structDecs.Store(&all)
}

// decBuilder builds plans under planMu. Plans enter its maps before they
// are filled in, so recursive types refer to themselves.
type decBuilder struct {
	plans, bases map[unsafe.Pointer]*decPlan
}

func newDecBuilder() *decBuilder {
	return &decBuilder{plans: map[unsafe.Pointer]*decPlan{}, bases: map[unsafe.Pointer]*decPlan{}}
}

func (b *decBuilder) publish() {
	decPlans.publish(b.plans)
	decBases.publish(b.bases)
}

// rootDecPlan returns the dPtr plan of pointer type rt, as used for the
// value passed to Unmarshal and for pointers found in interfaces.
func rootDecPlan(rt unsafe.Pointer) *decPlan {
	if p := rootPlans.load(rt); p != nil {
		return p
	}
	planMu.Lock()
	defer planMu.Unlock()
	if p := rootPlans.load(rt); p != nil {
		return p
	}
	b := newDecBuilder()
	p := b.base(typeOfRtype(rt))
	b.publish()
	rootPlans.publish(map[unsafe.Pointer]*decPlan{rt: p})
	return p
}

// plan returns the plan for t reached as an addressable value: when t is a
// named non-pointer type whose pointer implements Unmarshaler or
// encoding.TextUnmarshaler, the plan calls that method.
func (b *decBuilder) plan(t reflect.Type) *decPlan {
	rt := rtypeOf(t)
	if p := b.plans[rt]; p != nil {
		return p
	}
	if p := decPlans.load(rt); p != nil {
		return p
	}
	if t.Name() != "" && t.Kind() != reflect.Pointer {
		pt := reflect.PointerTo(t)
		if implementsPtr(pt, unmarshalerType) && t != rawMessageType {
			p := &decPlan{kind: dUnm, typ: t, rtyp: rt, size: t.Size(), itab: itabOf(pt, unmarshalerType)}
			b.plans[rt] = p
			p.elem = b.base(t)
			return p
		}
		if implementsPtr(pt, textUnmarshalerType) {
			p := &decPlan{kind: dText, typ: t, rtyp: rt, size: t.Size(), itab: itabOf(pt, textUnmarshalerType)}
			b.plans[rt] = p
			p.elem = b.base(t)
			return p
		}
	}
	p := b.base(t)
	b.plans[rt] = p
	return p
}

// base returns the plan for t without the method check of plan.
func (b *decBuilder) base(t reflect.Type) *decPlan {
	rt := rtypeOf(t)
	if p := b.bases[rt]; p != nil {
		return p
	}
	if p := decBases.load(rt); p != nil {
		return p
	}
	p := &decPlan{typ: t, rtyp: rt, size: t.Size()}
	b.bases[rt] = p
	switch t.Kind() {
	case reflect.Bool:
		p.kind = dBool
	case reflect.Int:
		p.kind = dInt
	case reflect.Int8:
		p.kind = dInt8
	case reflect.Int16:
		p.kind = dInt16
	case reflect.Int32:
		p.kind = dInt32
	case reflect.Int64:
		p.kind = dInt64
	case reflect.Uint:
		p.kind = dUint
	case reflect.Uint8:
		p.kind = dUint8
	case reflect.Uint16:
		p.kind = dUint16
	case reflect.Uint32:
		p.kind = dUint32
	case reflect.Uint64:
		p.kind = dUint64
	case reflect.Uintptr:
		p.kind = dUintptr
	case reflect.Float32:
		p.kind = dFloat32
	case reflect.Float64:
		p.kind = dFloat64
	case reflect.String:
		p.kind = dString
		if t == numberType {
			p.kind = dNumber
		}
	case reflect.Interface:
		p.kind = dIface
		if t.NumMethod() == 0 {
			p.kind = dAny
		}
	case reflect.Pointer:
		p.kind = dPtr
		et := t.Elem()
		if implementsPtr(t, unmarshalerType) {
			p.unm = itabOf(t, unmarshalerType)
		}
		if implementsPtr(t, textUnmarshalerType) {
			p.text = itabOf(t, textUnmarshalerType)
		}
		p.elemNoPtr = !hasPointers(et)
		p.elemSlab = p.elemNoPtr && et.Size() <= 16 && et.Size() > 0
		p.elem = b.base(et)
	case reflect.Slice:
		et := t.Elem()
		if et.Kind() == reflect.Uint8 {
			p.kind = dBytes
			if t == rawMessageType {
				p.kind = dRaw
			}
			p.elemNoPtr = true
			// A byte slice whose element type has methods still decodes
			// from a JSON array element by element.
			p.elem = b.plan(et)
			break
		}
		p.kind = dSlice
		p.elemNoPtr = !hasPointers(et)
		p.scratch = et.Size() > 0 && et.Size() <= scratchMax/8 && !inlineMethods(et)
		p.elem = b.plan(et)
	case reflect.Array:
		p.kind = dArray
		p.alen = t.Len()
		p.elem = b.plan(t.Elem())
	case reflect.Map:
		p.kind = dMap
		p.key = t.Key()
		p.keyKind = p.key.Kind()
		if pt := reflect.PointerTo(p.key); pt.Implements(textUnmarshalerType) {
			p.keyText = itabOf(pt, textUnmarshalerType)
		}
		switch p.keyKind {
		case reflect.String,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		default:
			// On encoding/json/v2, each key is converted, and those of
			// other kinds fail one by one.
			if p.keyText == nil && !jsonv2 {
				p.keyKind = reflect.Invalid // objects do not decode into this map
			}
		}
		if p.key == stringType {
			switch t.Elem() {
			case anyType:
				p.mapFast = mapStringAny
			case stringType:
				p.mapFast = mapStringString
			case rawMessageType:
				p.mapFast = mapStringRaw
			}
		}
		p.elem = b.plan(t.Elem())
	case reflect.Struct:
		p.kind = dStruct
		p.st = b.structDec(t)
	default:
		p.kind = dBad
	}
	return p
}

func (b *decBuilder) structDec(t reflect.Type) *structDec {
	fs, in := structFields(t)
	sd := &structDec{typ: t, fields: fs, plans: make([]*decPlan, len(fs)), inline: in}
	registerStructDec(sd)
	if in != nil && in.typ.Kind() == reflect.Map {
		sd.inlinePlan = b.plan(in.typ.Elem())
	}
	for i := range fs {
		sd.plans[i] = b.plan(fs[i].typ)
	}
	sd.buildTable()
	// The names, each followed by a quote, share one block.
	n := 0
	for i := range fs {
		n += len(fs[i].name) + 1
	}
	qs := make([]byte, 0, n)
	for i := range fs {
		qs = append(append(qs, fs[i].name...), '"')
	}
	block := unsafe.String(unsafe.SliceData(qs), len(qs))
	sd.names = make([]memberName, max(1, len(fs)))
	for i, at := 0, 0; i < len(fs); i++ {
		end := at + len(fs[i].name) + 1
		sd.names[i] = newMemberName(fs[i].name, block[at:end])
		at = end
	}
	sd.next = make([]atomic.Int32, len(fs)+1)
	for i := 0; i+1 < len(fs); i++ {
		sd.next[i].Store(int32(i + 1))
	}
	return sd
}

// foldTable returns the fold table of sd, which it makes the first time.
// Of the fields whose names fold alike, it holds the first in index order,
// or, with encoding/json/v2, the first of the shallowest.
func (sd *structDec) foldTable() []uint16 {
	size := sd.foldSize()
	if p := sd.fold.Load(); p != nil {
		return unsafe.Slice(p, size)
	}
	tab := make([]uint16, size)
	mask := uint64(size - 1)
	var buf [64]byte
	for i := range sd.fields {
		k := appendFold(buf[:0], sd.fields[i].name)
		h := keyHash(unsafe.String(unsafe.SliceData(k), len(k)), sd.seed) & mask
		for {
			e := tab[h]
			if e == 0 {
				tab[h] = uint16(i + 1)
				break
			}
			if j := int(e - 1); foldsTo(sd.fields[j].name, k) {
				if jsonv2 && len(sd.fields[i].index) < len(sd.fields[j].index) {
					tab[h] = uint16(i + 1)
				}
				break
			}
			h = (h + 1) & mask
		}
	}
	// Calls that race make equal tables; the first one stored is kept.
	if !sd.fold.CompareAndSwap(nil, &tab[0]) {
		return unsafe.Slice(sd.fold.Load(), size)
	}
	return tab
}

// foldSize returns the number of entries of the fold table of sd: a power
// of two at least twice the number of fields.
func (sd *structDec) foldSize() int {
	size := 8
	for size < 2*len(sd.fields) {
		size <<= 1
	}
	return size
}

// foldsTo reports whether appendFold(nil, name) is k.
func foldsTo(name string, k []byte) bool {
	j := 0
	for i := 0; i < len(name); {
		c := name[i]
		if c < utf8.RuneSelf {
			if 'a' <= c && c <= 'z' {
				c -= 'a' - 'A'
			}
			if j == len(k) || k[j] != c {
				return false
			}
			i++
			j++
			continue
		}
		r, n := utf8.DecodeRuneInString(name[i:])
		var b [utf8.UTFMax]byte
		m := utf8.EncodeRune(b[:], foldRune(r))
		if len(k)-j < m || string(k[j:j+m]) != string(b[:m]) {
			return false
		}
		i += n
		j += m
	}
	return j == len(k)
}

// buildTable picks a seed for which the field names fall into distinct
// slots, so a lookup reads one slot. Very large structs may keep a few
// collisions, resolved by probing.
func (sd *structDec) buildTable() {
	n := len(sd.fields)
	size := 8
	for size < 2*n {
		size <<= 1
	}
	// A try clears the table of the one before, unless the size grew.
	var table []uint16
	for tries := 0; ; tries++ {
		if tries > 0 && tries%64 == 0 && size < 1<<15 {
			size <<= 1
		}
		seed := uint64(tries)*0x9E3779B97F4A7C15 + 1
		mask := uint64(size - 1)
		if len(table) == size {
			clear(table)
		} else {
			table = make([]uint16, size)
		}
		collisions := 0
		for i := range sd.fields {
			h := keyHash(sd.fields[i].name, seed) & mask
			if table[h] != 0 {
				collisions++
				for table[h] != 0 {
					h = (h + 1) & mask
				}
			}
			table[h] = uint16(i + 1)
		}
		if collisions == 0 || tries >= 512 {
			sd.table, sd.seed, sd.mask = table, seed, mask
			return
		}
	}
}

// plainName reports whether name appears in a JSON string as it is.
func plainName(name string) bool {
	for i := 0; i < len(name); i++ {
		if c := name[i]; c < 0x20 || c == '"' || c == '\\' {
			return false
		}
	}
	return name != "" && utf8.ValidString(name)
}

// keyHash hashes a member name.
func keyHash(s string, seed uint64) uint64 {
	// Up to 16 bytes, two overlapping loads cover the key without a loop.
	n := len(s)
	var a, b uint64
	switch {
	case n >= 8:
		a, b = load64(s, 0), load64(s, n-8)
		for i := 8; i < n-8; i += 8 {
			a = (a ^ load64(s, i)) * 0xc4ceb9fe1a85ec53
		}
	case n >= 4:
		a, b = uint64(jsonsimd.Load32(s, 0)), uint64(jsonsimd.Load32(s, n-4))
	case n > 0:
		a = uint64(s[0]) | uint64(s[n>>1])<<8 | uint64(s[n-1])<<16
	}
	h := (a ^ seed ^ uint64(n)<<56) * 0xff51afd7ed558ccd
	h ^= (b ^ h>>29) * 0xc4ceb9fe1a85ec53
	return h ^ h>>32
}

// lookup returns the index of the field named exactly key, or of one
// whose name matches it ignoring case, or -1.
func (sd *structDec) lookup(key string) int {
	if len(sd.table) == 0 {
		return -1
	}
	h := keyHash(key, sd.seed) & sd.mask
	for {
		e := sd.table[h]
		if e == 0 {
			break
		}
		if sd.fields[e-1].name == key {
			return int(e - 1)
		}
		h = (h + 1) & sd.mask
	}
	return sd.lookupFold(key)
}

// lookupFold returns the index of the field whose name matches key
// ignoring case, or -1.
func (sd *structDec) lookupFold(key string) int {
	var buf [64]byte
	k := appendFold(buf[:0], key)
	tab := sd.foldTable()
	mask := uint64(len(tab) - 1)
	h := keyHash(unsafe.String(unsafe.SliceData(k), len(k)), sd.seed) & mask
	for {
		e := tab[h]
		if e == 0 {
			return -1
		}
		if foldsTo(sd.fields[e-1].name, k) {
			return int(e - 1)
		}
		h = (h + 1) & mask
	}
}

// findShort looks up the member name whose opening quote is at s[i], when
// it is shorter than 16 bytes and holds only ASCII characters that need no
// escape. It returns the length of the name, or -1 when it is not such a
// name, and the index of its field as lookup does. s holds more than 16
// bytes from i.
func (sd *structDec) findShort(s string, i int) (n, fi int) {
	w0, w1 := jsonsimd.Load64(s, i+1), jsonsimd.Load64(s, i+9)
	if m := nameStop(w0); m != 0 {
		n = bits.TrailingZeros64(m) >> 3
	} else if m = nameStop(w1); m != 0 {
		n = 8 + bits.TrailingZeros64(m)>>3
	} else {
		return -1, -1
	}
	if s[i+1+n] != '"' {
		return -1, -1
	}
	if len(sd.table) == 0 {
		return n, -1
	}
	key := s[i+1 : i+1+n]
	h := keyHash(key, sd.seed) & sd.mask
	for {
		e := sd.table[h]
		if e == 0 {
			break
		}
		// The quote after the name is compared with the one in nm.q.
		if nm := &sd.names[e-1]; len(nm.q) == n+1 && (w0^nm.w[0])&nm.m[0]|(w1^nm.w[1])&nm.m[1] == 0 {
			return n, int(e - 1)
		}
		h = (h + 1) & sd.mask
	}
	return n, sd.lookupFold(key)
}

// nameStop has the high bit set of the first byte of w, in memory order,
// that is a quote, a backslash, a control byte or 0x80 and above, and
// maybe of later bytes.
func nameStop(w uint64) uint64 {
	q := w ^ '"'*lsb
	b := w ^ '\\'*lsb
	return ((q-lsb)&^q | (b-lsb)&^b | (w - ' '*lsb) | w) & msb
}

// inlineMethods reports whether a value of type t holds, in its own
// memory, a value that is decoded by an UnmarshalJSON or UnmarshalText
// method of its pointer, which would see the address of that value.
func inlineMethods(t reflect.Type) bool {
	if t.Name() != "" && t.Kind() != reflect.Pointer {
		pt := reflect.PointerTo(t)
		if implementsPtr(pt, unmarshalerType) && t != rawMessageType || implementsPtr(pt, textUnmarshalerType) {
			return true
		}
	}
	switch t.Kind() {
	case reflect.Array:
		return t.Len() > 0 && inlineMethods(t.Elem())
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if inlineMethods(t.Field(i).Type) {
				return true
			}
		}
	}
	return false
}

func hasPointers(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		return false
	case reflect.Array:
		return t.Len() > 0 && hasPointers(t.Elem())
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if hasPointers(t.Field(i).Type) {
				return true
			}
		}
		return false
	}
	return true
}
