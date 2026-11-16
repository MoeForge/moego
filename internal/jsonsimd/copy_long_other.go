//go:build !amd64 || purego

package jsonsimd

import "unsafe"

// CopyLong copies s to dst.
func CopyLong(dst []byte, s string) { copy(dst, s) }

// ZeroAVX2 is not called without HaveZero.
func ZeroAVX2(p unsafe.Pointer, n uintptr) bool { panic("jsonsimd: ZeroAVX2 without SIMD") }
