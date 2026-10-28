//go:build arm64 && !purego

package base64

// Inputs from these lengths use the NEON kernels.
const encodeMin, decodeMin = 48, 64

// encodeBlocks encodes complete 12-byte groups, returning source bytes consumed.
//
//go:noescape
func encodeBlocks(dst, src []byte, alphabet *[64]byte) int

// decodeBlocks decodes complete 64-byte groups. It stops before any group with
// padding, whitespace, or invalid input so the scalar decoder can report errors.
//
//go:noescape
func decodeBlocks(dst, src []byte, url bool) (consumed int)

// The alphabet spans ASCII 43..122. A 64-byte table covers the first
// interval and a 16-byte extension covers k..z. Valid entries have bit 7
// set; invalid and out-of-range lookups produce zero.
var decodeTableStd = makeDecodeTable(standardAlphabet)
var decodeTableURL = makeDecodeTable(urlAlphabet)

func makeDecodeTable(alphabet string) [80]byte {
	var table [80]byte
	for i := range alphabet {
		table[alphabet[i]-43] = byte(i) | 128
	}
	return table
}
