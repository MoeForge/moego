//go:build !amd64 || purego

package jsonsimd

// EscapeBlocks leaves every byte to the caller.
func EscapeBlocks(out []byte, s string, i, w int, html bool) (int, int) { return i, w }

// UnescapeBlocks leaves every byte to the caller.
func UnescapeBlocks(out []byte, s string, i, w int) (int, int, bool) { return i, w, false }

// EscapedLen leaves s to the caller.
func EscapedLen(s string, html bool) int { return -1 }

// CheckUTF8 returns 0 when s is valid UTF-8 without U+2028 or U+2029,
// else UTF8Invalid, UTF8LineSep or both.
func CheckUTF8(s string) int { return checkUTF8(s) }
