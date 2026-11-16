package webp

// The subtract green and color transforms (RFC 9649 sections 3.5.2 and
// 3.5.3).

// subtractGreen returns p with green subtracted from red and blue.
func subtractGreen(p uint32) uint32 {
	g := p >> 8 & 0xff
	return p&0xff00ff00 | ((p|0xff00ff00)-(g|g<<16))&0x00ff00ff
}

// subtractGreenRow applies subtract green to every pixel of src into dst.
func subtractGreenRowGeneric(dst, src []uint32) {
	src = src[:len(dst)]
	for i, p := range src {
		dst[i] = subtractGreen(p)
	}
}

// ctElem is one color transform element: the three multipliers, as the
// sub-image pixel 0xff<<24 | red_to_blue<<16 | green_to_blue<<8 |
// green_to_red.
type ctElem struct{ g2r, g2b, r2b int8 }

func (c ctElem) pixel() uint32 {
	return 0xff000000 | uint32(uint8(c.r2b))<<16 | uint32(uint8(c.g2b))<<8 | uint32(uint8(c.g2r))
}

// ctDelta is ColorTransformDelta: t*c/32 in 3.5 fixed point, of which only
// the low 8 bits matter.
func ctDelta(t, c int8) int32 { return int32(t) * int32(c) >> 5 }

// colorTransform returns p with the forward color transform of c applied:
// red minus the green term, blue minus the green and the red terms, red
// being the input red.
func colorTransform(p uint32, c ctElem) uint32 {
	g := int8(p >> 8)
	r := int8(p >> 16)
	nr := (int32(r) - ctDelta(c.g2r, g)) & 0xff
	nb := (int32(p&0xff) - ctDelta(c.g2b, g) - ctDelta(c.r2b, r)) & 0xff
	return p&0xff00ff00 | uint32(nr)<<16 | uint32(nb)
}

// colorTransformRowGeneric applies element c to every pixel of src into dst.
func colorTransformRowGeneric(dst, src []uint32, c ctElem) {
	src = src[:len(dst)]
	for i, p := range src {
		dst[i] = colorTransform(p, c)
	}
}

// applyColorTransform transforms img (w x h) in place with one element per
// tile of 1<<bits pixels squared.
func applyColorTransform(img []uint32, w, h, bits int, elems []ctElem) {
	tw := subSize(w, bits)
	tile := 1 << bits
	for y := 0; y < h; y++ {
		er := elems[(y>>bits)*tw:]
		row := img[y*w : y*w+w]
		for x := 0; x < w; x += tile {
			end := min(w, x+tile)
			colorTransformRow(row[x:end], row[x:end], er[x>>bits])
		}
	}
}

// undoColorTransform inverts applyColorTransform in place.
func undoColorTransform(img []uint32, w, h, bits int, elems []ctElem) {
	tw := subSize(w, bits)
	tile := 1 << bits
	for y := 0; y < h; y++ {
		er := elems[(y>>bits)*tw:]
		row := img[y*w : y*w+w]
		for x := 0; x < w; x += tile {
			seg := row[x:min(w, x+tile)]
			undoColorTransformRow(seg, seg, er[x>>bits])
		}
	}
}

// undoColorTransformRowGeneric inverts element c on src into dst.
func undoColorTransformRowGeneric(dst, src []uint32, c ctElem) {
	src = src[:len(dst)]
	for k, p := range src {
		g := int8(p >> 8)
		r := (int32(p>>16&0xff) + ctDelta(c.g2r, g)) & 0xff
		b := (int32(p&0xff) + ctDelta(c.g2b, g) + ctDelta(c.r2b, int8(r))) & 0xff
		dst[k] = p&0xff00ff00 | uint32(r)<<16 | uint32(b)
	}
}
