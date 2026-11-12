package json

import (
	"unsafe"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// Decoded short strings, RawMessage copies, Number literals and pointed-to
// scalars such as *bool or *float64 are carved from per-call chunks of
// pointer-free memory, so one Unmarshal makes few allocations. For an
// input of up to slabChunkMax bytes, a chunk is as large as the input (but
// at least slabChunkMin). For a longer input, the first chunk has room for
// what the first value needs only, as short plain strings and RawMessage
// values are copied into one block when the decode ends; the second has
// slabChunkFirst bytes and each next one twice as many, up to slabChunkMax.
// Keeping one such value alive keeps its chunk alive. Strings of slabMax
// bytes or more get an allocation of their own.
const (
	slabMax        = 1024
	slabChunkFirst = 256
	slabChunkMax   = 4096
	slabChunkMin   = 32
)

// newChunk starts a new chunk with room for n bytes. What is left to
// decode needs less than the rest of the input, whose keys and punctuation
// are not copied, so the chunk takes the largest allocation size class
// within the rest of the input, and none of it goes to rounding. A chunk,
// whose size class is a multiple of 8, is aligned for any scalar at both
// ends. Strings take bytes from its start, and scalars
// blocks of a multiple of 8 bytes from its end, so that neither needs
// padding.
func (d *decodeState) newChunk(n int) { d.newChunkFor(n, len(d.s)-d.i) }

// newChunkFor starts a new chunk with room for n bytes, when what is left
// to decode needs at most rest bytes.
func (d *decodeState) newChunkFor(n, rest int) {
	long, first := len(d.data) > slabChunkMax, d.slabBuf == nil
	size := classBelow(min(int(d.slabChunk), max(rest, slabChunkMin)))
	if first {
		if hint := d.memo.slabHint(d.root); hint > 0 {
			// The last decode into this type took hint bytes: a chunk of
			// that size, if the rest of a short input could need more.
			if h := classAbove(min(hint, slabChunkMax)); long || h < size {
				size = h
			}
		} else if long {
			size = 8
		}
	} else {
		d.slabSpent = min(d.slabSpent+int32(len(d.slabBuf)-d.slabLeft()), slabSpentMax)
	}
	if size < n {
		size = classAbove(n)
	}
	d.slabBuf, d.slabLo, d.slabHi = makeNoZero(size), 0, int32(size)
	if long && !first {
		d.slabChunk = min(2*d.slabChunk, slabChunkMax)
	}
}

// slabSpentMax bounds d.slabSpent, which only needs to tell whether a
// decode took more than one chunk can hold.
const slabSpentMax = 1 << 20

// slabTaken returns the slab bytes that the decode took so far, with the
// strings that finishStrings is to copy.
func (d *decodeState) slabTaken() int {
	n := int(d.slabSpent) + d.pendBytes
	if d.slabBuf != nil {
		n += len(d.slabBuf) - d.slabLeft()
	}
	return n
}

// sizeClasses are the sizes, up to slabChunkMax, that the Go allocator
// rounds a small allocation up to.
var sizeClasses = [...]uint16{8, 16, 24, 32, 48, 64, 80, 96, 112, 128, 144, 160,
	176, 192, 208, 224, 240, 256, 288, 320, 352, 384, 416, 448, 480, 512, 576,
	640, 704, 768, 896, 1024, 1152, 1280, 1408, 1536, 1792, 2048, 2304, 2688,
	3072, 3200, 3456, 4096}

// classBelows[k] and classAboves[k] are the largest size class of at most
// 8k bytes and the smallest one of at least 8k bytes; size classes are
// multiples of 8.
var classBelows, classAboves = func() (below, above [slabChunkMax/8 + 1]uint16) {
	for k := range below {
		below[k], above[k] = 8, slabChunkMax
		for _, s := range sizeClasses {
			if int(s) <= 8*k {
				below[k] = s
			}
		}
		for j := len(sizeClasses) - 1; j >= 0; j-- {
			if s := sizeClasses[j]; int(s) >= 8*k {
				above[k] = s
			}
		}
	}
	return
}()

// classBelow returns the largest size class of at most n bytes, for n of
// at least 8.
func classBelow(n int) int {
	if n >= slabChunkMax {
		return slabChunkMax
	}
	return int(classBelows[n>>3])
}

// classAbove returns the smallest size class of at least n bytes, for n
// of at most slabChunkMax.
func classAbove(n int) int {
	if uint(n) > slabChunkMax {
		return n
	}
	return int(classAboves[(n+7)>>3])
}

// alloc returns n bytes of pointer-free memory with unspecified contents.
func (d *decodeState) alloc(n int) []byte {
	if n >= slabMax || n > int(d.slabChunk) {
		if n >= slabMax {
			d.long += n
		}
		return d.bigBlock(n)
	}
	if n > d.slabLeft() {
		d.newChunk(n)
	}
	return d.slabTake(n)
}

// slabLeft returns the bytes left in the slab chunk.
func (d *decodeState) slabLeft() int { return int(d.slabHi - d.slabLo) }

// slabTake returns the next n bytes of the slab chunk, which has room for
// them.
func (d *decodeState) slabTake(n int) []byte {
	lo := int(d.slabLo)
	d.slabLo += int32(n)
	return d.slabBuf[lo : lo+n : lo+n]
}

// slabAlloc returns n zero bytes aligned for any scalar.
func (d *decodeState) slabAlloc(n int) unsafe.Pointer {
	p := d.slabAllocNoZero(n)
	clear(unsafe.Slice((*byte)(p), n))
	return p
}

// slabAllocNoZero returns n bytes with unspecified contents, aligned for
// any scalar, from the end of the slab.
func (d *decodeState) slabAllocNoZero(n int) unsafe.Pointer {
	r := (n + 7) &^ 7
	if r > d.slabLeft() {
		if r > int(d.slabChunk) {
			return unsafe.Pointer(unsafe.SliceData(makeNoZero(n)))
		}
		d.newChunk(r)
	}
	d.slabHi -= int32(r)
	return unsafe.Pointer(&d.slabBuf[d.slabHi])
}

// copyString returns a copy of d.s[start:end], which may be interned, or
// the input itself for UnmarshalNoCopy.
func (d *decodeState) copyString(start, end int) string {
	if start == end {
		return ""
	}
	if d.noCopy {
		if end-start >= slabMax {
			d.long += end - start
		}
		return d.s[start:end]
	}
	if end-start <= internMax {
		if p := d.intern(d.s[start:end]); p != nil {
			return *(*string)(p)
		}
	}
	b := d.alloc(end - start)
	copyLong(b, d.s[start:end])
	return bytesView(b)
}

// ownString returns a copy of b as a string, which may be interned.
func (d *decodeState) ownString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if len(b) <= internMax && !d.noCopy {
		if p := d.intern(bytesView(b)); p != nil {
			return *(*string)(p)
		}
	}
	c := d.alloc(len(b))
	copyLong(c, bytesView(b))
	return bytesView(c)
}

// copyLongMin is the length from which copyLong writes around the caches.
// A copy that long goes to new memory and does not fit in the core's
// cache: stores around the caches skip reading each line of it before
// writing it, and leave the input in them.
const copyLongMin = 256 << 10

// copyLong copies s to b, which has room for it.
func copyLong(b []byte, s string) {
	if len(s) >= copyLongMin {
		jsonsimd.CopyLong(b, s)
		return
	}
	copy(b, s)
}

// bytesView returns b as a string without copying. The caller must not
// modify b while the string is in use.
func bytesView(b []byte) string { return unsafe.String(unsafe.SliceData(b), len(b)) }

// stringView returns s as a byte slice without copying. It must not be
// modified.
func stringView(s string) []byte { return unsafe.Slice(unsafe.StringData(s), len(s)) }

func load64(s string, i int) uint64 { return jsonsimd.Load64(s, i) }
