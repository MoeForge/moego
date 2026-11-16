package png

import "encoding/binary"

// colorSet collects up to 256 distinct 8-bit RGBA colors, packed as
// R | G<<8 | B<<16 | A<<24, in an open-addressing table.
type colorSet struct {
	slot [1024]uint16 // 0: empty, else 1 + index into list
	list [256]uint32  // colors in the order they were first seen
	n    int          // colors seen; 257 means more than 256
	pidx [256]uint8   // palette index of list[i] once the palette is built
}

func (c *colorSet) reset() {
	clear(c.slot[:])
	c.n = 0
}

func hashColor(v uint32) uint32 { return v * 0x9e3779b1 >> 22 }

// add records v. It returns false once there are more than 256 colors.
func (c *colorSet) add(v uint32) bool {
	h := hashColor(v)
	for {
		s := c.slot[h]
		if s == 0 {
			break
		}
		if c.list[s-1] == v {
			return true
		}
		h = (h + 1) & 1023
	}
	if c.n == 256 {
		c.n = 257
		return false
	}
	c.list[c.n] = v
	c.n++
	c.slot[h] = uint16(c.n)
	return true
}

// index returns the palette index of a color in the set.
func (c *colorSet) index(v uint32) uint8 {
	h := hashColor(v)
	for {
		s := c.slot[h]
		if c.list[s-1] == v {
			return c.pidx[s-1]
		}
		h = (h + 1) & 1023
	}
}

// stats is what one pass over the canonical rows finds out.
type stats struct {
	opaque bool // every alpha sample at its maximum
	gray   bool // R = G = B everywhere
	narrow bool // 16-bit formats: every sample's high byte equals its low byte
	// grays[v] is set for each gray value of an 8-bit (or narrow) gray
	// image; colors are in encoder.set otherwise.
	grays [256]bool
}

// analyze scans the canonical rows. It stops early once nothing it could
// still find would change the color type.
func (e *encoder) analyze() {
	s := &e.src
	st := &e.st
	st.opaque, st.gray, st.narrow = true, true, true
	clear(st.grays[:])
	e.set.reset()
	switch s.format {
	case fmtG8:
		for y := 0; y < s.h; y++ {
			for _, v := range s.row(y, nil) {
				st.grays[v] = true
			}
		}
	case fmtG16:
		for y := 0; y < s.h && st.narrow; y++ {
			p := s.row(y, nil)
			for i := 0; i+1 < len(p); i += 2 {
				if p[i] != p[i+1] {
					st.narrow = false
					break
				}
				st.grays[p[i]] = true
			}
		}
	case fmtRGBA8:
		counting := true
		for y := 0; y < s.h; y++ {
			p := s.row(y, e.sbuf[0])
			if st.opaque || st.gray {
				o, g := rgbaFlags(p)
				st.opaque = st.opaque && o
				st.gray = st.gray && g
			}
			if counting {
				counting = e.countRow8(p)
			}
			if !counting && !st.opaque && !st.gray {
				break
			}
		}
	case fmtRGBA16:
		counting := true
		for y := 0; y < s.h; y++ {
			p := s.row(y, e.sbuf[0])
			if st.opaque && !alphaOpaque16(p) {
				st.opaque = false
			}
			if st.gray && !rowGray16(p) {
				st.gray = false
			}
			if st.narrow && !rowNarrow(p) {
				st.narrow = false
				counting = false
			}
			if counting {
				counting = e.countRow16(p)
			}
			if !counting && !st.opaque && !st.gray && !st.narrow {
				break
			}
		}
	}
	if (s.format == fmtRGBA8 || s.format == fmtRGBA16 && st.narrow) && st.gray && st.opaque {
		// At most 256 colors (v, v, v, 255): the set holds them all.
		for _, v := range e.set.list[:min(e.set.n, 256)] {
			st.grays[uint8(v)] = true
		}
	}
}

// fewColors reports whether a truecolor image has at most 256 colors.
// Only the 8-bit formats are counted; it stops at the 257th color.
func (e *encoder) fewColors() bool {
	s := &e.src
	if s.format != fmtRGBA8 {
		return false
	}
	e.set.reset()
	for y := 0; y < s.h; y++ {
		if !e.countRow8(s.row(y, e.sbuf[0])) {
			return false
		}
	}
	return true
}

func rowGray16(p []byte) bool {
	for i := 0; i+7 < len(p); i += 8 {
		if p[i] != p[i+2] || p[i] != p[i+4] || p[i+1] != p[i+3] || p[i+1] != p[i+5] {
			return false
		}
	}
	return true
}

func rowNarrow(p []byte) bool {
	for i := 0; i+1 < len(p); i += 2 {
		if p[i] != p[i+1] {
			return false
		}
	}
	return true
}

// countRow8 adds the colors of an RGBA8 row to the set; false once there
// are more than 256.
func (e *encoder) countRow8(p []byte) bool {
	if len(p) < 4 {
		return true
	}
	last := ^binary.LittleEndian.Uint32(p) // differs from the first pixel
	for i := 0; i+3 < len(p); i += 4 {
		v := uint32(p[i]) | uint32(p[i+1])<<8 | uint32(p[i+2])<<16 | uint32(p[i+3])<<24
		if v == last {
			continue
		}
		last = v
		if !e.set.add(v) {
			return false
		}
	}
	return true
}

// countRow16 adds the colors of a narrow RGBA16 row by their high bytes.
func (e *encoder) countRow16(p []byte) bool {
	if len(p) < 8 {
		return true
	}
	last := ^(uint32(p[0]) | uint32(p[2])<<8 | uint32(p[4])<<16 | uint32(p[6])<<24)
	for i := 0; i+7 < len(p); i += 8 {
		v := uint32(p[i]) | uint32(p[i+2])<<8 | uint32(p[i+4])<<16 | uint32(p[i+6])<<24
		if v == last {
			continue
		}
		last = v
		if !e.set.add(v) {
			return false
		}
	}
	return true
}

// PNG color types.
const (
	ctGray      = 0
	ctRGB       = 2
	ctPalette   = 3
	ctGrayAlpha = 4
	ctRGBA      = 6
)

// bitsFor returns the palette bit depth for n colors.
func bitsFor(n int) uint8 {
	switch {
	case n <= 2:
		return 1
	case n <= 4:
		return 2
	case n <= 16:
		return 4
	}
	return 8
}

// chooseStd sets the layout image/png writes.
func (e *encoder) chooseStd() {
	s := &e.src
	e.narrow = false
	switch s.format {
	case fmtIndex:
		e.ct, e.depth = ctPalette, bitsFor(len(s.palette))
	case fmtG8:
		e.ct, e.depth = ctGray, 8
	case fmtG16:
		e.ct, e.depth = ctGray, 16
	case fmtRGBA8:
		e.ct, e.depth = ctRGBA, 8
		if s.opaque {
			e.ct = ctRGB
		}
	case fmtRGBA16:
		e.ct, e.depth = ctRGBA, 16
		if s.opaque {
			e.ct = ctRGB
		}
	}
}

// chooseReduced sets the smallest layout that holds the analyzed pixels.
//
// For truecolor images of at most 256 colors it chooses the palette and
// sets altCT to the 8-bit truecolor type that also holds them.
func (e *encoder) chooseReduced() {
	s, st := &e.src, &e.st
	e.narrow = false
	e.altCT = ctPalette
	if s.format == fmtIndex {
		e.chooseStd()
		return
	}
	wide := (s.format == fmtRGBA16 || s.format == fmtG16) && !st.narrow
	if wide {
		e.depth = 16
		gray := s.format == fmtG16 || st.gray
		opaque := s.format == fmtG16 || st.opaque
		switch {
		case gray && opaque:
			e.ct = ctGray
		case gray:
			e.ct = ctGrayAlpha
		case opaque:
			e.ct = ctRGB
		default:
			e.ct = ctRGBA
		}
		return
	}
	e.narrow = s.format == fmtRGBA16 || s.format == fmtG16
	gray := s.format == fmtG8 || s.format == fmtG16 || st.gray
	opaque := s.format == fmtG8 || s.format == fmtG16 || st.opaque
	if gray && opaque {
		n, gd := 0, uint8(1)
		for v, ok := range st.grays {
			if !ok {
				continue
			}
			n++
			switch {
			case v%0xff == 0:
			case v%0x55 == 0:
				gd = max(gd, 2)
			case v%0x11 == 0:
				gd = max(gd, 4)
			default:
				gd = 8
			}
		}
		if pd := bitsFor(n); pd < gd {
			e.ct, e.depth = ctPalette, pd
			e.grayPalette()
			return
		}
		e.ct, e.depth = ctGray, gd
		return
	}
	e.depth = 8
	switch {
	case gray:
		e.ct = ctGrayAlpha
	case opaque:
		e.ct = ctRGB
	default:
		e.ct = ctRGBA
	}
	if e.set.n <= 256 {
		e.altCT = e.ct
		e.ct, e.depth = ctPalette, bitsFor(e.set.n)
		e.colorPalette()
	}
}

// grayPalette builds the palette of an opaque gray image, in value order.
func (e *encoder) grayPalette() {
	n := 0
	for v, ok := range e.st.grays {
		if ok {
			e.grayIdx[v] = uint8(n)
			e.plte[3*n], e.plte[3*n+1], e.plte[3*n+2] = uint8(v), uint8(v), uint8(v)
			n++
		}
	}
	e.npal, e.ntrns = n, 0
}

// colorPalette builds the palette from the color set: translucent colors
// first, so that tRNS stays short, each group in the order first seen.
func (e *encoder) colorPalette() {
	set := &e.set
	n := 0
	for pass := 0; pass < 2; pass++ {
		for i, v := range set.list[:set.n] {
			if (v>>24 == 0xff) != (pass == 1) {
				continue
			}
			set.pidx[i] = uint8(n)
			e.plte[3*n], e.plte[3*n+1], e.plte[3*n+2] = uint8(v), uint8(v>>8), uint8(v>>16)
			e.trns[n] = uint8(v >> 24)
			n++
		}
		if pass == 0 {
			e.ntrns = n
		}
	}
	e.npal = n
}
