//go:build (amd64 || arm64) && !purego && !moego_zeroalloc

package ojson

import "unsafe"

// mallocgc is the runtime allocator. Go keeps this linkname target stable
// because widely used packages depend on it. typ is nil, so the memory is
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
