package vp8l

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"testing"

	"github.com/MoeForge/moego/internal/vp8l/vp8ltest"
)

// longTests turns on the slow round trips (repeats near the farthest
// distance, every level on every case).
var longTests = os.Getenv("MOEGO_WEBP_LONG") == "1"

func checkRoundTrip(t *testing.T, e *Encoder, level int, c vp8ltest.Case) []byte {
	t.Helper()
	out := vp8ltest.Encode(e, level, c.ARGB, c.W, c.H, c.TS)
	w, h, got, err := vp8ltest.Decode(out)
	if err != nil {
		t.Fatalf("%s level %d: %v", c.Name, level, err)
	}
	if w != c.W || h != c.H {
		t.Fatalf("%s level %d: decoded %dx%d", c.Name, level, w, h)
	}
	for i, p := range c.ARGB {
		if got[i] != p {
			t.Fatalf("%s level %d: pixel %d (%d,%d) is %08x, want %08x", c.Name, level, i, i%w, i/w, got[i], p)
		}
	}
	if _, _, _, err := vp8ltest.DecodeWebP(vp8ltest.WebP(out)); err != nil {
		t.Fatalf("%s level %d: container: %v", c.Name, level, err)
	}
	return out
}

func TestRoundTrip(t *testing.T) {
	var e Encoder
	levels := []int{0, 1, 3, 6, 9}
	for i, c := range vp8ltest.Cases(1, longTests) {
		if longTests {
			for l := 0; l <= 9; l++ {
				checkRoundTrip(t, &e, l, c)
			}
			continue
		}
		// A few levels on every case, all of them on a part.
		checkRoundTrip(t, &e, levels[i%len(levels)], c)
		if i%7 == 0 {
			checkRoundTrip(t, &e, 9-i%10, c)
		}
	}
}

// TestDeterministic encodes the same images with a fresh and a reused
// encoder: the bytes must not differ.
func TestDeterministic(t *testing.T) {
	var reused Encoder
	for i, c := range vp8ltest.Cases(2, false) {
		if i%3 != 0 {
			continue
		}
		for _, l := range []int{1, 6, 9} {
			a := checkRoundTrip(t, &reused, l, c)
			var fresh Encoder
			b := vp8ltest.Encode(&fresh, l, c.ARGB, c.W, c.H, c.TS)
			if !bytes.Equal(a, b) {
				t.Fatalf("%s level %d: reused encoder wrote different bytes", c.Name, l)
			}
		}
	}
}

func TestBitsAndWriteBits(t *testing.T) {
	var e Encoder
	e.Reset(6)
	e.Header(3, 2, false)
	if e.Bits() != 8+28+4 {
		t.Fatalf("Bits after Header = %d", e.Bits())
	}
	e.WriteBits(0xffffffff, 32)
	e.WriteBits(0xff, 3) // only the low 3 bits count
	if e.Bits() != 40+35 {
		t.Fatalf("Bits = %d", e.Bits())
	}
	out := e.Finish()
	if len(out) != (75+7)/8 {
		t.Fatalf("Finish length %d", len(out))
	}
	for _, f := range []func(){
		func() { e.Reset(10) },
		func() { e.Reset(-1) },
		func() { e.Header(0, 1, false) },
		func() { e.Header(1<<14+1, 1, false) },
		func() { e.WriteBits(0, 33) },
		func() { e.MainImage(make([]uint32, 5), 2, 2) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("no panic")
				}
			}()
			f()
		}()
	}
}

func TestZeroAllocs(t *testing.T) {
	if raceEnabled || checkptrEnabled() {
		t.Skip("allocation counts differ under the race detector and checkptr")
	}
	rng := rand.New(rand.NewSource(3))
	img := vp8ltest.Smooth(rng, 200, 150)
	sub := vp8ltest.FewColors(rng, 50, 38, 14)
	for _, l := range []int{0, 1, 6, 9} {
		var e Encoder
		enc := func() {
			e.Reset(l)
			e.Header(200, 150, true)
			e.WriteBits(1, 1)
			e.WriteBits(0, 2)
			e.WriteBits(0, 3)
			e.SubImage(sub, 50, 38)
			e.WriteBits(0, 1)
			e.MainImage(img, 200, 150)
			e.Finish()
		}
		enc()
		if n := testing.AllocsPerRun(3, enc); n != 0 {
			t.Errorf("level %d: %v allocations per warm encode", l, n)
		}
	}
}

func TestPrefixOf(t *testing.T) {
	for v := uint32(1); v <= 1<<20; v++ {
		c, n, x := prefixOf(v)
		if n != prefixExtra(c) {
			t.Fatalf("%d: %d extra bits, prefixExtra(%d) = %d", v, n, c, prefixExtra(c))
		}
		// The decoding rule of RFC 9649 section 3.6.2.2.
		var got uint32
		if c < 4 {
			got = c + 1
		} else {
			eb := (c - 2) >> 1
			got = (2+c&1)<<eb + x + 1
		}
		if got != v || x >= 1<<n {
			t.Fatalf("%d: code %d extra %d/%d decodes to %d", v, c, x, n, got)
		}
		if v <= maxLength && uint32(lengthPrefix[v-1]) != c {
			t.Fatalf("lengthPrefix[%d] = %d, want %d", v-1, lengthPrefix[v-1], c)
		}
	}
	if c, _, _ := prefixOf(maxLength); c != numLengthCodes-1 {
		t.Fatalf("length 4096 has code %d", c)
	}
	if c, _, _ := prefixOf(1 << 20); c != numDistCodes-1 {
		t.Fatalf("distance code 1<<20 has code %d", c)
	}
}

func ExampleEncoder() {
	var e Encoder
	e.Reset(DefaultCompression)
	e.Header(2, 1, false)
	e.WriteBits(0, 1) // no transforms
	e.MainImage([]uint32{0xff102030, 0xff102030}, 2, 1)
	w, h, px, err := vp8ltest.Decode(e.Finish())
	fmt.Println(w, h, err)
	fmt.Printf("%08x\n", px)
	// Output:
	// 2 1 <nil>
	// [ff102030 ff102030]
}

// TestMetaCodes checks images whose parts have different statistics: the
// encoder should give them prefix code groups of their own.
func TestMetaCodes(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	var e Encoder
	for _, sz := range [][2]int{{64, 64}, {200, 90}, {33, 257}, {1000, 20}} {
		w, h := sz[0], sz[1]
		px := make([]uint32, w*h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				// Four regions: noise in different channel ranges.
				switch (x*4/w + 2*(y*2/h)) % 4 {
				case 0:
					px[y*w+x] = 0xff000000 | uint32(rng.Intn(8))<<8
				case 1:
					px[y*w+x] = 0xff000000 | uint32(rng.Intn(256))<<16 | 0x80<<8
				case 2:
					px[y*w+x] = uint32(rng.Intn(256))<<24 | uint32(rng.Intn(4))
				default:
					px[y*w+x] = rng.Uint32()
				}
			}
		}
		c := vp8ltest.Case{Name: "meta", W: w, H: h, ARGB: px}
		for _, l := range []int{2, 4, 6, 9} {
			checkRoundTrip(t, &e, l, c)
			// Lower levels try only large blocks, which can span every
			// region.
			if l >= 6 && e.main.metaBits == 0 {
				t.Errorf("%dx%d level %d: no meta prefix codes", w, h, l)
			}
		}
	}
}

// TestSeedBins codes an image whose blocks have entropies so close that
// their float32 rounding fell outside the extremes the bins were cut from
// (a negative bin index); every level must code it.
func TestSeedBins(t *testing.T) {
	raw, err := os.ReadFile("testdata/seed-42x28.argb")
	if err != nil {
		t.Fatal(err)
	}
	px := make([]uint32, len(raw)/4)
	for i := range px {
		px[i] = uint32(raw[4*i]) | uint32(raw[4*i+1])<<8 | uint32(raw[4*i+2])<<16 | uint32(raw[4*i+3])<<24
	}
	c := vp8ltest.Case{Name: "seed-42x28", W: 42, H: 28, ARGB: px}
	var e Encoder
	for l := 0; l <= 9; l++ {
		checkRoundTrip(t, &e, l, c)
	}
}
