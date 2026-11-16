package codecs

import (
	"image"
	"math/rand/v2"
	"strings"
	"testing"
)

// synthetic returns small images covering the cases the corpus has:
// opaque photo-like, alpha with hidden RGB, few colors, odd sizes.
func synthetic() map[string]*image.NRGBA {
	r := rand.New(rand.NewPCG(1, 2))
	gen := func(w, h int, f func(x, y int) [4]uint8) *image.NRGBA {
		m := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				c := f(x, y)
				copy(m.Pix[m.PixOffset(x, y):], c[:])
			}
		}
		return m
	}
	return map[string]*image.NRGBA{
		"1x1": gen(1, 1, func(x, y int) [4]uint8 { return [4]uint8{10, 20, 30, 255} }),
		"gradient-opaque": gen(67, 45, func(x, y int) [4]uint8 {
			return [4]uint8{uint8(x * 3), uint8(y * 5), uint8(x + y + r.IntN(8)), 255}
		}),
		"alpha-hidden-rgb": gen(40, 33, func(x, y int) [4]uint8 {
			a := uint8(0)
			if (x+y)%3 != 0 {
				a = uint8(r.IntN(256))
			}
			return [4]uint8{uint8(r.IntN(256)), uint8(x * 6), uint8(y * 7), a}
		}),
		"few-colors": gen(90, 31, func(x, y int) [4]uint8 {
			p := [][4]uint8{{255, 255, 255, 255}, {0, 0, 0, 255}, {200, 30, 30, 255}, {0, 0, 0, 0}}
			return p[(x/5+y/3)%len(p)]
		}),
		"noise-rgba": gen(31, 17, func(x, y int) [4]uint8 {
			return [4]uint8{uint8(r.IntN(256)), uint8(r.IntN(256)), uint8(r.IntN(256)), uint8(1 + r.IntN(255))}
		}),
	}
}

// TestRoundTrip reports which encoders reproduce every pixel through each
// reference decoder. It fails only on errors of the reference path itself.
func TestRoundTrip(t *testing.T) {
	imgs := synthetic()
	for _, e := range Encoders() {
		for name, m := range imgs {
			out, err := e.Encode(m)
			if err != nil {
				t.Logf("%s %s: encode error: %v", e.ID(), name, err)
				continue
			}
			for _, d := range Decoders(e.Format) {
				got, err := d.Decode(out)
				if err != nil {
					t.Logf("%s %s: %s decode error: %v", e.ID(), name, d.Name, err)
					continue
				}
				if diff := Compare(m, ToNRGBA(got)); diff != "" {
					t.Logf("%s %s: %s: %s", e.ID(), name, d.Name, diff)
					if e.Name == "png-std" || e.Name == "libpng" || e.Name == "libwebp" {
						t.Errorf("reference encoder %s mismatched on %s", e.ID(), name)
					}
				}
			}
		}
	}
}

func TestSelect(t *testing.T) {
	es, err := Select("png-std/default,png-std/b*")
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 2 || es[0].ID() != "png-std/default" || es[1].ID() != "png-std/best" {
		t.Fatalf("Select = %v", es)
	}
	es, err = Select("all,-png-std,-calamus*/*")
	if err != nil {
		t.Fatal(err)
	}
	if len(es) == 0 {
		t.Fatal("Select dropped everything")
	}
	for _, e := range es {
		if e.Name == "png-std" || strings.HasPrefix(e.Name, "calamus") {
			t.Fatalf("Select kept %s", e.ID())
		}
	}
	if _, err := Select("nope/*"); err == nil {
		t.Fatal("Select accepted an unknown pattern")
	}
}
