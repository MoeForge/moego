//go:build amd64 && !purego

package base64

// Inputs from these lengths use the AVX2 kernels: one 24-byte encode block or
// one 32-byte decode block.
const encodeMin, decodeMin = 24, 32

var haveAVX2 = supportsAVX2()

// supportsAVX2 checks both CPU features and operating-system YMM state support.
//
//go:noescape
func supportsAVX2() bool

// encodeBlocks encodes all whole 3-byte groups that fit in dst when at least
// 24 such bytes exist, returning the source bytes consumed.
func encodeBlocks(dst, src []byte, alphabet *[64]byte) int {
	if !haveAVX2 {
		return 0
	}
	return encodeAVX2(dst, src, alphabet)
}

// decodeBlocks decodes a prefix of whole quanta containing only alphabet
// characters. It writes exactly consumed/4*3 bytes to dst.
func decodeBlocks(dst, src []byte, url bool) int {
	if !haveAVX2 {
		return 0
	}
	return decodeAVX2(dst, src, url)
}

// validBlocks returns a whole 64-byte block prefix containing only alphabet
// characters. Padding and CR/LF terminate the prefix.
func validBlocks(src []byte, url bool) int {
	if !haveAVX2 {
		return 0
	}
	return validAVX2(src, url)
}

//go:noescape
func encodeAVX2(dst, src []byte, alphabet *[64]byte) int

//go:noescape
func decodeAVX2(dst, src []byte, url bool) (consumed int)

//go:noescape
func validAVX2(src []byte, url bool) int
