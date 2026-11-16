//go:build (amd64 || arm64) && !purego

package deflate

import (
	stdadler "hash/adler32"
	"math/rand"
	"testing"
)

// TestAdler32Vec checks the vector kernel itself, which adler32 skips for
// short inputs, at every length up to 1024 and every alignment.
func TestAdler32Vec(t *testing.T) {
	requireVec(t)
	rng := rand.New(rand.NewSource(10))
	buf := make([]byte, 4*adlerVecChunk+200)
	rng.Read(buf)
	ones := adlerOnes(len(buf))
	for _, src := range [][]byte{buf, ones} {
		for off := 0; off < 32; off++ {
			for n := 0; n <= 1024; n++ {
				p := src[off : off+n]
				if got, want := adler32Vec(1, p), stdadler.Checksum(p); got != want {
					t.Fatalf("offset %d length %d: %08x, want %08x", off, n, got, want)
				}
			}
		}
		for _, n := range []int{adlerVecChunk - 32, adlerVecChunk, adlerVecChunk + 1, adlerVecChunk + 32, 2*adlerVecChunk - 1, 4 * adlerVecChunk} {
			p := src[5 : 5+n]
			if got, want := adler32Vec(1, p), stdadler.Checksum(p); got != want {
				t.Fatalf("length %d: %08x, want %08x", n, got, want)
			}
			for _, init := range []uint32{0, 0xfff0fff0, uint32(rng.Int31n(adlerMod))<<16 | uint32(rng.Int31n(adlerMod))} {
				if got, want := adler32Vec(init, p), adler32Generic(init, p); got != want {
					t.Fatalf("from %08x, length %d: %08x, want %08x", init, n, got, want)
				}
			}
		}
	}
}
