package png

import (
	"bufio"
	"bytes"
	"image"
	"image/draw"
	stdpng "image/png"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// benchImages returns the corpus images listed in $MOEGO_PNG_LIST (lines of
// "sha256  relpath") under $MOEGO_PNG_CORPUS as tight *image.NRGBA, or a
// synthetic image when the variables are unset.
func benchImages(b *testing.B) []*image.NRGBA {
	dir, list := os.Getenv("MOEGO_PNG_CORPUS"), os.Getenv("MOEGO_PNG_LIST")
	if dir == "" || list == "" {
		g := newGen(rand.New(rand.NewSource(1)), modeSmooth, 0, false, 1024)
		return []*image.NRGBA{makeImage("NRGBA", image.Rect(0, 0, 1024, 768), g, 0).(*image.NRGBA)}
	}
	f, err := os.Open(list)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	only := os.Getenv("MOEGO_PNG_ONLY")
	var out []*image.NRGBA
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if only != "" && !strings.Contains(fields[1], only) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, fields[1]))
		if err != nil {
			b.Fatal(err)
		}
		m, err := stdpng.Decode(bytes.NewReader(data))
		if err != nil {
			b.Fatal(err)
		}
		n := image.NewNRGBA(image.Rect(0, 0, m.Bounds().Dx(), m.Bounds().Dy()))
		draw.Draw(n, n.Rect, m, m.Bounds().Min, draw.Src)
		out = append(out, n)
	}
	return out
}

func benchmarkEncode(b *testing.B, l CompressionLevel, keep bool) {
	ms := benchImages(b)
	px := 0
	for _, m := range ms {
		px += m.Rect.Dx() * m.Rect.Dy()
	}
	enc := &Encoder{CompressionLevel: l, KeepColorType: keep, BufferPool: &pool{}}
	b.SetBytes(int64(4 * px))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, m := range ms {
			if err := enc.Encode(io.Discard, m); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkEncodeSpeed(b *testing.B)       { benchmarkEncode(b, BestSpeed, false) }
func BenchmarkEncodeDefault(b *testing.B)     { benchmarkEncode(b, DefaultCompression, false) }
func BenchmarkEncodeBest(b *testing.B)        { benchmarkEncode(b, BestCompression, false) }
func BenchmarkEncodeKeepSpeed(b *testing.B)   { benchmarkEncode(b, BestSpeed, true) }
func BenchmarkEncodeKeepDefault(b *testing.B) { benchmarkEncode(b, DefaultCompression, true) }
func BenchmarkEncodeKeepBest(b *testing.B)    { benchmarkEncode(b, BestCompression, true) }

// BenchmarkEncodeNoPool encodes without a BufferPool, as Encode does.
func BenchmarkEncodeNoPool(b *testing.B) {
	ms := benchImages(b)
	enc := &Encoder{KeepColorType: true}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, m := range ms {
			if err := enc.Encode(io.Discard, m); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkFilterOnly runs everything but the compression: rows are
// converted and filtered as at DefaultCompression and stored.
func BenchmarkFilterOnly(b *testing.B) {
	ms := benchImages(b)
	px := 0
	for _, m := range ms {
		px += m.Rect.Dx() * m.Rect.Dy()
	}
	e := &encoder{}
	b.SetBytes(int64(4 * px))
	for i := 0; i < b.N; i++ {
		for _, m := range ms {
			e.tune = tuning{strat: stMinSum, zlevel: 0}
			if err := e.encode(io.Discard, m, &Encoder{KeepColorType: true}); err != nil {
				b.Fatal(err)
			}
		}
	}
}
