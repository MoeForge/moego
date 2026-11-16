package vp8l

// Levels as moego/webp maps them; every level from 0 to 9 is valid.
const (
	BestSpeed          = 1
	DefaultCompression = 6
	BestCompression    = 9
)

// Encoder builds one VP8L bitstream (the payload of a 'VP8L' chunk, starting
// with the 0x2f signature). Its buffers are kept across Reset and sized to
// what an image needs, so once warm, encoding an image again allocates
// nothing, and another image allocates only for a buffer it needs larger.
//
// A stream is written in order: Header, then the transforms with WriteBits
// and SubImage, the 0 bit that ends them, MainImage once, and Finish.
type Encoder struct {
	level int
	bw    bitWriter
	huff  huffman
	hw    headerWriter
	codes [maxGreen]uint16 // canonical code scratch

	// main codes the main image, sub the sub-images (transform images
	// and the entropy image, which is written while main holds its
	// parse).
	main, sub coder
	tmp       scratch
}

// Reset starts a new, empty bitstream at level 0 to 9; levels out of
// range panic.
func (e *Encoder) Reset(level int) {
	if level < 0 || level > 9 {
		panic("vp8l: level out of range")
	}
	e.level = level
	e.bw.reset()
}

// Header writes the signature and the image header: 1 <= w, h <= 16384.
func (e *Encoder) Header(w, h int, alphaUsed bool) {
	if w < 1 || h < 1 || w > 1<<14 || h > 1<<14 {
		panic("vp8l: image size out of range")
	}
	e.bw.put(0x2f, 8)
	e.bw.put(uint32(w-1), 14)
	e.bw.put(uint32(h-1), 14)
	a := uint32(0)
	if alphaUsed {
		a = 1
	}
	e.bw.put(a, 1)
	e.bw.put(0, 3)
}

// WriteBits writes the low n bits of v, LSB first; n is at most 32.
func (e *Encoder) WriteBits(v uint32, n uint) {
	if n > 32 {
		panic("vp8l: more than 32 bits")
	}
	if n < 32 {
		v &= 1<<n - 1
	}
	e.bw.put(v, n)
}

// SubImage writes argb (w*h pixels, read only) as an entropy-coded image:
// a predictor or color transform image, or the color table.
func (e *Encoder) SubImage(argb []uint32, w, h int) {
	checkImage(argb, w, h)
	e.sub.encode(e, argb, w, h, false)
}

// MainImage writes argb (w*h pixels, read only) as the spatially coded
// image that ends the stream.
func (e *Encoder) MainImage(argb []uint32, w, h int) {
	checkImage(argb, w, h)
	e.main.encode(e, argb, w, h, true)
}

// Bits returns the number of bits written since Reset.
func (e *Encoder) Bits() int { return e.bw.bits() }

// Finish pads the stream to a byte and returns it; the slice is valid until
// the next Reset.
func (e *Encoder) Finish() []byte { return e.bw.finish() }

func checkImage(argb []uint32, w, h int) {
	if w < 1 || h < 1 || len(argb) != w*h {
		panic("vp8l: image size does not match its pixels")
	}
}
