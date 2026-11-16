package webp

import (
	"math"
	"slices"
)

// The color indexing transform (RFC 9649 section 3.5.4): at most 256
// colors become indices into a color table, and with at most 16 colors
// 2, 4 or 8 indices share one pixel.

// bundleBits returns width_bits for a table of n colors.
func bundleBits(n int) int {
	switch {
	case n <= 2:
		return 3
	case n <= 4:
		return 2
	case n <= 16:
		return 1
	}
	return 0
}

// palette is a color table and the index of each color of the image.
type palette struct {
	n     int
	color [256]uint32 // the table, in order
	// index[i] is the table position of e.an.colors.list[i].
	index [256]uint8
}

// paletteDeltas writes the table as the format stores it: every entry
// minus the one before it, per byte.
func (p *palette) deltas(dst []uint32) []uint32 {
	dst = dst[:p.n]
	prev := uint32(0)
	for i, c := range p.color[:p.n] {
		dst[i] = subPixels(c, prev)
		prev = c
	}
	return dst
}

// deltaBits estimates the size of the delta-coded table: the entropy of
// each channel's deltas plus the code headers, which grow with the number
// of distinct values.
func (p *palette) deltaBits() float64 {
	var h hist4
	prev := uint32(0)
	for _, c := range p.color[:p.n] {
		h.add(subPixels(c, prev))
		prev = c
	}
	bits := 0.0
	for c := range h {
		used := 0
		for _, k := range h[c] {
			if k != 0 {
				used++
			}
		}
		bits += histBits(h[c][:])
		if used > 1 {
			bits += 4 * float64(used)
		}
	}
	return bits
}

// setOrder fills the table from the image colors in order (indices into
// e.an.colors.list).
func (p *palette) setOrder(cs *colorSet, order []uint8) {
	p.n = len(order)
	for i, k := range order {
		p.color[i] = cs.list[k]
		p.index[k] = uint8(i)
	}
}

// Palette orders.
const (
	orderValue     = iota // by ARGB value
	orderLuma             // by alpha, then luma
	orderFrequency        // most frequent first
	orderChain            // greedy chain of the colors most often next to each other
	orderNearest          // greedy chain of the closest colors, for a cheap table
	nOrders
)

// paletteOrder returns the colors of cs (indices into cs.list) in order
// kind.
func (e *encoder) paletteOrder(kind int, dst []uint8) []uint8 {
	cs := &e.an.colors
	n := cs.n
	dst = dst[:n]
	for i := range dst {
		dst[i] = uint8(i)
	}
	switch kind {
	case orderValue:
		slices.SortFunc(dst, func(a, b uint8) int { return cmpU32(cs.list[a], cs.list[b]) })
	case orderLuma:
		key := func(v uint32) uint32 {
			r, g, b := v>>16&0xff, v>>8&0xff, v&0xff
			return v>>24<<24 | (299*r+587*g+114*b)<<4 | (v >> 8 & 0xf)
		}
		slices.SortStableFunc(dst, func(a, b uint8) int { return cmpU32(key(cs.list[a]), key(cs.list[b])) })
	case orderFrequency:
		slices.SortStableFunc(dst, func(a, b uint8) int { return cmpU32(cs.count[b], cs.count[a]) })
	case orderChain:
		e.chainOrder(dst)
	case orderNearest:
		nearestOrder(cs, dst)
	}
	return dst
}

func cmpU32(a, b uint32) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// colorDist is the cost proxy of storing b after a in the delta-coded
// table: the magnitude of each byte's delta as a signed value.
func colorDist(a, b uint32) int {
	d := subPixels(b, a)
	s := 0
	for sh := 0; sh < 32; sh += 8 {
		v := int(int8(d >> sh))
		if v < 0 {
			v = -v
		}
		s += v
	}
	return s
}

// nearestOrder chains the colors from the darkest, always to the closest
// color not yet used.
func nearestOrder(cs *colorSet, dst []uint8) {
	n := len(dst)
	var used [256]bool
	cur := 0
	for i := 1; i < n; i++ {
		if cs.list[i] < cs.list[cur] {
			cur = i
		}
	}
	for k := 0; k < n; k++ {
		dst[k] = uint8(cur)
		used[cur] = true
		best, bestD := -1, math.MaxInt
		for j := 0; j < n; j++ {
			if !used[j] {
				if d := colorDist(cs.list[cur], cs.list[j]); d < bestD {
					best, bestD = j, d
				}
			}
		}
		cur = best
	}
}

// chainOrder chains the colors by how often they are next to each other in
// the image (left and top neighbors), starting from the most frequent
// color and growing the chain at whichever end has the stronger link.
func (e *encoder) chainOrder(dst []uint8) {
	cs := &e.an.colors
	n := cs.n
	e.cooc = growU32(e.cooc, 256*256)
	co := e.cooc
	clear(co)
	w, h := e.w, e.h
	idx := e.idx
	for y := 0; y < h; y++ {
		r := idx[y*w : y*w+w]
		for x := 1; x < w; x++ {
			if a, b := r[x-1], r[x]; a != b {
				co[int(a)<<8|int(b)]++
				co[int(b)<<8|int(a)]++
			}
		}
		if y > 0 {
			t := idx[(y-1)*w : y*w]
			for x := 0; x < w; x++ {
				if a, b := t[x], r[x]; a != b {
					co[int(a)<<8|int(b)]++
					co[int(b)<<8|int(a)]++
				}
			}
		}
	}
	var used [256]bool
	start := 0
	for i := 1; i < n; i++ {
		if cs.count[i] > cs.count[start] {
			start = i
		}
	}
	// The chain grows in buf from the middle outwards.
	var buf [512]uint8
	lo, hi := 256, 257
	buf[256] = uint8(start)
	used[start] = true
	for k := 1; k < n; k++ {
		l, r := int(buf[lo]), int(buf[hi-1])
		best, bestW, atLo := -1, -1, false
		for j := 0; j < n; j++ {
			if used[j] {
				continue
			}
			if c := int(co[r<<8|j]); c > bestW {
				best, bestW, atLo = j, c, false
			}
			if c := int(co[l<<8|j]); c > bestW {
				best, bestW, atLo = j, c, true
			}
		}
		if bestW == 0 {
			// Not next to the chain's ends: the most frequent color left.
			best = -1
			for j := 0; j < n; j++ {
				if !used[j] && (best < 0 || cs.count[j] > cs.count[best]) {
					best = j
				}
			}
		}
		used[best] = true
		if atLo {
			lo--
			buf[lo] = uint8(best)
		} else {
			buf[hi] = uint8(best)
			hi++
		}
	}
	copy(dst, buf[lo:hi])
}

// indexImage writes the color list positions of e.argb into e.idx.
func (e *encoder) indexImage() {
	cs := &e.an.colors
	e.idx = growU8(e.idx, len(e.argb))
	idx := e.idx
	last, li := ^e.argb[0], uint8(0)
	for i, v := range e.argb {
		if v != last {
			last, li = v, uint8(cs.lookup(v))
		}
		idx[i] = li
	}
}

// packIndices writes the image of table positions under p, bundled for
// width_bits wb, into dst (subSize(w, wb) * h pixels) and returns it.
func (e *encoder) packIndices(dst []uint32, p *palette, wb int) []uint32 {
	w, h := e.w, e.h
	pw := subSize(w, wb)
	dst = growU32(dst, pw*h)
	idx := e.idx
	if wb == 0 {
		for i, k := range idx[:w*h] {
			dst[i] = 0xff000000 | uint32(p.index[k])<<8
		}
		return dst
	}
	per := 1 << wb
	shift := 8 >> wb
	for y := 0; y < h; y++ {
		r := idx[y*w : y*w+w]
		d := dst[y*pw : y*pw+pw]
		for x := 0; x < pw; x++ {
			var g uint32
			for k := 0; k < per && x*per+k < w; k++ {
				g |= uint32(p.index[r[x*per+k]]) << (k * shift)
			}
			d[x] = 0xff000000 | g<<8
		}
	}
	return dst
}
