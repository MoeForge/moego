//go:build (!amd64 && !arm64) || purego

package ojson

// uglyBlocks leaves every byte to the word loops of modUgly.
func uglyBlocks(out []byte, json string, i, w int, st *uglyState) (int, int) { return i, w }
