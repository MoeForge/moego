//go:build !amd64 && !arm64

package deflate

import "encoding/binary"

// load32 returns the four bytes at b[i:] as a little-endian word. The
// caller ensures i >= 0 and i+4 <= cap(b).
func load32(b []byte, i int) uint32 { return binary.LittleEndian.Uint32(b[i : i+4 : i+4]) }

// load64 is load32 for eight bytes. The caller ensures i+8 <= cap(b).
func load64(b []byte, i int) uint64 { return binary.LittleEndian.Uint64(b[i : i+8 : i+8]) }
