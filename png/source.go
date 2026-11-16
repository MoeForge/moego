package png

import (
	"image"
	"image/color"
	"strconv"
)

// Canonical sample formats: the samples image/png writes for an image,
// before any color reduction.
const (
	fmtRGBA8  = iota // 4 bytes per pixel, non-premultiplied
	fmtG8            // 1 byte per pixel
	fmtRGBA16        // 8 bytes per pixel, big-endian, non-premultiplied
	fmtG16           // 2 bytes per pixel, big-endian
	fmtIndex         // 1 palette index per pixel
)

// fmtBytes is the size of one pixel in each canonical format.
var fmtBytes = [...]int{fmtRGBA8: 4, fmtG8: 1, fmtRGBA16: 8, fmtG16: 2, fmtIndex: 1}

// Source kinds: the concrete image types read directly from Pix, and
// srcGeneric for every other image, read once with At into the cache.
const (
	srcNRGBA = iota
	srcRGBA
	srcGray
	srcGray16
	srcNRGBA64
	srcRGBA64
	srcAlpha
	srcPaletted
	srcYCbCr
	srcCMYK
	srcGeneric
)

// source is an image and how to read its canonical rows.
type source struct {
	m      image.Image
	kind   uint8
	format uint8
	w, h   int
	pix    []byte // direct kinds: Pix from the first pixel of the bounds
	stride int
	// opaque is image/png's choice between the color types with and
	// without alpha for the RGBA formats; set by encoder.setup.
	opaque  bool
	palette color.Palette // fmtIndex
	ycbcr   *image.YCbCr
}

// classify fills s for m. A palette with no entries or more than 256 is an
// error, as in image/png.
func (s *source) classify(m image.Image) error {
	b := m.Bounds()
	*s = source{m: m, w: b.Dx(), h: b.Dy(), kind: srcGeneric}
	switch m := m.(type) {
	case *image.NRGBA:
		s.kind, s.format = srcNRGBA, fmtRGBA8
		s.pix, s.stride = m.Pix[m.PixOffset(b.Min.X, b.Min.Y):], m.Stride
	case *image.RGBA:
		s.kind, s.format = srcRGBA, fmtRGBA8
		s.pix, s.stride = m.Pix[m.PixOffset(b.Min.X, b.Min.Y):], m.Stride
	case *image.Gray:
		s.kind, s.format = srcGray, fmtG8
		s.pix, s.stride = m.Pix[m.PixOffset(b.Min.X, b.Min.Y):], m.Stride
	case *image.Gray16:
		s.kind, s.format = srcGray16, fmtG16
		s.pix, s.stride = m.Pix[m.PixOffset(b.Min.X, b.Min.Y):], m.Stride
	case *image.NRGBA64:
		s.kind, s.format = srcNRGBA64, fmtRGBA16
		s.pix, s.stride = m.Pix[m.PixOffset(b.Min.X, b.Min.Y):], m.Stride
	case *image.RGBA64:
		s.kind, s.format = srcRGBA64, fmtRGBA16
		s.pix, s.stride = m.Pix[m.PixOffset(b.Min.X, b.Min.Y):], m.Stride
	case *image.Alpha:
		s.kind, s.format = srcAlpha, fmtRGBA8
		s.pix, s.stride = m.Pix[m.PixOffset(b.Min.X, b.Min.Y):], m.Stride
	case *image.Paletted:
		if m.Palette != nil {
			s.kind, s.format = srcPaletted, fmtIndex
			s.pix, s.stride = m.Pix[m.PixOffset(b.Min.X, b.Min.Y):], m.Stride
			s.palette = m.Palette
		}
	case *image.YCbCr:
		s.kind, s.format = srcYCbCr, fmtRGBA16
		s.ycbcr = m
		s.opaque = true
	case *image.CMYK:
		s.kind, s.format = srcCMYK, fmtRGBA16
		s.pix, s.stride = m.Pix[m.PixOffset(b.Min.X, b.Min.Y):], m.Stride
		s.opaque = true
	}
	if s.kind == srcGeneric {
		// image/png's choice, in its order: a palette needs ColorIndexAt.
		if _, ok := m.(image.PalettedImage); ok {
			s.palette, _ = m.ColorModel().(color.Palette)
		}
		if s.palette != nil {
			s.format = fmtIndex
		} else {
			switch m.ColorModel() {
			case color.GrayModel:
				s.format = fmtG8
			case color.Gray16Model:
				s.format = fmtG16
			case color.RGBAModel, color.NRGBAModel, color.AlphaModel:
				s.format = fmtRGBA8
			default:
				s.format = fmtRGBA16
			}
		}
	}
	if s.format == fmtIndex && (len(s.palette) < 1 || len(s.palette) > 256) {
		return FormatError("bad palette length: " + strconv.Itoa(len(s.palette)))
	}
	return nil
}

type opaquer interface{ Opaque() bool }

// stdOpaque reports image/png's opacity for the direct kinds with an alpha
// channel: every alpha sample at its maximum.
func (s *source) stdOpaque() bool {
	n := s.w
	switch s.kind {
	case srcNRGBA, srcRGBA:
		for y := 0; y < s.h; y++ {
			if o, _ := rgbaFlags(s.pix[y*s.stride:][:4*n]); !o {
				return false
			}
		}
	case srcNRGBA64, srcRGBA64:
		for y := 0; y < s.h; y++ {
			if !alphaOpaque16(s.pix[y*s.stride:][:8*n]) {
				return false
			}
		}
	case srcAlpha:
		for y := 0; y < s.h; y++ {
			for _, a := range s.pix[y*s.stride:][:n] {
				if a != 0xff {
					return false
				}
			}
		}
	}
	return true
}

func alphaOpaque8(p []byte) bool {
	for i := 3; i < len(p); i += 4 {
		if p[i] != 0xff {
			return false
		}
	}
	return true
}

func alphaOpaque16(p []byte) bool {
	for i := 6; i < len(p); i += 8 {
		if p[i]&p[i+1] != 0xff {
			return false
		}
	}
	return true
}

// loadGeneric converts a generic image into cache with image/png's color
// conversions and sets s.opaque. Images whose ColorModel is not a palette,
// Gray or Gray16 decide between alpha and no alpha as image/png does: by
// their Opaque method if they have one, else by every pixel's alpha.
func (s *source) loadGeneric(cache []byte) []byte {
	m, b := s.m, s.m.Bounds()
	rowLen := s.w * fmtBytes[s.format]
	n := rowLen * s.h
	if cap(cache) < n {
		cache = make([]byte, n)
	}
	cache = cache[:n]
	o, hasOpaque := m.(opaquer)
	tc := hasOpaque && o.Opaque()
	opaque := true
	i := 0
	switch s.format {
	case fmtIndex:
		pi := m.(image.PalettedImage)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				cache[i] = pi.ColorIndexAt(x, y)
				i++
			}
		}
	case fmtG8:
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				cache[i] = color.GrayModel.Convert(m.At(x, y)).(color.Gray).Y
				i++
			}
		}
	case fmtG16:
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				c := color.Gray16Model.Convert(m.At(x, y)).(color.Gray16)
				cache[i], cache[i+1] = uint8(c.Y>>8), uint8(c.Y)
				i += 2
			}
		}
	case fmtRGBA8:
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				c := m.At(x, y)
				d := cache[i : i+4 : i+4]
				if tc {
					r, g, b, _ := c.RGBA()
					d[0], d[1], d[2], d[3] = uint8(r>>8), uint8(g>>8), uint8(b>>8), 0xff
				} else {
					if !hasOpaque {
						if _, _, _, a := c.RGBA(); a != 0xffff {
							opaque = false
						}
					}
					n := color.NRGBAModel.Convert(c).(color.NRGBA)
					d[0], d[1], d[2], d[3] = n.R, n.G, n.B, n.A
				}
				i += 4
			}
		}
	case fmtRGBA16:
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				c := m.At(x, y)
				d := cache[i : i+8 : i+8]
				var r, g, bl, a uint32
				if tc {
					r, g, bl, _ = c.RGBA()
					a = 0xffff
				} else {
					if !hasOpaque {
						if _, _, _, a := c.RGBA(); a != 0xffff {
							opaque = false
						}
					}
					n := color.NRGBA64Model.Convert(c).(color.NRGBA64)
					r, g, bl, a = uint32(n.R), uint32(n.G), uint32(n.B), uint32(n.A)
				}
				d[0], d[1], d[2], d[3] = uint8(r>>8), uint8(r), uint8(g>>8), uint8(g)
				d[4], d[5], d[6], d[7] = uint8(bl>>8), uint8(bl), uint8(a>>8), uint8(a)
				i += 8
			}
		}
	}
	if hasOpaque {
		opaque = tc
	}
	s.opaque = opaque
	s.pix, s.stride = cache, rowLen
	return cache
}

// row returns canonical row y, converting into scratch (at least one row
// long) when the source's Pix is not already canonical.
func (s *source) row(y int, scratch []byte) []byte {
	n := s.w
	switch s.kind {
	case srcNRGBA:
		return s.pix[y*s.stride:][:4*n]
	case srcGray, srcPaletted:
		return s.pix[y*s.stride:][:n]
	case srcGray16:
		return s.pix[y*s.stride:][:2*n]
	case srcNRGBA64:
		return s.pix[y*s.stride:][:8*n]
	case srcGeneric:
		return s.pix[y*s.stride:][:fmtBytes[s.format]*n]
	case srcRGBA:
		p := s.pix[y*s.stride:][:4*n]
		if alphaOpaque8(p) {
			return p
		}
		d := scratch[:4*n]
		unpremultiply8(d, p)
		return d
	case srcRGBA64:
		p := s.pix[y*s.stride:][:8*n]
		if alphaOpaque16(p) {
			return p
		}
		d := scratch[:8*n]
		unpremultiply16(d, p)
		return d
	case srcAlpha:
		p := s.pix[y*s.stride:][:n]
		d := scratch[:4*n]
		for i, a := range p {
			v := uint8(0xff)
			if a == 0 {
				v = 0
			}
			d[4*i], d[4*i+1], d[4*i+2], d[4*i+3] = v, v, v, a
		}
		return d
	case srcCMYK:
		p := s.pix[y*s.stride:][:4*n]
		d := scratch[:8*n]
		for i := 0; i < n; i++ {
			r, g, b, _ := color.CMYK{C: p[4*i], M: p[4*i+1], Y: p[4*i+2], K: p[4*i+3]}.RGBA()
			put16(d[8*i:], r, g, b, 0xffff)
		}
		return d
	case srcYCbCr:
		m := s.ycbcr
		yy := m.Rect.Min.Y + y
		d := scratch[:8*n]
		for i := 0; i < n; i++ {
			x := m.Rect.Min.X + i
			r, g, b, _ := color.YCbCr{Y: m.Y[m.YOffset(x, yy)], Cb: m.Cb[m.COffset(x, yy)], Cr: m.Cr[m.COffset(x, yy)]}.RGBA()
			put16(d[8*i:], r, g, b, 0xffff)
		}
		return d
	}
	panic("png: unknown source")
}

func put16(d []byte, r, g, b, a uint32) {
	d = d[:8]
	d[0], d[1], d[2], d[3] = uint8(r>>8), uint8(r), uint8(g>>8), uint8(g)
	d[4], d[5], d[6], d[7] = uint8(b>>8), uint8(b), uint8(a>>8), uint8(a)
}

// unpremultiply8 converts alpha-premultiplied 8-bit RGBA to
// non-premultiplied as color.NRGBAModel does, through 16 bits.
func unpremultiply8(d, s []byte) {
	for i := 0; i+3 < len(s); i += 4 {
		switch a := s[i+3]; a {
		case 0xff:
			d[i], d[i+1], d[i+2], d[i+3] = s[i], s[i+1], s[i+2], 0xff
		case 0:
			d[i], d[i+1], d[i+2], d[i+3] = 0, 0, 0, 0
		default:
			a16 := uint32(a) * 0x101
			d[i] = uint8(uint32(s[i]) * 0x101 * 0xffff / a16 >> 8)
			d[i+1] = uint8(uint32(s[i+1]) * 0x101 * 0xffff / a16 >> 8)
			d[i+2] = uint8(uint32(s[i+2]) * 0x101 * 0xffff / a16 >> 8)
			d[i+3] = a
		}
	}
}

// unpremultiply16 is unpremultiply8 for big-endian 16-bit samples, as
// color.NRGBA64Model converts.
func unpremultiply16(d, s []byte) {
	for i := 0; i+7 < len(s); i += 8 {
		a := uint32(s[i+6])<<8 | uint32(s[i+7])
		switch a {
		case 0xffff:
			copy(d[i:i+8], s[i:i+8])
		case 0:
			clear(d[i : i+8])
		default:
			r := uint32(s[i])<<8 | uint32(s[i+1])
			g := uint32(s[i+2])<<8 | uint32(s[i+3])
			b := uint32(s[i+4])<<8 | uint32(s[i+5])
			put16(d[i:], r*0xffff/a, g*0xffff/a, b*0xffff/a, a)
		}
	}
}
