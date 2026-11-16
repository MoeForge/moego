package png

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	stdpng "image/png"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var levels = []CompressionLevel{DefaultCompression, NoCompression, BestSpeed, BestCompression}

// nrgba64 returns the non-premultiplied color of c, keeping the color
// under alpha 0 for the types that store it, as image/png's decoder
// returns them.
func nrgba64(c color.Color) color.NRGBA64 {
	switch c := c.(type) {
	case color.NRGBA:
		return color.NRGBA64{uint16(c.R) * 0x101, uint16(c.G) * 0x101, uint16(c.B) * 0x101, uint16(c.A) * 0x101}
	case color.NRGBA64:
		return c
	}
	return color.NRGBA64Model.Convert(c).(color.NRGBA64)
}

// sameDecoded reports the first pixel where a and b differ.
func sameDecoded(a, b image.Image) error {
	ab, bb := a.Bounds(), b.Bounds()
	if ab.Dx() != bb.Dx() || ab.Dy() != bb.Dy() {
		return fmt.Errorf("size %v, want %v", bb, ab)
	}
	for y := 0; y < ab.Dy(); y++ {
		for x := 0; x < ab.Dx(); x++ {
			p, q := nrgba64(a.At(ab.Min.X+x, ab.Min.Y+y)), nrgba64(b.At(bb.Min.X+x, bb.Min.Y+y))
			if p != q {
				return fmt.Errorf("pixel (%d,%d) = %v, want %v", x, y, q, p)
			}
		}
	}
	return nil
}

// chunkInfo checks the chunk structure of a PNG file and returns the IHDR
// bit depth and color type.
func chunkInfo(data []byte) (depth, ct uint8, err error) {
	if !bytes.HasPrefix(data, []byte(pngHeader)) {
		return 0, 0, fmt.Errorf("no signature")
	}
	p := data[8:]
	var names []string
	for len(p) > 0 {
		if len(p) < 12 {
			return 0, 0, fmt.Errorf("truncated chunk")
		}
		n := int(binary.BigEndian.Uint32(p))
		if len(p) < 12+n {
			return 0, 0, fmt.Errorf("truncated chunk")
		}
		name := string(p[4:8])
		if crc32.ChecksumIEEE(p[4:8+n]) != binary.BigEndian.Uint32(p[8+n:]) {
			return 0, 0, fmt.Errorf("bad CRC in %s", name)
		}
		if name == "IHDR" {
			depth, ct = p[16], p[17]
			if n != 13 || p[18] != 0 || p[19] != 0 || p[20] != 0 {
				return 0, 0, fmt.Errorf("bad IHDR")
			}
		}
		if len(names) == 0 || names[len(names)-1] != name || name != "IDAT" {
			names = append(names, name)
		}
		p = p[12+n:]
	}
	s := fmt.Sprint(names)
	switch s {
	case "[IHDR IDAT IEND]", "[IHDR PLTE IDAT IEND]", "[IHDR PLTE tRNS IDAT IEND]":
	default:
		return 0, 0, fmt.Errorf("chunks %s", s)
	}
	return depth, ct, nil
}

// checkImage encodes m at every level, with and without KeepColorType, and
// compares the decoded pixels with image/png's round trip.
func checkImage(t *testing.T, name string, m image.Image) {
	t.Helper()
	var ref bytes.Buffer
	if err := stdpng.Encode(&ref, m); err != nil {
		t.Fatalf("%s: image/png: %v", name, err)
	}
	want, err := stdpng.Decode(bytes.NewReader(ref.Bytes()))
	if err != nil {
		t.Fatalf("%s: image/png decode: %v", name, err)
	}
	stdDepth, stdCT, err := chunkInfo(ref.Bytes())
	if err != nil {
		t.Fatalf("%s: image/png output: %v", name, err)
	}
	for _, keep := range []bool{false, true} {
		for _, l := range levels {
			enc := Encoder{CompressionLevel: l, KeepColorType: keep}
			var buf bytes.Buffer
			if err := enc.Encode(&buf, m); err != nil {
				t.Fatalf("%s level %d keep %v: %v", name, l, keep, err)
			}
			depth, ct, err := chunkInfo(buf.Bytes())
			if err != nil {
				t.Fatalf("%s level %d keep %v: %v", name, l, keep, err)
			}
			if keep && (depth != stdDepth || ct != stdCT) {
				t.Fatalf("%s level %d: KeepColorType wrote type %d depth %d, image/png %d %d", name, l, ct, depth, stdCT, stdDepth)
			}
			got, err := stdpng.Decode(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatalf("%s level %d keep %v: decode: %v", name, l, keep, err)
			}
			if err := sameDecoded(want, got); err != nil {
				t.Fatalf("%s level %d keep %v (type %d depth %d): %v", name, l, keep, ct, depth, err)
			}
		}
	}
}

// Content modes for generated images.
const (
	modeNoise  = iota // every sample random
	modeOpaque        // random colors, alpha at its maximum
	modeFew           // a few distinct colors, some translucent
	modeFewOpq        // a few distinct opaque colors
	modeGray          // opaque gray
	modeGrayA         // gray with random alpha
	modeAlpha0        // random colors, many with alpha 0
	modeSmooth        // gradients with a little noise, opaque
	modeExact         // opaque gray exact at 1, 2 or 4 bits
	nModes
)

// gen produces the samples of one generated image.
type gen struct {
	rng    *rand.Rand
	mode   int
	colors []color.NRGBA64
	narrow bool // 16-bit samples with equal bytes
	w      int
}

func newGen(rng *rand.Rand, mode, ncolors int, narrow bool, w int) *gen {
	g := &gen{rng: rng, mode: mode, narrow: narrow, w: w}
	if mode == modeFew || mode == modeFewOpq {
		seen := map[color.NRGBA64]bool{}
		for len(g.colors) < ncolors {
			c := g.color(modeNoise, 0, 0)
			if mode == modeFewOpq {
				c.A = 0xffff
			} else if rng.Intn(3) == 0 {
				c.A = 0xffff
			}
			if !seen[c] {
				seen[c] = true
				g.colors = append(g.colors, c)
			}
		}
	}
	if mode == modeExact {
		step := []uint16{0xffff, 0x5555, 0x1111}[rng.Intn(3)]
		for v := 0; v <= 0xffff; v += int(step) {
			g.colors = append(g.colors, color.NRGBA64{uint16(v), uint16(v), uint16(v), 0xffff})
		}
	}
	return g
}

func (g *gen) sample() uint16 {
	v := uint16(g.rng.Uint32())
	if g.narrow {
		v = v&0xff | v<<8
	}
	return v
}

func (g *gen) color(mode, x, y int) color.NRGBA64 {
	switch mode {
	case modeNoise:
		return color.NRGBA64{g.sample(), g.sample(), g.sample(), g.sample()}
	case modeOpaque:
		return color.NRGBA64{g.sample(), g.sample(), g.sample(), 0xffff}
	case modeFew, modeFewOpq, modeExact:
		return g.colors[g.rng.Intn(len(g.colors))]
	case modeGray:
		v := g.sample()
		return color.NRGBA64{v, v, v, 0xffff}
	case modeGrayA:
		v := g.sample()
		return color.NRGBA64{v, v, v, g.sample()}
	case modeAlpha0:
		c := color.NRGBA64{g.sample(), g.sample(), g.sample(), g.sample()}
		if g.rng.Intn(2) == 0 {
			c.A = 0
		}
		return c
	case modeSmooth:
		f := func(k int) uint16 {
			v := uint16((x*(37+k*11) + y*(23+k*7)) * 97)
			v += uint16(g.rng.Intn(3)) * 0x101
			if g.narrow {
				v = v&0xff | v<<8
			}
			return v
		}
		return color.NRGBA64{f(0), f(1), f(2), 0xffff}
	}
	panic("bad mode")
}

func (g *gen) at(x, y int) color.NRGBA64 { return g.color(g.mode, x, y) }

// Image kinds the generator builds.
var kinds = []string{"NRGBA", "RGBA", "Gray", "Gray16", "NRGBA64", "RGBA64", "Paletted", "YCbCr", "Alpha", "Alpha16", "CMYK", "generic", "genericPaletted"}

// opaqueImage hides the concrete type and the Opaque method.
type opaqueImage struct{ image.Image }

// palettedImage is a PalettedImage that is not *image.Paletted.
type palettedImage struct{ *image.Paletted }

func (p palettedImage) SubImage(r image.Rectangle) image.Image {
	return palettedImage{p.Paletted.SubImage(r).(*image.Paletted)}
}

// makeImage builds an image of the kind with bounds r, filled from g.
func makeImage(kind string, r image.Rectangle, g *gen, ncolors int) image.Image {
	switch kind {
	case "NRGBA", "generic":
		m := image.NewNRGBA(r)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				c := g.at(x, y)
				m.SetNRGBA(x, y, color.NRGBA{uint8(c.R >> 8), uint8(c.G >> 8), uint8(c.B >> 8), uint8(c.A >> 8)})
			}
		}
		if kind == "generic" {
			return opaqueImage{m}
		}
		return m
	case "RGBA":
		m := image.NewRGBA(r)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				c := g.at(x, y)
				n := color.NRGBA{uint8(c.R >> 8), uint8(c.G >> 8), uint8(c.B >> 8), uint8(c.A >> 8)}
				p := color.RGBAModel.Convert(n).(color.RGBA)
				if g.rng.Intn(50) == 0 {
					// Not a valid premultiplied color: a sample above alpha.
					p.R = p.A + uint8(g.rng.Intn(256-int(p.A)))
				}
				m.SetRGBA(x, y, p)
			}
		}
		return m
	case "Gray":
		m := image.NewGray(r)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				m.SetGray(x, y, color.Gray{uint8(g.at(x, y).R >> 8)})
			}
		}
		return m
	case "Gray16":
		m := image.NewGray16(r)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				m.SetGray16(x, y, color.Gray16{g.at(x, y).R})
			}
		}
		return m
	case "NRGBA64":
		m := image.NewNRGBA64(r)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				m.SetNRGBA64(x, y, g.at(x, y))
			}
		}
		return m
	case "RGBA64":
		m := image.NewRGBA64(r)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				m.SetRGBA64(x, y, color.RGBA64Model.Convert(g.at(x, y)).(color.RGBA64))
			}
		}
		return m
	case "Paletted", "genericPaletted":
		n := max(1, min(ncolors, 256))
		pal := make(color.Palette, n)
		for i := range pal {
			c := g.color(modeNoise, i, 0)
			switch g.rng.Intn(4) {
			case 0:
				pal[i] = color.NRGBA{uint8(c.R >> 8), uint8(c.G >> 8), uint8(c.B >> 8), uint8(c.A >> 8)}
			case 1:
				pal[i] = color.RGBAModel.Convert(color.NRGBA{uint8(c.R >> 8), uint8(c.G >> 8), uint8(c.B >> 8), uint8(c.A >> 8)})
			case 2:
				pal[i] = color.Gray{uint8(c.R >> 8)}
			default:
				pal[i] = color.NRGBA{uint8(c.R >> 8), uint8(c.G >> 8), uint8(c.B >> 8), 0xff}
			}
		}
		m := image.NewPaletted(r, pal)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				m.SetColorIndex(x, y, uint8(g.rng.Intn(n)))
			}
		}
		if kind == "genericPaletted" {
			return palettedImage{m}
		}
		return m
	case "YCbCr":
		ratios := []image.YCbCrSubsampleRatio{image.YCbCrSubsampleRatio444, image.YCbCrSubsampleRatio422, image.YCbCrSubsampleRatio420,
			image.YCbCrSubsampleRatio440, image.YCbCrSubsampleRatio411, image.YCbCrSubsampleRatio410}
		m := image.NewYCbCr(r, ratios[g.rng.Intn(len(ratios))])
		for i := range m.Y {
			m.Y[i] = uint8(g.at(i, 0).R >> 8)
		}
		for i := range m.Cb {
			c := g.at(i, 1)
			m.Cb[i], m.Cr[i] = uint8(c.G>>8), uint8(c.B>>8)
		}
		return m
	case "Alpha":
		m := image.NewAlpha(r)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				m.SetAlpha(x, y, color.Alpha{uint8(g.at(x, y).A >> 8)})
			}
		}
		return m
	case "Alpha16":
		m := image.NewAlpha16(r)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				m.SetAlpha16(x, y, color.Alpha16{g.at(x, y).A})
			}
		}
		return m
	case "CMYK":
		m := image.NewCMYK(r)
		for i := range m.Pix {
			m.Pix[i] = uint8(g.at(i, 0).R >> 8)
		}
		return m
	}
	panic("bad kind " + kind)
}

type subImager interface {
	SubImage(image.Rectangle) image.Image
}

// variants returns m and, when it can, a sub-image with a non-zero origin
// and a stride wider than its rows.
func variants(m image.Image) []image.Image {
	out := []image.Image{m}
	b := m.Bounds()
	if s, ok := m.(subImager); ok && b.Dx() > 2 && b.Dy() > 2 {
		out = append(out, s.SubImage(image.Rect(b.Min.X+1, b.Min.Y+1, b.Max.X-1, b.Max.Y)))
	}
	return out
}

var colorCounts = []int{1, 2, 3, 4, 5, 16, 17, 255, 256, 257}

func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	widths := []int{1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17, 31, 33, 64, 101, 257, 300}
	for ki, kind := range kinds {
		for mode := 0; mode < nModes; mode++ {
			for _, narrow := range []bool{false, true} {
				for wi, w := range widths {
					if raceEnabled && (ki+mode+wi)%4 != 0 {
						// The race detector slows encoding about 13 times:
						// a quarter of the cases, every kind, mode and width.
						continue
					}
					h := 1 + rng.Intn(6)
					if w <= 3 {
						h = 1 + rng.Intn(40)
					}
					nc := colorCounts[rng.Intn(len(colorCounts))]
					origin := image.Pt(rng.Intn(7)-3, rng.Intn(7)-3)
					if kind == "YCbCr" {
						// image.YCbCr's chroma offsets break below 0 for
						// subsampled ratios, in image/png too.
						origin = image.Pt(rng.Intn(4), rng.Intn(4))
					}
					r := image.Rectangle{origin, origin.Add(image.Pt(w, h))}
					g := newGen(rng, mode, nc, narrow, w)
					for i, m := range variants(makeImage(kind, r, g, nc)) {
						checkImage(t, fmt.Sprintf("%s mode %d narrow %v %dx%d colors %d variant %d", kind, mode, narrow, w, h, nc, i), m)
					}
				}
			}
		}
	}
}

// TestPaletteBoundaries covers every palette size boundary for reduced
// truecolor images and for palette images.
func TestPaletteBoundaries(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for _, nc := range colorCounts {
		for _, mode := range []int{modeFew, modeFewOpq} {
			for _, kind := range []string{"NRGBA", "RGBA", "NRGBA64", "Paletted", "genericPaletted", "generic"} {
				w, h := 37, 1+nc/20
				g := newGen(rng, mode, nc, true, w)
				m := makeImage(kind, image.Rect(0, 0, w, h), g, nc)
				checkImage(t, fmt.Sprintf("%s mode %d colors %d", kind, mode, nc), m)
			}
		}
	}
}

// TestGrayDepths covers gray images exact at each bit depth and with each
// number of gray levels.
func TestGrayDepths(t *testing.T) {
	for _, step := range []int{0xff, 0x55, 0x11, 1, 3} {
		for _, n := range []int{1, 2, 3, 4, 5, 16, 17, 256} {
			var vals []uint8
			for v := 0; v <= 255 && len(vals) < n; v += step {
				vals = append(vals, uint8(v))
			}
			for _, w := range []int{1, 7, 8, 9, 33} {
				h := 3
				m := image.NewGray(image.Rect(0, 0, w, h))
				m16 := image.NewNRGBA(m.Rect)
				for i := range m.Pix {
					v := vals[(i*7)%len(vals)]
					m.Pix[i] = v
					m16.Pix[4*i], m16.Pix[4*i+1], m16.Pix[4*i+2], m16.Pix[4*i+3] = v, v, v, 0xff
				}
				checkImage(t, fmt.Sprintf("gray step %d n %d w %d", step, n, w), m)
				checkImage(t, fmt.Sprintf("NRGBA gray step %d n %d w %d", step, n, w), m16)
			}
		}
	}
}

func TestLarge(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for ki, kind := range []string{"NRGBA", "RGBA", "Gray", "NRGBA64", "Paletted"} {
		for mi, mode := range []int{modeSmooth, modeNoise, modeFew} {
			if raceEnabled && (ki+mi)%3 != 0 {
				continue
			}
			g := newGen(rng, mode, 200, false, 700)
			m := makeImage(kind, image.Rect(0, 0, 700, 300), g, 200)
			checkImage(t, fmt.Sprintf("%s mode %d", kind, mode), m)
		}
	}
}

func TestInvalidSize(t *testing.T) {
	for _, r := range []image.Rectangle{image.Rect(0, 0, 0, 5), image.Rect(0, 0, 5, 0), {image.Pt(3, 3), image.Pt(2, 4)}} {
		m := &image.NRGBA{Rect: r}
		var buf bytes.Buffer
		err := Encode(&buf, m)
		want := stdpng.Encode(&buf, m)
		if err == nil || want == nil || err.Error() != want.Error() {
			t.Errorf("%v: error %v, image/png %v", r, err, want)
		}
	}
	m := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{})
	if err := Encode(&bytes.Buffer{}, m); err == nil || err.Error() != "png: invalid format: bad palette length: 0" {
		t.Errorf("empty palette: %v", err)
	}
}

// errWriter fails after n bytes.
type errWriter struct{ n int }

func (w *errWriter) Write(p []byte) (int, error) {
	if len(p) > w.n {
		k := w.n
		w.n = 0
		return k, fmt.Errorf("write failed")
	}
	w.n -= len(p)
	return len(p), nil
}

func TestWriteError(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	g := newGen(rng, modeNoise, 0, false, 300)
	m := makeImage("NRGBA", image.Rect(0, 0, 300, 300), g, 0)
	for _, n := range []int{0, 10, 100, 70000, 200000} {
		if err := Encode(&errWriter{n: n}, m); err == nil {
			t.Errorf("no error with a writer failing after %d bytes", n)
		}
	}
}

type pool struct{ b *EncoderBuffer }

func (p *pool) Get() *EncoderBuffer  { return p.b }
func (p *pool) Put(b *EncoderBuffer) { p.b = b }

// TestBufferPool checks that a warm pool gives the same output and
// allocates nothing.
func TestBufferPool(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for ki, kind := range []string{"NRGBA", "RGBA", "Gray", "NRGBA64", "Paletted", "YCbCr"} {
		for mi, mode := range []int{modeSmooth, modeFew, modeGray} {
			if raceEnabled && (ki+mi)%3 != 0 {
				continue
			}
			g := newGen(rng, mode, 20, false, 200)
			m := makeImage(kind, image.Rect(0, 0, 200, 150), g, 20)
			for _, l := range levels {
				var want bytes.Buffer
				if err := (&Encoder{CompressionLevel: l}).Encode(&want, m); err != nil {
					t.Fatal(err)
				}
				enc := &Encoder{CompressionLevel: l, BufferPool: &pool{}}
				var buf bytes.Buffer
				for i := 0; i < 2; i++ {
					buf.Reset()
					if err := enc.Encode(&buf, m); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(buf.Bytes(), want.Bytes()) {
						t.Fatalf("%s mode %d level %d: pooled output differs", kind, mode, l)
					}
				}
				if raceEnabled || checkptrEnabled() {
					continue
				}
				allocs := testing.AllocsPerRun(5, func() {
					buf.Reset()
					enc.Encode(&buf, m)
				})
				if allocs != 0 {
					t.Errorf("%s mode %d level %d: %v allocations with a warm pool", kind, mode, l, allocs)
				}
			}
		}
	}
}

// TestPaletteOrTruecolor checks that an image of at most 256 colors is
// written in truecolor when that is smaller: in a circular gradient,
// neighboring colors differ little, but their palette indices do not.
func TestPaletteOrTruecolor(t *testing.T) {
	const w, h = 300, 300
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8((x*x + y*y) / 8 % 256)
			m.SetNRGBA(x, y, color.NRGBA{v, v / 2, 255 - v, 255})
		}
	}
	for _, l := range []CompressionLevel{DefaultCompression, BestCompression} {
		var buf bytes.Buffer
		if err := (&Encoder{CompressionLevel: l}).Encode(&buf, m); err != nil {
			t.Fatal(err)
		}
		_, ct, err := chunkInfo(buf.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if ct != ctRGB {
			t.Errorf("level %d: color type %d, want RGB", l, ct)
		}
	}
	checkImage(t, "gradient", m)
}

// TestOtherLevels checks that levels image/png does not define encode as
// DefaultCompression, as in image/png.
func TestOtherLevels(t *testing.T) {
	g := newGen(rand.New(rand.NewSource(11)), modeSmooth, 0, false, 64)
	m := makeImage("NRGBA", image.Rect(0, 0, 64, 64), g, 0)
	var want bytes.Buffer
	if err := (&Encoder{}).Encode(&want, m); err != nil {
		t.Fatal(err)
	}
	for _, l := range []CompressionLevel{-7, -4, 1, 9, 100} {
		var buf bytes.Buffer
		if err := (&Encoder{CompressionLevel: l}).Encode(&buf, m); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf.Bytes(), want.Bytes()) {
			t.Errorf("level %d differs from DefaultCompression", l)
		}
	}
}

// TestCorpus round-trips a sample of the benchmark corpus, as image/png
// decodes it (NRGBA, RGBA, Paletted, Gray, 16-bit …) and as tight NRGBA.
// It runs with $MOEGO_PNG_CORPUS and $MOEGO_PNG_LIST set; every image
// with $MOEGO_PNG_EVERY=1, else every fifth one of at most 1 MP.
func TestCorpus(t *testing.T) {
	dir, list := os.Getenv("MOEGO_PNG_CORPUS"), os.Getenv("MOEGO_PNG_LIST")
	if dir == "" || list == "" {
		t.Skip("set MOEGO_PNG_CORPUS and MOEGO_PNG_LIST")
	}
	data, err := os.ReadFile(list)
	if err != nil {
		t.Fatal(err)
	}
	all := os.Getenv("MOEGO_PNG_EVERY") == "1"
	k := 0
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || strings.HasPrefix(f[0], "#") {
			continue
		}
		k++
		if !all && k%5 != 0 {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, f[1]))
		if err != nil {
			t.Fatal(err)
		}
		m, err := stdpng.Decode(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if !all && m.Bounds().Dx()*m.Bounds().Dy() > 1<<20 {
			continue
		}
		checkImage(t, fmt.Sprintf("%s (%T)", f[1], m), m)
		n := image.NewNRGBA(image.Rect(0, 0, m.Bounds().Dx(), m.Bounds().Dy()))
		draw.Draw(n, n.Rect, m, m.Bounds().Min, draw.Src)
		checkImage(t, f[1]+" (NRGBA)", n)
	}
}

// TestPaletteIndexOutOfRange writes indices beyond the palette as
// image/png does; its decoder shows them as opaque black.
func TestPaletteIndexOutOfRange(t *testing.T) {
	for _, n := range []int{2, 3, 5, 17} {
		pal := make(color.Palette, n)
		for i := range pal {
			pal[i] = color.NRGBA{uint8(40 * i), 0, 0, 255}
		}
		m := image.NewPaletted(image.Rect(0, 0, 9, 3), pal)
		for i := range m.Pix {
			m.Pix[i] = uint8(i % (n + 3))
		}
		checkImage(t, fmt.Sprintf("%d colors", n), m)
	}
}
