//go:build amd64 && !purego

package webp

// haveAVX2 reports whether the AVX2 kernels run: the CPU has AVX2 and the
// operating system saves YMM state.
var haveAVX2 = cpuHasAVX2()

//go:noescape
func cpuHasAVX2() bool

//go:noescape
func residualsAVX2(m int, dst, cur, top *uint32, n int) int

//go:noescape
func subGreenAVX2(dst, src *uint32, n int) int

//go:noescape
func fromNRGBAAVX2(dst *uint32, src *byte, n int) (int, uint32)

// residuals writes to dst the residuals of mode m for pixels i to
// i+len(dst)-1 of img, an image w pixels wide; see residualsGeneric.
func residuals(m int, dst, img []uint32, i, w int) {
	n := len(dst)
	if !haveAVX2 || n < 8 {
		residualsGeneric(m, dst, img, i, w)
		return
	}
	// The bounds the kernel reads: img[i-1 : i+n] and img[i-w-1 : i-w+n+1].
	_ = img[i-w-1 : i+n]
	k := residualsAVX2(m, &dst[0], &img[i-1], &img[i-w-1], n)
	if k < n {
		// The last block again, overlapping: dst is not img.
		j := i + n - 8
		residualsAVX2(m, &dst[n-8], &img[j-1], &img[j-w-1], 8)
	}
}

//go:noescape
func addGreenAVX2(dst, src *uint32, n int) int

func addGreenRow(dst, src []uint32) {
	n := len(dst)
	src = src[:n]
	if !haveAVX2 || n < 8 {
		addGreenRowGeneric(dst, src)
		return
	}
	k := addGreenAVX2(&dst[0], &src[0], n)
	addGreenRowGeneric(dst[k:], src[k:])
}

func subtractGreenRow(dst, src []uint32) {
	n := len(dst)
	src = src[:n]
	if !haveAVX2 || n < 8 {
		subtractGreenRowGeneric(dst, src)
		return
	}
	k := subGreenAVX2(&dst[0], &src[0], n)
	subtractGreenRowGeneric(dst[k:], src[k:])
}

//go:noescape
func colorTransformAVX2(dst, src *uint32, n int, m1, m2 uint32) int

//go:noescape
func undoColorTransformAVX2(dst, src *uint32, n int, m1, m2 uint32) int

// ctMults packs element c for the color transform kernels.
func ctMults(c ctElem) (m1, m2 uint32) {
	return uint32(uint16(int16(c.g2r)*8))<<16 | uint32(uint16(int16(c.g2b)*8)), uint32(uint16(int16(c.r2b) * 8))
}

func colorTransformRow(dst, src []uint32, c ctElem) {
	n := len(dst)
	src = src[:n]
	if !haveAVX2 || n < 8 {
		colorTransformRowGeneric(dst, src, c)
		return
	}
	m1, m2 := ctMults(c)
	k := colorTransformAVX2(&dst[0], &src[0], n, m1, m2)
	colorTransformRowGeneric(dst[k:], src[k:], c)
}

func undoColorTransformRow(dst, src []uint32, c ctElem) {
	n := len(dst)
	src = src[:n]
	if !haveAVX2 || n < 8 {
		undoColorTransformRowGeneric(dst, src, c)
		return
	}
	m1, m2 := ctMults(c)
	k := undoColorTransformAVX2(&dst[0], &src[0], n, m1, m2)
	undoColorTransformRowGeneric(dst[k:], src[k:], c)
}

//go:noescape
func addTopAVX2(m int, cur, top *uint32, n int) int

// addTopRow adds the predictions of mode m, one that reads only the row
// above, to cur; see addTopRowGeneric.
func addTopRow(m int, cur, top []uint32) {
	n := len(cur)
	k := 0
	if haveAVX2 && n >= 8 {
		_ = top[n+1]
		k = addTopAVX2(m, &cur[0], &top[0], n)
	}
	addTopRowGeneric(m, cur[k:], top[k:])
}

//go:noescape
func pixelCostsAVX2(tab *[4][256]uint32, res *uint32, n int, out *uint32, chans int) int

//go:noescape
func ctCostAVX2(tab *[256]uint32, x, y *int8, n int, m int) (int, uint32)

// pixelCosts adds to out[k] the cost under m of residual k of res,
// scoring the channels of set.
func (m *model) pixelCosts(out, res []uint32, set int) {
	k := 0
	if haveAVX2 && len(res) >= 8 {
		chans := 4
		switch set {
		case scoreGreen:
			chans = 1
		case scoreRGB:
			chans = 3
		}
		_ = out[len(res)-1]
		k = pixelCostsAVX2(&m.cost, &res[0], len(res), &out[0], chans)
	}
	m.pixelCostsGeneric(out[k:], res[k:], set)
}

// ctCostSum returns the sum of tab[uint8(x[i] - m*y[i]>>5)].
func ctCostSum(tab *[256]uint32, x, y []int8, m int32) uint32 {
	x = x[:len(y)]
	var s uint32
	k := 0
	if haveAVX2 && len(y) >= 16 {
		k, s = ctCostAVX2(tab, &x[0], &y[0], len(y), int(m))
	}
	return s + ctCostGeneric(tab, x[k:], y[k:], m)
}

// fromNRGBA converts a row of 8-bit non-premultiplied RGBA and returns
// the AND of the pixels.
func fromNRGBA(d []uint32, p []byte) uint32 {
	n := len(d)
	p = p[:4*n]
	if !haveAVX2 || n < 8 {
		return fromNRGBAGeneric(d, p)
	}
	k, and := fromNRGBAAVX2(&d[0], &p[0], n)
	return and & fromNRGBAGeneric(d[k:], p[4*k:])
}

//go:noescape
func ctTileAVX2(src *uint32, stride, w, rows int, tg, tr, tb *int8, sums *[5]int32)

// haveCTTile reports whether ctTile takes tiles.
var haveCTTile = haveAVX2

// ctTile writes the green, red and blue bytes of a tile, rows rows of w
// pixels at img[i:], stride pixels apart, to g, r and b in order, and the
// sums of g*g, g*r, r*r, g*b and r*b over them, as signed bytes, to sums.
// It returns false, doing nothing, for a tile the kernel does not take.
func ctTile(img []uint32, i, stride, w, rows int, g, r, b []int8, sums *[5]int32) bool {
	// Up to 1<<14 pixels, the sums stay within 2^28.
	n := w * rows
	if !haveAVX2 || w%16 != 0 || rows < 1 || n > 1<<14 {
		return false
	}
	_ = img[i : i+(rows-1)*stride+w]
	_, _, _ = g[:n], r[:n], b[:n]
	ctTileAVX2(&img[i], stride, w, rows, &g[0], &r[0], &b[0], sums)
	return true
}
