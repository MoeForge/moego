package vp8ltest

import (
	"encoding/binary"
	"fmt"
	"math/rand"
)

// Writer is the stream-writing API of moego/internal/vp8l's Encoder.
type Writer interface {
	Reset(level int)
	Header(w, h int, alphaUsed bool)
	WriteBits(v uint32, n uint)
	SubImage(argb []uint32, w, h int)
	MainImage(argb []uint32, w, h int)
	Finish() []byte
}

// Transform kinds, as the bitstream numbers them.
const (
	Predictor     = predictorTransform
	Color         = colorTransform
	SubtractGreen = subtractGreen
	ColorIndexing = colorIndexing
)

// A Transform is applied by Encode before the main image. Predictor: Data
// holds a mode (0 to 13) per block of 1<<Bits pixels squared. Color: Data
// holds a color transform element per block, packed as the sub-image pixel
// (red_to_blue<<16 | green_to_blue<<8 | green_to_red). ColorIndexing: Data
// is the color table, which must hold every pixel; tables of up to 16
// colors bundle pixels.
type Transform struct {
	Kind int
	Bits int
	Data []uint32
}

// Encode writes argb (w*h pixels) through wr at level, applying ts in
// order, and returns the payload. argb is not modified.
func Encode(wr Writer, level int, argb []uint32, w, h int, ts []Transform) []byte {
	wr.Reset(level)
	alpha := false
	for _, p := range argb {
		if p>>24 != 0xff {
			alpha = true
		}
	}
	wr.Header(w, h, alpha)
	px := append([]uint32(nil), argb...)
	for _, t := range ts {
		wr.WriteBits(1, 1)
		wr.WriteBits(uint32(t.Kind), 2)
		switch t.Kind {
		case Predictor:
			wr.WriteBits(uint32(t.Bits-2), 3)
			bw, bh := divRoundUp(w, 1<<t.Bits), divRoundUp(h, 1<<t.Bits)
			sub := make([]uint32, bw*bh)
			for i, m := range t.Data[:bw*bh] {
				sub[i] = 0xff000000 | m<<8
			}
			wr.SubImage(sub, bw, bh)
			px = predictForward(px, w, h, t.Bits, t.Data)
		case Color:
			wr.WriteBits(uint32(t.Bits-2), 3)
			bw, bh := divRoundUp(w, 1<<t.Bits), divRoundUp(h, 1<<t.Bits)
			sub := make([]uint32, bw*bh)
			for i, e := range t.Data[:bw*bh] {
				sub[i] = 0xff000000 | e&0xffffff
			}
			wr.SubImage(sub, bw, bh)
			colorForward(px, w, h, t.Bits, sub)
		case SubtractGreen:
			for i, p := range px {
				g := p >> 8 & 0xff
				px[i] = p&0xff00ff00 | ((p>>16-g)&0xff)<<16 | (p-g)&0xff
			}
		case ColorIndexing:
			n := len(t.Data)
			wr.WriteBits(uint32(n-1), 8)
			sub := make([]uint32, n)
			sub[0] = t.Data[0]
			for i := 1; i < n; i++ {
				sub[i] = subPixels(t.Data[i], t.Data[i-1])
			}
			wr.SubImage(sub, n, 1)
			px, w = indexForward(px, w, h, t.Data)
		}
	}
	wr.WriteBits(0, 1)
	wr.MainImage(px, w, h)
	return append([]byte(nil), wr.Finish()...)
}

// WebP wraps a VP8L payload in a simple lossless WebP file.
func WebP(payload []byte) []byte {
	n := len(payload)
	pad := n & 1
	f := make([]byte, 20+n+pad)
	copy(f, "RIFF")
	binary.LittleEndian.PutUint32(f[4:], uint32(12+n+pad))
	copy(f[8:], "WEBPVP8L")
	binary.LittleEndian.PutUint32(f[16:], uint32(n))
	copy(f[20:], payload)
	return f
}

// subPixels subtracts b from a per channel, modulo 256.
func subPixels(a, b uint32) uint32 {
	ag := (a | 0x00ff00ff) - (b & 0xff00ff00)
	rb := (a | 0xff00ff00) - (b & 0x00ff00ff)
	return ag&0xff00ff00 | rb&0x00ff00ff
}

func predictForward(px []uint32, w, h, bits int, modes []uint32) []uint32 {
	bw := divRoundUp(w, 1<<bits)
	out := make([]uint32, len(px))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			var pred uint32
			switch {
			case x == 0 && y == 0:
				pred = 0xff000000
			case y == 0:
				pred = px[i-1]
			case x == 0:
				pred = px[i-w]
			default:
				m := modes[(y>>bits)*bw+x>>bits]
				pred = predict(int(m), px[i-1], px[i-w], px[i-w+1], px[i-w-1])
			}
			out[i] = subPixels(px[i], pred)
		}
	}
	return out
}

func colorForward(px []uint32, w, h, bits int, sub []uint32) {
	bw := divRoundUp(w, 1<<bits)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			e := sub[(y>>bits)*bw+x>>bits]
			g2r, g2b, r2b := int8(e), int8(e>>8), int8(e>>16)
			p := px[i]
			g, r := int8(p>>8), int8(p>>16)
			nr := int(p>>16&0xff) - delta(g2r, g)
			nb := int(p&0xff) - delta(g2b, g) - delta(r2b, r)
			px[i] = p&0xff00ff00 | uint32(nr&0xff)<<16 | uint32(nb&0xff)
		}
	}
}

func indexForward(px []uint32, w, h int, pal []uint32) ([]uint32, int) {
	idx := make(map[uint32]uint32, len(pal))
	for i := len(pal) - 1; i >= 0; i-- {
		idx[pal[i]] = uint32(i)
	}
	bits := 0
	switch n := len(pal); {
	case n <= 2:
		bits = 3
	case n <= 4:
		bits = 2
	case n <= 16:
		bits = 1
	}
	nw := divRoundUp(w, 1<<bits)
	per := 8 >> bits
	out := make([]uint32, nw*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			k, ok := idx[px[y*w+x]]
			if !ok {
				panic(fmt.Sprintf("vp8ltest: pixel %08x not in the color table", px[y*w+x]))
			}
			out[y*nw+x>>bits] |= k << (8 + uint(per*(x&(1<<bits-1))))
		}
	}
	for i := range out {
		out[i] |= 0xff000000
	}
	return out, nw
}

// A Case is a test image with the transforms to code it with.
type Case struct {
	Name string
	W, H int
	ARGB []uint32
	TS   []Transform
}

// Cases returns test images for round trips: random, flat, gradients, few
// colors, long runs, repeats far back, every width from 1 to 300 and the
// widest, with hand-written transforms of every kind. long adds the
// slower ones (a 4096x300 image with repeats near the farthest distance).
func Cases(seed int64, long bool) []Case {
	rng := rand.New(rand.NewSource(seed))
	var cs []Case
	add := func(name string, w, h int, px []uint32, ts ...Transform) {
		cs = append(cs, Case{Name: fmt.Sprintf("%s-%dx%d", name, w, h), W: w, H: h, ARGB: px, TS: ts})
	}
	for _, sz := range [][2]int{{1, 1}, {2, 1}, {1, 3}, {5, 7}, {64, 64}, {300, 17}} {
		w, h := sz[0], sz[1]
		add("random", w, h, Random(rng, w, h))
		add("const", w, h, Constant(w, h, 0x80402010))
		add("zero", w, h, Constant(w, h, 0))
		add("gradient", w, h, Gradient(w, h))
		add("few", w, h, FewColors(rng, w, h, 5))
		add("runs", w, h, Runs(rng, w, h))
	}
	// Every channel value equally often: all code lengths 8, which the
	// header codes with repeats only.
	uni := make([]uint32, 256*4)
	for i := range uni {
		v := uint32(i)
		uni[i] = (v*7)&0xff<<24 | v&0xff<<16 | (v*3)&0xff<<8 | (v*5)&0xff
	}
	add("uniform", 256, 4, uni)
	for k := 1; k <= 3; k++ {
		px := make([]uint32, 64)
		for i := range px {
			px[i] = 0xff000000 | uint32(i%k*200)<<8
		}
		add(fmt.Sprintf("greens%d", k), 8, 8, px)
	}
	for w := 1; w <= 300; w++ {
		h := 1 + rng.Intn(4)
		add("width", w, h, Smooth(rng, w, h))
	}
	add("widest", 1<<14, 2, Smooth(rng, 1<<14, 2))
	add("tallest", 1, 1<<14, Smooth(rng, 1, 1<<14))
	add("repeat", 1000, 40, Repeats(rng, 1000, 40, 9000))

	// Transforms by hand.
	for _, sz := range [][2]int{{1, 1}, {3, 2}, {17, 9}, {64, 33}, {129, 70}} {
		w, h := sz[0], sz[1]
		img := Smooth(rng, w, h)
		for bits := 2; bits <= 9; bits += 3 {
			add(fmt.Sprintf("pred%d", bits), w, h, img, Transform{Kind: Predictor, Bits: bits, Data: randModes(rng, w, h, bits)})
			add(fmt.Sprintf("color%d", bits), w, h, img, Transform{Kind: Color, Bits: bits, Data: randElems(rng, w, h, bits)})
		}
		add("green", w, h, img, Transform{Kind: SubtractGreen})
		add("all3", w, h, img,
			Transform{Kind: SubtractGreen},
			Transform{Kind: Color, Bits: 3, Data: randElems(rng, w, h, 3)},
			Transform{Kind: Predictor, Bits: 2, Data: randModes(rng, w, h, 2)})
		add("all3b", w, h, img,
			Transform{Kind: Predictor, Bits: 4, Data: randModes(rng, w, h, 4)},
			Transform{Kind: SubtractGreen},
			Transform{Kind: Color, Bits: 2, Data: randElems(rng, w, h, 2)})
		for _, n := range []int{1, 2, 3, 4, 5, 16, 17, 256} {
			pal := Palette(rng, n)
			px := make([]uint32, w*h)
			for i := range px {
				px[i] = pal[rng.Intn(n)]
			}
			add(fmt.Sprintf("index%d", n), w, h, px, Transform{Kind: ColorIndexing, Data: pal})
			pw := divRoundUp(w, 1<<bundleBits(n))
			add(fmt.Sprintf("index%dpred", n), w, h, px,
				Transform{Kind: ColorIndexing, Data: pal},
				Transform{Kind: Predictor, Bits: 2, Data: randModes(rng, pw, h, 2)})
		}
	}
	if long {
		add("far", 4096, 300, Repeats(rng, 4096, 300, 1<<20-121))
		add("farther", 4096, 300, Repeats(rng, 4096, 300, 1<<20))
	}
	return cs
}

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

func randModes(rng *rand.Rand, w, h, bits int) []uint32 {
	m := make([]uint32, divRoundUp(w, 1<<bits)*divRoundUp(h, 1<<bits))
	for i := range m {
		m[i] = uint32(rng.Intn(14))
	}
	return m
}

func randElems(rng *rand.Rand, w, h, bits int) []uint32 {
	m := make([]uint32, divRoundUp(w, 1<<bits)*divRoundUp(h, 1<<bits))
	for i := range m {
		m[i] = rng.Uint32() & 0xffffff
	}
	return m
}

// Random returns uniformly random pixels.
func Random(rng *rand.Rand, w, h int) []uint32 {
	px := make([]uint32, w*h)
	for i := range px {
		px[i] = rng.Uint32()
	}
	return px
}

// Constant returns one color everywhere.
func Constant(w, h int, c uint32) []uint32 {
	px := make([]uint32, w*h)
	for i := range px {
		px[i] = c
	}
	return px
}

// Gradient returns a smooth opaque gradient.
func Gradient(w, h int) []uint32 {
	px := make([]uint32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			px[y*w+x] = 0xff000000 | uint32(x&0xff)<<16 | uint32(y&0xff)<<8 | uint32((x+y)&0xff)
		}
	}
	return px
}

// Smooth returns a noisy gradient with some transparency.
func Smooth(rng *rand.Rand, w, h int) []uint32 {
	px := make([]uint32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := uint32(0xff)
			if (x/7+y/5)%5 == 0 {
				a = uint32(rng.Intn(256))
			}
			r := uint32(x*3+rng.Intn(4)) & 0xff
			g := uint32(y*2+x+rng.Intn(3)) & 0xff
			b := uint32(x^y) & 0xff
			px[y*w+x] = a<<24 | r<<16 | g<<8 | b
		}
	}
	return px
}

// Palette returns n distinct random colors.
func Palette(rng *rand.Rand, n int) []uint32 {
	seen := map[uint32]bool{}
	pal := make([]uint32, 0, n)
	for len(pal) < n {
		c := rng.Uint32()
		if rng.Intn(3) == 0 {
			c |= 0xff000000
		}
		if !seen[c] {
			seen[c] = true
			pal = append(pal, c)
		}
	}
	return pal
}

// FewColors returns pixels drawn from n random colors.
func FewColors(rng *rand.Rand, w, h, n int) []uint32 {
	pal := Palette(rng, n)
	px := make([]uint32, w*h)
	for i := range px {
		px[i] = pal[rng.Intn(n)]
	}
	return px
}

// Runs returns runs of random length of few colors.
func Runs(rng *rand.Rand, w, h int) []uint32 {
	pal := Palette(rng, 4)
	px := make([]uint32, w*h)
	for i := 0; i < len(px); {
		n := 1 + rng.Intn(5000)
		c := pal[rng.Intn(4)]
		for ; n > 0 && i < len(px); n-- {
			px[i] = c
			i++
		}
	}
	return px
}

// Repeats returns random pixels in which blocks repeat dist pixels back.
func Repeats(rng *rand.Rand, w, h, dist int) []uint32 {
	px := Random(rng, w, h)
	for i := dist; i+64 <= len(px); i += 1 + rng.Intn(5000) {
		copy(px[i:i+1+rng.Intn(64)], px[i-dist:])
	}
	return px
}
