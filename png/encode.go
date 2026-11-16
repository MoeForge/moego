package png

import (
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"io"
	"strconv"
	"sync"

	"github.com/MoeForge/moego/internal/deflate"
)

const pngHeader = "\x89PNG\r\n\x1a\n"

// idatSize is the payload size of every IDAT chunk but the last.
const idatSize = 1 << 16

// maxKeptCache is the largest converted copy of a generic image that an
// encoder keeps for the next image.
const maxKeptCache = 16 << 20

// stageSize is how many filtered bytes are collected before they go to the
// compressor in one Write.
const stageSize = 1 << 15

var encoders = sync.Pool{New: func() any { return new(encoder) }}

// encoder is the state of one Encode call; EncoderBuffer keeps it between
// calls.
type encoder struct {
	w     io.Writer
	err   error
	level CompressionLevel
	src   source
	st    stats
	set   colorSet

	// Output layout.
	ct, depth uint8
	narrow    bool // 16-bit source written with 8-bit samples
	few       bool // truecolor with at most 256 colors
	// altCT is the truecolor type a palette image could also use, or
	// ctPalette when there is none.
	altCT    uint8
	bpp      int // filter distance in bytes
	rowBytes int // bytes per row without the filter byte

	plte    [3 * 256]byte
	trns    [256]byte
	npal    int
	ntrns   int
	grayIdx [256]uint8

	cache []byte    // generic images converted once
	sbuf  [2][]byte // canonical rows converted from the source
	nbuf  []byte    // narrowed 16-bit row
	ibuf  []byte    // palette indices before packing
	obuf  [2][]byte // rows in the output layout
	zero  []byte    // the row above the first
	stage []byte    // filtered rows waiting for the compressor
	out   []byte    // file bytes waiting for w; an open IDAT chunk at idat
	idat  int

	comp  *deflate.Writer
	count countWriter
	hist  [5][256]uint32
	fbuf  [4][]byte // residuals of Sub to Paeth for the entropy strategy
	// trialSizes holds the trial output size of each strategy, costSum
	// and costBytes the minsum cost of the chosen filters (tests).
	trialSizes [nStrats]int
	costSum    uint64
	costBytes  int
	model      byteModel

	tune tuning
}

// tuning overrides the filter strategy and deflate level (tests).
type tuning struct {
	strat  uint8 // a strategy, or tuneOff
	zlevel int
	sample int // BestCompression's trial sampling of images over 16 bands, 0 for the default
	// trialOnly stops BestCompression after the strategy trials.
	trialOnly bool
	// levelOnly overrides only the deflate level.
	levelOnly bool
}

const tuneOff = 255

// Encode writes the Image m to w in PNG format.
func (enc *Encoder) Encode(w io.Writer, m image.Image) error {
	mw, mh := int64(m.Bounds().Dx()), int64(m.Bounds().Dy())
	if mw <= 0 || mh <= 0 || mw >= 1<<32 || mh >= 1<<32 {
		return FormatError("invalid image size: " + strconv.FormatInt(mw, 10) + "x" + strconv.FormatInt(mh, 10))
	}
	var e *encoder
	if enc.BufferPool != nil {
		e = (*encoder)(enc.BufferPool.Get())
		if e == nil {
			e = &encoder{}
		}
		defer enc.BufferPool.Put((*EncoderBuffer)(e))
	} else {
		// Without a pool, encoders are kept here: the deflate window and
		// match tables are large next to the buffers of a small image.
		e = encoders.Get().(*encoder)
		defer encoders.Put(e)
	}
	e.tune = tuning{strat: tuneOff}
	err := e.encode(w, m, enc)
	e.w, e.src = nil, source{}
	if cap(e.cache) > maxKeptCache {
		e.cache = nil
	}
	return err
}

func (e *encoder) encode(w io.Writer, m image.Image, enc *Encoder) error {
	e.w, e.err = w, nil
	e.level = enc.CompressionLevel
	switch e.level {
	case NoCompression, BestSpeed, BestCompression:
	default:
		// As in image/png, other values mean DefaultCompression.
		e.level = DefaultCompression
	}
	if err := e.src.classify(m); err != nil {
		return err
	}
	s := &e.src
	maxRow := 8 * s.w
	e.sbuf[0] = grow(e.sbuf[0], maxRow)
	e.sbuf[1] = grow(e.sbuf[1], maxRow)
	if s.kind == srcGeneric {
		e.cache = s.loadGeneric(e.cache)
	}
	e.few, e.altCT = false, ctPalette
	if enc.KeepColorType || s.format == fmtIndex {
		if s.kind != srcGeneric {
			s.opaque = s.stdOpaque()
		}
		e.chooseStd()
		if e.level == DefaultCompression || e.level == BestSpeed {
			e.few = e.fewColors()
		}
	} else {
		e.analyze()
		e.chooseReduced()
		e.few = e.set.n <= 256 && s.format == fmtRGBA8
	}
	if e.ct == ctPalette && s.format == fmtIndex {
		e.stdPalette()
	}
	e.layout()
	if e.altCT != ctPalette && (e.level == DefaultCompression || e.level == BestCompression) && e.tune.strat == tuneOff {
		e.paletteOrTruecolor()
	}

	// The output buffer holds the chunks before the first IDAT and one
	// IDAT, or a small image's whole stream with room for stored blocks.
	out := idatSize
	if r := e.rowBytes + 1; e.src.h <= idatSize/r {
		out = e.src.h*r + 64
	}
	e.out = grow(e.out, out+2048)[:0]
	e.out = append(e.out, pngHeader...)
	e.writeIHDR()
	if e.ct == ctPalette {
		e.chunk("PLTE", e.plte[:3*e.npal])
		if e.ntrns > 0 {
			e.chunk("tRNS", e.trns[:e.ntrns])
		}
	}
	e.writeIDATs()
	e.chunk("IEND", nil)
	e.flush()
	return e.err
}

func grow(b []byte, n int) []byte {
	if cap(b) < n {
		return make([]byte, n)
	}
	return b[:n]
}

// stdPalette copies a palette image's palette as image/png writes it:
// converted with color.NRGBAModel, which keeps the color of a color.NRGBA
// entry even under alpha 0.
func (e *encoder) stdPalette() {
	last := -1
	for i, c := range e.src.palette {
		var n [4]uint8
		if nc, ok := c.(color.NRGBA); ok {
			n = [4]uint8{nc.R, nc.G, nc.B, nc.A}
		} else {
			r, g, b, a := c.RGBA()
			switch a {
			case 0xffff:
			case 0:
				r, g, b = 0, 0, 0
			default:
				r, g, b = r*0xffff/a, g*0xffff/a, b*0xffff/a
			}
			n = [4]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
		}
		e.plte[3*i], e.plte[3*i+1], e.plte[3*i+2] = n[0], n[1], n[2]
		e.trns[i] = n[3]
		if n[3] != 0xff {
			last = i
		}
	}
	e.npal, e.ntrns = len(e.src.palette), last+1
}

// layout sets the row geometry for the chosen color type and depth.
func (e *encoder) layout() {
	ch := 1
	switch e.ct {
	case ctRGB:
		ch = 3
	case ctGrayAlpha:
		ch = 2
	case ctRGBA:
		ch = 4
	}
	bits := ch * int(e.depth)
	e.bpp = max(1, bits/8)
	e.rowBytes = (e.src.w*bits + 7) / 8
	// The conversion kernels store whole 32-byte vectors.
	e.obuf[0] = grow(e.obuf[0], e.rowBytes+32)[:e.rowBytes]
	e.obuf[1] = grow(e.obuf[1], e.rowBytes+32)[:e.rowBytes]
	e.zero = grow(e.zero, e.rowBytes)
	clear(e.zero)
	if e.narrow {
		e.nbuf = grow(e.nbuf, 4*e.src.w)
	}
	if e.ct == ctPalette && e.depth < 8 {
		e.ibuf = grow(e.ibuf, e.src.w)
	}
	// Small images need less than stageSize; filtered is their whole size.
	filtered := stageSize
	if r := e.rowBytes + 1; e.src.h <= stageSize/r {
		filtered = e.src.h * r
	}
	e.stage = grow(e.stage, max(filtered, 2*(e.rowBytes+1)))[:0]
}

func (e *encoder) writeIHDR() {
	var b [13]byte
	binary.BigEndian.PutUint32(b[0:], uint32(e.src.w))
	binary.BigEndian.PutUint32(b[4:], uint32(e.src.h))
	b[8], b[9] = e.depth, e.ct
	e.chunk("IHDR", b[:])
}

// chunk appends a complete chunk to the output.
func (e *encoder) chunk(name string, data []byte) {
	n := len(e.out)
	e.out = binary.BigEndian.AppendUint32(e.out, uint32(len(data)))
	e.out = append(e.out, name...)
	e.out = append(e.out, data...)
	e.out = binary.BigEndian.AppendUint32(e.out, crc32.ChecksumIEEE(e.out[n+4:]))
}

func (e *encoder) flush() {
	if e.err == nil && len(e.out) > 0 {
		_, e.err = e.w.Write(e.out)
	}
	e.out = e.out[:0]
}

// idatWriter receives the zlib stream and cuts it into IDAT chunks.
type idatWriter encoder

func (iw *idatWriter) Write(p []byte) (int, error) {
	e := (*encoder)(iw)
	n := len(p)
	for len(p) > 0 {
		if e.idat < 0 {
			e.idat = len(e.out)
			e.out = append(e.out, 0, 0, 0, 0, 'I', 'D', 'A', 'T')
		}
		k := min(len(p), idatSize-(len(e.out)-e.idat-8))
		e.out = append(e.out, p[:k]...)
		p = p[k:]
		if len(e.out)-e.idat-8 == idatSize {
			e.closeIDAT()
			e.flush()
		}
	}
	return n, e.err
}

// closeIDAT finishes the open IDAT chunk.
func (e *encoder) closeIDAT() {
	binary.BigEndian.PutUint32(e.out[e.idat:], uint32(len(e.out)-e.idat-8))
	e.out = binary.BigEndian.AppendUint32(e.out, crc32.ChecksumIEEE(e.out[e.idat+4:]))
	e.idat = -1
}

// bestLevel is the deflate level of BestCompression: the near-optimal
// parse with a shallow tree. With the strategy trials it is 5.4% smaller
// than image/png's default over the benchmark corpus at about twice the
// speed of deflate level 9, which is 1.8% smaller still.
const bestLevel = 8

// compressionLevel maps a CompressionLevel to a deflate level.
func compressionLevel(l CompressionLevel) int {
	switch l {
	case NoCompression:
		return deflate.NoCompression
	case BestSpeed:
		return deflate.BestSpeed
	case BestCompression:
		return bestLevel
	}
	return deflate.DefaultCompression
}

func (e *encoder) writeIDATs() {
	e.idat = -1
	strat, zlevel := e.strategy(), compressionLevel(e.level)
	if e.tune.levelOnly {
		zlevel = e.tune.zlevel
	} else if e.tune.strat != tuneOff {
		strat, zlevel = e.tune.strat, e.tune.zlevel
	} else if e.level == BestCompression {
		strat = e.bestStrategy()
		if e.tune.trialOnly {
			return
		}
	}
	if e.comp == nil {
		e.comp = deflate.NewWriter((*idatWriter)(e), zlevel)
	} else {
		e.comp.Reset((*idatWriter)(e), zlevel)
	}
	e.filterRows(strat, e.comp, 1)
	if e.err == nil {
		if err := e.comp.Close(); err != nil && e.err == nil {
			e.err = err
		}
	}
	if e.idat >= 0 {
		e.closeIDAT()
	}
}

// filterRows filters every row with strategy strat and feeds the rows to
// z. With sample > 1 it feeds only one band of bandRows rows in every
// sample bands, for a quick estimate.
func (e *encoder) filterRows(strat uint8, z *deflate.Writer, sample int) {
	z.SetHint(e.bpp, e.rowBytes+1)
	e.model.rows = 0
	prev := e.zero
	for y := 0; y < e.src.h && e.err == nil; y++ {
		if sample > 1 && y/bandRows%sample != 0 {
			if y%bandRows == bandRows-1 {
				prev = e.outRow(y)
			}
			continue
		}
		cur := e.outRow(y)
		f := e.pick(strat, cur, prev)
		if len(e.stage)+1+e.rowBytes > cap(e.stage) {
			e.feed(z)
		}
		n := len(e.stage)
		e.stage = e.stage[:n+1+e.rowBytes]
		e.stage[n] = f
		filterRow(e.stage[n+1:], cur, prev, e.bpp, f)
		prev = cur
	}
	e.feed(z)
}

func (e *encoder) feed(z *deflate.Writer) {
	if len(e.stage) > 0 && e.err == nil {
		if _, err := z.Write(e.stage); err != nil && e.err == nil {
			e.err = err
		}
	}
	e.stage = e.stage[:0]
}

// outRow returns row y in the output layout, unfiltered. The slice stays
// valid while row y+1 is converted.
func (e *encoder) outRow(y int) []byte {
	s := &e.src
	p := s.row(y, e.sbuf[y&1])
	format := s.format
	if e.narrow {
		d := e.nbuf
		if format == fmtG16 {
			d = d[:s.w]
		}
		for i := range d {
			d[i] = p[2*i]
		}
		p = d
		if format == fmtG16 {
			format = fmtG8
		} else {
			format = fmtRGBA8
		}
	}
	d := e.obuf[y&1]
	switch format {
	case fmtIndex:
		if e.depth == 8 {
			return p
		}
		pack(d, p, e.depth)
		return d
	case fmtG8:
		switch {
		case e.ct == ctPalette:
			idx := d
			if e.depth < 8 {
				idx = e.ibuf[:len(p)]
			}
			for i, v := range p {
				idx[i] = e.grayIdx[v]
			}
			if e.depth < 8 {
				pack(d, idx, e.depth)
			}
			return d
		case e.depth < 8:
			packGray(d, p, 1, e.depth)
			return d
		}
		if e.narrow {
			copy(d, p)
			return d
		}
		return p
	case fmtRGBA8:
		switch e.ct {
		case ctRGBA:
			if e.narrow {
				copy(d, p)
				return d
			}
			return p
		case ctRGB:
			toRGB(d, p)
		case ctGrayAlpha:
			toGA(d, p)
		case ctGray:
			if e.depth < 8 {
				packGray(d, p, 4, e.depth)
				break
			}
			toG(d, p)
		case ctPalette:
			e.paletteRow(d, p)
		}
		return d
	case fmtG16:
		return p
	case fmtRGBA16:
		switch e.ct {
		case ctRGBA:
			return p
		case ctRGB:
			for i, j := 0, 0; j+7 < len(p); i, j = i+6, j+8 {
				copy(d[i:i+6], p[j:j+6])
			}
		case ctGrayAlpha:
			for i, j := 0, 0; j+7 < len(p); i, j = i+4, j+8 {
				d[i], d[i+1], d[i+2], d[i+3] = p[j], p[j+1], p[j+6], p[j+7]
			}
		case ctGray:
			for i, j := 0, 0; j+7 < len(p); i, j = i+2, j+8 {
				d[i], d[i+1] = p[j], p[j+1]
			}
		}
		return d
	}
	panic("png: bad format")
}

// paletteRow writes the palette indices of an RGBA8 row.
func (e *encoder) paletteRow(d, p []byte) {
	idx := d
	if e.depth < 8 {
		idx = e.ibuf[:len(p)/4]
	}
	if e.st.gray && e.st.opaque {
		for i := range idx {
			idx[i] = e.grayIdx[p[4*i]]
		}
	} else {
		last, li := ^binary.LittleEndian.Uint32(p), uint8(0)
		for i := range idx {
			v := binary.LittleEndian.Uint32(p[4*i:])
			if v != last {
				last, li = v, e.set.index(v)
			}
			idx[i] = li
		}
	}
	if e.depth < 8 {
		pack(d, idx, e.depth)
	}
}

// pack packs 8-bit indices into depth-bit samples as image/png does,
// shifting each index in whole.
func pack(d, idx []byte, depth uint8) {
	per := 8 / int(depth)
	j := 0
	var a uint8
	c := 0
	for _, v := range idx {
		a = a<<depth | v
		c++
		if c == per {
			d[j] = a
			j++
			a, c = 0, 0
		}
	}
	if c != 0 {
		for ; c < per; c++ {
			a <<= depth
		}
		d[j] = a
	}
}

// packGray packs every step-th byte of p, a gray value that is exact at
// depth bits, into depth-bit samples.
func packGray(d, p []byte, step int, depth uint8) {
	div := [9]uint8{1: 0xff, 2: 0x55, 4: 0x11}[depth]
	per := 8 / int(depth)
	j := 0
	var a uint8
	c := 0
	for i := 0; i < len(p); i += step {
		a = a<<depth | p[i]/div
		c++
		if c == per {
			d[j] = a
			j++
			a, c = 0, 0
		}
	}
	if c != 0 {
		for ; c < per; c++ {
			a <<= depth
		}
		d[j] = a
	}
}
