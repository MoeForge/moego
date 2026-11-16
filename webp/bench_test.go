package webp

import (
	"bytes"
	"image"
	"math/rand"
	"os"
	"testing"
)

// benchImages returns the tuning corpus (see tune_test.go) when its
// variables are set, else a synthetic photo-like image.
func benchImages(b *testing.B) []*image.NRGBA {
	if os.Getenv("MOEGO_WEBP_CORPUS") != "" {
		ms, err := corpusOnce()
		if err != nil {
			b.Fatal(err)
		}
		out := make([]*image.NRGBA, len(ms))
		for i, c := range ms {
			out[i] = c.m
		}
		return out
	}
	rng := rand.New(rand.NewSource(1))
	m := image.NewNRGBA(image.Rect(0, 0, 512, 384))
	fillNRGBA(rng, m, 0)
	for i := 3; i < len(m.Pix); i += 4 {
		m.Pix[i] = 0xff
	}
	return []*image.NRGBA{m}
}

func benchEncode(b *testing.B, l CompressionLevel) {
	ms := benchImages(b)
	px := 0
	for _, m := range ms {
		px += m.Rect.Dx() * m.Rect.Dy()
	}
	enc := Encoder{CompressionLevel: l, BufferPool: &pool{}}
	var buf bytes.Buffer
	b.ReportAllocs()
	b.SetBytes(int64(4 * px))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, m := range ms {
			buf.Reset()
			if err := enc.Encode(&buf, m); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ReportMetric(float64(px)*float64(b.N)/b.Elapsed().Seconds()/1e6, "MP/s")
}

func BenchmarkEncodeSpeed(b *testing.B)   { benchEncode(b, BestSpeed) }
func BenchmarkEncodeDefault(b *testing.B) { benchEncode(b, DefaultCompression) }
func BenchmarkEncodeBest(b *testing.B)    { benchEncode(b, BestCompression) }
