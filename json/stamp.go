package json

import (
	"sync/atomic"
	"unsafe"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// The encoder checks and compacts what a MarshalJSON method returns, and
// Unmarshal checks its input. A method that marshals an alias of its type
// returns Marshal output, which is valid, compact and HTML-escaped already,
// and an UnmarshalJSON method that unmarshals into an alias of its type
// passes on input that the decoder checked: stamps let the encoder copy
// such output and Unmarshal skip the check.
//
// A stamp records the address, the length and a keyed hash of a Marshal
// output, or of the input of an UnmarshalJSON method. Bytes for which a
// stamp holds their address, their length and their hash are taken as
// stamped. A change to the bytes after the stamp, like any other bytes, is
// checked as before. Hashing costs about a tenth of the check, so stamps
// go only to the bytes of types seen to come back: of other types some
// bytes are noted, and the encoder or Unmarshal marks the type of noted
// bytes it gets. Bytes that are mostly long strings are not stamped where
// the check reads those faster than the hash does: the input of an
// UnmarshalJSON method, and Marshal output unless jsonsimd.FastSum holds
// and it is shorter than stampHashMax. The hash reads the bytes twice, to
// stamp them and to check the stamp, and the check once: past the size of
// a core's cache, where each read costs what reading memory does, one
// check costs less. Marshal counts strings of at least stampLong bytes;
// the decoder learns from the Unmarshal calls of a method, which count
// strings and RawMessage values of at least slabMax bytes.
//
// The decoder also stamps the RawMessage values it checks itself and finds
// compact and not mostly long strings, once the encoder took such values
// back: the encoder then copies them unchecked, and escapes them when they
// hold HTML characters.
const (
	stampMin   = 64 // shorter bytes are checked
	stampSlots = 256
	stampKinds = 1024
	stampNote  = 16 // one call in stampNote for an unmarked type is noted
	stampLong  = 256

	stampHashMax = 256 << 10
)

// rawKind is the type slot of the RawMessage values the decoder stamps.
var rawKind = kindOf(unsafe.Pointer(&rawKindKey))

var rawKindKey byte

// States of a type in a stampTable.
const (
	stampUnmarked = iota
	stampDense    // stamp its bytes
	stampSparse   // record its bytes without a hash
)

type stampTable struct {
	slots [stampSlots]stampSlot
	kinds [stampKinds]atomic.Uint32
	key   *jsonsimd.HashKey
}

type stampSlot struct {
	// key mixes the address and the length of the bytes, sum is their hash
	// with the low bit set and bit 1 set for a RawMessage value, which may
	// hold HTML characters, or 0 for a record without one, and kind is
	// their type slot plus one. A read may mix two records, which is
	// harmless: a sum is still one of stamped bytes, and a wrong type only
	// stamps or not some bytes it need not.
	key, sum, kind atomic.Uint64
	_              [40]byte
}

var encStamps, decStamps = newStampTable(), newStampTable()

func newStampTable() *stampTable { return &stampTable{key: jsonsimd.NewHashKey()} }

func (st *stampTable) slot(b []byte) (*stampSlot, uint64) {
	p := uint64(uintptr(unsafe.Pointer(unsafe.SliceData(b))))
	k := p ^ uint64(len(b))*0x9e3779b97f4a7c15
	return &st.slots[(p>>4)*0xff51afd7ed558ccd>>56], k
}

// kindOf returns the type slot of a type, given a pointer that identifies
// it.
func kindOf(p unsafe.Pointer) uint64 {
	return uint64(uintptr(p)) * 0xc4ceb9fe1a85ec53 >> 54
}

// put records b of type slot t: with its hash, when dense is set, or
// without one, when noted. Otherwise it drops the record of bytes that
// were at the same place, which would tell of b. raw marks a RawMessage
// value.
func (st *stampTable) put(b []byte, t uint64, dense, noted, raw bool) {
	sl, k := st.slot(b)
	var sum uint64
	switch {
	case dense:
		sum = st.key.Sum(b)&^3 | 1
		if raw {
			sum |= 2
		}
	case !noted:
		if sl.key.Load() == k {
			sl.key.Store(0)
		}
		return
	}
	sl.key.Store(k)
	sl.sum.Store(sum)
	sl.kind.Store(t + 1)
}

// get looks b up. It returns the type slot plus one of the record for b, or
// 0, whether b is stamped, and whether it is stamped as a RawMessage value.
func (st *stampTable) get(b []byte) (kind uint64, ok, raw bool) {
	sl, k := st.slot(b)
	if sl.key.Load() != k {
		return 0, false, false
	}
	sum, kind := sl.sum.Load(), sl.kind.Load()
	ok = sum != 0 && st.key.Sum(b)&^3|1 == sum&^2
	return kind, ok, ok && sum&2 != 0
}

// mark sets the state of type slot kind-1 to s.
func (st *stampTable) mark(kind uint64, s uint32) {
	if t := &st.kinds[(kind-1)%stampKinds]; t.Load() != s {
		t.Store(s)
	}
}

// stamp stamps or notes b, the output of Marshal for a value whose type is
// rt.
func (e *encodeState) stamp(b []byte, rt unsafe.Pointer) {
	t := kindOf(rt)
	if encStamps.kinds[t].Load() == stampUnmarked {
		// Most bytes of a type not seen to come back are left alone: a
		// record left at their place may make the encoder or Unmarshal
		// mark some type as coming back, which stamps it for nothing, as
		// its hash does not match these bytes.
		if e.notes++; e.notes%stampNote == 0 {
			encStamps.put(b, t, false, true, false)
		}
		return
	}
	encStamps.put(b, t, jsonsimd.FastSum() && len(b) < stampHashMax || (len(b)-e.long)*4 >= len(b), false, false)
}

// stamped reports whether b, the output of a method or a RawMessage, is
// stamped, as Marshal output or as a RawMessage value the decoder checked,
// and marks the type of noted bytes.
func stamped(b []byte) (ok, raw bool) {
	kind, ok, raw := encStamps.get(b)
	if kind != 0 && !ok {
		encStamps.mark(kind, stampDense)
	}
	return ok, raw
}

// stampRaw stamps b, a RawMessage value the decoder stored, when stamp is
// set and the encoder takes such values back, or notes it.
func (d *decodeState) stampRaw(b []byte, stamp bool) {
	if len(b) < stampMin {
		return
	}
	if encStamps.kinds[rawKind].Load() == stampUnmarked {
		// As in encodeState.stamp, most values are left alone.
		if d.notes++; d.notes%stampNote == 0 {
			encStamps.put(b, rawKind, false, true, true)
		}
		return
	}
	encStamps.put(b, rawKind, stamp, false, true)
}

// stampInput stamps or notes b, the input of an UnmarshalJSON method whose
// itab is tab.
func (d *decodeState) stampInput(b []byte, tab unsafe.Pointer) {
	t := kindOf(tab)
	switch decStamps.kinds[t].Load() {
	case stampUnmarked:
		if d.notes++; d.notes%stampNote == 0 {
			decStamps.put(b, t, false, true, false)
		}
	case stampDense:
		decStamps.put(b, t, true, false, false)
	default:
		decStamps.put(b, t, false, true, false)
	}
}

// inputStamped looks up data, the input of Unmarshal, and reports whether
// it is stamped. When data was recorded, d.stampKind tells its type to
// stampLearn.
func (d *decodeState) inputStamped(data []byte) bool {
	kind, ok, _ := decStamps.get(data)
	d.stampKind = kind
	return ok
}

// stampLearn sets the state of the type of the input recorded, of n bytes,
// from the strings of the decode.
func (d *decodeState) stampLearn(n int) {
	s := uint32(stampDense)
	if (n-d.long)*4 < n {
		s = stampSparse
	}
	decStamps.mark(d.stampKind, s)
	d.stampKind = 0
}
