//go:build arm64 && !purego

package regexp

import (
	"bytes"
	"math/rand/v2"
	"testing"
	"unsafe"
)

func TestARM64PairPositions(t *testing.T) {
	lengths := make([]int, 130)
	for i := range lengths {
		lengths[i] = i
	}
	lengths = append(lengths, 255, 256, 257, 511, 512, 513, 1023)
	for align := 0; align < 32; align++ {
		for _, n := range lengths {
			for _, mask := range []byte{0, 0x20} {
				a := bytes.Repeat([]byte{'-'}, align+n)[align:]
				b := bytes.Repeat([]byte{'_'}, 31-align+n)[31-align:]
				for pos := -1; pos < n; pos++ {
					if pos >= 0 {
						a[pos], b[pos] = 'x'&^mask, 'y'&^mask
					}
					if n >= 16 {
						got := pairNEON(unsafe.SliceData(a), unsafe.SliceData(b), n, 'x', 'y', mask, mask)
						if got != pos {
							t.Fatalf("n=%d align=%d mask=%x pos=%d: pair=%d", n, align, mask, pos, got)
						}
					}
					if pos >= 0 {
						// A rare-byte hit without the second byte must not match.
						b[pos] = '_'
						if n >= 16 && pairNEON(unsafe.SliceData(a), unsafe.SliceData(b), n, 'x', 'y', mask, mask) != -1 {
							t.Fatalf("n=%d align=%d pos=%d: unpaired byte matched", n, align, pos)
						}
						a[pos] = '-'
					}
				}
			}
		}
	}
}

func TestARM64PairMasks(t *testing.T) {
	rng := rand.New(rand.NewPCG(31, 41))
	for iter := 0; iter < randomIterations(10000); iter++ {
		buf := make([]byte, 16+rng.IntN(1024))
		for i := range buf {
			buf[i] = byte(rng.Uint32())
		}
		s := string(buf)
		o1, o2 := rng.IntN(16), rng.IntN(16)
		r1, r2 := byte(rng.Uint32()), byte(rng.Uint32())
		m1, m2 := byte(rng.Uint32()), byte(rng.Uint32())
		if got, want := indexPair(s, o1, o2, r1, r2, m1, m2), indexPairScalar(s, o1, o2, r1, r2, m1, m2); got != want {
			t.Fatalf("len=%d offsets=%d,%d needles=%x,%x masks=%x,%x: got=%d want=%d", len(s), o1, o2, r1, r2, m1, m2, got, want)
		}
	}
}

func TestARM64SpanEveryByte(t *testing.T) {
	var in [256]bool
	for _, c := range []byte("0123456789AZaz \t\n\x00\x7f") {
		in[c] = true
	}
	lo, hi := nibbleTables(&in)
	for _, member := range []bool{true, false} {
		fill := byte('-')
		if member {
			fill = 'A'
		}
		buf := bytes.Repeat([]byte{fill}, 257)
		for pos := range buf {
			for value := 0; value < 256; value++ {
				buf[pos] = byte(value)
				want := len(buf)
				if in[value] != member {
					want = pos
				}
				inv := byte(0)
				if !member {
					inv = 255
				}
				if got := spanNEON(unsafe.SliceData(buf), len(buf), &lo, &hi, inv); got != want {
					t.Fatalf("member=%v pos=%d value=%x: got=%d want=%d", member, pos, value, got, want)
				}
			}
			buf[pos] = fill
		}
	}
}

func TestARM64SpanLengthsAndAlignment(t *testing.T) {
	var in [256]bool
	in['x'] = true
	lo, hi := nibbleTables(&in)
	for align := 0; align < 32; align++ {
		for n := 0; n <= 257; n++ {
			for _, member := range []bool{false, true} {
				fill, stop := byte('x'), byte(0x80)
				if !member {
					fill, stop = stop, fill
				}
				buf := bytes.Repeat([]byte{fill}, align+n)[align:]
				for _, pos := range []int{-1, 0, 7, 8, 15, 16, 31, 32, 63, 64, n - 1} {
					if pos >= n {
						continue
					}
					want := n
					if pos >= 0 {
						buf[pos], want = stop, pos
					}
					s := unsafe.String(unsafe.SliceData(buf), len(buf))
					if got := span(s, &lo, &hi, &in, member); got != want {
						t.Fatalf("align=%d n=%d member=%v pos=%d: got=%d want=%d", align, n, member, pos, got, want)
					}
					if pos >= 0 {
						buf[pos] = fill
					}
				}
			}
		}
	}
}

func TestARM64ClassMasksEveryBit(t *testing.T) {
	var in [256]bool
	for b := 0; b < 128; b++ {
		in[b] = b%3 == 0
	}
	lo, hi := nibbleTables(&in)
	for align := 0; align < 16; align++ {
		buf := bytes.Repeat([]byte{1}, align+128)[align:]
		for pos := range buf {
			for value := 0; value < 256; value++ {
				buf[pos] = byte(value)
				out := [4]uint64{0x1234, ^uint64(0), ^uint64(0), 0x5678}
				classMasksNEON(unsafe.SliceData(buf), len(buf), &lo, &hi, &out[1])
				want := [4]uint64{0x1234, 0, 0, 0x5678}
				if in[value] {
					want[1+pos/64] = 1 << (pos % 64)
				}
				if out != want {
					t.Fatalf("align=%d pos=%d value=%x: masks=%x want=%x", align, pos, value, out, want)
				}
			}
			buf[pos] = 1
		}
	}
}
