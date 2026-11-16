package deflate

import (
	stdadler "hash/adler32"
	"math/rand"
	"testing"
)

// adlerOnes returns n bytes of 0xff, which push every sum to its largest.
func adlerOnes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = 0xff
	}
	return b
}

func TestAdler32(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	buf := make([]byte, 1<<20+77)
	rng.Read(buf)
	for n := 0; n <= 1024; n++ {
		p := buf[:n]
		want := stdadler.Checksum(p)
		if got := adler32Generic(1, p); got != want {
			t.Fatalf("generic length %d: %08x, want %08x", n, got, want)
		}
		if got := adler32(1, p); got != want {
			t.Fatalf("length %d: %08x, want %08x", n, got, want)
		}
	}
	ones := adlerOnes(1 << 20)
	for _, p := range [][]byte{buf, ones, buf[3:], ones[:adlerBlock*3+1], ones[:1<<15+31], buf[:1<<16+32]} {
		want := stdadler.Checksum(p)
		if got := adler32(1, p); got != want {
			t.Fatalf("length %d: %08x, want %08x", len(p), got, want)
		}
		// In pieces, continuing from the running value.
		a := uint32(1)
		for q := p; len(q) > 0; {
			n := min(len(q), 1+rng.Intn(10000))
			a = adler32(a, q[:n])
			q = q[n:]
		}
		if a != want {
			t.Fatalf("pieces of length %d: %08x, want %08x", len(p), a, want)
		}
	}
	// Any running value, including sums at their largest.
	for _, init := range []uint32{0, 1, 0xfff0fff0, 0x0000fff0, 0xfff00000, 0x12345678 % adlerMod} {
		for _, p := range [][]byte{buf[:100], ones[:4096], ones[:1<<15+64], buf[7 : 7+3<<15+5]} {
			if got, want := adler32(init, p), adler32Generic(init, p); got != want {
				t.Fatalf("from %08x, length %d: %08x, want %08x", init, len(p), got, want)
			}
		}
	}
}

func BenchmarkAdler32(b *testing.B) {
	p := make([]byte, 1<<16)
	rand.New(rand.NewSource(13)).Read(p)
	b.Run("dispatch", func(b *testing.B) {
		b.SetBytes(int64(len(p)))
		for i := 0; i < b.N; i++ {
			adler32(1, p)
		}
	})
	b.Run("generic", func(b *testing.B) {
		b.SetBytes(int64(len(p)))
		for i := 0; i < b.N; i++ {
			adler32Generic(1, p)
		}
	})
}
