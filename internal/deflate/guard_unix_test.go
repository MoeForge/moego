//go:build darwin || linux

package deflate

import (
	"bytes"
	"fmt"
	stdadler "hash/adler32"
	"math/rand"
	"syscall"
	"testing"
)

func TestGuardPages(t *testing.T) {
	lengths := []int{0, 1, 2, 3, 4, 7, 8, 15, 16, 31, 32, 33, 63, 64, 65, 95, 96, 97, 127, 128, 129, 255, 256, 257, 4095, 4096, 4097, 1<<15 - 1, 1 << 15, 1<<15 + 33}
	for _, size := range lengths {
		src := guardedTail(t, size)
		for i := range src {
			src[i] = byte(i*71 + size)
		}
		if got, want := adler32(1, src), stdadler.Checksum(bytes.Clone(src)); got != want {
			t.Fatalf("Adler-32 of %d bytes next to inaccessible page: %08x, want %08x", size, got, want)
		}
	}
	rows := filteredRows(rand.New(rand.NewSource(11)), 3, 300, 200)
	for _, size := range []int{0, 1, 3, 4, 5, 8, 9, 31, 64, 65, 1000, 4097, len(rows)} {
		for level := 0; level <= 9; level++ {
			t.Run(fmt.Sprintf("%d/%d", level, size), func(t *testing.T) {
				src := guardedTail(t, size)
				copy(src, rows)
				var buf bytes.Buffer
				w := NewWriter(&buf, level)
				w.SetHint(3, 901)
				// Whole, then a stream written in pieces ending at the page.
				w.Write(src)
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				checkDecode(t, buf.Bytes(), src)
				buf.Reset()
				w.Reset(&buf, level)
				for p := src; len(p) > 0; {
					n := min(len(p), 901)
					w.Write(p[:n])
					p = p[n:]
				}
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				checkDecode(t, buf.Bytes(), src)
			})
		}
	}
}

// guardedTail ends at a page with no read or write permission. A vector load
// extending past the slice faults even when an ordinary Go allocation would
// have spare addressable bytes.
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
