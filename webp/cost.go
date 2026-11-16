package webp

import (
	"math"
	"math/bits"
)

// Entropy estimates. The transforms are chosen by the Shannon size of
// their output under histograms: for counts n_v summing to N, the total is
// N*log2(N) - sum n_v*log2(n_v) bits.

// log2Tab[i] is log2(i) for small i; larger values use the top bits.
const log2TabBits = 12

var log2Tab [1 << log2TabBits]float32

// xlogxTab[i] is i*log2(i).
var xlogxTab [1 << log2TabBits]float32

func init() {
	for i := 1; i < len(log2Tab); i++ {
		log2Tab[i] = float32(math.Log2(float64(i)))
		xlogxTab[i] = float32(float64(i) * math.Log2(float64(i)))
	}
}

// fastLog2 returns log2(x) for x >= 1, within about 1e-3.
func fastLog2(x uint32) float32 {
	if x < 1<<log2TabBits {
		return log2Tab[x]
	}
	s := uint(bits.Len32(x)) - log2TabBits
	return float32(s) + log2Tab[x>>s]
}

// xlogx returns x*log2(x).
func xlogx(x uint32) float32 {
	if x < 1<<log2TabBits {
		return xlogxTab[x]
	}
	return float32(x) * fastLog2(x)
}

// histBits returns the Shannon size in bits of the symbols counted in h.
func histBits(h []uint32) float64 {
	var n uint32
	var s float64
	for _, c := range h {
		if c != 0 {
			n += c
			s += float64(xlogx(c))
		}
	}
	return float64(xlogx(n)) - s
}
