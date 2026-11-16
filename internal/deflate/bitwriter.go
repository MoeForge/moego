package deflate

import "encoding/binary"

// A bitWriter packs codes LSB first into out, eight bytes per store. out
// keeps at least outSlack bytes past n, so a store never runs off its end.
type bitWriter struct {
	out   []byte
	n     int
	bits  uint64
	nbits uint
}

const outSlack = 16

// put appends the low nb bits of v, which has no higher bits set. nb is
// at most 32.
func (b *bitWriter) put(v uint64, nb uint) {
	b.bits |= v << b.nbits
	b.nbits += nb
	if b.nbits >= 32 {
		b.spill()
	}
}

// spill stores the whole bytes of the accumulator.
func (b *bitWriter) spill() {
	binary.LittleEndian.PutUint64(b.out[b.n:], b.bits)
	k := b.nbits >> 3
	b.n += int(k)
	b.bits >>= k * 8
	b.nbits &= 7
}

// align pads with zero bits to a byte boundary and stores every pending
// byte.
func (b *bitWriter) align() {
	b.nbits = (b.nbits + 7) &^ 7
	b.spill()
}

// bytes appends p after align.
func (b *bitWriter) bytes(p []byte) {
	b.n += copy(b.out[b.n:], p)
}
