// Package vp8ltest decodes WebP lossless (VP8L) streams for tests. It is
// written from RFC 9649 independently of the encoder and of other decoders,
// favours plain code over speed, and is strict: every stream the RFC calls
// invalid, and every stream a conformant decoder may reject, is an error.
package vp8ltest

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Decode decodes a VP8L payload (starting at the 0x2f signature) into w*h
// ARGB pixels, 0xAARRGGBB, applying every transform. The payload must end
// with the byte holding the stream's last bit, padded with zero bits.
func Decode(payload []byte) (w, h int, argb []uint32, err error) {
	d := &decoder{br: bitReader{p: payload}}
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(decodeError); ok {
				w, h, argb, err = 0, 0, nil, e
				return
			}
			panic(r)
		}
	}()
	w, h, argb = d.decode()
	return w, h, argb, nil
}

// DecodeWebP checks the RIFF/WEBP/VP8L container of a simple lossless file
// (sizes, padding) and decodes its payload.
func DecodeWebP(file []byte) (w, h int, argb []uint32, err error) {
	if len(file) < 20 || string(file[0:4]) != "RIFF" || string(file[8:12]) != "WEBP" {
		return 0, 0, nil, errors.New("vp8ltest: not a RIFF WEBP file")
	}
	if string(file[12:16]) != "VP8L" {
		return 0, 0, nil, fmt.Errorf("vp8ltest: chunk %q, want VP8L", file[12:16])
	}
	riff := binary.LittleEndian.Uint32(file[4:])
	n := binary.LittleEndian.Uint32(file[16:])
	pad := n & 1
	if uint64(len(file)) != 20+uint64(n)+uint64(pad) {
		return 0, 0, nil, fmt.Errorf("vp8ltest: file is %d bytes, VP8L chunk of %d needs %d", len(file), n, 20+uint64(n)+uint64(pad))
	}
	if uint64(riff) != uint64(len(file))-8 {
		return 0, 0, nil, fmt.Errorf("vp8ltest: RIFF size %d, file has %d after it", riff, len(file)-8)
	}
	if pad == 1 && file[len(file)-1] != 0 {
		return 0, 0, nil, errors.New("vp8ltest: nonzero chunk padding")
	}
	return Decode(file[20 : 20+n])
}

type decodeError struct{ error }

func fail(format string, args ...any) {
	panic(decodeError{fmt.Errorf("vp8ltest: "+format, args...)})
}

// bitReader reads bits LSB first.
type bitReader struct {
	p   []byte
	pos int // bit position
}

func (b *bitReader) read(n int) uint32 {
	v := uint32(0)
	for i := 0; i < n; i++ {
		byteIdx := b.pos >> 3
		if byteIdx >= len(b.p) {
			fail("stream ends early")
		}
		v |= uint32(b.p[byteIdx]>>(b.pos&7)&1) << i
		b.pos++
	}
	return v
}

// end checks that the stream has no bytes past its last bit and that the
// padding bits are zero.
func (b *bitReader) end() {
	if (b.pos+7)>>3 != len(b.p) {
		fail("%d bytes after the stream", len(b.p)-(b.pos+7)>>3)
	}
	if b.pos&7 != 0 && b.p[len(b.p)-1]>>(b.pos&7) != 0 {
		fail("nonzero padding bits")
	}
}

type decoder struct {
	br bitReader
	// mainOnly stops decode after the main image, before the inverse
	// transforms; mainStart is the bit where the main image starts.
	mainOnly  bool
	mainStart int
	mainW     int
}

// DecodeMain decodes a VP8L payload up to its main image and returns that
// image as coded (before the inverse transforms, so its width is reduced
// by pixel bundling), and the bits the main image takes in the stream.
func DecodeMain(payload []byte) (w, h int, argb []uint32, bits int, err error) {
	d := &decoder{br: bitReader{p: payload}, mainOnly: true}
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(decodeError); ok {
				w, h, argb, bits, err = 0, 0, nil, 0, e
				return
			}
			panic(r)
		}
	}()
	_, h, argb = d.decode()
	return d.mainW, h, argb, d.br.pos - d.mainStart, nil
}

const (
	predictorTransform = iota
	colorTransform
	subtractGreen
	colorIndexing
)

type transform struct {
	kind  int
	bits  int      // block size bits, or the bundling width bits
	w     int      // image width before the transform's own reduction
	data  []uint32 // sub-image or color table
	dataW int      // width of data
}

func divRoundUp(n, d int) int { return (n + d - 1) / d }

func (d *decoder) decode() (int, int, []uint32) {
	if d.br.read(8) != 0x2f {
		fail("bad signature")
	}
	w := int(d.br.read(14)) + 1
	h := int(d.br.read(14)) + 1
	d.br.read(1) // alpha_is_used, a hint
	if v := d.br.read(3); v != 0 {
		fail("version %d", v)
	}
	var seen [4]bool
	var ts []transform
	xsize := w
	for d.br.read(1) == 1 {
		kind := int(d.br.read(2))
		if seen[kind] {
			fail("transform %d used twice", kind)
		}
		seen[kind] = true
		t := transform{kind: kind, w: xsize}
		switch kind {
		case predictorTransform, colorTransform:
			t.bits = int(d.br.read(3)) + 2
			t.dataW = divRoundUp(xsize, 1<<t.bits)
			t.data = d.image(t.dataW, divRoundUp(h, 1<<t.bits), false)
		case colorIndexing:
			n := int(d.br.read(8)) + 1
			t.data = d.image(n, 1, false)
			for i := 1; i < n; i++ {
				t.data[i] = addPixels(t.data[i], t.data[i-1])
			}
			t.dataW = n
			switch {
			case n <= 2:
				t.bits = 3
			case n <= 4:
				t.bits = 2
			case n <= 16:
				t.bits = 1
			}
			xsize = divRoundUp(xsize, 1<<t.bits)
		}
		ts = append(ts, t)
	}
	d.mainStart, d.mainW = d.br.pos, xsize
	px := d.image(xsize, h, true)
	d.br.end()
	if d.mainOnly {
		return w, h, px
	}
	for i := len(ts) - 1; i >= 0; i-- {
		px = ts[i].invert(px, h)
	}
	return w, h, px
}

// addPixels adds a and b per channel, modulo 256.
func addPixels(a, b uint32) uint32 {
	ag := (a & 0xff00ff00) + (b & 0xff00ff00)
	rb := (a & 0x00ff00ff) + (b & 0x00ff00ff)
	return ag&0xff00ff00 | rb&0x00ff00ff
}

// A code is a canonical prefix code read from the stream.
type code struct {
	single int     // the symbol of a one-leaf code, or -1
	count  [16]int // codes per length
	syms   []int   // symbols by length, then value
}

func (d *decoder) readSymbol(c *code) int {
	if c.single >= 0 {
		return c.single
	}
	v, first, idx := 0, 0, 0
	for l := 1; l <= 15; l++ {
		v |= int(d.br.read(1))
		n := c.count[l]
		if v-first < n {
			return c.syms[idx+v-first]
		}
		idx += n
		first = (first + n) << 1
		v <<= 1
	}
	panic("unreachable: the code is complete")
}

// buildCode makes the canonical code of lens; it must be complete, or have
// exactly one length, of 1.
func buildCode(lens []int) *code {
	c := &code{single: -1}
	used, last := 0, 0
	for s, l := range lens {
		if l != 0 {
			c.count[l]++
			used++
			last = s
		}
	}
	switch used {
	case 0:
		fail("empty prefix code")
	case 1:
		if lens[last] != 1 {
			fail("one-symbol code of length %d", lens[last])
		}
		c.single = last
		return c
	}
	room := 1 << 15
	for l := 1; l <= 15; l++ {
		room -= c.count[l] << (15 - l)
	}
	if room < 0 {
		fail("over-subscribed prefix code")
	}
	if room > 0 {
		fail("incomplete prefix code")
	}
	for l := 1; l <= 15; l++ {
		for s, sl := range lens {
			if sl == l {
				c.syms = append(c.syms, s)
			}
		}
	}
	return c
}

var codeLenOrder = [19]int{17, 18, 0, 1, 2, 3, 4, 5, 16, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

// readCode reads one prefix code over an alphabet of n symbols.
func (d *decoder) readCode(n int) *code {
	lens := make([]int, n)
	if d.br.read(1) == 1 {
		num := int(d.br.read(1)) + 1
		first8 := d.br.read(1)
		s0 := int(d.br.read(1 + 7*int(first8)))
		if s0 >= n {
			fail("simple code symbol %d of %d", s0, n)
		}
		lens[s0] = 1
		if num == 2 {
			s1 := int(d.br.read(8))
			if s1 >= n {
				fail("simple code symbol %d of %d", s1, n)
			}
			lens[s1] = 1
		}
		return buildCode(lens)
	}
	var clLens [19]int
	ncl := 4 + int(d.br.read(4))
	for i := 0; i < ncl; i++ {
		clLens[codeLenOrder[i]] = int(d.br.read(3))
	}
	cl := buildCode(clLens[:])
	maxSym := n
	if d.br.read(1) == 1 {
		nb := 2 + 2*int(d.br.read(3))
		maxSym = 2 + int(d.br.read(nb))
		if maxSym > n {
			fail("max_symbol %d over alphabet %d", maxSym, n)
		}
	}
	prev := 8
	for i := 0; i < n; {
		if maxSym == 0 {
			break
		}
		maxSym--
		s := d.readSymbol(cl)
		if s < 16 {
			lens[i] = s
			i++
			if s != 0 {
				prev = s
			}
			continue
		}
		var rep, val int
		switch s {
		case 16:
			rep, val = 3+int(d.br.read(2)), prev
		case 17:
			rep = 3 + int(d.br.read(3))
		case 18:
			rep = 11 + int(d.br.read(7))
		}
		if i+rep > n {
			fail("code length repeat past the alphabet")
		}
		for ; rep > 0; rep-- {
			lens[i] = val
			i++
		}
	}
	return buildCode(lens)
}

// group is the five codes of a prefix code group.
type group [5]*code

// numLengthCodes is the count of length prefix codes in the green alphabet.
const numLengthCodes = 24

// prefixValue reads the extra bits of prefix code c and returns the value.
func (d *decoder) prefixValue(c int) int {
	if c < 4 {
		return c + 1
	}
	extra := (c - 2) >> 1
	offset := (2 + (c & 1)) << extra
	return offset + int(d.br.read(extra)) + 1
}

var distMap = [120][2]int{
	{0, 1}, {1, 0}, {1, 1}, {-1, 1}, {0, 2}, {2, 0}, {1, 2},
	{-1, 2}, {2, 1}, {-2, 1}, {2, 2}, {-2, 2}, {0, 3}, {3, 0},
	{1, 3}, {-1, 3}, {3, 1}, {-3, 1}, {2, 3}, {-2, 3}, {3, 2},
	{-3, 2}, {0, 4}, {4, 0}, {1, 4}, {-1, 4}, {4, 1}, {-4, 1},
	{3, 3}, {-3, 3}, {2, 4}, {-2, 4}, {4, 2}, {-4, 2}, {0, 5},
	{3, 4}, {-3, 4}, {4, 3}, {-4, 3}, {5, 0}, {1, 5}, {-1, 5},
	{5, 1}, {-5, 1}, {2, 5}, {-2, 5}, {5, 2}, {-5, 2}, {4, 4},
	{-4, 4}, {3, 5}, {-3, 5}, {5, 3}, {-5, 3}, {0, 6}, {6, 0},
	{1, 6}, {-1, 6}, {6, 1}, {-6, 1}, {2, 6}, {-2, 6}, {6, 2},
	{-6, 2}, {4, 5}, {-4, 5}, {5, 4}, {-5, 4}, {3, 6}, {-3, 6},
	{6, 3}, {-6, 3}, {0, 7}, {7, 0}, {1, 7}, {-1, 7}, {5, 5},
	{-5, 5}, {7, 1}, {-7, 1}, {4, 6}, {-4, 6}, {6, 4}, {-6, 4},
	{2, 7}, {-2, 7}, {7, 2}, {-7, 2}, {3, 7}, {-3, 7}, {7, 3},
	{-7, 3}, {5, 6}, {-5, 6}, {6, 5}, {-6, 5}, {8, 0}, {4, 7},
	{-4, 7}, {7, 4}, {-7, 4}, {8, 1}, {8, 2}, {6, 6}, {-6, 6},
	{8, 3}, {5, 7}, {-5, 7}, {7, 5}, {-7, 5}, {8, 4}, {6, 7},
	{-6, 7}, {7, 6}, {-7, 6}, {8, 5}, {7, 7}, {-7, 7}, {8, 6},
	{8, 7},
}

// image decodes an entropy-coded image of w*h pixels; main allows meta
// prefix codes.
func (d *decoder) image(w, h int, main bool) []uint32 {
	cacheBits := 0
	if d.br.read(1) == 1 {
		cacheBits = int(d.br.read(4))
		if cacheBits < 1 || cacheBits > 11 {
			fail("color cache bits %d", cacheBits)
		}
	}
	var meta []uint32
	metaBits, metaW := 0, 0
	ngroups := 1
	if main && d.br.read(1) == 1 {
		metaBits = int(d.br.read(3)) + 2
		metaW = divRoundUp(w, 1<<metaBits)
		meta = d.image(metaW, divRoundUp(h, 1<<metaBits), false)
		for _, p := range meta {
			ngroups = max(ngroups, int(p>>8&0xffff)+1)
		}
	}
	cacheSize := 0
	if cacheBits > 0 {
		cacheSize = 1 << cacheBits
	}
	groups := make([]group, ngroups)
	for i := range groups {
		groups[i] = group{
			d.readCode(256 + numLengthCodes + cacheSize),
			d.readCode(256), d.readCode(256), d.readCode(256),
			d.readCode(40),
		}
	}
	var cache []uint32
	if cacheBits > 0 {
		cache = make([]uint32, cacheSize)
	}
	insert := func(p uint32) {
		if cache != nil {
			cache[(0x1e35a7bd*p)>>(32-cacheBits)] = p
		}
	}
	total := w * h
	out := make([]uint32, total)
	for pos := 0; pos < total; {
		g := &groups[0]
		if meta != nil {
			x, y := pos%w, pos/w
			g = &groups[meta[(y>>metaBits)*metaW+(x>>metaBits)]>>8&0xffff]
		}
		s := d.readSymbol(g[0])
		switch {
		case s < 256:
			r := d.readSymbol(g[1])
			b := d.readSymbol(g[2])
			a := d.readSymbol(g[3])
			p := uint32(a)<<24 | uint32(r)<<16 | uint32(s)<<8 | uint32(b)
			out[pos] = p
			insert(p)
			pos++
		case s < 256+numLengthCodes:
			n := d.prefixValue(s - 256)
			dc := d.prefixValue(d.readSymbol(g[4]))
			var dist int
			if dc > 120 {
				dist = dc - 120
			} else {
				o := distMap[dc-1]
				dist = o[0] + o[1]*w
				if dist < 1 {
					dist = 1
				}
			}
			if dist > pos {
				fail("distance %d at pixel %d", dist, pos)
			}
			if pos+n > total {
				fail("copy of %d at pixel %d runs past the %d pixels", n, pos, total)
			}
			for i := 0; i < n; i++ {
				p := out[pos-dist]
				out[pos] = p
				insert(p)
				pos++
			}
		default:
			// The alphabet has cache symbols only with a cache.
			p := cache[s-256-numLengthCodes]
			out[pos] = p
			insert(p)
			pos++
		}
	}
	return out
}

// invert applies the inverse of t to px, an image of t's output width and
// height h, and returns the image before t.
func (t *transform) invert(px []uint32, h int) []uint32 {
	w := t.w
	switch t.kind {
	case subtractGreen:
		for i, p := range px {
			g := p >> 8 & 0xff
			r := (p>>16 + g) & 0xff
			b := (p + g) & 0xff
			px[i] = p&0xff00ff00 | r<<16 | b
		}
	case predictorTransform:
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				i := y*w + x
				var pred uint32
				switch {
				case x == 0 && y == 0:
					pred = 0xff000000
				case y == 0:
					pred = px[i-1]
				case x == 0:
					pred = px[i-w]
				default:
					m := t.data[(y>>t.bits)*t.dataW+(x>>t.bits)] >> 8 & 0xff
					if m >= 14 {
						fail("predictor mode %d", m)
					}
					// TR of the rightmost column is the row's first
					// pixel, which is where i-w+1 points.
					pred = predict(int(m), px[i-1], px[i-w], px[i-w+1], px[i-w-1])
				}
				px[i] = addPixels(px[i], pred)
			}
		}
	case colorTransform:
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				i := y*w + x
				e := t.data[(y>>t.bits)*t.dataW+(x>>t.bits)]
				g2r, g2b, r2b := int8(e), int8(e>>8), int8(e>>16)
				p := px[i]
				g := int8(p >> 8)
				r := int(p>>16&0xff) + delta(g2r, g)
				b := int(p&0xff) + delta(g2b, g)
				b += delta(r2b, int8(r))
				px[i] = p&0xff00ff00 | uint32(r&0xff)<<16 | uint32(b&0xff)
			}
		}
	case colorIndexing:
		in := px
		inW := divRoundUp(w, 1<<t.bits)
		out := make([]uint32, w*h)
		perBits := 8 >> t.bits
		mask := uint32(1)<<perBits - 1
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				p := in[y*inW+x>>t.bits]
				idx := int((p >> 8 & 0xff) >> (uint(x&(1<<t.bits-1)) * uint(perBits)) & mask)
				if idx < len(t.data) {
					out[y*w+x] = t.data[idx]
				}
			}
		}
		return out
	}
	return px
}

func delta(t, c int8) int { return int(t) * int(c) >> 5 }

func avg2(a, b uint32) uint32 {
	var r uint32
	for s := 0; s < 32; s += 8 {
		r |= ((a>>s&0xff + b>>s&0xff) / 2) << s
	}
	return r
}

func clamp(v int) uint32 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint32(v)
}

func ch(p uint32, s int) int { return int(p >> s & 0xff) }

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func predict(m int, l, t, tr, tl uint32) uint32 {
	switch m {
	case 0:
		return 0xff000000
	case 1:
		return l
	case 2:
		return t
	case 3:
		return tr
	case 4:
		return tl
	case 5:
		return avg2(avg2(l, tr), t)
	case 6:
		return avg2(l, tl)
	case 7:
		return avg2(l, t)
	case 8:
		return avg2(tl, t)
	case 9:
		return avg2(t, tr)
	case 10:
		return avg2(avg2(l, tl), avg2(t, tr))
	case 11:
		pl, pt := 0, 0
		for s := 0; s < 32; s += 8 {
			p := ch(l, s) + ch(t, s) - ch(tl, s)
			pl += abs(p - ch(l, s))
			pt += abs(p - ch(t, s))
		}
		if pl < pt {
			return l
		}
		return t
	case 12:
		var r uint32
		for s := 0; s < 32; s += 8 {
			r |= clamp(ch(l, s)+ch(t, s)-ch(tl, s)) << s
		}
		return r
	default: // 13
		a := avg2(l, t)
		var r uint32
		for s := 0; s < 32; s += 8 {
			r |= clamp(ch(a, s)+(ch(a, s)-ch(tl, s))/2) << s
		}
		return r
	}
}
