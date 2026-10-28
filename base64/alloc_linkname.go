//go:build (amd64 || arm64) && !purego && !moego_zeroalloc

package base64

import "unsafe"

// mallocgc is the runtime allocator. Go keeps this linkname target stable
// because widely used packages depend on it. typ is nil, so the memory is
// treated as pointer-free.
//
//go:linkname mallocgc runtime.mallocgc
func mallocgc(size uintptr, typ unsafe.Pointer, needzero bool) unsafe.Pointer

// makeNoZero returns n bytes with unspecified contents. Callers must write all
// n bytes, or clear the unwritten ones, before the slice becomes visible.
// Build with moego_zeroalloc to use an ordinary zeroed allocation instead.
func makeNoZero(n int) []byte {
	if n == 0 {
		return []byte{}
	}
	return unsafe.Slice((*byte)(mallocgc(uintptr(n), nil, false)), n)
}
