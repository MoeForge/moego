package webp

import (
	"encoding/binary"
	"errors"
	"image"
	"image/color"
)

// maxSize is the format's limit on the width and the height.
const maxSize = 1 << 14

var errPaletteIndex = errors.New("webp: palette index out of range")

// load converts m into e.argb, w*h pixels in 0xAARRGGBB, each one equal to
// color.NRGBAModel.Convert(m.At(x, y)).
func (e *encoder) load(m image.Image) error {
	b := m.Bounds()
	w, h := b.Dx(), b.Dy()
	e.w, e.h = w, h
	e.argb = growU32(e.argb, w*h)
	d := e.argb
	e.loadAnd, e.loadAndOK = 0, false
	switch m := m.(type) {
	case *image.NRGBA:
		p := m.Pix[m.PixOffset(b.Min.X, b.Min.Y):]
		and := uint32(0xffffffff)
		for y := 0; y < h; y++ {
			and &= fromNRGBA(d[y*w:][:w], p[y*m.Stride:][:4*w])
		}
		e.loadAnd, e.loadAndOK = and, true
	case *image.RGBA:
		p := m.Pix[m.PixOffset(b.Min.X, b.Min.Y):]
		for y := 0; y < h; y++ {
			fromRGBA(d[y*w:][:w], p[y*m.Stride:][:4*w])
		}
	case *image.NRGBA64:
		p := m.Pix[m.PixOffset(b.Min.X, b.Min.Y):]
		for y := 0; y < h; y++ {
			fromNRGBA64(d[y*w:][:w], p[y*m.Stride:][:8*w])
		}
	case *image.RGBA64:
		p := m.Pix[m.PixOffset(b.Min.X, b.Min.Y):]
		for y := 0; y < h; y++ {
			fromRGBA64(d[y*w:][:w], p[y*m.Stride:][:8*w])
		}
	case *image.Gray:
		p := m.Pix[m.PixOffset(b.Min.X, b.Min.Y):]
		for y := 0; y < h; y++ {
			r := d[y*w:][:w]
			for i, v := range p[y*m.Stride:][:w] {
				r[i] = 0xff000000 | uint32(v)*0x10101
			}
		}
	case *image.Gray16:
		p := m.Pix[m.PixOffset(b.Min.X, b.Min.Y):]
		for y := 0; y < h; y++ {
			r := d[y*w:][:w]
			s := p[y*m.Stride:][:2*w]
			for i := range r {
				r[i] = 0xff000000 | uint32(s[2*i])*0x10101
			}
		}
	case *image.Alpha:
		p := m.Pix[m.PixOffset(b.Min.X, b.Min.Y):]
		for y := 0; y < h; y++ {
			r := d[y*w:][:w]
			for i, a := range p[y*m.Stride:][:w] {
				v := uint32(a) << 24
				if a != 0 {
					v |= 0xffffff
				}
				r[i] = v
			}
		}
	case *image.Paletted:
		if len(m.Palette) == 0 {
			return errors.New("webp: empty palette")
		}
		var tab [256]uint32
		for i, c := range m.Palette[:min(len(m.Palette), 256)] {
			tab[i] = paletteARGB(c)
		}
		n := len(m.Palette)
		p := m.Pix[m.PixOffset(b.Min.X, b.Min.Y):]
		for y := 0; y < h; y++ {
			r := d[y*w:][:w]
			var top uint8
			for i, v := range p[y*m.Stride:][:w] {
				top |= v
				r[i] = tab[v]
			}
			if n < 256 && int(top) >= n {
				for _, v := range p[y*m.Stride:][:w] {
					if int(v) >= n {
						return errPaletteIndex
					}
				}
			}
		}
	case *image.YCbCr:
		for y := 0; y < h; y++ {
			r := d[y*w:][:w]
			yy := b.Min.Y + y
			for i := range r {
				x := b.Min.X + i
				cr, cg, cb, _ := color.YCbCr{Y: m.Y[m.YOffset(x, yy)], Cb: m.Cb[m.COffset(x, yy)], Cr: m.Cr[m.COffset(x, yy)]}.RGBA()
				r[i] = 0xff000000 | cr>>8<<16 | cg>>8<<8 | cb>>8
			}
		}
	case *image.CMYK:
		p := m.Pix[m.PixOffset(b.Min.X, b.Min.Y):]
		for y := 0; y < h; y++ {
			r := d[y*w:][:w]
			s := p[y*m.Stride:][:4*w]
			for i := range r {
				cr, cg, cb, _ := color.CMYK{C: s[4*i], M: s[4*i+1], Y: s[4*i+2], K: s[4*i+3]}.RGBA()
				r[i] = 0xff000000 | cr>>8<<16 | cg>>8<<8 | cb>>8
			}
		}
	default:
		i := 0
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				d[i] = nrgbaARGB(color.NRGBAModel.Convert(m.At(x, y)).(color.NRGBA))
				i++
			}
		}
	}
	return nil
}

// paletteARGB is color.NRGBAModel.Convert(c) as ARGB. It does not call
// Convert, whose color.Color result would allocate for every color that
// is not already a color.NRGBA.
func paletteARGB(c color.Color) uint32 {
	if n, ok := c.(color.NRGBA); ok {
		return nrgbaARGB(n)
	}
	r, g, b, a := c.RGBA()
	return unpremul16(r, g, b, a)
}

func nrgbaARGB(c color.NRGBA) uint32 {
	return uint32(c.A)<<24 | uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)
}

// fromNRGBAGeneric converts a row of 8-bit non-premultiplied RGBA and
// returns the AND of the pixels.
func fromNRGBAGeneric(d []uint32, p []byte) uint32 {
	p = p[:4*len(d)]
	and := uint32(0xffffffff)
	for i := range d {
		v := binary.LittleEndian.Uint32(p[4*i:])
		v = v&0xff00ff00 | v>>16&0xff | v&0xff<<16
		d[i] = v
		and &= v
	}
	return and
}

// fromRGBA converts a row of 8-bit premultiplied RGBA as color.NRGBAModel
// does, through 16 bits.
func fromRGBA(d []uint32, p []byte) {
	p = p[:4*len(d)]
	for i := range d {
		s := p[4*i : 4*i+4 : 4*i+4]
		switch a := s[3]; a {
		case 0xff:
			d[i] = 0xff000000 | uint32(s[0])<<16 | uint32(s[1])<<8 | uint32(s[2])
		case 0:
			d[i] = 0
		default:
			a16 := uint32(a) * 0x101
			r := uint32(s[0]) * 0x101 * 0xffff / a16 >> 8 & 0xff
			g := uint32(s[1]) * 0x101 * 0xffff / a16 >> 8 & 0xff
			b := uint32(s[2]) * 0x101 * 0xffff / a16 >> 8 & 0xff
			d[i] = uint32(a)<<24 | r<<16 | g<<8 | b
		}
	}
}

// fromNRGBA64 converts a row of big-endian 16-bit non-premultiplied RGBA
// as color.NRGBAModel does: premultiplied by color.NRGBA64.RGBA first.
func fromNRGBA64(d []uint32, p []byte) {
	p = p[:8*len(d)]
	for i := range d {
		s := p[8*i : 8*i+8 : 8*i+8]
		a := uint32(s[6])<<8 | uint32(s[7])
		r := uint32(s[0])<<8 | uint32(s[1])
		g := uint32(s[2])<<8 | uint32(s[3])
		b := uint32(s[4])<<8 | uint32(s[5])
		d[i] = unpremul16(r*a/0xffff, g*a/0xffff, b*a/0xffff, a)
	}
}

// fromRGBA64 converts a row of big-endian 16-bit premultiplied RGBA.
func fromRGBA64(d []uint32, p []byte) {
	p = p[:8*len(d)]
	for i := range d {
		s := p[8*i : 8*i+8 : 8*i+8]
		a := uint32(s[6])<<8 | uint32(s[7])
		r := uint32(s[0])<<8 | uint32(s[1])
		g := uint32(s[2])<<8 | uint32(s[3])
		b := uint32(s[4])<<8 | uint32(s[5])
		d[i] = unpremul16(r, g, b, a)
	}
}

// unpremul16 is color.NRGBAModel's conversion of a 16-bit premultiplied
// color to 8-bit ARGB.
func unpremul16(r, g, b, a uint32) uint32 {
	switch a {
	case 0xffff:
		return 0xff000000 | r>>8<<16 | g>>8<<8 | b>>8
	case 0:
		return 0
	}
	r = r * 0xffff / a >> 8 & 0xff
	g = g * 0xffff / a >> 8 & 0xff
	b = b * 0xffff / a >> 8 & 0xff
	return a>>8<<24 | r<<16 | g<<8 | b
}

func growU32(b []uint32, n int) []uint32 {
	if cap(b) < n {
		return make([]uint32, n)
	}
	return b[:n]
}
