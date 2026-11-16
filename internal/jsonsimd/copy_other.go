//go:build (!amd64 && !arm64) || purego

package jsonsimd

// CopyShort leaves every byte to the caller.
func CopyShort(dst []byte, s string, html bool) bool { return false }

// CopyShortFast leaves every byte to the caller.
func CopyShortFast(dst, src *byte, n int, html bool) bool { return false }

// CopyPlainBlocks leaves every byte to the caller.
func CopyPlainBlocks(dst []byte, s string, html bool) int { return 0 }
