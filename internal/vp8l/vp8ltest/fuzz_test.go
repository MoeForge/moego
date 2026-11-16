package vp8ltest

import "testing"

// FuzzDecode feeds arbitrary bytes to the decoder: it must return, with an
// error or pixels, and never panic or run away.
func FuzzDecode(f *testing.F) {
	f.Add(zeroPixelStream(none))
	f.Add(WebP(zeroPixelStream(none)))
	for _, c := range Cases(9, false)[:40] {
		f.Add(Encode(&literalWriter{}, 0, c.ARGB, c.W, c.H, c.TS))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// A one-leaf code takes no bits per pixel, so a short stream can
		// name a huge image; the header caps the work.
		if big(data) || len(data) >= 20 && big(data[20:]) {
			return
		}
		if w, h, px, err := Decode(data); err == nil && len(px) != w*h {
			t.Fatalf("%dx%d image with %d pixels", w, h, len(px))
		}
		DecodeWebP(data)
	})
}

// big reports whether a payload's header names more than 1<<20 pixels.
func big(p []byte) bool {
	if len(p) < 5 {
		return false
	}
	w := int(p[1]) | int(p[2]&0x3f)<<8
	h := int(p[2]>>6) | int(p[3])<<2 | int(p[4]&0xf)<<10
	return (w+1)*(h+1) > 1<<20
}
