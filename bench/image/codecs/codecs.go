// Package codecs lists the lossless image encoders and decoders that the
// image benchmark compares. Every encoder takes the same *image.NRGBA
// (origin 0,0, stride 4*width) and returns a complete file.
package codecs

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"path"
	"sort"
	"strings"
)

// Encoder is one encoder at one setting.
type Encoder struct {
	Name    string // library, e.g. "png-std"
	Setting string // effort setting, e.g. "default"
	Format  string // "png" or "webp"
	Pure    bool   // no cgo (WASM-in-Go counts as pure but is marked in Note)
	Lib     string // module path and version or C library and version
	Note    string
	Encode  func(img *image.NRGBA) ([]byte, error)
}

// ID is "name/setting".
func (e *Encoder) ID() string { return e.Name + "/" + e.Setting }

// Decoder is a reference decoder for one format. Decode returns the
// decoder's native result, which is what gets timed; ToNRGBA converts it
// for the pixel comparison.
type Decoder struct {
	Name   string // "go" or "c"
	Format string
	Lib    string
	Decode func(data []byte) (image.Image, error)
}

var (
	encoders []*Encoder
	decoders []*Decoder
)

func addEncoder(e *Encoder) { encoders = append(encoders, e) }
func addDecoder(d *Decoder) { decoders = append(decoders, d) }

// Encoders returns every registered encoder in registration order.
func Encoders() []*Encoder { return encoders }

// Decoders returns the decoders for a format, "go" first.
func Decoders(format string) []*Decoder {
	var out []*Decoder
	for _, d := range decoders {
		if d.Format == format {
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name == "go" && out[j].Name != "go" })
	return out
}

// Select returns the encoders whose ID matches the comma-separated
// path.Match patterns (e.g. "png-std/*,libwebp/z6-default"), in
// registration order. "all" matches everything; a pattern starting with
// "-" removes the encoders it matches. A bare name means "name/*".
func Select(spec string) ([]*Encoder, error) {
	if spec == "" {
		spec = "all"
	}
	keep := make([]bool, len(encoders))
	for _, p := range strings.Split(spec, ",") {
		p = strings.TrimSpace(p)
		drop := strings.HasPrefix(p, "-")
		p = strings.TrimPrefix(p, "-")
		if p == "all" {
			p = "*/*"
		} else if !strings.Contains(p, "/") {
			p += "/*"
		}
		hit := false
		for i, e := range encoders {
			ok, err := path.Match(p, e.ID())
			if err != nil {
				return nil, err
			}
			if ok {
				keep[i] = !drop
				hit = true
			}
		}
		if !hit {
			return nil, fmt.Errorf("pattern %q matches no encoder", p)
		}
	}
	var out []*Encoder
	for i, e := range encoders {
		if keep[i] {
			out = append(out, e)
		}
	}
	return out, nil
}

// toBytes runs an Encode(io.Writer, ...) style call into a fresh buffer.
func toBytes(f func(*bytes.Buffer) error) ([]byte, error) {
	var buf bytes.Buffer
	if err := f(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ToNRGBA converts any decoded image to a tight *image.NRGBA at origin 0,0.
// Conversions from 8-bit sources are exact; 16-bit sources are reduced to
// their high byte, which defines the benchmark's source pixels.
func ToNRGBA(m image.Image) *image.NRGBA {
	b := m.Bounds()
	if n, ok := m.(*image.NRGBA); ok && b.Min == (image.Point{}) && n.Stride == 4*b.Dx() {
		return n
	}
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	switch s := m.(type) {
	case *image.NRGBA:
		for y := 0; y < b.Dy(); y++ {
			copy(out.Pix[y*out.Stride:][:4*b.Dx()], s.Pix[s.PixOffset(b.Min.X, b.Min.Y+y):])
		}
		return out
	case *image.RGBA:
		// Only exact when every pixel is opaque or fully transparent;
		// callers decode PNG with alpha to *image.NRGBA, so RGBA here is opaque.
		for y := 0; y < b.Dy(); y++ {
			src := s.Pix[s.PixOffset(b.Min.X, b.Min.Y+y):][:4*b.Dx()]
			dst := out.Pix[y*out.Stride:][:4*b.Dx()]
			for i := 0; i < len(src); i += 4 {
				a := src[i+3]
				switch a {
				case 255:
					copy(dst[i:i+4], src[i:i+4])
				case 0:
					dst[i], dst[i+1], dst[i+2], dst[i+3] = 0, 0, 0, 0
				default:
					dst[i] = uint8(uint32(src[i]) * 255 / uint32(a))
					dst[i+1] = uint8(uint32(src[i+1]) * 255 / uint32(a))
					dst[i+2] = uint8(uint32(src[i+2]) * 255 / uint32(a))
					dst[i+3] = a
				}
			}
		}
		return out
	}
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			var c color.NRGBA
			switch p := m.At(b.Min.X+x, b.Min.Y+y).(type) {
			case color.NRGBA64:
				c = color.NRGBA{uint8(p.R >> 8), uint8(p.G >> 8), uint8(p.B >> 8), uint8(p.A >> 8)}
			case color.RGBA64:
				// 16-bit PNGs without alpha decode as opaque RGBA64.
				c = color.NRGBA{uint8(p.R >> 8), uint8(p.G >> 8), uint8(p.B >> 8), uint8(p.A >> 8)}
			case color.Gray16:
				c = color.NRGBA{uint8(p.Y >> 8), uint8(p.Y >> 8), uint8(p.Y >> 8), 255}
			default:
				// color.NRGBA (palette with tRNS) converts to itself, keeping
				// RGB under alpha 0; Gray and opaque RGBA convert exactly.
				c = color.NRGBAModel.Convert(p).(color.NRGBA)
			}
			o := out.PixOffset(x, y)
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = c.R, c.G, c.B, c.A
		}
	}
	return out
}

// Opaque reports whether every pixel has alpha 255.
func Opaque(m *image.NRGBA) bool {
	for i := 3; i < len(m.Pix); i += 4 {
		if m.Pix[i] != 255 {
			return false
		}
	}
	return true
}

// Compare checks got against want. It returns "" when they are identical,
// "alpha0" when only RGB under alpha 0 differs, or a description of the
// first visible difference.
func Compare(want, got *image.NRGBA) string {
	if want.Rect.Dx() != got.Rect.Dx() || want.Rect.Dy() != got.Rect.Dy() {
		return fmt.Sprintf("size %dx%d, want %dx%d", got.Rect.Dx(), got.Rect.Dy(), want.Rect.Dx(), want.Rect.Dy())
	}
	w, h := want.Rect.Dx(), want.Rect.Dy()
	alpha0 := false
	for y := 0; y < h; y++ {
		a := want.Pix[y*want.Stride:][:4*w]
		b := got.Pix[got.PixOffset(got.Rect.Min.X, got.Rect.Min.Y+y):][:4*w]
		if bytes.Equal(a, b) {
			continue
		}
		for x := 0; x < w; x++ {
			p, q := a[4*x:4*x+4], b[4*x:4*x+4]
			if bytes.Equal(p, q) {
				continue
			}
			if p[3] == 0 && q[3] == 0 {
				alpha0 = true
				continue
			}
			return fmt.Sprintf("pixel (%d,%d) = %v, want %v", x, y, q, p)
		}
	}
	if alpha0 {
		return "alpha0"
	}
	return ""
}
