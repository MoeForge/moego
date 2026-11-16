//go:build amd64 || arm64

package deflate

import "unsafe"

// load32 returns the four bytes at b[i:] as a little-endian word. The
// caller ensures i >= 0 and i+4 <= cap(b).
func load32(b []byte, i int) uint32 {
	return *(*uint32)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(b)), i))
}

// load64 is load32 for eight bytes. The caller ensures i+8 <= cap(b).
func load64(b []byte, i int) uint64 {
	return *(*uint64)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(b)), i))
}
