package codecs

import (
	"bytes"
	"image"
	"testing"

	moegowebp "github.com/MoeForge/moego/webp"
)

// FuzzMoegoWebP encodes an image built from the fuzz input at every
// moego/webp level and checks that x/image/webp, and libwebp in cgo
// builds, decode it to the input's pixels exactly, hidden RGB under
// alpha 0 included.
func FuzzMoegoWebP(f *testing.F) {
	f.Add(uint8(1), uint8(1), uint8(0), []byte{1, 2, 3, 4})
	f.Add(uint8(17), uint8(5), uint8(1), []byte{0, 0, 0, 0, 9, 8, 7, 0, 255, 255, 255, 255})
	f.Add(uint8(40), uint8(33), uint8(2), bytes.Repeat([]byte{3, 200, 17, 255, 0, 0, 0, 0}, 50))
	f.Add(uint8(64), uint8(9), uint8(3), []byte("the quick brown fox jumps over the lazy dog"))
	ds := Decoders("webp")
	if len(ds) == 0 {
		f.Fatal("no webp decoder")
	}
	f.Fuzz(func(t *testing.T, w, h, kind uint8, pix []byte) {
		if w == 0 || h == 0 || len(pix) == 0 {
			return
		}
		m := image.NewNRGBA(image.Rect(0, 0, int(w), int(h)))
		for i := range m.Pix {
			b := pix[i%len(pix)]
			switch kind % 4 {
			case 1:
				// Opaque.
				if i%4 == 3 {
					b = 255
				}
			case 2:
				// Few colors: whole pixels from the input.
				b = pix[(i/4*4%len(pix)+i%4)%len(pix)] & 0xc0
			case 3:
				// Smooth: a ramp perturbed by the input.
				b = uint8(i/4%int(w)+i/4/int(w)) + b&3
			}
			m.Pix[i] = b
		}
		for _, l := range []moegowebp.CompressionLevel{moegowebp.BestSpeed, moegowebp.DefaultCompression, moegowebp.BestCompression} {
			var buf bytes.Buffer
			if err := (&moegowebp.Encoder{CompressionLevel: l}).Encode(&buf, m); err != nil {
				t.Fatalf("level %d: %v", l, err)
			}
			for _, d := range ds {
				got, err := d.Decode(buf.Bytes())
				if err != nil {
					t.Fatalf("level %d: %s decoder: %v", l, d.Name, err)
				}
				if diff := Compare(m, ToNRGBA(got)); diff != "" {
					t.Fatalf("level %d: %s decoder: %s", l, d.Name, diff)
				}
			}
		}
	})
}
