//go:build !amd64 || purego

package jsonsimd

import "unsafe"

// CopyShort leaves every byte to the caller.
func CopyShort(dst []byte, s string, html bool) bool { return false }

// CopyShortAVX2 leaves every byte to the caller.
func CopyShortAVX2(dst, src *byte, n int, html bool) bool { return false }

// CopyPlainBlocks leaves every byte to the caller.
func CopyPlainBlocks(dst []byte, s string, html bool) int { return 0 }

// CopyLong copies s to dst.
func CopyLong(dst []byte, s string) { copy(dst, s) }

// ZeroAVX2 is not called without AVX2.
func ZeroAVX2(p unsafe.Pointer, n uintptr) bool { panic("jsonsimd: ZeroAVX2 without AVX2") }
