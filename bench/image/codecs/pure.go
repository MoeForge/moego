package codecs

import (
	"bytes"
	"image"
	"image/png"
	"runtime"
	"strconv"

	"github.com/HugoSmits86/nativewebp"
	karpeles "github.com/KarpelesLab/gowebp"
	seriousbug "github.com/SeriousBug/webp-go-pure"
	deepteams "github.com/deepteams/webp"
	"github.com/eringen/gowebper"
	"github.com/gameparrot/fastpng"
	gen2brain "github.com/gen2brain/webp"
	mayahiro "github.com/mayahiro/go-webp"
	calamuspng "github.com/timzifer/calamus/png"
	calamuswebp "github.com/timzifer/calamus/webp"
	woozypng "github.com/woozymasta/png"
	xwebp "golang.org/x/image/webp"
)

// Versions of the pure-Go competitors; keep in step with go.mod.
const (
	verNativeWebP = "github.com/HugoSmits86/nativewebp v1.3.0"
	verKarpeles   = "github.com/KarpelesLab/gowebp v0.1.1"
	verSeriousBug = "github.com/SeriousBug/webp-go-pure v1.2.0"
	verDeepteams  = "github.com/deepteams/webp v1.2.9"
	verGowebper   = "github.com/eringen/gowebper v0.1.2"
	verFastPNG    = "github.com/gameparrot/fastpng v0.0.0-20250305185850-d72e123a2123 (klauspost/compress v1.20.4)"
	verGen2brain  = "github.com/gen2brain/webp v0.6.4 (libwebp 1.6.0 as WASM translated to Go, -tags nodynamic)"
	verMayahiro   = "github.com/mayahiro/go-webp v0.4.0"
	verCalamus    = "github.com/timzifer/calamus v0.3.0"
	verWoozyPNG   = "github.com/woozymasta/png v1.2.0 (klauspost/compress v1.20.4, amd64 SIMD filters)"
	verXImage     = "golang.org/x/image v0.48.0"
)

// libwebp's WebPConfigLosslessPreset table (src/enc/config_enc.c):
// level -> method, quality. cwebp -lossless defaults to level 6.
var losslessPresets = [10][2]int{{0, 0}, {1, 20}, {2, 25}, {3, 30}, {3, 50}, {4, 50}, {4, 75}, {4, 90}, {5, 90}, {6, 100}}

func init() {
	stdlib := "image/png (" + runtime.Version() + ")"
	for _, l := range []struct {
		s string
		l png.CompressionLevel
	}{{"none", png.NoCompression}, {"speed", png.BestSpeed}, {"default", png.DefaultCompression}, {"best", png.BestCompression}} {
		enc := &png.Encoder{CompressionLevel: l.l}
		addEncoder(&Encoder{Name: "png-std", Setting: l.s, Format: "png", Pure: true, Lib: stdlib,
			Encode: func(m *image.NRGBA) ([]byte, error) {
				return toBytes(func(b *bytes.Buffer) error { return enc.Encode(b, m) })
			}})
	}
	for _, l := range []struct {
		s string
		l fastpng.CompressionLevel
	}{{"speed", fastpng.BestSpeed}, {"default", fastpng.DefaultCompression}, {"best", fastpng.BestCompression}} {
		enc := &fastpng.Encoder{CompressionLevel: l.l}
		addEncoder(&Encoder{Name: "fastpng", Setting: l.s, Format: "png", Pure: true, Lib: verFastPNG,
			Encode: func(m *image.NRGBA) ([]byte, error) {
				return toBytes(func(b *bytes.Buffer) error { return enc.Encode(b, m) })
			}})
	}
	for _, l := range []struct {
		s string
		l woozypng.CompressionLevel
	}{{"speed", woozypng.BestSpeed}, {"default", woozypng.DefaultCompression}, {"best", woozypng.BestCompression}} {
		enc := &woozypng.Encoder{CompressionLevel: l.l}
		addEncoder(&Encoder{Name: "woozypng", Setting: l.s, Format: "png", Pure: true, Lib: verWoozyPNG,
			Encode: func(m *image.NRGBA) ([]byte, error) {
				return toBytes(func(b *bytes.Buffer) error { return enc.Encode(b, m) })
			}})
	}
	for _, l := range []struct {
		s string
		l calamuspng.CompressionLevel
	}{{"fast", calamuspng.FastCompression}, {"speed", calamuspng.BestSpeed}, {"default", calamuspng.DefaultCompression}, {"best", calamuspng.BestCompression}} {
		enc := &calamuspng.Encoder{CompressionLevel: l.l, Workers: 1}
		addEncoder(&Encoder{Name: "calamus-png", Setting: l.s, Format: "png", Pure: true, Lib: verCalamus, Note: "Workers=1",
			Encode: func(m *image.NRGBA) ([]byte, error) {
				return toBytes(func(b *bytes.Buffer) error { return enc.Encode(b, m) })
			}})
	}

	for _, l := range []struct {
		s string
		l nativewebp.CompressionLevel
	}{{"speed", nativewebp.BestSpeed}, {"default", nativewebp.DefaultCompression}, {"best", nativewebp.BestCompression}} {
		o := &nativewebp.Options{CompressionLevel: l.l}
		addEncoder(&Encoder{Name: "nativewebp", Setting: l.s, Format: "webp", Pure: true, Lib: verNativeWebP,
			Encode: func(m *image.NRGBA) ([]byte, error) {
				return toBytes(func(b *bytes.Buffer) error { return nativewebp.Encode(b, m, o) })
			}})
	}
	for _, z := range []int{0, 6, 9} {
		o := deepteams.DefaultOptions()
		o.Lossless, o.Exact = true, true
		o.Method, o.Quality = losslessPresets[z][0], float32(losslessPresets[z][1])
		addEncoder(&Encoder{Name: "deepteams", Setting: zname(z), Format: "webp", Pure: true, Lib: verDeepteams, Note: "Exact=true",
			Encode: func(m *image.NRGBA) ([]byte, error) {
				return toBytes(func(b *bytes.Buffer) error { return deepteams.Encode(b, m, o) })
			}})
	}
	for _, l := range []int{gowebper.LevelFastest, gowebper.LevelDefault, gowebper.LevelBest} {
		o := &gowebper.Options{Level: l}
		set := "level" + strconv.Itoa(l)
		if l == gowebper.LevelDefault {
			set += "-default"
		}
		addEncoder(&Encoder{Name: "gowebper", Setting: set, Format: "webp", Pure: true, Lib: verGowebper,
			Encode: func(m *image.NRGBA) ([]byte, error) {
				return toBytes(func(b *bytes.Buffer) error { return gowebper.Encode(b, m, o) })
			}})
	}
	addEncoder(&Encoder{Name: "karpeles", Setting: "default", Format: "webp", Pure: true, Lib: verKarpeles, Note: "no lossless effort setting",
		Encode: func(m *image.NRGBA) ([]byte, error) {
			return toBytes(func(b *bytes.Buffer) error { return karpeles.Encode(b, m, nil) })
		}})
	for _, l := range []struct {
		s string
		m mayahiro.Mode
	}{{"fast", mayahiro.ModeFast}, {"default", mayahiro.ModeDefault}, {"best", mayahiro.ModeBestCompression}} {
		o := &mayahiro.Options{Compression: mayahiro.CompressionLossless, Mode: l.m}
		addEncoder(&Encoder{Name: "mayahiro", Setting: l.s, Format: "webp", Pure: true, Lib: verMayahiro,
			Encode: func(m *image.NRGBA) ([]byte, error) {
				return toBytes(func(b *bytes.Buffer) error { return mayahiro.Encode(b, m, o) })
			}})
	}
	for _, e := range []uint8{0, 3, 6} {
		o := &seriousbug.LosslessOptions{Effort: e}
		set := "effort" + strconv.Itoa(int(e))
		if e == 6 {
			set += "-default"
		}
		addEncoder(&Encoder{Name: "seriousbug", Setting: set, Format: "webp", Pure: true, Lib: verSeriousBug,
			Encode: func(m *image.NRGBA) ([]byte, error) {
				return seriousbug.EncodeLossless(&seriousbug.Image{Width: m.Rect.Dx(), Height: m.Rect.Dy(), RGBA: m.Pix}, o)
			}})
	}
	{
		enc := &calamuswebp.Encoder{Workers: 1}
		addEncoder(&Encoder{Name: "calamus-webp", Setting: "default", Format: "webp", Pure: true, Lib: verCalamus, Note: "Workers=1",
			Encode: func(m *image.NRGBA) ([]byte, error) {
				return toBytes(func(b *bytes.Buffer) error { return enc.Encode(b, m) })
			}})
	}
	if gen2brain.Dynamic() != nil { // never time a dlopen'ed libwebp under this name
		for _, z := range []int{0, 6, 9} {
			q := losslessPresets[z][1]
			note := "Exact=true"
			if q == 0 {
				q = 1 // the wrapper maps quality 0 to its default 75
				note += ", z0 as method 0 quality 1"
			}
			o := gen2brain.Options{Lossless: true, Exact: true, Method: losslessPresets[z][0], Quality: q}
			addEncoder(&Encoder{Name: "gen2brain-wasm", Setting: zname(z), Format: "webp", Pure: true, Lib: verGen2brain, Note: note,
				Encode: func(m *image.NRGBA) ([]byte, error) {
					return toBytes(func(b *bytes.Buffer) error { return gen2brain.Encode(b, m, o) })
				}})
		}
	}

	addDecoder(&Decoder{Name: "go", Format: "png", Lib: stdlib, Decode: func(data []byte) (image.Image, error) {
		return png.Decode(bytes.NewReader(data))
	}})
	addDecoder(&Decoder{Name: "go", Format: "webp", Lib: verXImage + " webp", Decode: func(data []byte) (image.Image, error) {
		return xwebp.Decode(bytes.NewReader(data))
	}})
}

func zname(z int) string {
	s := "z" + strconv.Itoa(z)
	if z == 6 {
		s += "-default"
	}
	return s
}
