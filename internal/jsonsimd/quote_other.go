//go:build (!amd64 && !arm64) || purego

package jsonsimd

// LazyCopy is false: no string is checked as it is copied.
const LazyCopy = false

// QuoteCountBlocks leaves every byte to the caller's word loop.
func QuoteCountBlocks(s string, i int, html bool) (int, int) { return i, 0 }

// QuoteBlocks leaves every byte to the caller's word loop.
func QuoteBlocks(out []byte, s string, i, w int, html bool) (int, int) { return i, w }
