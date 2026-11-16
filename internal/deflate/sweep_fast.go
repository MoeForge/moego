//go:build amd64 || arm64

package deflate

import "unsafe"

// sweep marks the head entries more than windowSize back from abs, which
// is at or past every inserted position, as stale: windowSize+1 back. It
// works on four entries per word, as their ages are unpredictable and a
// branch per entry would mispredict.
func (m *chainMatcher) sweep(abs int) {
	const (
		one = 0x0001_0001_0001_0001
		top = 0x8000 * one
		low = 0x7fff * one
	)
	now := uint64(uint16(abs)) * one
	stale := uint64(uint16(abs-windowSize-1)) * one
	words := (*[len(m.head) / 4]uint64)(unsafe.Pointer(&m.head))
	for i, x := range words {
		// The age of each entry, now-x per 16-bit lane: the top bits are
		// set aside so no borrow crosses lanes, then fixed up.
		age := ((now | top) - (x &^ top)) ^ ((now ^ ^x) & top)
		// Stale when the age has its top bit and some low bit set: over
		// windowSize.
		old := age & ((age & low) + low) & top
		mask := (old >> 15) * 0xffff
		words[i] = x&^mask | stale&mask
	}
}
