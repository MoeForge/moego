package json

import (
	"testing"
	"unsafe"
)

func TestSizeClassTables(t *testing.T) {
	for n := 0; n <= 2*slabChunkMax; n++ {
		below, above := 8, n
		for _, s := range sizeClasses {
			if int(s) <= n {
				below = int(s)
			}
		}
		for j := len(sizeClasses) - 1; j >= 0; j-- {
			if int(sizeClasses[j]) >= n {
				above = int(sizeClasses[j])
			}
		}
		if n >= 8 && classBelow(n) != below {
			t.Fatalf("classBelow(%d) = %d, want %d", n, classBelow(n), below)
		}
		if n <= slabChunkMax && classAbove(n) != above {
			t.Fatalf("classAbove(%d) = %d, want %d", n, classAbove(n), above)
		}
	}
}

func TestIsZeroMem(t *testing.T) {
	var buf [300]byte
	for off := 0; off < 9; off++ {
		for n := 0; off+n <= 280; n++ {
			if !isZeroMem(unsafe.Pointer(&buf[off]), uintptr(n)) {
				t.Fatalf("isZeroMem(%d, %d) = false on zeros", off, n)
			}
			for k := 0; k < n; k++ {
				buf[off+k] = 1 << (k % 8)
				if isZeroMem(unsafe.Pointer(&buf[off]), uintptr(n)) {
					t.Fatalf("isZeroMem(%d, %d) = true with byte %d set", off, n, k)
				}
				buf[off+k] = 0
			}
			// Bytes around the range are not read.
			buf[off+n] = 1
			if off > 0 {
				buf[off-1] = 1
			}
			if !isZeroMem(unsafe.Pointer(&buf[off]), uintptr(n)) {
				t.Fatalf("isZeroMem(%d, %d) reads past its range", off, n)
			}
			buf[off+n] = 0
			if off > 0 {
				buf[off-1] = 0
			}
		}
	}
}
