//go:build arm64 && !purego

package png

//go:noescape
func costsNEON(cur, prev *byte, n, bpp int, c *[5]uint64) int

//go:noescape
func filterNEON(dst, cur, prev *byte, n, bpp, f int) int

//go:noescape
func rgbaFlagsNEON(p *byte, n int) (opaque, gray bool)

//go:noescape
func toRGBNEON(dst, src *byte, n int)

//go:noescape
func toGANEON(dst, src *byte, n int)

//go:noescape
func toGNEON(dst, src *byte, n int)

// filterCosts returns the cost of each filter for row cur below prev.
func filterCosts(cur, prev []byte, bpp int) (c [5]uint64) {
	n := len(cur)
	prev = prev[:n]
	if n < bpp+16 {
		costRange(cur, prev, bpp, 0, n, &c)
		return c
	}
	end := costsNEON(&cur[0], &prev[0], n, bpp, &c)
	costRange(cur, prev, bpp, 0, bpp, &c)
	costRange(cur, prev, bpp, end, n, &c)
	return c
}

// filterRow writes filter f of row cur below prev into dst.
func filterRow(dst, cur, prev []byte, bpp int, f uint8) {
	n := len(cur)
	dst, prev = dst[:n], prev[:n]
	if n < bpp+16 || f == ftNone {
		filterRange(dst, cur, prev, bpp, 0, n, f)
		return
	}
	end := filterNEON(&dst[0], &cur[0], &prev[0], n, bpp, int(f))
	filterRange(dst, cur, prev, bpp, 0, bpp, f)
	filterRange(dst, cur, prev, bpp, end, n, f)
}

// rgbaFlags reports whether every pixel of an RGBA8 row is opaque and
// whether every pixel is gray.
func rgbaFlags(p []byte) (opaque, gray bool) {
	n := len(p) &^ 63
	if n == 0 {
		return rgbaFlagsGeneric(p)
	}
	opaque, gray = rgbaFlagsNEON(&p[0], n)
	o, g := rgbaFlagsGeneric(p[n:])
	return opaque && o, gray && g
}

// toRGB writes the RGB of an RGBA8 row.
func toRGB(d, p []byte) {
	n := len(p) / 4 &^ 15
	if n > 0 {
		_ = d[3*n-1]
		toRGBNEON(&d[0], &p[0], n)
	}
	toRGBGeneric(d[3*n:], p[4*n:])
}

// toGA writes the R and A of an RGBA8 row.
func toGA(d, p []byte) {
	n := len(p) / 4 &^ 15
	if n > 0 {
		_ = d[2*n-1]
		toGANEON(&d[0], &p[0], n)
	}
	toGAGeneric(d[2*n:], p[4*n:])
}

// toG writes the R of an RGBA8 row.
func toG(d, p []byte) {
	n := len(p) / 4 &^ 15
	if n > 0 {
		_ = d[n-1]
		toGNEON(&d[0], &p[0], n)
	}
	toGGeneric(d[n:], p[4*n:])
}
