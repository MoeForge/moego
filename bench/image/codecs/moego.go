package codecs

import (
	"bytes"
	"image"
	"runtime"

	moegopng "github.com/MoeForge/moego/png"
	moegowebp "github.com/MoeForge/moego/webp"
)

func init() {
	lib := "moego/png (" + runtime.Version() + ")"
	for _, keep := range []bool{false, true} {
		name, note := "moego-png", "lossless color reduction"
		if keep {
			name, note = "moego-png-keep", "KeepColorType: image/png's color type"
		}
		for _, l := range []struct {
			s string
			l moegopng.CompressionLevel
		}{{"speed", moegopng.BestSpeed}, {"default", moegopng.DefaultCompression}, {"best", moegopng.BestCompression}} {
			enc := &moegopng.Encoder{CompressionLevel: l.l, KeepColorType: keep}
			addEncoder(&Encoder{Name: name, Setting: l.s, Format: "png", Pure: true, Lib: lib, Note: note,
				Encode: func(m *image.NRGBA) ([]byte, error) {
					return toBytes(func(b *bytes.Buffer) error { return enc.Encode(b, m) })
				}})
		}
	}
	wlib := "moego/webp (" + runtime.Version() + ")"
	for _, l := range []struct {
		s string
		l moegowebp.CompressionLevel
	}{{"speed", moegowebp.BestSpeed}, {"default", moegowebp.DefaultCompression}, {"best", moegowebp.BestCompression}} {
		enc := &moegowebp.Encoder{CompressionLevel: l.l}
		addEncoder(&Encoder{Name: "moego-webp", Setting: l.s, Format: "webp", Pure: true, Lib: wlib, Note: "lossless, exact",
			Encode: func(m *image.NRGBA) ([]byte, error) {
				return toBytes(func(b *bytes.Buffer) error { return enc.Encode(b, m) })
			}})
	}
}
