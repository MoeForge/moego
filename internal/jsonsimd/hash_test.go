package jsonsimd

import (
	"math/rand"
	"testing"
)

func TestHashKey(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	buf := make([]byte, 4200)
	r.Read(buf)
	keys := 8
	if raceEnabled {
		keys = 1
	}
	for range keys {
		h := NewHashKey()
		for _, n := range []int{0, 1, 63, 64, 65, 127, 128, 129, 200, 255, 256, 1023, 1024, 1025, 1088, 2100, 4200} {
			b := buf[:n]
			s := h.Sum(b)
			// Equal bytes at another place.
			c := append(make([]byte, 0, n+7)[:3], b...)[3:]
			if h.Sum(c) != s {
				t.Fatalf("n %d: the sum depends on the place of the bytes", n)
			}
			if n > 0 && h.Sum(b[:n-1]) == s {
				t.Fatalf("n %d: a shorter prefix has the same sum", n)
			}
			// Each bit changed.
			for i := range n {
				for bit := 0; bit < 8; bit += 3 {
					b[i] ^= 1 << bit
					if h.Sum(b) == s {
						t.Fatalf("n %d: bit %d of byte %d changed, same sum", n, bit, i)
					}
					b[i] ^= 1 << bit
				}
			}
			// Two runs of 64 or 128 bytes swapped.
			for _, k := range []int{64, 128} {
				if n >= 3*k {
					x := append([]byte(nil), b...)
					copy(x[0:k], b[k:2*k])
					copy(x[k:2*k], b[0:k])
					if h.Sum(x) == s {
						t.Fatalf("n %d: two runs of %d swapped, same sum", n, k)
					}
				}
			}
		}
		if NewHashKey().Sum(buf) == h.Sum(buf) {
			t.Fatal("two keys give the same sum")
		}
	}
}
