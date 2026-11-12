//go:build amd64 && !purego

package regexp

import (
	"bytes"
	"os"
	"slices"
	"testing"
)

func requireAVX2(t *testing.T) {
	t.Helper()
	if !haveAVX2 {
		if os.Getenv("MOEGO_REQUIRE_AVX2") == "1" {
			t.Fatal("MOEGO_REQUIRE_AVX2=1 but CPU/OS AVX2 support is unavailable")
		}
		t.Skip("AVX2 unavailable on this host")
	}
}

func TestAMD64Kernels(t *testing.T) {
	requireAVX2(t)
	if !hasPairKernel || !hasSpanKernel {
		t.Fatal("AVX2 available but vector dispatch is disabled")
	}
	var in [256]bool
	in['B'] = true
	lo, hi := nibbleTables(&in)
	for pos := -1; pos < 128; pos++ {
		buf := bytes.Repeat([]byte{'a'}, 128)
		if pos >= 0 {
			buf[pos] = 'B'
		}
		s := string(buf)
		got := pairAVX2(&buf[0], &buf[1], len(buf)-1, 'a', 'b', 0, 0x20)
		if want := indexPairScalar(s, 0, 1, 'a', 'b', 0, 0x20); got != want {
			t.Fatalf("pair position %d: got %d, want %d", pos, got, want)
		}
		for _, member := range []bool{true, false} {
			var inv uint32
			if !member {
				inv = ^uint32(0)
			}
			got := spanAVX2(&buf[0], len(buf), &lo, &hi, inv)
			if want := spanScalar(s, &in, member); got != want {
				t.Fatalf("span position %d member %v: got %d, want %d", pos, member, got, want)
			}
		}
		var masks, want [2]uint64
		classMasksAVX2(&buf[0], len(buf), &lo, &hi, &masks[0])
		classMasksScalar(s, &in, want[:])
		if !slices.Equal(masks[:], want[:]) {
			t.Fatalf("class masks position %d: got %x, want %x", pos, masks, want)
		}
	}
}
