package webp

import (
	"math"
	"math/rand"
	"testing"
)

// The inverse transforms, written from RFC 9649 for the tests.

func invPredictor(res []uint32, w, h, bits int, modes []uint8) []uint32 {
	out := make([]uint32, w*h)
	tw := subSize(w, bits)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			var pred uint32
			switch {
			case x == 0 && y == 0:
				pred = 0xff000000
			case y == 0:
				pred = out[i-1]
			case x == 0:
				pred = out[i-w]
			default:
				tr := out[i-w+1]
				if x == w-1 {
					tr = out[y*w]
				}
				pred = predict(int(modes[(y>>bits)*tw+x>>bits]), out[i-1], out[i-w], tr, out[i-w-1])
			}
			out[i] = addPixels(res[i], pred)
		}
	}
	return out
}

func invColorTransform(img []uint32, w, h, bits int, elems []ctElem) {
	tw := subSize(w, bits)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := elems[(y>>bits)*tw+x>>bits]
			p := img[y*w+x]
			g := int8(p >> 8)
			r := (int32(p>>16&0xff) + ctDelta(c.g2r, g)) & 0xff
			b := (int32(p&0xff) + ctDelta(c.g2b, g) + ctDelta(c.r2b, int8(r))) & 0xff
			img[y*w+x] = p&0xff00ff00 | uint32(r)<<16 | uint32(b)
		}
	}
}

func addGreen(p uint32) uint32 {
	g := p >> 8 & 0xff
	r := (p>>16 + g) & 0xff
	b := (p + g) & 0xff
	return p&0xff00ff00 | r<<16 | b
}

// unbundle expands a packed index image of width subSize(w, wb).
func unbundle(px []uint32, w, h, wb int, table []uint32) []uint32 {
	pw := subSize(w, wb)
	out := make([]uint32, w*h)
	per := 1 << wb
	bitsPer := 8 >> wb
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			g := px[y*pw+x/per] >> 8 & 0xff
			k := g >> (uint(x%per) * uint(bitsPer)) & (1<<bitsPer - 1)
			v := uint32(0)
			if int(k) < len(table) {
				v = table[k]
			}
			out[y*w+x] = v
		}
	}
	return out
}

func randomImage(rng *rand.Rand, w, h, kind int) []uint32 {
	px := make([]uint32, w*h)
	switch kind {
	case 0:
		for i := range px {
			px[i] = rng.Uint32()
		}
	case 1: // smooth
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				v := uint32(x*3+y*5+rng.Intn(4)) & 0xff
				px[y*w+x] = 0xff000000 | v<<16 | (v+uint32(rng.Intn(3)))&0xff<<8 | (255-v)&0xff
			}
		}
	case 2: // extremes, for the clamps
		vals := []uint32{0, 1, 127, 128, 254, 255}
		for i := range px {
			var p uint32
			for sh := 0; sh < 32; sh += 8 {
				p |= vals[rng.Intn(len(vals))] << sh
			}
			px[i] = p
		}
	}
	return px
}

func TestPixelArithmetic(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 100000; i++ {
		a, b := rng.Uint32(), rng.Uint32()
		var sub, add, avg uint32
		for sh := 0; sh < 32; sh += 8 {
			x, y := a>>sh&0xff, b>>sh&0xff
			sub |= (x - y) & 0xff << sh
			add |= (x + y) & 0xff << sh
			avg |= (x + y) / 2 << sh
		}
		if got := subPixels(a, b); got != sub {
			t.Fatalf("subPixels(%#x, %#x) = %#x, want %#x", a, b, got, sub)
		}
		if got := addPixels(a, b); got != add {
			t.Fatalf("addPixels(%#x, %#x) = %#x, want %#x", a, b, got, add)
		}
		if got := average2(a, b); got != avg {
			t.Fatalf("average2(%#x, %#x) = %#x, want %#x", a, b, got, avg)
		}
		if got := addGreen(subtractGreen(a)); got != a {
			t.Fatalf("subtract green of %#x does not invert: %#x", a, got)
		}
	}
}

func TestPredictorInverse(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for _, sz := range [][2]int{{1, 1}, {1, 7}, {7, 1}, {2, 2}, {5, 3}, {17, 9}, {33, 31}, {64, 5}} {
		w, h := sz[0], sz[1]
		for kind := 0; kind < 3; kind++ {
			img := randomImage(rng, w, h, kind)
			for bits := 2; bits <= 9; bits++ {
				tw, th := subSize(w, bits), subSize(h, bits)
				modes := make([]uint8, tw*th)
				for m := 0; m <= nModes; m++ {
					for i := range modes {
						if m == nModes {
							modes[i] = uint8(rng.Intn(nModes))
						} else {
							modes[i] = uint8(m)
						}
					}
					res := append([]uint32(nil), img...)
					applyPredictor(res, w, h, bits, modes, make([]uint32, w))
					back := invPredictor(res, w, h, bits, modes)
					for i := range img {
						if back[i] != img[i] {
							t.Fatalf("%dx%d kind %d bits %d mode %d: pixel %d = %#x, want %#x", w, h, kind, bits, m, i, back[i], img[i])
						}
					}
					undoPredictor(res, w, h, bits, modes)
					for i := range img {
						if res[i] != img[i] {
							t.Fatalf("%dx%d kind %d bits %d mode %d: undo pixel %d = %#x, want %#x", w, h, kind, bits, m, i, res[i], img[i])
						}
					}
				}
			}
		}
	}
}

func TestColorTransformInverse(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for _, sz := range [][2]int{{1, 1}, {9, 4}, {40, 33}} {
		w, h := sz[0], sz[1]
		img := randomImage(rng, w, h, 0)
		for bits := 2; bits <= 9; bits++ {
			tw, th := subSize(w, bits), subSize(h, bits)
			elems := make([]ctElem, tw*th)
			for i := range elems {
				elems[i] = ctElem{int8(rng.Intn(256)), int8(rng.Intn(256)), int8(rng.Intn(256))}
			}
			if bits == 2 {
				elems[0] = ctElem{-128, 127, -128}
			}
			c := append([]uint32(nil), img...)
			applyColorTransform(c, w, h, bits, elems)
			u := append([]uint32(nil), c...)
			invColorTransform(c, w, h, bits, elems)
			undoColorTransform(u, w, h, bits, elems)
			for i := range img {
				if c[i] != img[i] {
					t.Fatalf("%dx%d bits %d: pixel %d = %#x, want %#x", w, h, bits, i, c[i], img[i])
				}
				if u[i] != img[i] {
					t.Fatalf("%dx%d bits %d: undo pixel %d = %#x, want %#x", w, h, bits, i, u[i], img[i])
				}
			}
		}
	}
	// Every multiplier against every channel value.
	for m := -128; m < 128; m++ {
		for v := 0; v < 256; v++ {
			p := 0xff000000 | uint32(v)<<16 | uint32(255-v)<<8 | uint32(v^0x5a)
			c := ctElem{int8(m), int8(-m - 1), int8(m ^ 0x33)}
			q := []uint32{colorTransform(p, c)}
			invColorTransform(q, 1, 1, 2, []ctElem{c})
			if q[0] != p {
				t.Fatalf("element %v pixel %#x: %#x", c, p, q[0])
			}
		}
	}
}

func TestBundleInverse(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	for _, n := range []int{1, 2, 3, 4, 5, 16, 17, 255, 256} {
		for w := 1; w <= 19; w++ {
			h := 1 + rng.Intn(4)
			e := &encoder{w: w, h: h}
			e.argb = make([]uint32, w*h)
			table := make([]uint32, n)
			for i := range table {
				table[i] = rng.Uint32() | uint32(i)
			}
			for i := range e.argb {
				e.argb[i] = table[rng.Intn(n)]
			}
			e.analyze()
			if e.an.ncolors > n {
				t.Fatalf("%d colors counted, at most %d used", e.an.ncolors, n)
			}
			e.indexImage()
			for k := 0; k < nOrders; k++ {
				e.pal.setOrder(&e.an.colors, e.paletteOrder(k, e.order[:]))
				wb := bundleBits(e.pal.n)
				packed := e.packIndices(nil, &e.pal, wb)
				back := unbundle(packed, w, h, wb, e.pal.color[:e.pal.n])
				for i := range back {
					if back[i] != e.argb[i] {
						t.Fatalf("%d colors w %d order %d: pixel %d = %#x, want %#x", n, w, k, i, back[i], e.argb[i])
					}
				}
				// The delta-coded table.
				d := e.pal.deltas(make([]uint32, 256))
				prev := uint32(0)
				for i, v := range d {
					prev = addPixels(prev, v)
					if prev != e.pal.color[i] {
						t.Fatalf("table delta %d", i)
					}
				}
			}
		}
	}
}

// refPredict is RFC 9649's predictor table written out per channel.
func refPredict(m int, l, t, tr, tl uint32) uint32 {
	ch := func(p uint32, c int) int { return int(p >> (8 * c) & 0xff) }
	clamp := func(v int) int { return min(255, max(0, v)) }
	avg := func(a, b int) int { return (a + b) / 2 }
	if m == 11 {
		pl, pt := 0, 0
		for c := 0; c < 4; c++ {
			p := ch(l, c) + ch(t, c) - ch(tl, c)
			pl += max(p-ch(l, c), ch(l, c)-p)
			pt += max(p-ch(t, c), ch(t, c)-p)
		}
		if pl < pt {
			return l
		}
		return t
	}
	var out uint32
	for c := 0; c < 4; c++ {
		L, T, TR, TL := ch(l, c), ch(t, c), ch(tr, c), ch(tl, c)
		var v int
		switch m {
		case 0:
			v = []int{0, 0, 0, 255}[c]
		case 1:
			v = L
		case 2:
			v = T
		case 3:
			v = TR
		case 4:
			v = TL
		case 5:
			v = avg(avg(L, TR), T)
		case 6:
			v = avg(L, TL)
		case 7:
			v = avg(L, T)
		case 8:
			v = avg(TL, T)
		case 9:
			v = avg(T, TR)
		case 10:
			v = avg(avg(L, TL), avg(T, TR))
		case 12:
			v = clamp(L + T - TL)
		case 13:
			a := avg(L, T)
			v = clamp(a + (a-TL)/2)
		}
		out |= uint32(v) << (8 * c)
	}
	return out
}

func TestPredictReference(t *testing.T) {
	rng := rand.New(rand.NewSource(8))
	vals := []uint32{0, 1, 2, 127, 128, 129, 253, 254, 255}
	pick := func() uint32 {
		if rng.Intn(2) == 0 {
			return rng.Uint32()
		}
		var p uint32
		for sh := 0; sh < 32; sh += 8 {
			p |= vals[rng.Intn(len(vals))] << sh
		}
		return p
	}
	for i := 0; i < 200000; i++ {
		l, tp, tr, tl := pick(), pick(), pick(), pick()
		for m := 0; m < nModes; m++ {
			if got, want := predict(m, l, tp, tr, tl), refPredict(m, l, tp, tr, tl); got != want {
				t.Fatalf("mode %d L %#x T %#x TR %#x TL %#x: %#x, want %#x", m, l, tp, tr, tl, got, want)
			}
		}
	}
	// The row residuals against predict, for every mode.
	for _, w := range []int{2, 3, 9, 40} {
		img := randomImage(rng, w, 3, 2)
		dst := make([]uint32, w-1)
		for m := 0; m < nModes; m++ {
			residualsGeneric(m, dst, img, 2*w+1, w)
			for k := range dst {
				j := 2*w + 1 + k
				x := j % w
				tr := img[j-w+1]
				if x == w-1 {
					tr = img[2*w]
				}
				if want := subPixels(img[j], refPredict(m, img[j-1], img[j-w], tr, img[j-w-1])); dst[k] != want {
					t.Fatalf("w %d mode %d pixel %d: %#x, want %#x", w, m, k, dst[k], want)
				}
			}
		}
	}
}

func TestLog2f(t *testing.T) {
	for _, x := range []float32{0.5, 1, 1.5, 2, 3, 7, 100, 1234.5, 1e6, 3e9} {
		if d := log2f(x) - float32(math.Log2(float64(x))); d > 2e-4 || d < -2e-4 {
			t.Errorf("log2f(%v) off by %v", x, d)
		}
	}
	for i := 1; i < 1<<20; i += 37 {
		x := float32(i) / 16
		if d := log2f(x) - float32(math.Log2(float64(x))); d > 2e-4 || d < -2e-4 {
			t.Fatalf("log2f(%v) off by %v", x, d)
		}
	}
}

func TestAddGreenPixel(t *testing.T) {
	rng := rand.New(rand.NewSource(15))
	for i := 0; i < 100000; i++ {
		p := rng.Uint32()
		if addGreenPixel(subtractGreen(p)) != p || addGreenPixel(p) != addGreen(p) {
			t.Fatalf("addGreenPixel(%#x)", p)
		}
	}
}
