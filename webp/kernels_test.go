package webp

import (
	"math/rand"
	"testing"
)

// kernelPixels returns random pixels that favor the byte values where the
// predictors' averages and clamps change behavior.
func kernelPixels(rng *rand.Rand, n int) []uint32 {
	vals := []uint32{0, 1, 2, 126, 127, 128, 129, 253, 254, 255}
	p := make([]uint32, n)
	for i := range p {
		if rng.Intn(3) == 0 {
			p[i] = rng.Uint32()
			continue
		}
		for sh := 0; sh < 32; sh += 8 {
			p[i] |= vals[rng.Intn(len(vals))] << sh
		}
	}
	return p
}

// checkResiduals compares residuals with residualsGeneric for every mode
// on the second row of img (w pixels wide, at least two rows).
func checkResiduals(t *testing.T, img []uint32, w int, dst []uint32) {
	t.Helper()
	n := len(dst)
	want := make([]uint32, n)
	for m := 0; m < nModes; m++ {
		residualsGeneric(m, want, img, w+1, w)
		residuals(m, dst, img, w+1, w)
		for k := range want {
			if dst[k] != want[k] {
				t.Fatalf("w %d n %d mode %d: residual %d = %#x, want %#x", w, n, m, k, dst[k], want[k])
			}
		}
	}
}

func TestResidualKernels(t *testing.T) {
	requireKernels(t)
	rng := rand.New(rand.NewSource(11))
	for w := 2; w <= 301; w++ {
		img := kernelPixels(rng, 2*w)
		checkResiduals(t, img, w, make([]uint32, w-1))
		// Shorter spans starting mid-row.
		if w > 20 {
			n := 1 + rng.Intn(w-10)
			dst := make([]uint32, n)
			want := make([]uint32, n)
			start := w + 1 + rng.Intn(w-1-n)
			for m := 0; m < nModes; m++ {
				residualsGeneric(m, want, img, start, w)
				residuals(m, dst, img, start, w)
				for k := range want {
					if dst[k] != want[k] {
						t.Fatalf("w %d span %d+%d mode %d: residual %d differs", w, start, n, m, k)
					}
				}
			}
		}
	}
}

func TestSubtractGreenKernel(t *testing.T) {
	requireKernels(t)
	rng := rand.New(rand.NewSource(12))
	for n := 0; n <= 300; n++ {
		src := kernelPixels(rng, n)
		dst := make([]uint32, n)
		subtractGreenRow(dst, src)
		for i, p := range src {
			if dst[i] != subtractGreen(p) {
				t.Fatalf("n %d: pixel %d = %#x, want %#x", n, i, dst[i], subtractGreen(p))
			}
		}
	}
}

func TestAddGreenKernel(t *testing.T) {
	requireKernels(t)
	rng := rand.New(rand.NewSource(19))
	for n := 0; n <= 300; n++ {
		src := kernelPixels(rng, n)
		dst := make([]uint32, n)
		addGreenRow(dst, src)
		for i, p := range src {
			if dst[i] != addGreenPixel(p) {
				t.Fatalf("n %d: pixel %d = %#x, want %#x", n, i, dst[i], addGreenPixel(p))
			}
		}
	}
}

func TestFromNRGBAKernel(t *testing.T) {
	requireKernels(t)
	rng := rand.New(rand.NewSource(14))
	for n := 0; n <= 300; n++ {
		p := make([]byte, 4*n)
		rng.Read(p)
		got := make([]uint32, n)
		want := make([]uint32, n)
		gotAnd := fromNRGBA(got, p)
		wantAnd := uint32(0xffffffff)
		for i := range want {
			v := uint32(p[4*i+3])<<24 | uint32(p[4*i])<<16 | uint32(p[4*i+1])<<8 | uint32(p[4*i+2])
			want[i] = v
			wantAnd &= v
		}
		if gotAnd != wantAnd || fromNRGBAGeneric(make([]uint32, n), p) != wantAnd {
			t.Fatalf("n %d: AND %#x, want %#x", n, gotAnd, wantAnd)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("n %d: pixel %d = %#x, want %#x", n, i, got[i], want[i])
			}
		}
	}
}

func TestCostKernels(t *testing.T) {
	requireKernels(t)
	rng := rand.New(rand.NewSource(16))
	var m model
	for c := range m.cost {
		for v := range m.cost[c] {
			m.cost[c][v] = uint32(rng.Intn(1 << 20))
		}
	}
	for n := 0; n <= 300; n++ {
		res := kernelPixels(rng, n)
		for _, set := range []int{scoreAll, scoreRGB, scoreGreen} {
			got := make([]uint32, n)
			want := make([]uint32, n)
			for k := range got {
				got[k] = uint32(k * 1000)
				want[k] = got[k]
			}
			m.pixelCosts(got, res, set)
			m.pixelCostsGeneric(want, res, set)
			for k := range want {
				if got[k] != want[k] {
					t.Fatalf("n %d set %d: cost %d = %d, want %d", n, set, k, got[k], want[k])
				}
			}
		}
		x := make([]int8, n)
		y := make([]int8, n)
		for i := range x {
			x[i], y[i] = int8(rng.Intn(256)), int8(rng.Intn(256))
		}
		for _, mul := range []int32{-128, -77, -1, 0, 1, 33, 127} {
			if got, want := ctCostSum(&m.cost[2], x, y, mul), ctCostGeneric(&m.cost[2], x, y, mul); got != want {
				t.Fatalf("n %d m %d: color transform cost %d, want %d", n, mul, got, want)
			}
		}
	}
}

func TestAddTopKernel(t *testing.T) {
	requireKernels(t)
	rng := rand.New(rand.NewSource(17))
	for n := 0; n <= 300; n++ {
		top := kernelPixels(rng, n+2)
		res := kernelPixels(rng, n)
		for _, m := range []int{0, 2, 3, 4, 8, 9} {
			got := append([]uint32(nil), res...)
			want := append([]uint32(nil), res...)
			addTopRow(m, got, top)
			addTopRowGeneric(m, want, top)
			for k := range want {
				if got[k] != want[k] {
					t.Fatalf("n %d mode %d: pixel %d = %#x, want %#x", n, m, k, got[k], want[k])
				}
			}
		}
	}
}

func TestColorTransformKernels(t *testing.T) {
	requireKernels(t)
	rng := rand.New(rand.NewSource(18))
	for n := 0; n <= 300; n++ {
		src := kernelPixels(rng, n)
		c := ctElem{int8(rng.Intn(256)), int8(rng.Intn(256)), int8(rng.Intn(256))}
		switch n % 4 {
		case 1:
			c = ctElem{-128, -128, -128}
		case 2:
			c = ctElem{127, -128, 127}
		}
		got := make([]uint32, n)
		want := make([]uint32, n)
		colorTransformRow(got, src, c)
		colorTransformRowGeneric(want, src, c)
		for k := range want {
			if got[k] != want[k] {
				t.Fatalf("n %d %v: pixel %d = %#x, want %#x", n, c, k, got[k], want[k])
			}
		}
		undoColorTransformRow(got, src, c)
		undoColorTransformRowGeneric(want, src, c)
		for k := range want {
			if got[k] != want[k] {
				t.Fatalf("n %d %v: undo pixel %d = %#x, want %#x", n, c, k, got[k], want[k])
			}
		}
	}
}

func TestColorTransformTileKernel(t *testing.T) {
	requireKernels(t)
	rng := rand.New(rand.NewSource(17))
	for _, sz := range [][2]int{{16, 1}, {16, 16}, {32, 7}, {48, 3}, {512, 32}, {128, 128}} {
		w, rows := sz[0], sz[1]
		stride := w + rng.Intn(9)
		img := kernelPixels(rng, (rows-1)*stride+w+5)
		if rows == 128 {
			// Extremes everywhere, for the sums' range.
			for i := range img {
				img[i] = []uint32{0x80808080, 0x7f7f7f7f, 0x807f8080}[rng.Intn(3)]
			}
		}
		n := w * rows
		g, r, b := make([]int8, n), make([]int8, n), make([]int8, n)
		var sums [5]int32
		if !ctTile(img, 3, stride, w, rows, g, r, b, &sums) {
			// Only the AVX2 build has the kernel; the others gather
			// in searchColorTransform.
			if haveCTTile {
				t.Fatalf("%dx%d: not taken", w, rows)
			}
			return
		}
		var want [5]int64
		k := 0
		for y := 0; y < rows; y++ {
			for x := 0; x < w; x++ {
				p := img[3+y*stride+x]
				gi, ri, bi := int8(p>>8), int8(p>>16), int8(p)
				if g[k] != gi || r[k] != ri || b[k] != bi {
					t.Fatalf("%dx%d: pixel %d,%d: %d %d %d, want %d %d %d", w, rows, x, y, g[k], r[k], b[k], gi, ri, bi)
				}
				G, R, B := int64(gi), int64(ri), int64(bi)
				want[0] += G * G
				want[1] += G * R
				want[2] += R * R
				want[3] += G * B
				want[4] += R * B
				k++
			}
		}
		for i := range want {
			if int64(sums[i]) != want[i] {
				t.Fatalf("%dx%d: sum %d = %d, want %d", w, rows, i, sums[i], want[i])
			}
		}
	}
	// Tiles the kernel leaves to the caller.
	var sums [5]int32
	for _, sz := range [][2]int{{15, 4}, {8, 8}, {17, 2}} {
		buf := make([]int8, sz[0]*sz[1])
		if ctTile(make([]uint32, 64), 0, 32, sz[0], sz[1], buf, buf, buf, &sums) {
			t.Fatalf("%dx%d taken", sz[0], sz[1])
		}
	}
}
