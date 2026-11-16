// Package png writes PNG images with the encoding API of image/png. Decoding
// forwards to image/png, so the package can replace that import.
//
// The output decodes to the same pixels as image/png's: for every image m,
// image/png.Decode of the output gives, at every pixel, the same
// non-premultiplied 16-bit color as image/png.Decode(image/png.Encode(m)),
// including the color under fully transparent pixels wherever image/png
// keeps it. Unless Encoder.KeepColorType is set, the encoder reduces the
// color type as far as the pixels allow: no alpha channel for opaque
// images, gray for R=G=B, a palette for at most 256 colors (packed to 1, 2
// or 4 bits for at most 2, 4 or 16), and 8 bits for 16-bit samples whose
// two bytes are equal. For 17 to 256 colors, DefaultCompression and
// BestCompression write the palette only when a quick trial shows it
// compresses smaller than the image without one. The decoded image can
// therefore have a different Go type than image/png's output, for example
// *image.Paletted. A *image.Paletted keeps its palette and indices as they
// are.
package png

import (
	"image"
	"image/png"
	"io"
)

// CompressionLevel is the compression effort; the values are image/png's.
type CompressionLevel int

const (
	DefaultCompression CompressionLevel = 0
	NoCompression      CompressionLevel = -1
	BestSpeed          CompressionLevel = -2
	BestCompression    CompressionLevel = -3

	// Positive values are reserved as in image/png and mean
	// DefaultCompression.
)

// Encoder holds the settings for encoding; the zero value encodes at
// DefaultCompression with color reduction.
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

	// KeepColorType writes the color type and bit depth that image/png
	// would write instead of the smallest one that holds the pixels.
	KeepColorType bool
}

// EncoderBufferPool lends EncoderBuffers to Encode and takes them back, so
// that a program encoding many images can reuse one set of buffers, for
// example with a sync.Pool behind Get and Put.
type EncoderBufferPool interface {
	Get() *EncoderBuffer
	Put(*EncoderBuffer)
}

// EncoderBuffer is the encoder's reusable state: row, analysis and
// compression buffers. Its contents are private.
type EncoderBuffer encoder

// FormatError is image/png's error for input that is not a valid PNG.
type FormatError = png.FormatError

// UnsupportedError is image/png's error for valid PNG input that uses a
// feature its decoder does not implement.
type UnsupportedError = png.UnsupportedError

// Decode decodes the PNG image read from r with image/png's decoder.
func Decode(r io.Reader) (image.Image, error) { return png.Decode(r) }

// DecodeConfig reads only the PNG header from r and returns the image's
// color model and size, using image/png.
func DecodeConfig(r io.Reader) (image.Config, error) { return png.DecodeConfig(r) }

// Encode writes m to w as a PNG at DefaultCompression, with the color
// type reduced as described in the package comment.
func Encode(w io.Writer, m image.Image) error {
	var e Encoder
	return e.Encode(w, m)
}
