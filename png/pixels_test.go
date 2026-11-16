package png

import (
	"bytes"
	"math/rand"
	"testing"
)

// rgbaRow returns n pixels that are opaque and gray unless broken at one
// position.
func rgbaRow(rng *rand.Rand, n int, breakOpaque, breakGray int) []byte {
	p := make([]byte, 4*n)
	for i := 0; i < n; i++ {
		v := byte(rng.Intn(256))
		p[4*i], p[4*i+1], p[4*i+2], p[4*i+3] = v, v, v, 0xff
	}
	if breakOpaque >= 0 {
		p[4*breakOpaque+3] = byte(rng.Intn(255))
	}
	if breakGray >= 0 {
		p[4*breakGray+1+rng.Intn(2)] ^= byte(1 + rng.Intn(255))
	}
	return p
}

func TestPixelKernels(t *testing.T) {
	requireKernels(t)
	rng := rand.New(rand.NewSource(8))
	for n := 0; n <= 130; n++ {
		for trial := 0; trial < 20; trial++ {
			bo, bg := -1, -1
			if n > 0 && trial&1 != 0 {
				bo = rng.Intn(n)
			}
			if n > 0 && trial&2 != 0 {
				bg = rng.Intn(n)
			}
			p := rgbaRow(rng, n, bo, bg)
			checkPixelKernels(t, p, make([]byte, 4*n+32))
		}
	}
}

func checkPixelKernels(t *testing.T, p, buf []byte) {
	t.Helper()
	n := len(p) / 4
	o, g := rgbaFlags(p)
	wo, wg := rgbaFlagsGeneric(p)
	if o != wo || g != wg {
		t.Fatalf("%d pixels: rgbaFlags = %v %v, want %v %v", n, o, g, wo, wg)
	}
	for _, c := range []struct {
		name     string
		f, ref   func(d, p []byte)
		perPixel int
	}{{"RGB", toRGB, toRGBGeneric, 3}, {"GA", toGA, toGAGeneric, 2}, {"G", toG, toGGeneric, 1}} {
		want := make([]byte, c.perPixel*n)
		c.ref(want, p)
		got := buf[:c.perPixel*n]
		c.f(got, p)
		if !bytes.Equal(got, want) {
			t.Fatalf("%d pixels: to%s differs", n, c.name)
		}
	}
}
