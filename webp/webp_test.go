package webp

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math/rand"
	"os"
	"testing"

	"github.com/MoeForge/moego/internal/vp8l/vp8ltest"
)

var levels = []CompressionLevel{BestSpeed, DefaultCompression, BestCompression}

var longTests = os.Getenv("MOEGO_WEBP_LONG") == "1"

type pool struct{ b *EncoderBuffer }

func (p *pool) Get() *EncoderBuffer  { return p.b }
func (p *pool) Put(b *EncoderBuffer) { p.b = b }

// want returns the pixels the output must decode to.
func want(m image.Image) []uint32 {
	b := m.Bounds()
	out := make([]uint32, 0, b.Dx()*b.Dy())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			out = append(out, nrgbaARGB(color.NRGBAModel.Convert(m.At(x, y)).(color.NRGBA)))
		}
	}
	return out
}

// checkFile decodes a WebP file and compares it with the pixels of m.
func checkFile(file []byte, m image.Image) error {
	w, h, argb, err := vp8ltest.DecodeWebP(file)
	if err != nil {
		return err
	}
	b := m.Bounds()
	if w != b.Dx() || h != b.Dy() {
		return fmt.Errorf("decoded %dx%d, want %dx%d", w, h, b.Dx(), b.Dy())
	}
	ref := want(m)
	opaque := true
	for i := range ref {
		if argb[i] != ref[i] {
			return fmt.Errorf("pixel (%d,%d) = %#08x, want %#08x", i%w, i/w, argb[i], ref[i])
		}
		opaque = opaque && ref[i]>>24 == 0xff
	}
	// alpha_is_used: bit 28 of the header after the signature.
	p := file[20:]
	alpha := p[4]>>4&1 == 1
	if alpha == opaque {
		return fmt.Errorf("alpha_is_used = %v for an image with opaque = %v", alpha, opaque)
	}
	return nil
}

// roundTrip encodes m at every level, with and without a pool, and checks
// the outputs.
func roundTrip(t *testing.T, name string, m image.Image) {
	t.Helper()
	for _, l := range levels {
		var first []byte
		for _, withPool := range []bool{false, true} {
			enc := Encoder{CompressionLevel: l}
			if withPool {
				enc.BufferPool = &pool{}
			}
			var buf bytes.Buffer
			if err := enc.Encode(&buf, m); err != nil {
				t.Fatalf("%s level %d: %v", name, l, err)
			}
			if err := checkFile(buf.Bytes(), m); err != nil {
				t.Fatalf("%s level %d pool %v: %v", name, l, withPool, err)
			}
			if first == nil {
				first = buf.Bytes()
			} else if !bytes.Equal(first, buf.Bytes()) {
				t.Fatalf("%s level %d: output differs with a pool", name, l)
			}
		}
	}
}

func fillNRGBA(rng *rand.Rand, m *image.NRGBA, colors int) {
	b := m.Bounds()
	var tab []color.NRGBA
	if colors > 0 {
		tab = make([]color.NRGBA, colors)
		for i := range tab {
			tab[i] = color.NRGBA{uint8(rng.Intn(256)), uint8(i), uint8(rng.Intn(256)), uint8(255 - i%3*100)}
		}
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			var c color.NRGBA
			if tab != nil {
				c = tab[rng.Intn(len(tab))]
				if rng.Intn(4) != 0 && x > b.Min.X {
					c = m.NRGBAAt(x-1, y)
				}
			} else {
				c = color.NRGBA{uint8(x * 7), uint8(y * 3), uint8(x ^ y), uint8(rng.Intn(256))}
			}
			m.SetNRGBA(x, y, c)
		}
	}
}

func TestRoundTripSizes(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	sizes := [][2]int{{1, 1}, {1, 2}, {2, 1}, {3, 3}, {5, 7}, {16, 16}, {17, 3}, {31, 33}, {64, 64}, {127, 5}, {300, 11}}
	if longTests {
		for w := 1; w <= 300; w += 7 {
			sizes = append(sizes, [2]int{w, 1 + w%13})
		}
	}
	for _, sz := range sizes {
		for _, colors := range []int{0, 1, 2, 3, 4, 5, 16, 17, 255, 256, 257} {
			m := image.NewNRGBA(image.Rect(0, 0, sz[0], sz[1]))
			fillNRGBA(rng, m, colors)
			roundTrip(t, fmt.Sprintf("%dx%d %d colors", sz[0], sz[1], colors), m)
		}
	}
}

func TestRoundTripPhotoLike(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for _, opaque := range []bool{true, false} {
		w, h := 97, 61
		m := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				v := uint8(x*2 + y + rng.Intn(5))
				a := uint8(255)
				if !opaque {
					a = uint8(x * 255 / w)
				}
				m.SetNRGBA(x, y, color.NRGBA{v, v + uint8(rng.Intn(3)), 255 - v, a})
			}
		}
		roundTrip(t, fmt.Sprintf("smooth opaque=%v", opaque), m)
	}
	// Noise.
	m := image.NewNRGBA(image.Rect(0, 0, 50, 40))
	rng.Read(m.Pix)
	roundTrip(t, "noise", m)
}

// TestHiddenColor checks that RGB under alpha 0 survives.
func TestHiddenColor(t *testing.T) {
	m := image.NewNRGBA(image.Rect(0, 0, 20, 10))
	for i := 0; i < len(m.Pix); i += 4 {
		m.Pix[i], m.Pix[i+1], m.Pix[i+2], m.Pix[i+3] = uint8(i), uint8(i>>2), 0x80, 0
		if i%12 == 0 {
			m.Pix[i+3] = 0x40
		}
	}
	roundTrip(t, "hidden", m)
}

type genericImage struct{ image.Image }

func imageKinds(rng *rand.Rand, r image.Rectangle) map[string]image.Image {
	out := map[string]image.Image{}
	nrgba := image.NewNRGBA(r)
	rng.Read(nrgba.Pix)
	out["NRGBA"] = nrgba
	rgba := image.NewRGBA(r)
	rng.Read(rgba.Pix)
	for i := 0; i < len(rgba.Pix); i += 4 {
		// Valid premultiplied colors, with the edge values.
		a := rgba.Pix[i+3]
		switch i / 4 % 5 {
		case 0:
			a = 0
		case 1:
			a = 255
		case 2:
			a = 1
		}
		rgba.Pix[i+3] = a
		for k := 0; k < 3; k++ {
			if rgba.Pix[i+k] > a {
				rgba.Pix[i+k] = a
			}
		}
	}
	out["RGBA"] = rgba
	n64 := image.NewNRGBA64(r)
	rng.Read(n64.Pix)
	out["NRGBA64"] = n64
	r64 := image.NewRGBA64(r)
	rng.Read(r64.Pix)
	for i := 0; i < len(r64.Pix); i += 8 {
		a := uint16(r64.Pix[i+6])<<8 | uint16(r64.Pix[i+7])
		if i/8%4 == 0 {
			a = 0xffff
			r64.Pix[i+6], r64.Pix[i+7] = 0xff, 0xff
		}
		for k := 0; k < 6; k += 2 {
			if v := uint16(r64.Pix[i+k])<<8 | uint16(r64.Pix[i+k+1]); v > a {
				r64.Pix[i+k], r64.Pix[i+k+1] = uint8(a>>8), uint8(a)
			}
		}
	}
	out["RGBA64"] = r64
	g := image.NewGray(r)
	rng.Read(g.Pix)
	out["Gray"] = g
	g16 := image.NewGray16(r)
	rng.Read(g16.Pix)
	out["Gray16"] = g16
	al := image.NewAlpha(r)
	rng.Read(al.Pix)
	out["Alpha"] = al
	pal := color.Palette{color.NRGBA{1, 2, 3, 0}, color.RGBA{10, 20, 30, 40}, color.Gray{9}, color.NRGBA{200, 100, 50, 255}, color.NRGBA64{0x1234, 0x5678, 0x9abc, 0x8000}}
	pm := image.NewPaletted(r, pal)
	for i := range pm.Pix {
		pm.Pix[i] = uint8(rng.Intn(len(pal)))
	}
	out["Paletted"] = pm
	for _, ratio := range []image.YCbCrSubsampleRatio{image.YCbCrSubsampleRatio444, image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio410} {
		y := image.NewYCbCr(r, ratio)
		rng.Read(y.Y)
		rng.Read(y.Cb)
		rng.Read(y.Cr)
		out["YCbCr"+ratio.String()] = y
	}
	cm := image.NewCMYK(r)
	rng.Read(cm.Pix)
	out["CMYK"] = cm
	out["generic"] = genericImage{nrgba}
	return out
}

// TestImageKinds checks every fast path against the generic conversion,
// with offset bounds and strides longer than a row.
func TestImageKinds(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for _, r := range []image.Rectangle{image.Rect(0, 0, 13, 7), image.Rect(-5, 3, 9, 12), image.Rect(2, 2, 3, 3)} {
		for name, m := range imageKinds(rng, r) {
			var e encoder
			if err := e.load(m); err != nil {
				t.Fatal(err)
			}
			ref := want(m)
			for i := range ref {
				if e.argb[i] != ref[i] {
					t.Fatalf("%s %v: pixel %d = %#08x, want %#08x", name, r, i, e.argb[i], ref[i])
				}
			}
			roundTrip(t, name, m)
			// A sub-image: offset Pix and a stride longer than the row.
			if s, ok := m.(interface {
				SubImage(image.Rectangle) image.Image
			}); ok && r.Dx() > 3 && r.Dy() > 3 {
				sr := image.Rect(r.Min.X+1, r.Min.Y+2, r.Max.X-1, r.Max.Y-1)
				roundTrip(t, name+" sub", s.SubImage(sr))
			}
		}
	}
}

// TestConversionsExhaustive checks the 8-bit and 16-bit conversions
// against color.NRGBAModel for many values.
func TestConversionsExhaustive(t *testing.T) {
	d := make([]uint32, 1)
	for a := 0; a < 256; a++ {
		for v := 0; v < 256; v++ {
			p := []byte{uint8(v), uint8(v * 7), uint8(v / 3), uint8(a)}
			for k := 0; k < 3; k++ {
				if int(p[k]) > a {
					p[k] = uint8(a)
				}
			}
			fromRGBA(d, p)
			want := nrgbaARGB(color.NRGBAModel.Convert(color.RGBA{p[0], p[1], p[2], p[3]}).(color.NRGBA))
			if d[0] != want {
				t.Fatalf("RGBA %v: %#08x, want %#08x", p, d[0], want)
			}
		}
	}
	rng := rand.New(rand.NewSource(5))
	p := make([]byte, 8)
	for i := 0; i < 200000; i++ {
		rng.Read(p)
		fromNRGBA64(d, p)
		c := color.NRGBA64{uint16(p[0])<<8 | uint16(p[1]), uint16(p[2])<<8 | uint16(p[3]), uint16(p[4])<<8 | uint16(p[5]), uint16(p[6])<<8 | uint16(p[7])}
		if want := nrgbaARGB(color.NRGBAModel.Convert(c).(color.NRGBA)); d[0] != want {
			t.Fatalf("NRGBA64 %v: %#08x, want %#08x", c, d[0], want)
		}
	}
}

func TestSizeLimits(t *testing.T) {
	for _, sz := range [][2]int{{16384, 1}, {1, 16384}} {
		m := image.NewNRGBA(image.Rect(0, 0, sz[0], sz[1]))
		for i := range m.Pix {
			m.Pix[i] = uint8(i / 7)
		}
		roundTrip(t, fmt.Sprintf("%dx%d", sz[0], sz[1]), m)
	}
	for _, r := range []image.Rectangle{image.Rect(0, 0, 16385, 1), image.Rect(0, 0, 1, 16385), image.Rect(0, 0, 0, 5), image.Rect(3, 3, 3, 3)} {
		err := Encode(&bytes.Buffer{}, image.NewNRGBA(r))
		want := fmt.Sprintf("webp: invalid image size: %dx%d", r.Dx(), r.Dy())
		if err == nil || err.Error() != want {
			t.Errorf("%v: error %v, want %q", r, err, want)
		}
	}
}

type failWriter struct{ n int }

var errWrite = errors.New("write failed")

func (f *failWriter) Write(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errWrite
	}
	f.n--
	return len(p), nil
}

func TestWriterError(t *testing.T) {
	m := image.NewNRGBA(image.Rect(0, 0, 9, 9))
	m.Pix[0] = 1
	// The header, then the payload.
	for n := 0; n < 2; n++ {
		if err := Encode(&failWriter{n: n}, m); err != errWrite {
			t.Errorf("after %d writes: %v", n, err)
		}
	}
}

func TestBadPalette(t *testing.T) {
	m := image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{color.Black})
	m.Pix[5] = 3
	if err := Encode(&bytes.Buffer{}, m); err == nil {
		t.Fatal("no error for an index past the palette")
	}
}

func TestDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(6))
	m := image.NewNRGBA(image.Rect(0, 0, 70, 50))
	fillNRGBA(rng, m, 0)
	for _, l := range levels {
		var a, b bytes.Buffer
		enc := Encoder{CompressionLevel: l}
		if err := enc.Encode(&a, m); err != nil {
			t.Fatal(err)
		}
		other := image.NewNRGBA(image.Rect(0, 0, 33, 120))
		fillNRGBA(rng, other, 17)
		if err := enc.Encode(&bytes.Buffer{}, other); err != nil {
			t.Fatal(err)
		}
		if err := enc.Encode(&b, m); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a.Bytes(), b.Bytes()) {
			t.Fatalf("level %d: output differs between runs", l)
		}
	}
}

func TestOtherLevels(t *testing.T) {
	m := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	fillNRGBA(rand.New(rand.NewSource(7)), m, 0)
	var ref bytes.Buffer
	if err := Encode(&ref, m); err != nil {
		t.Fatal(err)
	}
	for _, l := range []CompressionLevel{-1, 1, 9, -4, 100} {
		var buf bytes.Buffer
		if err := (&Encoder{CompressionLevel: l}).Encode(&buf, m); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf.Bytes(), ref.Bytes()) {
			t.Errorf("level %d differs from DefaultCompression", l)
		}
	}
}

// TestZeroAllocs checks that a warm pool makes encodes allocation-free.
func TestZeroAllocs(t *testing.T) {
	if raceEnabled || checkptrEnabled() {
		t.Skip("allocation counts differ under the race detector and checkptr")
	}
	rng := rand.New(rand.NewSource(9))
	big := image.NewNRGBA(image.Rect(0, 0, 120, 90))
	fillNRGBA(rng, big, 0)
	few := image.NewNRGBA(image.Rect(0, 0, 100, 60))
	fillNRGBA(rng, few, 40)
	two := image.NewNRGBA(image.Rect(0, 0, 77, 30))
	fillNRGBA(rng, two, 2)
	gray := image.NewGray(image.Rect(0, 0, 64, 64))
	rng.Read(gray.Pix)
	// A palette of colors that are not color.NRGBA, as image/png decodes
	// an opaque paletted PNG.
	pal := image.NewPaletted(image.Rect(0, 0, 50, 40), nil)
	for i := 0; i < 64; i++ {
		pal.Palette = append(pal.Palette, color.RGBA{uint8(4 * i), uint8(255 - 4*i), uint8(i), 255})
	}
	for i := range pal.Pix {
		pal.Pix[i] = uint8(rng.Intn(64))
	}
	ims := []image.Image{big, few, two, gray, pal}
	for _, l := range levels {
		p := &pool{}
		enc := Encoder{CompressionLevel: l, BufferPool: p}
		for _, m := range ims {
			if err := enc.Encode(io.Discard, m); err != nil {
				t.Fatal(err)
			}
		}
		for _, m := range ims {
			if n := testing.AllocsPerRun(3, func() {
				if err := enc.Encode(io.Discard, m); err != nil {
					t.Fatal(err)
				}
			}); n != 0 {
				t.Errorf("level %d %T %v: %v allocations per warm encode", l, m, m.Bounds(), n)
			}
		}
	}
}

// TestPaletteARGB checks the palette conversion against color.NRGBAModel
// on random colors of every standard color type.
func TestPaletteARGB(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	u8 := func() uint8 { return uint8(rng.Intn(256)) }
	u16 := func() uint16 { return uint16(rng.Intn(1 << 16)) }
	for i := 0; i < 50000; i++ {
		var c color.Color
		switch i % 10 {
		case 0:
			c = color.NRGBA{u8(), u8(), u8(), u8()}
		case 1:
			a := u8()
			c = color.RGBA{min(u8(), a), min(u8(), a), min(u8(), a), a}
		case 2:
			c = color.RGBA{u8(), u8(), u8(), u8()} // not validly premultiplied
		case 3:
			c = color.Gray{u8()}
		case 4:
			c = color.Gray16{u16()}
		case 5:
			c = color.Alpha{u8()}
		case 6:
			c = color.NRGBA64{u16(), u16(), u16(), u16()}
		case 7:
			a := u16()
			c = color.RGBA64{min(u16(), a), min(u16(), a), min(u16(), a), a}
		case 8:
			c = color.YCbCr{u8(), u8(), u8()}
		case 9:
			c = color.CMYK{u8(), u8(), u8(), u8()}
		}
		want := nrgbaARGB(color.NRGBAModel.Convert(c).(color.NRGBA))
		if got := paletteARGB(c); got != want {
			t.Fatalf("%#v: got %08x, want %08x", c, got, want)
		}
	}
}
