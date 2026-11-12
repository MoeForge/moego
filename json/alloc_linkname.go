//go:build (amd64 || arm64) && !purego && !moego_zeroalloc

package json

import (
	"reflect"
	"unsafe"
)

// mallocgc is the runtime allocator. Go keeps this linkname target stable
// because widely used packages depend on it. With typ nil the memory is
// treated as pointer-free.
//
//go:linkname mallocgc runtime.mallocgc
func mallocgc(size uintptr, typ unsafe.Pointer, needzero bool) unsafe.Pointer

// makeNoZero returns n bytes with unspecified contents. Callers must write all
// n bytes before the slice becomes visible.
func makeNoZero(n int) []byte {
	if n == 0 {
		return []byte{}
	}
	return unsafe.Slice((*byte)(mallocgc(uintptr(n), nil, false)), n)
}

// newObject returns a new zero value of type t, whose runtime type is rt
// and size size.
func newObject(t reflect.Type, rt unsafe.Pointer, size uintptr) unsafe.Pointer {
	return mallocgc(size, rt, true)
}

// newArray returns n new zero values of type t in a row.
func newArray(t reflect.Type, rt unsafe.Pointer, size uintptr, n int) unsafe.Pointer {
	return mallocgc(size*uintptr(n), rt, true)
}

//go:linkname typedslicecopy runtime.typedslicecopy
func typedslicecopy(typ unsafe.Pointer, dst unsafe.Pointer, dstLen int, src unsafe.Pointer, srcLen int) int

//go:linkname memclrHasPointers runtime.memclrHasPointers
func memclrHasPointers(p unsafe.Pointer, n uintptr)

// copyElems copies n elements of the slice plan pl from src to dst.
func copyElems(pl *decPlan, dst, src unsafe.Pointer, n int) {
	if pl.elemNoPtr {
		b := uintptr(n) * pl.elem.size
		copy(unsafe.Slice((*byte)(dst), b), unsafe.Slice((*byte)(src), b))
		return
	}
	typedslicecopy(pl.elem.rtyp, dst, n, src, n)
}

// clearElems zeroes n elements of the slice plan pl at p.
func clearElems(pl *decPlan, p unsafe.Pointer, n int) {
	b := uintptr(n) * pl.elem.size
	if b == 0 {
		return
	}
	if pl.elemNoPtr {
		clear(unsafe.Slice((*byte)(p), b))
		return
	}
	memclrHasPointers(p, b)
}
