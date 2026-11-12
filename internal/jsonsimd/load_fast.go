//go:build amd64 || arm64

package jsonsimd

import "unsafe"

// Load64 returns the eight bytes at s[i:] as a little-endian word, so the
// lowest byte is s[i]. The caller ensures i+8 <= len(s).
func Load64(s string, i int) uint64 {
	return *(*uint64)(unsafe.Add(unsafe.Pointer(unsafe.StringData(s)), i))
}

// Load32 is Load64 for four bytes. The caller ensures i+4 <= len(s).
func Load32(s string, i int) uint32 {
	return *(*uint32)(unsafe.Add(unsafe.Pointer(unsafe.StringData(s)), i))
}
