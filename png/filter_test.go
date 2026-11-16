package png

import (
	"bytes"
	"math/rand"
	"testing"
)

var filterBpps = []int{1, 2, 3, 4, 6, 8}

// rowPair returns a row and the row above it with the given content.
func rowPair(rng *rand.Rand, n, mode int) (cur, prev []byte) {
	cur, prev = make([]byte, n), make([]byte, n)
	for i := range cur {
		switch mode {
		case 0: // random
			cur[i], prev[i] = byte(rng.Intn(256)), byte(rng.Intn(256))
		case 1: // extremes, where saturation and sign handling matter
			v := []byte{0, 1, 127, 128, 129, 254, 255}
			cur[i], prev[i] = v[rng.Intn(len(v))], v[rng.Intn(len(v))]
		case 2: // smooth
			cur[i] = byte(i/3 + rng.Intn(3))
			prev[i] = byte(i/3 + rng.Intn(3))
		}
	}
	return cur, prev
}

// TestFilterKernels compares the dispatched filter costs and residuals
// with the portable code for every row length up to 300 and every bpp.
func TestFilterKernels(t *testing.T) {
	requireKernels(t)
	rng := rand.New(rand.NewSource(6))
	for _, bpp := range filterBpps {
		for n := 1; n <= 300; n++ {
			for mode := 0; mode < 3; mode++ {
				cur, prev := rowPair(rng, n, mode)
				checkFilters(t, cur, prev, bpp)
			}
		}
	}
}

// TestPaethExhaustive checks the kernels' Paeth predictor on every (a, b,
// c) triple.
func TestPaethExhaustive(t *testing.T) {
	requireKernels(t)
	const n = 256 * 256
	for c := 0; c < 256; c++ {
		// bpp 1 row: cur[i-1] = a, prev[i] = b, prev[i-1] = c, so lay out
		// pairs (a, x) and (c, b).
		cur, prev := make([]byte, 2*n), make([]byte, 2*n)
		for a := 0; a < 256; a++ {
			for b := 0; b < 256; b++ {
				k := 2 * (a*256 + b)
				cur[k], cur[k+1] = byte(a), byte(a*7+b)
				prev[k], prev[k+1] = byte(c), byte(b)
			}
		}
		got := make([]byte, len(cur))
		want := make([]byte, len(cur))
		filterRow(got, cur, prev, 1, ftPaeth)
		filterRowGeneric(want, cur, prev, 1, ftPaeth)
		for k := 1; k < len(cur); k += 2 {
			if got[k] != want[k] {
				a, b := int(cur[k-1]), int(prev[k])
				t.Fatalf("Paeth(%d, %d, %d): residual %d, want %d", a, b, c, got[k], want[k])
			}
		}
	}
}

func checkFilters(t *testing.T, cur, prev []byte, bpp int) {
	t.Helper()
	got, want := filterCosts(cur, prev, bpp), filterCostsGeneric(cur, prev, bpp)
	if got != want {
		t.Fatalf("len %d bpp %d: costs %v, want %v", len(cur), bpp, got, want)
	}
	d1, d2 := make([]byte, len(cur)), make([]byte, len(cur))
	for f := uint8(0); f < 5; f++ {
		filterRow(d1, cur, prev, bpp, f)
		filterRowGeneric(d2, cur, prev, bpp, f)
		if !bytes.Equal(d1, d2) {
			t.Fatalf("len %d bpp %d filter %d: residuals differ", len(cur), bpp, f)
		}
	}
}

// TestPaethReference checks paeth against the specification's formula.
func TestPaethReference(t *testing.T) {
	abs := func(v int) int {
		if v < 0 {
			return -v
		}
		return v
	}
	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
			for c := 0; c < 256; c++ {
				p := a + b - c
				pa, pb, pc := abs(p-a), abs(p-b), abs(p-c)
				want := c
				if pa <= pb && pa <= pc {
					want = a
				} else if pb <= pc {
					want = b
				}
				if got := paeth(uint8(a), uint8(b), uint8(c)); int(got) != want {
					t.Fatalf("paeth(%d, %d, %d) = %d, want %d", a, b, c, got, want)
				}
			}
		}
	}
}
