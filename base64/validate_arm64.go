//go:build arm64 && !purego

package base64

// validBlocks returns a whole-block prefix containing only alphabet characters.
// Padding and CR/LF terminate the prefix for the scalar decoder to handle.
//
//go:noescape
func validBlocks(src []byte, url bool) (consumed int)

var validTableStd = makeValidTable(standardAlphabet)
var validTableURL = makeValidTable(urlAlphabet)

func makeValidTable(alphabet string) [32]byte {
	var table [32]byte
	for i := range alphabet {
		c := alphabet[i]
		table[c&15] |= 1 << (c >> 4)
	}
	for i := 0; i < 8; i++ {
		table[16+i] = 1 << i
	}
	return table
}
