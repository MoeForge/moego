package vp8l

import "encoding/binary"

// A bitWriter packs bits LSB first into out, eight bytes per store. It
// grows out to the planned size of each image, else by doubling, so once
// out has held a stream of some size, a stream no larger is written
// without allocating.
type bitWriter struct {
	out  []byte
	n    int // bytes stored
	acc  uint64
	nacc uint // bits pending in acc, below 32 between calls
}

func (b *bitWriter) reset() {
	b.n, b.acc, b.nacc = 0, 0, 0
}

// put appends the low nb bits of v, which has no higher bits set. nb is at
// most 32.
func (b *bitWriter) put(v uint32, nb uint) {
	b.acc |= uint64(v) << b.nacc
	b.nacc += nb
	if b.nacc >= 32 {
		b.spill()
	}
}

// put64 appends the low nb bits of v, which has no higher bits set. nb is
// at most 60.
func (b *bitWriter) put64(v uint64, nb uint) {
	if b.nacc+nb >= 64 {
		// Store what fits, then the rest.
		lo := 32 - b.nacc&31
		b.put(uint32(v)&(1<<lo-1), lo)
		v >>= lo
		nb -= lo
	}
	b.acc |= v << b.nacc
	b.nacc += nb
	for b.nacc >= 32 {
		b.spill()
	}
}

// spill stores the whole bytes of the accumulator.
func (b *bitWriter) spill() {
	if len(b.out)-b.n < 8 {
		b.grow()
	}
	binary.LittleEndian.PutUint64(b.out[b.n:], b.acc)
	k := b.nacc >> 3
	b.n += int(k)
	b.acc >>= k * 8
	b.nacc &= 7
}

func (b *bitWriter) grow() {
	b.resize(max(2*len(b.out), 4096))
}

// reserve makes room for about nb more bits: an image's planned size, so
// the stream grows once to about its final size.
func (b *bitWriter) reserve(nb int) {
	if need := b.n + nb/8 + 64; need > len(b.out) {
		b.resize(max(need, 4096))
	}
}

func (b *bitWriter) resize(n int) {
	out := make([]byte, n)
	copy(out, b.out[:b.n])
	b.out = out
}

// bits returns the number of bits written.
func (b *bitWriter) bits() int { return 8*b.n + int(b.nacc) }

// finish pads with zero bits to a byte boundary and returns the stream.
func (b *bitWriter) finish() []byte {
	b.nacc = (b.nacc + 7) &^ 7
	b.spill()
	return b.out[:b.n]
}
