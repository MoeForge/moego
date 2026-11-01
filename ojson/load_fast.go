//go:build amd64 || arm64

package ojson

import "unsafe"

// load64 returns the eight bytes at s[i:] as a little-endian word, so the
// lowest byte is s[i]. The caller ensures i+8 <= len(s).
func load64(s string, i int) uint64 {
	return *(*uint64)(unsafe.Add(unsafe.Pointer(unsafe.StringData(s)), i))
}
