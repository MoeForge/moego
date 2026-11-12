//go:build arm64 && !purego && (linux || darwin)

package regexp

import (
	"strings"
	"testing"
	"unsafe"
)

func TestARM64KernelGuardPages(t *testing.T) {
	var in [256]bool
	in['x'] = true
	lo, hi := nibbleTables(&in)
	for _, atEnd := range []bool{false, true} {
		for _, n := range []int{0, 1, 15, 16, 17, 31, 32, 33, 47, 48, 49, 63, 64, 65, 127, 128, 129, 255, 256, 257, 4095, 4096, 4097} {
			s := guarded(t, strings.Repeat("x", n), atEnd)
			for _, offsets := range [][2]int{{0, 0}, {0, 1}, {1, 0}, {1, 15}, {31, 3}} {
				for _, r := range []byte{'x', 'y'} {
					got := indexPair(s, offsets[0], offsets[1], r, r, 0, 0)
					want := indexPairScalar(s, offsets[0], offsets[1], r, r, 0, 0)
					if got != want {
						t.Fatalf("n=%d atEnd=%v offsets=%v: pair=%d want=%d", n, atEnd, offsets, got, want)
					}
				}
			}
			for _, member := range []bool{false, true} {
				if got, want := span(s, &lo, &hi, &in, member), spanScalar(s, &in, member); got != want {
					t.Fatalf("n=%d atEnd=%v member=%v: span=%d want=%d", n, atEnd, member, got, want)
				}
			}
			if n%64 == 0 {
				// Guard the output as well: one extra store crosses the page.
				outText := guarded(t, strings.Repeat("\x00", n/8), atEnd)
				out := unsafe.Slice((*uint64)(unsafe.Pointer(unsafe.StringData(outText))), len(outText)/8)
				classMasks(s, &lo, &hi, &in, out)
				for _, mask := range out {
					if mask != ^uint64(0) {
						t.Fatalf("n=%d atEnd=%v: mask=%x", n, atEnd, mask)
					}
				}
			}
		}
	}
}
