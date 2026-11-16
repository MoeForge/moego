package webp

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

// fuzzImage builds an image of one of the supported kinds from fuzz bytes.
// Samples come from pix, repeated as needed.
func fuzzImage(w, h int, kind uint8, pix []byte) image.Image {
	if len(pix) == 0 {
		pix = []byte{0}
	}
	k := 0
	next := func() uint8 {
		b := pix[k%len(pix)]
		k++
		return b
	}
	r := image.Rect(int(kind>>4)-8, 3, int(kind>>4)-8+w, 3+h)
	switch kind % 13 {
	case 0:
		m := image.NewNRGBA(r)
		for i := range m.Pix {
			m.Pix[i] = next()
		}
		return m
	case 1:
		m := image.NewRGBA(r)
		for i := range m.Pix {
			m.Pix[i] = next()
		}
		return m
	case 2:
		m := image.NewGray(r)
		for i := range m.Pix {
			m.Pix[i] = next()
		}
		return m
	case 3:
		m := image.NewGray16(r)
		for i := range m.Pix {
			m.Pix[i] = next()
		}
		return m
	case 4:
		m := image.NewNRGBA64(r)
		for i := range m.Pix {
			m.Pix[i] = next()
		}
		return m
	case 5:
		m := image.NewRGBA64(r)
		for i := range m.Pix {
			m.Pix[i] = next()
		}
		return m
	case 6:
		n := 1 + int(next())
		pal := make(color.Palette, n)
		for i := range pal {
			if next()&1 == 0 {
				pal[i] = color.NRGBA{next(), next(), next(), next()}
			} else {
				pal[i] = color.RGBA{next(), next(), next(), next()}
			}
		}
		m := image.NewPaletted(r, pal)
		for i := range m.Pix {
			m.Pix[i] = uint8(int(next()) % n)
		}
		return m
	case 7:
		m := image.NewAlpha(r)
		for i := range m.Pix {
			m.Pix[i] = next()
		}
		return m
	case 8:
		m := image.NewCMYK(r)
		for i := range m.Pix {
			m.Pix[i] = next()
		}
		return m
	case 9:
		r = r.Add(image.Pt(8, 0)) // keep YCbCr origins non-negative
		m := image.NewYCbCr(r, image.YCbCrSubsampleRatio(int(next())%6+1))
		for i := range m.Y {
			m.Y[i] = next()
		}
		for i := range m.Cb {
			m.Cb[i], m.Cr[i] = next(), next()
		}
		return m
	case 10, 11:
		// Few colors: a small table indexed by the bytes, with runs.
		n := 2 + int(kind%13-10)*15
		tab := make([][4]uint8, n)
		for i := range tab {
			tab[i] = [4]uint8{next(), next(), next(), next()}
			if i&1 == 0 {
				tab[i][3] = 0xff
			}
		}
		m := image.NewNRGBA(r)
		for i := 0; i < len(m.Pix); i += 4 {
			c := next()
			if c&3 == 0 && i >= 4 {
				copy(m.Pix[i:i+4], m.Pix[i-4:i])
				continue
			}
			copy(m.Pix[i:i+4], tab[int(c)%n][:])
		}
		return m
	default:
		m := image.NewNRGBA(r)
		for i := range m.Pix {
			m.Pix[i] = next()
		}
		return genericImage{m}
	}
}

func FuzzEncode(f *testing.F) {
	f.Add(uint8(3), uint8(2), uint8(0), uint8(0), []byte{1, 2, 3, 255})
	f.Add(uint8(17), uint8(5), uint8(6), uint8(1), []byte{0, 0xff, 0x80})
	f.Add(uint8(40), uint8(9), uint8(10), uint8(2), []byte("palette and gray"))
	f.Add(uint8(1), uint8(1), uint8(4), uint8(0), []byte{7, 7, 7, 7, 7, 7, 0xff, 0xff})
	f.Add(uint8(200), uint8(30), uint8(11), uint8(1), []byte("seventeen colors and some runs of them"))
	f.Fuzz(func(t *testing.T, w, h, kind, level uint8, pix []byte) {
		m := fuzzImage(1+int(w), 1+int(h)%64, kind, pix)
		enc := Encoder{CompressionLevel: levels[int(level)%len(levels)]}
		if level&4 != 0 {
			enc.BufferPool = &pool{}
		}
		var buf bytes.Buffer
		if err := enc.Encode(&buf, m); err != nil {
			t.Fatal(err)
		}
		if err := checkFile(buf.Bytes(), m); err != nil {
			t.Fatalf("kind %d level %d: %v", kind, level, err)
		}
	})
}
