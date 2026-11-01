//go:build !amd64 || purego

package ojson

// quoteCountBlocks leaves every byte to the word loop of quotedLen.
func quoteCountBlocks(s string, i int, html bool) (int, int) { return i, 0 }

// quoteBlocks leaves every byte to the word loop of writeQuoted.
func quoteBlocks(out []byte, s string, i, w int, html bool) (int, int) { return i, w }
