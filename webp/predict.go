package webp

// The predictor transform (RFC 9649 section 3.5.1). Pixels are 0xAARRGGBB;
// all arithmetic is per byte, modulo 256.

// nModes is the number of prediction modes.
const nModes = 14

// subPixels returns a - b per byte.
func subPixels(a, b uint32) uint32 {
	return ((a | 0x80808080) - (b & 0x7f7f7f7f)) ^ ((a ^ ^b) & 0x80808080)
}

// addPixels returns a + b per byte.
func addPixels(a, b uint32) uint32 {
	return ((a & 0x7f7f7f7f) + (b & 0x7f7f7f7f)) ^ ((a ^ b) & 0x80808080)
}

// average2 returns (a + b) / 2 per byte.
func average2(a, b uint32) uint32 {
	return (a & b) + ((a ^ b) & 0xfefefefe >> 1)
}

// sad returns the sum of the absolute differences of the four bytes.
func sad(a, b uint32) int32 {
	return absDiff(a, b, 0) + absDiff(a, b, 8) + absDiff(a, b, 16) + absDiff(a, b, 24)
}

func absDiff(a, b uint32, sh uint) int32 {
	d := int32(a>>sh&0xff) - int32(b>>sh&0xff)
	m := d >> 31
	return d ^ m - m
}

// selectPred is the Select predictor: L or T, whichever is closer to the
// gradient estimate L + T - TL.
func selectPred(l, t, tl uint32) uint32 {
	if sad(t, tl) < sad(l, tl) {
		return l
	}
	return t
}

// clamp255 clamps v, between -255 and 510, to a byte.
func clamp255(v int32) uint32 {
	v &^= v >> 31
	return uint32(v|(255-v)>>31) & 0xff
}

// clampAddSubFull is Clamp(a + b - c) per byte.
func clampAddSubFull(a, b, c uint32) uint32 {
	f := func(sh uint) uint32 {
		return clamp255(int32(a>>sh&0xff)+int32(b>>sh&0xff)-int32(c>>sh&0xff)) << sh
	}
	return f(0) | f(8) | f(16) | f(24)
}

// clampAddSubHalf is Clamp(a + (a - b) / 2) per byte, the division
// truncating towards zero.
func clampAddSubHalf(a, b uint32) uint32 {
	f := func(sh uint) uint32 {
		x := int32(a >> sh & 0xff)
		return clamp255(x+(x-int32(b>>sh&0xff))/2) << sh
	}
	return f(0) | f(8) | f(16) | f(24)
}

// predict returns mode m's prediction from the neighbors left, top,
// top-right and top-left.
func predict(m int, l, t, tr, tl uint32) uint32 {
	switch m {
	case 0:
		return 0xff000000
	case 1:
		return l
	case 2:
		return t
	case 3:
		return tr
	case 4:
		return tl
	case 5:
		return average2(average2(l, tr), t)
	case 6:
		return average2(l, tl)
	case 7:
		return average2(l, t)
	case 8:
		return average2(tl, t)
	case 9:
		return average2(t, tr)
	case 10:
		return average2(average2(l, tl), average2(t, tr))
	case 11:
		return selectPred(l, t, tl)
	case 12:
		return clampAddSubFull(l, t, tl)
	case 13:
		return clampAddSubHalf(average2(l, t), tl)
	}
	panic("webp: bad prediction mode")
}

// residualsGeneric writes to dst[k] the residual of pixel i+k of img, an
// image w pixels wide, under mode m. Every pixel must have x >= 1 and
// y >= 1. The top-right neighbor of pixel j is img[j-w+1], which for the
// last column is the first pixel of the same row, as the format defines.
func residualsGeneric(m int, dst, img []uint32, i, w int) {
	n := len(dst)
	cur := img[i-1 : i+n]
	top := img[i-w-1 : i-w+n+1]
	cur, top = cur[:n+1], top[:n+2]
	switch m {
	case 0:
		for k := range dst {
			dst[k] = subPixels(cur[k+1], 0xff000000)
		}
	case 1:
		for k := range dst {
			dst[k] = subPixels(cur[k+1], cur[k])
		}
	case 2:
		for k := range dst {
			dst[k] = subPixels(cur[k+1], top[k+1])
		}
	case 3:
		for k := range dst {
			dst[k] = subPixels(cur[k+1], top[k+2])
		}
	case 4:
		for k := range dst {
			dst[k] = subPixels(cur[k+1], top[k])
		}
	case 5:
		for k := range dst {
			dst[k] = subPixels(cur[k+1], average2(average2(cur[k], top[k+2]), top[k+1]))
		}
	case 6:
		for k := range dst {
			dst[k] = subPixels(cur[k+1], average2(cur[k], top[k]))
		}
	case 7:
		for k := range dst {
			dst[k] = subPixels(cur[k+1], average2(cur[k], top[k+1]))
		}
	case 8:
		for k := range dst {
			dst[k] = subPixels(cur[k+1], average2(top[k], top[k+1]))
		}
	case 9:
		for k := range dst {
			dst[k] = subPixels(cur[k+1], average2(top[k+1], top[k+2]))
		}
	case 10:
		for k := range dst {
			dst[k] = subPixels(cur[k+1], average2(average2(cur[k], top[k]), average2(top[k+1], top[k+2])))
		}
	case 11:
		for k := range dst {
			dst[k] = subPixels(cur[k+1], selectPred(cur[k], top[k+1], top[k]))
		}
	case 12:
		for k := range dst {
			dst[k] = subPixels(cur[k+1], clampAddSubFull(cur[k], top[k+1], top[k]))
		}
	case 13:
		for k := range dst {
			dst[k] = subPixels(cur[k+1], clampAddSubHalf(average2(cur[k], top[k+1]), top[k]))
		}
	default:
		panic("webp: bad prediction mode")
	}
}

// edgePrediction returns the prediction of pixel (x, y) when x == 0 or
// y == 0, which does not depend on the mode.
func edgePrediction(img []uint32, i, x, y, w int) uint32 {
	switch {
	case y == 0 && x == 0:
		return 0xff000000
	case y == 0:
		return img[i-1]
	}
	return img[i-w]
}

// applyPredictor replaces img (w x h) by its residuals under the tile
// modes. modes holds one mode per tile of 1<<bits pixels squared, in rows
// of DIV_ROUND_UP(w, 1<<bits). Rows go bottom up through row (at least w
// pixels), so every prediction still reads original pixels.
func applyPredictor(img []uint32, w, h, bits int, modes []uint8, row []uint32) {
	tw := subSize(w, bits)
	tile := 1 << bits
	row = row[:w]
	for y := h - 1; y >= 0; y-- {
		i := y * w
		row[0] = subPixels(img[i], edgePrediction(img, i, 0, y, w))
		if y == 0 {
			for x := 1; x < w; x++ {
				row[x] = subPixels(img[x], img[x-1])
			}
		} else {
			mrow := modes[(y>>bits)*tw : (y>>bits)*tw+tw]
			// One call per run of tiles with the same mode.
			for t := 0; t < tw; {
				m := mrow[t]
				u := t + 1
				for u < tw && mrow[u] == m {
					u++
				}
				x, end := max(1, t*tile), min(w, u*tile)
				if x < end {
					residuals(int(m), row[x:end], img, i+x, w)
				}
				t = u
			}
		}
		copy(img[i:i+w], row)
	}
}

// undoPredictor inverts applyPredictor in place.
func undoPredictor(img []uint32, w, h, bits int, modes []uint8) {
	tw := subSize(w, bits)
	tile := 1 << bits
	for y := 0; y < h; y++ {
		i := y * w
		img[i] = addPixels(img[i], edgePrediction(img, i, 0, y, w))
		if y == 0 {
			for x := 1; x < w; x++ {
				img[x] = addPixels(img[x], img[x-1])
			}
			continue
		}
		mrow := modes[(y>>bits)*tw : (y>>bits)*tw+tw]
		// One call per run of tiles with the same mode.
		for t := 0; t < tw; {
			m := mrow[t]
			u := t + 1
			for u < tw && mrow[u] == m {
				u++
			}
			x, end := max(1, t*tile), min(w, u*tile)
			if x < end {
				undoRun(int(m), img, i+x, end-x, w)
			}
			t = u
		}
	}
}

// undoRun adds mode m's predictions to the n residuals at img[i:], left to
// right; every pixel has x >= 1 and y >= 1, and the pixels before i and in
// the rows above are restored already. The modes that do not read the
// left pixel add a whole run at once.
func undoRun(m int, img []uint32, i, n, w int) {
	cur := img[i : i+n]
	top := img[i-w-1 : i-w+n+1]
	top = top[:n+2]
	switch m {
	case 0, 2, 3, 4, 8, 9:
		addTopRow(m, cur, top)
		return
	}
	l := img[i-1]
	switch m {
	case 1:
		for k, p := range cur {
			l = addPixels(p, l)
			cur[k] = l
		}
	case 5:
		for k, p := range cur {
			l = addPixels(p, average2(average2(l, top[k+2]), top[k+1]))
			cur[k] = l
		}
	case 6:
		for k, p := range cur {
			l = addPixels(p, average2(l, top[k]))
			cur[k] = l
		}
	case 7:
		for k, p := range cur {
			l = addPixels(p, average2(l, top[k+1]))
			cur[k] = l
		}
	case 10:
		for k, p := range cur {
			l = addPixels(p, average2(average2(l, top[k]), average2(top[k+1], top[k+2])))
			cur[k] = l
		}
	case 11:
		for k, p := range cur {
			l = addPixels(p, selectPred(l, top[k+1], top[k]))
			cur[k] = l
		}
	case 12:
		for k, p := range cur {
			l = addPixels(p, clampAddSubFull(l, top[k+1], top[k]))
			cur[k] = l
		}
	case 13:
		for k, p := range cur {
			l = addPixels(p, clampAddSubHalf(average2(l, top[k+1]), top[k]))
			cur[k] = l
		}
	default:
		panic("webp: bad prediction mode")
	}
}

// addTopRowGeneric adds the predictions of mode m, one that reads only
// the row above (0, 2, 3, 4, 8 or 9), to cur; top[k], top[k+1] and
// top[k+2] are the top-left, top and top-right of cur[k].
func addTopRowGeneric(m int, cur, top []uint32) {
	top = top[:len(cur)+2]
	switch m {
	case 0:
		for k, p := range cur {
			cur[k] = addPixels(p, 0xff000000)
		}
	case 2:
		for k, p := range cur {
			cur[k] = addPixels(p, top[k+1])
		}
	case 3:
		for k, p := range cur {
			cur[k] = addPixels(p, top[k+2])
		}
	case 4:
		for k, p := range cur {
			cur[k] = addPixels(p, top[k])
		}
	case 8:
		for k, p := range cur {
			cur[k] = addPixels(p, average2(top[k], top[k+1]))
		}
	case 9:
		for k, p := range cur {
			cur[k] = addPixels(p, average2(top[k+1], top[k+2]))
		}
	}
}

// subSize is DIV_ROUND_UP(n, 1<<bits).
func subSize(n, bits int) int { return (n + 1<<bits - 1) >> bits }
