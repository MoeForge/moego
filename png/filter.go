package png

// PNG filter types.
const (
	ftNone    = 0
	ftSub     = 1
	ftUp      = 2
	ftAverage = 3
	ftPaeth   = 4
)

// abs8 is the magnitude of a residual byte read as a signed int8, the cost
// of one byte in the minimum-sum-of-absolute-differences heuristic.
func abs8(d uint8) uint64 {
	v := int32(int8(d))
	m := v >> 31
	return uint64((v ^ m) - m)
}

// paeth is the Paeth predictor of the PNG specification: whichever of a
// (left), b (above) and c (above left) is closest to a + b - c, preferring
// a, then b.
func paeth(a, b, c uint8) uint8 {
	pa := int32(b) - int32(c)
	pb := int32(a) - int32(c)
	pc := pa + pb
	pa = pa ^ pa>>31 - pa>>31
	pb = pb ^ pb>>31 - pb>>31
	pc = pc ^ pc>>31 - pc>>31
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}

// costRange adds to c the sum of abs8 of each filter's residuals for bytes
// lo to hi of row cur below prev, with bpp bytes per pixel.
func costRange(cur, prev []byte, bpp, lo, hi int, c *[5]uint64) {
	s0, s1, s2, s3, s4 := c[0], c[1], c[2], c[3], c[4]
	i := lo
	for ; i < bpp && i < hi; i++ {
		x, b := cur[i], prev[i]
		s0 += abs8(x)
		s1 += abs8(x)
		s2 += abs8(x - b)
		s3 += abs8(x - b>>1)
		s4 += abs8(x - b)
	}
	for ; i < hi; i++ {
		x, a, b, c := cur[i], cur[i-bpp], prev[i], prev[i-bpp]
		s0 += abs8(x)
		s1 += abs8(x - a)
		s2 += abs8(x - b)
		s3 += abs8(x - uint8((uint32(a)+uint32(b))>>1))
		s4 += abs8(x - paeth(a, b, c))
	}
	c[0], c[1], c[2], c[3], c[4] = s0, s1, s2, s3, s4
}

// filterRange writes filter f's residuals of bytes lo to hi of row cur
// below prev into dst.
func filterRange(dst, cur, prev []byte, bpp, lo, hi int, f uint8) {
	switch f {
	case ftNone:
		copy(dst[lo:hi], cur[lo:hi])
	case ftSub:
		i := lo
		for ; i < bpp && i < hi; i++ {
			dst[i] = cur[i]
		}
		for ; i < hi; i++ {
			dst[i] = cur[i] - cur[i-bpp]
		}
	case ftUp:
		for i := lo; i < hi; i++ {
			dst[i] = cur[i] - prev[i]
		}
	case ftAverage:
		i := lo
		for ; i < bpp && i < hi; i++ {
			dst[i] = cur[i] - prev[i]>>1
		}
		for ; i < hi; i++ {
			dst[i] = cur[i] - uint8((uint32(cur[i-bpp])+uint32(prev[i]))>>1)
		}
	case ftPaeth:
		i := lo
		for ; i < bpp && i < hi; i++ {
			dst[i] = cur[i] - prev[i]
		}
		for ; i < hi; i++ {
			dst[i] = cur[i] - paeth(cur[i-bpp], prev[i], prev[i-bpp])
		}
	}
}

// filterCostsGeneric returns the cost of each filter for row cur below
// prev.
func filterCostsGeneric(cur, prev []byte, bpp int) (c [5]uint64) {
	costRange(cur, prev[:len(cur)], bpp, 0, len(cur), &c)
	return c
}

// filterRowGeneric writes filter f of row cur below prev into dst.
func filterRowGeneric(dst, cur, prev []byte, bpp int, f uint8) {
	n := len(cur)
	filterRange(dst[:n], cur, prev[:n], bpp, 0, n, f)
}

// pickFilter returns the filter in mask with the smallest cost, the lowest
// filter type on ties.
func pickFilter(c *[5]uint64, mask uint8) uint8 {
	best, f := ^uint64(0), uint8(0)
	for i := uint8(0); i < 5; i++ {
		if mask>>i&1 != 0 && c[i] < best {
			best, f = c[i], i
		}
	}
	return f
}
