//go:build (amd64 || arm64) && !purego

package json

import "unsafe"

// appendPadded appends s, which is followed in memory by at least seven
// readable bytes, with word copies.
func appendPadded(b []byte, s string) []byte {
	n := len(s)
	if cap(b)-len(b) < n+8 {
		b = growBytes(b, n+8)
	}
	dst := unsafe.Pointer(unsafe.SliceData(b[len(b):cap(b)]))
	src := unsafe.Pointer(unsafe.StringData(s))
	for i := 0; i < n; i += 8 {
		*(*uint64)(unsafe.Add(dst, i)) = *(*uint64)(unsafe.Add(src, i))
	}
	return b[:len(b)+n]
}

// keyPad is the padding after each member key, which appendKeyBytes may
// read.
const keyPad = 24

// wordCopy tells whether unaligned word loads and stores are used.
const wordCopy = true

// appendKeyBytes appends key, which is followed in memory by keyPad bytes.
func appendKeyBytes(b []byte, key string) []byte {
	n := len(b)
	if uint(len(key)) > keyPad || uint(cap(b)-n) < keyPad {
		return appendPadded(b, key)
	}
	d := unsafe.Add(unsafe.Pointer(unsafe.SliceData(b)), n)
	k := unsafe.Pointer(unsafe.StringData(key))
	*(*uint64)(d) = *(*uint64)(k)
	*(*uint64)(unsafe.Add(d, 8)) = *(*uint64)(unsafe.Add(k, 8))
	*(*uint64)(unsafe.Add(d, 16)) = *(*uint64)(unsafe.Add(k, 16))
	return b[:n+len(key)]
}

// storeWord writes the eight bytes of w at b[i:], which has room for them.
func storeWord(b []byte, i int, w uint64) {
	*(*uint64)(unsafe.Pointer(unsafe.SliceData(b[i : i+8]))) = w
}
