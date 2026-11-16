//go:build arm64 && !purego

package webp

//go:noescape
func residualsNEON(m int, dst, cur, top *uint32, n int) int

//go:noescape
func subGreenNEON(dst, src *uint32, n int) int

// residuals writes to dst the residuals of mode m for pixels i to
// i+len(dst)-1 of img, an image w pixels wide; see residualsGeneric.
func residuals(m int, dst, img []uint32, i, w int) {
	n := len(dst)
	if n < 4 {
		residualsGeneric(m, dst, img, i, w)
		return
	}
	// The bounds the kernel reads: img[i-1 : i+n] and img[i-w-1 : i-w+n+1].
	_ = img[i-w-1 : i+n]
	k := residualsNEON(m, &dst[0], &img[i-1], &img[i-w-1], n)
	if k < n {
		// The last block again, overlapping: dst is not img.
		j := i + n - 4
		residualsNEON(m, &dst[n-4], &img[j-1], &img[j-w-1], 4)
	}
}

func subtractGreenRow(dst, src []uint32) {
	n := len(dst)
	src = src[:n]
	if n < 4 {
		subtractGreenRowGeneric(dst, src)
		return
	}
	k := subGreenNEON(&dst[0], &src[0], n)
	subtractGreenRowGeneric(dst[k:], src[k:])
}

//go:noescape
func addGreenNEON(dst, src *uint32, n int) int

//go:noescape
func addTopNEON(m int, cur, top *uint32, n int) int

//go:noescape
func colorTransformNEON(dst, src *uint32, n int, m1, m2 uint32) int

//go:noescape
func undoColorTransformNEON(dst, src *uint32, n int, m1, m2 uint32) int

// ctMults packs element c for the color transform kernels.
func ctMults(c ctElem) (m1, m2 uint32) {
	return uint32(uint16(int16(c.g2r)*4))<<16 | uint32(uint16(int16(c.g2b)*4)), uint32(uint16(int16(c.r2b) * 4))
}

func colorTransformRow(dst, src []uint32, c ctElem) {
	n := len(dst)
	src = src[:n]
	if n < 4 {
		colorTransformRowGeneric(dst, src, c)
		return
	}
	m1, m2 := ctMults(c)
	k := colorTransformNEON(&dst[0], &src[0], n, m1, m2)
	colorTransformRowGeneric(dst[k:], src[k:], c)
}

func undoColorTransformRow(dst, src []uint32, c ctElem) {
	n := len(dst)
	src = src[:n]
	if n < 4 {
		undoColorTransformRowGeneric(dst, src, c)
		return
	}
	m1, m2 := ctMults(c)
	k := undoColorTransformNEON(&dst[0], &src[0], n, m1, m2)
	undoColorTransformRowGeneric(dst[k:], src[k:], c)
}

func addGreenRow(dst, src []uint32) {
	n := len(dst)
	src = src[:n]
	if n < 4 {
		addGreenRowGeneric(dst, src)
		return
	}
	k := addGreenNEON(&dst[0], &src[0], n)
	addGreenRowGeneric(dst[k:], src[k:])
}

// addTopRow adds the predictions of mode m, one that reads only the row
// above, to cur; see addTopRowGeneric.
func addTopRow(m int, cur, top []uint32) {
	n := len(cur)
	k := 0
	if n >= 4 {
		_ = top[n+1]
		k = addTopNEON(m, &cur[0], &top[0], n)
	}
	addTopRowGeneric(m, cur[k:], top[k:])
}

func (m *model) pixelCosts(out, res []uint32, set int) { m.pixelCostsGeneric(out, res, set) }

func ctCostSum(tab *[256]uint32, x, y []int8, m int32) uint32 {
	return ctCostGeneric(tab, x, y, m)
}

func fromNRGBA(d []uint32, p []byte) uint32 { return fromNRGBAGeneric(d, p) }

// haveCTTile reports whether ctTile takes tiles.
var haveCTTile = false

func ctTile(img []uint32, i, stride, w, rows int, g, r, b []int8, sums *[5]int32) bool {
	return false
}
