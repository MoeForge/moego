package deflate

import (
	"math/rand"
	"testing"
)

// TestSweep checks sweep against the rule entry by entry, for entries of
// every age around the window and positions around wraps of 16 bits.
func TestSweep(t *testing.T) {
	rng := rand.New(rand.NewSource(14))
	m := new(chainMatcher)
	var before [len(m.head)]uint16
	for _, abs := range []int{0, 1, windowSize, 1<<16 - 1, 1 << 16, 1<<16 + 5, 1<<20 + 12345, 1<<30 + 7} {
		now := uint16(abs)
		for i := range m.head {
			var age int
			switch rng.Intn(4) {
			case 0:
				age = windowSize - 2 + rng.Intn(5)
			case 1:
				age = rng.Intn(4)
			default:
				age = rng.Intn(1 << 16)
			}
			m.head[i] = now - uint16(age)
		}
		before = m.head
		m.sweep(abs)
		for i, v := range before {
			want := v
			if now-v > windowSize {
				want = uint16(abs - windowSize - 1)
			}
			if m.head[i] != want {
				t.Fatalf("position %d, age %d: %d, want %d", abs, now-v, m.head[i], want)
			}
		}
	}
}
