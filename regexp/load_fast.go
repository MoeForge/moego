//go:build (amd64 || arm64) && !purego

package regexp

import "unsafe"

// load64 returns the 8 bytes at s[o:] as a little-endian word; o+8 <=
// len(s). These architectures allow unaligned loads.
func load64(s string, o int) uint64 {
	return *(*uint64)(unsafe.Add(unsafe.Pointer(unsafe.StringData(s)), o))
}
