package deflate

import (
	"encoding/binary"
	"errors"
	"io"
)

// Compression levels. Levels 2 to 5 and 7 to 8 sit between the named ones;
// level 8 already uses the near-optimal parse of level 9, with less effort.
const (
	NoCompression      = 0 // stored blocks only
	BestSpeed          = 1
	DefaultCompression = 6
	BestCompression    = 9
)

const (
	// maxChunk is the input matched in one pass after the history. The
	// window grows to windowSize+maxChunk as input arrives.
	maxChunk = 1 << 18
	// maxPend bounds the input whose tokens wait in the block across a
	// chunk; past it the block is written at the chunk's end.
	maxPend = 1 << 17
	minWin  = 1<<15 - winSlack
	// winSlack keeps eight-byte loads near the end of the input inside
	// the allocation.
	winSlack = 16

	outSize = 1<<14 - outSlack
	// outRoom is the most a block writes between checks of the output
	// buffer: a dynamic header, or a batch of tokens.
	outRoom = 1 << 13
	batch   = 1024
)

var errClosed = errors.New("deflate: write to closed Writer")

// Writer compresses into a zlib stream (RFC 1950 header, RFC 1951 blocks,
// Adler-32 trailer). Its memory stays bounded whatever the input size, and
// a warm Reset followed by an encode allocates nothing.
type Writer struct {
	dst    io.Writer
	level  int
	err    error
	closed bool
	begun  bool
	adler  uint32
	bpp    int
	stride int

	// win holds up to windowSize bytes already compressed, then the input
	// still to compress, win[pos:end]. Match finders store positions as
	// position+base, which stays valid as the window slides and is out of
	// range for every position of an earlier stream.
	win  []byte
	pos  int
	end  int
	base int
	// win[pend:pos] is coded into tokens not written yet.
	pend int

	bw    bitWriter
	blk   block
	split splitter

	fast  *fastMatcher
	chain *chainMatcher
	swept int // where chain last swept its head
	opt   *optimizer
}

// NewWriter returns a Writer compressing at level 0 to 9, or -1 for
// DefaultCompression. Other levels panic.
func NewWriter(dst io.Writer, level int) *Writer {
	w := &Writer{base: windowSize + 1}
	w.Reset(dst, level)
	return w
}

// Reset discards the Writer's state and makes it write a new stream to dst
// at level, keeping its buffers. The hint is cleared.
func (w *Writer) Reset(dst io.Writer, level int) {
	if level == -1 {
		level = DefaultCompression
	}
	if level < 0 || level > 9 {
		panic("deflate: invalid compression level")
	}
	if w.bw.out == nil {
		w.bw.out = make([]byte, outSize+outSlack)
	}
	w.dst = dst
	w.level = level
	w.err = nil
	w.closed = false
	w.begun = false
	w.adler = 1
	w.bpp, w.stride = 0, 0
	w.base += w.end + windowSize + 1
	if w.base > 1<<30 {
		w.rebase()
	}
	w.pos, w.end, w.pend = 0, 0, 0
	w.bw.n, w.bw.bits, w.bw.nbits = 0, 0, 0
	w.blk.reset()
	if w.chain != nil {
		w.chain.reset(w.base)
		w.swept = w.base
	}
	if w.opt != nil {
		w.opt.warm = false
	}
}

// SetHint tells the Writer the layout of filtered PNG rows: bpp bytes per
// pixel (1 to 8) and stride bytes per row including the filter byte.
// Matches at those distances are the most common in such data. Call it
// before the first Write; values out of range are ignored.
func (w *Writer) SetHint(bpp, stride int) {
	if bpp >= 1 && bpp <= 8 {
		w.bpp = bpp
	}
	if stride > 1 && stride <= windowSize {
		w.stride = stride
	}
}

// Write compresses p. Errors from the destination are returned and stay.
func (w *Writer) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.closed {
		return 0, errClosed
	}
	w.adler = adler32(w.adler, p)
	total := len(p)
	for len(p) > 0 {
		if w.end == len(w.win) {
			if len(w.win) < windowSize+maxChunk {
				w.grow(len(p))
			} else {
				w.compress(false)
				if w.err != nil {
					return total - len(p), w.err
				}
				w.slide()
			}
		}
		c := copy(w.win[w.end:], p)
		w.end += c
		p = p[c:]
	}
	return total, nil
}

// Close compresses the remaining input and writes the final block and the
// Adler-32 trailer. It does not close the destination.
func (w *Writer) Close() error {
	if w.closed {
		return w.err
	}
	w.closed = true
	if w.err != nil {
		return w.err
	}
	if w.pend < w.end {
		w.compress(true)
	} else {
		w.header()
		// An empty final block with the fixed code: the end-of-block
		// code is seven zero bits.
		w.bw.put(3, 10)
	}
	w.bw.align()
	binary.BigEndian.PutUint32(w.bw.out[w.bw.n:], w.adler)
	w.bw.n += 4
	w.flush()
	return w.err
}

// grow enlarges the window for need more bytes: first as much as the
// input so far needs, at least minWin, then to its full size, so a large
// input allocates little more than one full window.
func (w *Writer) grow(need int) {
	const full = windowSize + maxChunk
	size := full
	if len(w.win) == 0 {
		size = min(full, max(minWin, w.end+need))
	}
	if size+winSlack <= cap(w.win) {
		w.win = w.win[:size]
		return
	}
	win := make([]byte, size+winSlack)[:size]
	copy(win, w.win[:w.end])
	w.win = win
}

// slide keeps the last windowSize bytes as history for the next chunk,
// and the input of tokens not written yet.
func (w *Writer) slide() {
	keep := min(w.end, max(windowSize, w.end-w.pend))
	copy(w.win, w.win[w.end-keep:w.end])
	w.base += w.end - keep
	w.pend -= w.end - keep
	w.pos, w.end = keep, keep
	if w.base > 1<<30 {
		w.rebase()
	}
}

// rebase lowers base by a multiple of 1<<16, dropping table entries that
// fall out of range.
func (w *Writer) rebase() {
	delta := (w.base - (windowSize + 1)) &^ 0xffff
	if w.fast != nil {
		w.fast.rebase(int32(delta))
	}
	w.swept -= delta
	if w.opt != nil {
		w.opt.bt.rebase(int32(delta))
	}
	w.base -= delta
}

func (w *Writer) header() {
	if w.begun {
		return
	}
	w.begun = true
	// CM 8 with a 32 KiB window, then FLEVEL as RFC 1950 describes it.
	var flevel uint16
	switch {
	case w.level <= 1:
		flevel = 0
	case w.level < 6:
		flevel = 1
	case w.level == 6:
		flevel = 2
	default:
		flevel = 3
	}
	h := uint16(0x78)<<8 | flevel<<6
	h += (31 - h%31) % 31
	binary.BigEndian.PutUint16(w.bw.out[w.bw.n:], h)
	w.bw.n += 2
}

// compress codes win[pos:end] and marks it done.
func (w *Writer) compress(final bool) {
	w.header()
	if w.level != NoCompression && w.level < minOptLevel {
		// Room for the tokens of this input, up to what an engine
		// collects at once. The near-optimal parse keeps its tokens in
		// its own arrays.
		if n := min(w.end-w.pend+1, bufTokens); cap(w.blk.tokens) < n {
			w.blk.tokens = make([]token, 0, n)
		}
	}
	switch {
	case w.level == NoCompression:
		w.writeStored(w.pos, w.end, final)
	case w.level == BestSpeed:
		if w.fast == nil {
			w.fast = new(fastMatcher)
		}
		w.compressFast(final)
	case w.level >= minOptLevel:
		if w.opt == nil {
			w.opt = new(optimizer)
		}
		w.compressOpt(final)
	default:
		if w.chain == nil {
			w.chain = new(chainMatcher)
			w.chain.reset(w.pend + w.base)
			w.swept = w.pend + w.base
		}
		w.compressChain(final)
	}
	w.pos = w.end
	if w.level == NoCompression || w.level >= minOptLevel {
		w.pend = w.end
	}
}

// flush hands the whole bytes of the output buffer to the destination.
func (w *Writer) flush() {
	b := &w.bw
	if b.n == 0 {
		return
	}
	if w.err == nil {
		_, w.err = w.dst.Write(b.out[:b.n])
	}
	// The partial byte stays in the accumulator; bytes past n are rewritten
	// by the next store.
	b.n = 0
}

// room makes sure the output buffer has outRoom free bytes.
func (w *Writer) room() {
	if w.bw.n > outSize-outRoom {
		w.flush()
	}
}

// writeBlock writes tokens, which cover win[start:end] and whose counts
// are in litFreq and distFreq, as the cheapest of a stored, fixed-code or
// dynamic-code block.
func (w *Writer) writeBlock(tokens []token, start, end int, final bool) {
	b := &w.blk
	b.litFreq[endOfBlock] = 1
	b.huff.build(b.litFreq[:], b.litLens[:], maxCodeBits)
	b.huff.build(b.distFreq[:], b.distLens[:], maxCodeBits)
	dynamic := 3 + b.dynamicHeader()
	fixed := 3
	extra := 0
	for s, f := range b.litFreq {
		dynamic += int(f) * int(b.litLens[s])
		fixed += int(f) * int(fixedLit.lens[s])
	}
	for s, f := range b.litFreq[257:] {
		extra += int(f) * int(lenExtra[s])
	}
	for s, f := range b.distFreq {
		dynamic += int(f) * int(b.distLens[s])
		fixed += int(f) * 5
		extra += int(f) * int(distExtra[s])
	}
	dynamic += extra
	fixed += extra
	stored := storedBits(end-start, w.bw.nbits)

	var fin uint64
	if final {
		fin = 1
	}
	switch {
	case stored <= fixed && stored <= dynamic:
		w.writeStored(start, end, final)
	case fixed <= dynamic:
		w.room()
		w.bw.put(fin|1<<1, 3)
		b.prepare(&fixedLit, &fixedDist)
		w.writeTokens(tokens, &fixedLit)
	default:
		w.room()
		w.bw.put(fin|2<<1, 3)
		w.writeHeader()
		b.lit.build(b.litLens[:])
		b.dist.build(b.distLens[:])
		b.prepare(&b.lit, &b.dist)
		w.writeTokens(tokens, &b.lit)
	}
}

// writeHeader writes the dynamic block header after the block type.
func (w *Writer) writeHeader() {
	b := &w.blk
	bw := &w.bw
	bw.put(uint64(b.nlit-257), 5)
	bw.put(uint64(b.ndist-1), 5)
	bw.put(uint64(b.nclen-4), 4)
	for _, s := range codeLenOrder[:b.nclen] {
		bw.put(uint64(b.clLens[s]), 3)
	}
	for _, r := range b.rle[:b.nrle] {
		s := r & 0xff
		c := b.cl.code[s]
		bw.put(uint64(c&0xffff), uint(c>>16))
		switch s {
		case 16:
			bw.put(uint64(r>>8), 2)
		case 17:
			bw.put(uint64(r>>8), 3)
		case 18:
			bw.put(uint64(r>>8), 7)
		}
	}
}

// writeTokens writes tokens with the literal code lit and the tables from
// prepare, then the end-of-block code.
func (w *Writer) writeTokens(tokens []token, lit *codeTable) {
	b := &w.blk
	ll, dc := &b.litLen, &b.distCode
	for len(tokens) > 0 {
		n := min(len(tokens), batch)
		w.room()
		bw := &w.bw
		out := bw.out[:outSize+outSlack]
		pos, acc, nb := bw.n, bw.bits, bw.nbits
		for _, t := range tokens[:n] {
			// Shift counts stay below 64 (nb is at most 7 plus 48 bits of
			// one token); the masks tell the compiler so.
			if t&matchFlag == 0 {
				c := ll[uint8(t)]
				acc |= uint64(c&0xffffff) << (nb & 63)
				nb += uint(c >> 24)
			} else {
				c := ll[256+int(uint8(t>>15))]
				s := t >> 23 & 31
				d := dc[s]
				cl := c >> 24
				v := uint64(c&0xffffff) | uint64(d&0xffff)<<(cl&63) | uint64(t&0x7fff-uint32(distBase[s]))<<((cl+d>>16&0xff)&63)
				acc |= v << (nb & 63)
				nb += uint(cl + d>>16&0xff + d>>24)
			}
			binary.LittleEndian.PutUint64(out[pos:], acc)
			k := nb >> 3
			pos += int(k)
			acc >>= k * 8 & 63
			nb &= 7
		}
		bw.n, bw.bits, bw.nbits = pos, acc, nb
		tokens = tokens[n:]
	}
	c := lit.code[endOfBlock]
	w.bw.put(uint64(c&0xffff), uint(c>>16))
}

// writeStored writes win[start:end] as stored blocks.
func (w *Writer) writeStored(start, end int, final bool) {
	bw := &w.bw
	for {
		n := min(end-start, 0xffff)
		last := start+n == end
		var fin uint64
		if final && last {
			fin = 1
		}
		w.room()
		bw.put(fin, 3)
		bw.align()
		binary.LittleEndian.PutUint32(bw.out[bw.n:], uint32(n)|uint32(^uint16(n))<<16)
		bw.n += 4
		for p := w.win[start : start+n]; len(p) > 0; {
			c := copy(bw.out[bw.n:outSize], p)
			bw.n += c
			p = p[c:]
			if len(p) > 0 {
				w.flush()
			}
		}
		start += n
		if last {
			return
		}
	}
}
