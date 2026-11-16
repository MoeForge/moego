package vp8l

import "math/bits"

const (
	numLiteral     = 256 // values of one color channel
	numLengthCodes = 24  // length prefix codes; lengths go up to 4096
	numDistCodes   = 40  // distance prefix codes
	maxCacheBits   = 11
	// The green alphabet: green values, then length prefix codes, then
	// color cache indices.
	greenBase = numLiteral + numLengthCodes
	maxGreen  = greenBase + 1<<maxCacheBits
	maxLength = 4096
	// maxDistCode is the largest distance code the encoder writes. The
	// format reaches 1<<20; one less keeps a code and a length in a
	// uint32 (see token).
	maxDistCode = 1<<20 - 1
	maxDist     = maxDistCode - numNeighbours // the longest plain distance

	numNeighbours = 120 // distance codes 1..120 name nearby pixels

	maxCodeLen     = 15 // longest prefix code
	numCodeLenSyms = 19 // code length code alphabet
	maxCodeLenLen  = 7  // longest code length code
)

// codeLenOrder is the order in which the code length code lengths are
// written.
var codeLenOrder = [numCodeLenSyms]uint8{17, 18, 0, 1, 2, 3, 4, 5, 16, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

// neighbours lists the pixel offsets of distance codes 1..120: columns to
// the left (negative: to the right) and rows up.
var neighbours = [numNeighbours][2]int8{
	{0, 1}, {1, 0}, {1, 1}, {-1, 1}, {0, 2}, {2, 0}, {1, 2},
	{-1, 2}, {2, 1}, {-2, 1}, {2, 2}, {-2, 2}, {0, 3}, {3, 0},
	{1, 3}, {-1, 3}, {3, 1}, {-3, 1}, {2, 3}, {-2, 3}, {3, 2},
	{-3, 2}, {0, 4}, {4, 0}, {1, 4}, {-1, 4}, {4, 1}, {-4, 1},
	{3, 3}, {-3, 3}, {2, 4}, {-2, 4}, {4, 2}, {-4, 2}, {0, 5},
	{3, 4}, {-3, 4}, {4, 3}, {-4, 3}, {5, 0}, {1, 5}, {-1, 5},
	{5, 1}, {-5, 1}, {2, 5}, {-2, 5}, {5, 2}, {-5, 2}, {4, 4},
	{-4, 4}, {3, 5}, {-3, 5}, {5, 3}, {-5, 3}, {0, 6}, {6, 0},
	{1, 6}, {-1, 6}, {6, 1}, {-6, 1}, {2, 6}, {-2, 6}, {6, 2},
	{-6, 2}, {4, 5}, {-4, 5}, {5, 4}, {-5, 4}, {3, 6}, {-3, 6},
	{6, 3}, {-6, 3}, {0, 7}, {7, 0}, {1, 7}, {-1, 7}, {5, 5},
	{-5, 5}, {7, 1}, {-7, 1}, {4, 6}, {-4, 6}, {6, 4}, {-6, 4},
	{2, 7}, {-2, 7}, {7, 2}, {-7, 2}, {3, 7}, {-3, 7}, {7, 3},
	{-7, 3}, {5, 6}, {-5, 6}, {6, 5}, {-6, 5}, {8, 0}, {4, 7},
	{-4, 7}, {7, 4}, {-7, 4}, {8, 1}, {8, 2}, {6, 6}, {-6, 6},
	{8, 3}, {5, 7}, {-5, 7}, {7, 5}, {-7, 5}, {8, 4}, {6, 7},
	{-6, 7}, {7, 6}, {-7, 6}, {8, 5}, {7, 7}, {-7, 7}, {8, 6},
	{8, 7},
}

// prefixOf splits a length or distance code v >= 1 into its prefix code,
// the number of extra bits and their value.
func prefixOf(v uint32) (code uint32, nextra uint, extra uint32) {
	if v <= 4 {
		return v - 1, 0, 0
	}
	d := v - 1
	h := uint(bits.Len32(d)) - 1
	nextra = h - 1
	return uint32(2*h) + (d>>nextra)&1, nextra, d & (1<<nextra - 1)
}

// prefixExtra returns the number of extra bits of prefix code c.
func prefixExtra(c uint32) uint {
	if c < 4 {
		return 0
	}
	return uint(c-2) >> 1
}

// lengthPrefix holds prefixOf(v).code for every length 1..4096 at index
// v-1, so the hot loops need no bit scan.
var lengthPrefix = func() (t [maxLength]uint8) {
	for v := 1; v <= maxLength; v++ {
		c, _, _ := prefixOf(uint32(v))
		t[v-1] = uint8(c)
	}
	return
}()
