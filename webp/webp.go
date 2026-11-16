// Package webp writes lossless WebP images (VP8L) with an encoding API like
// image/png's. It has no decoder: golang.org/x/image/webp or libwebp read
// the output.
//
// The output is a simple-format WebP file, a RIFF header and one VP8L
// chunk, with no VP8X, ICC, EXIF or animation chunks. Decoding it gives,
// at every pixel, color.NRGBAModel.Convert(m.At(x, y)): an *image.NRGBA
// comes back byte for byte, including the color under fully transparent
// pixels. WebP stores 8 bits per channel, so 16-bit images are reduced to
// 8 bits as color.NRGBAModel converts them, and only 8-bit images round
// trip exactly. Images wider or taller than 16384 pixels, the format's
// limit, are an error.
//
// Before entropy coding, the encoder applies the format's transforms as
// the pixels allow: a color table for at most 256 colors (with 2, 4 or 8
// pixels packed together for at most 16), and otherwise subtract green,
// spatial prediction and a color decorrelation transform, each chosen per
// image and per block by estimated compressed size.
package webp

import (
	"image"
	"io"
)

// CompressionLevel is the compression effort. The values are image/png's;
// any other value, including image/png's NoCompression (-1), means
// DefaultCompression, since VP8L has no stored mode.
type CompressionLevel int

const (
	DefaultCompression CompressionLevel = 0
	BestSpeed          CompressionLevel = -2
	BestCompression    CompressionLevel = -3
)

// Encoder holds the settings for encoding; the zero value encodes at
// DefaultCompression.
type Encoder struct {
	CompressionLevel CompressionLevel

	// BufferPool optionally keeps the encoder's buffers between Encode
	// calls. With a pool, an encode that reuses a buffer allocates nothing
	// for images of the same size and type when m is an *image.NRGBA,
	// *image.RGBA, *image.NRGBA64, *image.RGBA64, *image.Gray,
	// *image.Gray16, *image.Alpha, *image.Paletted, *image.YCbCr or
	// *image.CMYK. Other images are read through their At method, which
	// allocates a color.Color for each pixel.
	BufferPool EncoderBufferPool
}

// EncoderBufferPool lends EncoderBuffers to Encode and takes them back, so
// that a program encoding many images can reuse one set of buffers, for
// example with a sync.Pool behind Get and Put.
type EncoderBufferPool interface {
	Get() *EncoderBuffer
	Put(*EncoderBuffer)
}

// EncoderBuffer is the encoder's reusable state: pixel, transform and
// entropy coding buffers. Its contents are private.
type EncoderBuffer encoder

// Encode writes m to w as a lossless WebP at DefaultCompression.
func Encode(w io.Writer, m image.Image) error {
	var e Encoder
	return e.Encode(w, m)
}
