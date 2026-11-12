package json

import (
	"math/bits"
	"math/rand/v2"
	"unsafe"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// Short strings that a decode stores are interned. Each decoder state has
// an intern table, in its memo made when first needed, of internSets sets of
// internWays entries. An entry remembers the hash of a string met there
// and may keep that string: a string met while its hash is remembered is
// kept, and a decode that meets it once more stores the kept string rather
// than a copy of its own, so that the names and values inputs repeat cost
// no memory. A new hash takes the place of the least recently met entry of
// its set that keeps no string, or else of one whose string was not met
// for internAge meets. A kept string is a block of pointer-free memory,
// its header then its bytes, which is never written again: it serves as a
// string and as the data of an interface value holding one. The kept
// blocks of a table take at most internKeep bytes. UnmarshalNoCopy does
// not intern.
const (
	internSets = 32
	internWays = 4
	internMax  = 256 // longest string interned
	internKeep = 4 << 10
	internAge  = 4096
)

// decodeMemo is what a decoder state learns from its decodes: the intern
// table, and for a few root types the slab memory their last decode took.
type decodeMemo struct {
	tag   [internSets][internWays]uint32         // low half of the hash of each entry
	met   [internSets][internWays]uint32         // when each entry was last met
	blk   [internSets][internWays]unsafe.Pointer // the string each entry keeps, or nil
	clock uint32
	kept  int         // bytes of the kept blocks
	slab  [8]slabNote // in no order
	noted uint32      // counts the notes made
	sures int32       // the notes that are sure
	first int32       // the pages of the first large block of this decode
	spare []byte      // the block spareFor made for it, or nil
}

// slabNote is the slab memory that the last decode into root took, and the
// pages of its first large block, which sure says the decode before took
// too.
type slabNote struct {
	root uintptr
	n    int32
	big  int32
	at   uint32 // the count of notes when it was made
	sure bool
}

// internKey makes the hashes of a process its own.
var internKey = rand.Uint64() | 1

// internHash hashes s, of 1 to internMax bytes.
func internHash(s string) uint64 {
	n := len(s)
	h := uint64(n) ^ internKey
	if n < 8 {
		var a uint64
		if n >= 4 {
			a = uint64(jsonsimd.Load32(s, 0))<<32 | uint64(jsonsimd.Load32(s, n-4))
		} else {
			a = uint64(s[0])<<16 | uint64(s[n>>1])<<8 | uint64(s[n-1])
		}
		return mix64(a^internKey<<7, h)
	}
	i := 0
	for ; n-i > 16; i += 16 {
		h = mix64(load64(s, i)^internKey<<7, load64(s, i+8)^h)
	}
	if n-i > 8 {
		return mix64(load64(s, i)^internKey<<7, load64(s, n-8)^h)
	}
	return mix64(load64(s, n-8)^internKey<<7, h)
}

// mix64 folds the 128-bit product of a and b to 64 bits.
func mix64(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	return hi ^ lo
}

// intern returns the block of a kept string equal to s, of 1 to internMax
// bytes, or nil.
func (d *decodeState) intern(s string) unsafe.Pointer {
	t := d.memo
	if t == nil {
		t = new(decodeMemo)
		d.memo = t
	}
	h := internHash(s)
	k, tag := h>>59, uint32(h)
	t.clock++
	tags, met := &t.tag[k], &t.met[k]
	for w := range internWays {
		if tags[w] != tag {
			continue
		}
		met[w] = t.clock
		b := t.blk[k][w]
		if b == nil {
			b = t.keep(s)
			if b != nil {
				t.blk[k][w] = b
			}
			return b
		}
		if *(*string)(b) != s {
			// Another string of the same hash: neither is kept.
			t.drop(k, w)
			return nil
		}
		return b
	}
	// Met for the first time. Each entry has a key, made without branches
	// to guess, that is least for the entry to take: the least recently met
	// of those that keep no string, or else of all.
	least := t.order(k, 0)
	for v := 1; v < internWays; v++ {
		x := t.order(k, v)
		least += (x - least) & uint64(int64(x-least)>>63)
	}
	w := int(least & (internWays - 1))
	if least>>62 != 0 {
		if t.clock-met[w] < internAge {
			return nil
		}
		t.drop(k, w)
	}
	tags[w], met[w] = tag, t.clock
	return nil
}

// order returns the key of entry w of set k for intern, below 1<<63: bit
// 62 set when it keeps a string, then less the longer ago it was met, then
// w.
func (t *decodeMemo) order(k uint64, w int) uint64 {
	p := uint64(uintptr(t.blk[k][w]))
	return (p|-p)>>63<<62 | uint64(t.met[k][w]-t.clock)<<2 | uint64(w)
}

// keep returns a new block that keeps a copy of s, or nil when the kept
// blocks would take more than internKeep bytes.
func (t *decodeMemo) keep(s string) unsafe.Pointer {
	size := classAbove(16 + len(s))
	if t.kept+size > internKeep {
		return nil
	}
	t.kept += size
	buf := makeNoZero(size)
	copy(buf[16:], s)
	p := unsafe.Pointer(&buf[0])
	*(*uintptr)(p) = uintptr(unsafe.Pointer(&buf[16]))
	*(*int)(unsafe.Add(p, 8)) = len(s)
	return p
}

// drop lets entry w of set k keep no string.
func (t *decodeMemo) drop(k uint64, w int) {
	if p := t.blk[k][w]; p != nil {
		t.kept -= classAbove(16 + len(*(*string)(p)))
		t.blk[k][w] = nil
	}
}

// internAny returns the kept string of block p as an interface value.
func internAny(p unsafe.Pointer) any {
	var x any
	*(*eface)(unsafe.Pointer(&x)) = eface{stringRtype, p}
	return x
}

// slabHint returns the slab bytes that the last decode into root took, or
// 0 when m holds none.
func (m *decodeMemo) slabHint(root unsafe.Pointer) int {
	if m == nil {
		return 0
	}
	if e := m.note(root); e != nil {
		return int(e.n)
	}
	return 0
}

// note returns the note of root, or nil. A decode into one type may
// decode into others from the methods of its types, on other states that
// swap roles with it from call to call, so each state notes a few types.
func (m *decodeMemo) note(root unsafe.Pointer) *slabNote {
	for k := range m.slab {
		if e := &m.slab[k]; e.root == uintptr(root) {
			return e
		}
	}
	return nil
}

// noteSlab notes that the decode into root took n slab bytes, and what
// noteBig notes.
func (m *decodeMemo) noteSlab(root unsafe.Pointer, n int) {
	n = min(n, slabSpentMax)
	m.noted++
	e := m.note(root)
	if e == nil {
		// The note made longest ago gives way.
		e = &m.slab[0]
		for k := 1; k < len(m.slab); k++ {
			if m.noted-m.slab[k].at > m.noted-e.at {
				e = &m.slab[k]
			}
		}
		if e.sure {
			m.sures--
		}
		*e = slabNote{root: uintptr(root)}
	}
	e.n, e.at = int32(n), m.noted
	if m.first != 0 || e.big != 0 {
		m.noteBig(e)
	}
}

// noteBig notes the pages of the first large block of the decode that e
// notes, and drops the spare.
func (m *decodeMemo) noteBig(e *slabNote) {
	if sure := m.first != 0 && m.first == e.big; sure != e.sure {
		e.sure = sure
		if sure {
			m.sures++
		} else {
			m.sures--
		}
	}
	e.big, m.first = m.first, 0
	if m.spare != nil {
		m.spare = nil
	}
}

// Large blocks are those of more than bigMin bytes, which the allocator
// gives whole pages of bigPage bytes. It samples them for the heap profile
// often, and records the stack then, which takes longer the deeper the
// stack is. The first large block of a decode, as for a long string deep
// in the value, comes from a spare that run makes, where the stack is
// shallow, when the last two decodes into the root type took a first
// large block of the same pages. Blocks of up to bigLimit bytes are
// predicted.
const (
	bigMin   = 32 << 10
	bigPage  = 8 << 10
	bigLimit = 1 << 30
)

// spareFor starts a decode into root, when some note is sure: it makes the
// spare when the last two decodes into root took a first large block of
// the same pages.
func (m *decodeMemo) spareFor(root unsafe.Pointer) {
	if e := m.note(root); e != nil && e.sure {
		m.spare = makeNoZero(int(e.big))
	}
}

// bigBlock returns n bytes of pointer-free memory with unspecified
// contents, from the spare when it is the first large block of the decode
// and of the spare's pages.
func (d *decodeState) bigBlock(n int) []byte {
	if n > bigMin && n <= bigLimit && d.stream == nil {
		m := d.memo
		if m == nil {
			m = new(decodeMemo)
			d.memo = m
		}
		if m.first == 0 {
			m.first = int32((n + bigPage - 1) &^ (bigPage - 1))
			if b := m.spare; len(b) == int(m.first) {
				m.spare = nil
				return b[:n:n]
			}
		}
	}
	return makeNoZero(n)
}
