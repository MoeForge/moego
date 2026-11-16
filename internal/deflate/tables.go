package deflate

import "math/bits"

// DEFLATE format constants (RFC 1951).
const (
	windowSize = 1 << 15 // farthest match distance
	minMatch   = 3
	maxMatch   = 258

	numLitLen  = 286 // literal/length symbols that may appear in a block
	numDist    = 30
	numCodeLen = 19
	endOfBlock = 256

	maxCodeBits    = 15 // literal/length and distance codes
	maxCodeLenBits = 7  // code length code
)

// A token is a literal byte, or a match with its length and distance:
//
//	literal: the byte in bits 0-7, bit 31 clear
//	match:   distance-1 in bits 0-14, length-3 in bits 15-22, the distance
//	         symbol in bits 23-27, bit 31 set
type token = uint32

const matchFlag = 1 << 31

func matchToken(length, dist int) token {
	return matchFlag | uint32(dist-1) | uint32(length-minMatch)<<15 | distSymbol(dist-1)<<23
}

// distSymbol returns the distance symbol of distance d+1, d < 32768. The
// symbols come in pairs that double in range: symbol 2k+2 and 2k+3 cover
// 2^k distances each.
func distSymbol(d int) uint32 {
	if d < 4 {
		return uint32(d)
	}
	n := bits.Len32(uint32(d)) - 2
	return uint32(2*n+2) | uint32(d>>n)&1
}

var (
	// lenSymbol maps length-3 to the length symbol minus 257.
	lenSymbol [256]uint8
	lenBase   [29]uint16 // smallest length-3 of each length symbol
	lenExtra  [29]uint8
	distBase  [numDist]uint16 // smallest distance-1 of each distance symbol
	distExtra [numDist]uint8

	// codeLenOrder is the order in which a dynamic block header sends the
	// code length code lengths.
	codeLenOrder = [numCodeLen]uint8{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}

	// The fixed codes of block type 1.
	fixedLit  codeTable
	fixedDist codeTable
)

func init() {
	base := 0
	for s := 0; s < 28; s++ {
		extra := 0
		if s >= 8 {
			extra = s/4 - 1
		}
		lenBase[s] = uint16(base)
		lenExtra[s] = uint8(extra)
		for i := 0; i < 1<<extra && base+i < 255; i++ {
			lenSymbol[base+i] = uint8(s)
		}
		base += 1 << extra
	}
	// Length 258 has a symbol of its own; symbol 284 stops at 257.
	lenBase[28] = 255
	lenSymbol[255] = 28
	base = 0
	for s := range distBase {
		extra := 0
		if s >= 2 {
			extra = s/2 - 1
		}
		distBase[s] = uint16(base)
		distExtra[s] = uint8(extra)
		base += 1 << extra
	}

	var lens [288]uint8
	for i := range lens {
		switch {
		case i < 144:
			lens[i] = 8
		case i < 256:
			lens[i] = 9
		case i < 280:
			lens[i] = 7
		default:
			lens[i] = 8
		}
	}
	// Symbols 286 and 287 never occur but take part in the code.
	fixedLit.build(lens[:])
	for i := range lens[:numDist] {
		lens[i] = 5
	}
	fixedDist.build(lens[:numDist])
}

// A codeTable holds a canonical prefix code ready to write: per symbol,
// the code bit-reversed for LSB-first output in bits 0-15 and its length
// in bits 16-23.
type codeTable struct {
	code [288]uint32
	lens [288]uint8
}

// build assigns canonical codes (RFC 1951 section 3.2.2) to the lengths.
func (t *codeTable) build(lens []uint8) {
	var count [maxCodeBits + 1]uint16
	for _, l := range lens {
		count[l]++
	}
	count[0] = 0
	var next [maxCodeBits + 1]uint16
	code := uint16(0)
	for b := 1; b <= maxCodeBits; b++ {
		code = (code + count[b-1]) << 1
		next[b] = code
	}
	for i, l := range lens {
		t.lens[i] = l
		if l == 0 {
			t.code[i] = 0
			continue
		}
		t.code[i] = uint32(bits.Reverse16(next[l])>>(16-l)) | uint32(l)<<16
		next[l]++
	}
	for i := len(lens); i < len(t.lens); i++ {
		t.lens[i] = 0
		t.code[i] = 0
	}
}
