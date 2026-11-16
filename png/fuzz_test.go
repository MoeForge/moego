package png

import (
	"bytes"
	"image"
	"image/color"
	stdpng "image/png"
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
	switch kind % 12 {
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
	case 10:
		// Few colors: a small table indexed by the bytes.
		var tab [5][4]uint8
		for i := range tab {
			tab[i] = [4]uint8{next(), next(), next(), next()}
			if i&1 == 0 {
				tab[i][3] = 0xff
			}
		}
		m := image.NewNRGBA(r)
		for i := 0; i < len(m.Pix); i += 4 {
			copy(m.Pix[i:i+4], tab[next()%5][:])
		}
		return m
	default:
		m := image.NewNRGBA(r)
		for i := range m.Pix {
			m.Pix[i] = next()
		}
		return opaqueImage{m}
	}
}

func FuzzEncode(f *testing.F) {
	f.Add(uint8(3), uint8(2), uint8(0), uint8(0), []byte{1, 2, 3, 255})
	f.Add(uint8(17), uint8(5), uint8(6), uint8(1), []byte{0, 0xff, 0x80})
	f.Add(uint8(40), uint8(9), uint8(10), uint8(3), []byte("palette and gray"))
	f.Add(uint8(1), uint8(1), uint8(4), uint8(2), []byte{7, 7, 7, 7, 7, 7, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, w, h, kind, level uint8, pix []byte) {
		m := fuzzImage(1+int(w), 1+int(h)%64, kind, pix)
		var ref bytes.Buffer
		if err := stdpng.Encode(&ref, m); err != nil {
			t.Fatal(err)
		}
		want, err := stdpng.Decode(&ref)
		if err != nil {
			t.Fatal(err)
		}
		enc := Encoder{CompressionLevel: -CompressionLevel(level % 4), KeepColorType: level&4 != 0}
		var buf bytes.Buffer
		if err := enc.Encode(&buf, m); err != nil {
			t.Fatal(err)
		}
		if _, _, err := chunkInfo(buf.Bytes()); err != nil {
			t.Fatal(err)
		}
		got, err := stdpng.Decode(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if err := sameDecoded(want, got); err != nil {
			t.Fatalf("kind %d level %d: %v", kind, level, err)
		}
	})
}
