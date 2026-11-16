package webp

import (
	"encoding/binary"
	"errors"
	"image"
	"io"
	"strconv"
	"sync"

	"github.com/MoeForge/moego/internal/vp8l"
)

var zeroByte [1]byte

var encoders = sync.Pool{New: func() any { return new(encoder) }}

// maxKeptPixels is the largest pixel buffer an encoder keeps for the next
// image when it goes back to the package's own pool.
const maxKeptPixels = 8 << 20

// Transform types as the bitstream numbers them.
const (
	tPredictor     = 0
	tColor         = 1
	tSubtractGreen = 2
	tColorIndexing = 3
)

// encoder is the state of one Encode call; EncoderBuffer keeps it between
// calls.
type encoder struct {
	level CompressionLevel
	w, h  int
	argb  []uint32 // the image, 0xAARRGGBB
	an    analysis
	// With loadAndOK, loadAnd is the AND of the pixels load wrote.
	loadAnd   uint32
	loadAndOK bool

	// Transform state.
	work []uint32 // the index image
	// subGreenOn reports whether argb holds the pixels with subtract green.
	subGreenOn bool
	// The searches of the plans tried: a predictor and a color transform
	// search for each.
	cache  [2 * maxCands]searchCache
	ncache int
	sub    []uint32 // a sub-image
	row    []uint32 // residual scratch
	resid  []uint32 // the residuals of a chunk of tiles under every mode
	quads  []uint32 // their costs by column
	modes  []uint8  // predictor modes per tile
	tcost  []uint64
	elems  []ctElem
	cts    ctSearch
	tileG  []int8
	tileR  []int8
	tileB  []int8
	idx    []uint8  // color list position of each pixel
	cooc   []uint32 // color adjacency counts
	pal    palette
	order  [256]uint8

	est     estimator
	estBits float64

	vp  vp8l.Encoder
	out []byte // the best payload of the trials
	hdr [20]byte

	tune tuning
}

// tuning overrides the encoder's choices (tests).
type tuning struct {
	on       bool
	plan     plan
	vp8level int
	side     bool // leave out the main image's entropy coding (TestTuneSide)
}

// Encode writes the Image m to w in WebP lossless format.
func (enc *Encoder) Encode(w io.Writer, m image.Image) error {
	b := m.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 || b.Dx() > maxSize || b.Dy() > maxSize {
		return errors.New("webp: invalid image size: " + strconv.Itoa(b.Dx()) + "x" + strconv.Itoa(b.Dy()))
	}
	var e *encoder
	if enc.BufferPool != nil {
		e = (*encoder)(enc.BufferPool.Get())
		if e == nil {
			e = &encoder{}
		}
		defer enc.BufferPool.Put((*EncoderBuffer)(e))
	} else {
		e = encoders.Get().(*encoder)
		defer func() {
			if cap(e.argb) > maxKeptPixels {
				*e = encoder{}
			}
			encoders.Put(e)
		}()
	}
	e.level = enc.CompressionLevel
	switch e.level {
	case BestSpeed, BestCompression:
	default:
		e.level = DefaultCompression
	}
	e.tune = tuning{}
	payload, err := e.encode(m)
	if err != nil {
		return err
	}
	return e.write(w, payload)
}

// write writes the RIFF container around payload.
func (e *encoder) write(w io.Writer, payload []byte) error {
	n := len(payload)
	pad := n & 1
	h := e.hdr[:]
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(4+8+n+pad))
	copy(h[8:], "WEBPVP8L")
	binary.LittleEndian.PutUint32(h[16:], uint32(n))
	if _, err := w.Write(h); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	if pad != 0 {
		if _, err := w.Write(zeroByte[:]); err != nil {
			return err
		}
	}
	return nil
}
