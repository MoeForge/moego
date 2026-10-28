//go:build darwin || linux

package base64

import (
	"bytes"
	"fmt"
	"syscall"
	"testing"
)

func TestGuardPages(t *testing.T) {
	lengths := []int{0, 1, 2, 3, 15, 16, 17, 31, 32, 33, 44, 45, 46, 47, 48, 49, 50, 51, 60, 61, 62, 63, 64, 65, 66, 67, 95, 96, 97, 127, 128, 129, 191, 192, 193, 255, 256, 257, 4095, 4096, 4097}
	for _, pair := range encodingPairs()[:4] {
		for _, size := range lengths {
			t.Run(fmt.Sprintf("%s/%d", pair.name, size), func(t *testing.T) {
				src := guardedTail(t, size)
				for i := range src {
					src[i] = byte(i*71 + size)
				}
				want := pair.ref.EncodeToString(src)
				encoded := guardedTail(t, len(want))
				pair.fast.Encode(encoded, src)
				if string(encoded) != want || pair.fast.EncodeToString(src) != want {
					t.Fatal("Encode differs next to inaccessible page")
				}
				for _, capacity := range []int{size, pair.fast.DecodedLen(len(encoded))} {
					dst := guardedTail(t, capacity)
					n, err := pair.fast.Decode(dst, encoded)
					if err != nil || n != len(src) || !bytes.Equal(dst[:n], src) {
						t.Fatalf("Decode differs next to inaccessible page: count %d, error %v", n, err)
					}
				}
				if len(encoded) != 0 {
					encoded[len(encoded)-1] = '!'
					wantDecoded, wantErr := pair.ref.DecodeString(string(encoded))
					dst := guardedTail(t, pair.fast.DecodedLen(len(encoded)))
					n, err := pair.fast.Decode(dst, encoded)
					if !sameError(err, wantErr) || !bytes.Equal(dst[:n], wantDecoded) {
						t.Fatalf("invalid input differs next to inaccessible page: %v/%v", err, wantErr)
					}
				}
			})
		}
	}
}

// guardedTail ends at a page with no read or write permission. A vector load or
// store extending past the slice faults even when an ordinary Go allocation
// would have spare addressable bytes.
func guardedTail(t *testing.T, size int) []byte {
	t.Helper()
	page := syscall.Getpagesize()
	dataPages := max(1, (size+page-1)/page)
	mapping, err := syscall.Mmap(-1, 0, (dataPages+2)*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatalf("mmap guard pages: %v", err)
	}
	t.Cleanup(func() {
		if err := syscall.Munmap(mapping); err != nil {
			t.Errorf("munmap guard pages: %v", err)
		}
	})
	if err := syscall.Mprotect(mapping[:page], syscall.PROT_NONE); err != nil {
		t.Fatalf("protect leading page: %v", err)
	}
	end := len(mapping) - page
	if err := syscall.Mprotect(mapping[end:], syscall.PROT_NONE); err != nil {
		t.Fatalf("protect trailing page: %v", err)
	}
	return mapping[end-size : end : end]
}
