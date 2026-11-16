//go:build darwin || linux

package png

import (
	"math/rand"
	"syscall"
	"testing"
)

// TestFilterGuardPages places rows right before and after inaccessible
// pages: the kernels must not read or write outside the rows.
func TestFilterGuardPages(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, bpp := range filterBpps {
		for n := 1; n <= 200; n++ {
			cur, prev := guardedTail(t, n), guardedHead(t, n)
			dst := guardedTail(t, n)
			for i := range cur {
				cur[i], prev[i] = byte(rng.Intn(256)), byte(rng.Intn(256))
			}
			checkFilters(t, cur, prev, bpp)
			for f := uint8(0); f < 5; f++ {
				filterRow(dst, cur, prev, bpp, f)
			}
		}
	}
}

// TestPixelGuardPages runs the pixel kernels on rows and outputs that end
// at inaccessible pages.
func TestPixelGuardPages(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	for n := 0; n <= 100; n++ {
		p := guardedTail(t, 4*n)
		copy(p, rgbaRow(rng, n, -1, -1))
		o, g := rgbaFlags(p)
		if !o || !g {
			t.Fatalf("%d pixels: rgbaFlags = %v %v", n, o, g)
		}
		for _, per := range []int{1, 2, 3} {
			d := guardedTail(t, per*n)
			switch per {
			case 1:
				toG(d, p)
			case 2:
				toGA(d, p)
			case 3:
				toRGB(d, p)
			}
		}
	}
}

// guardedTail returns size bytes that end at a page with no access.
func guardedTail(t *testing.T, size int) []byte {
	m := guardedMapping(t, size)
	end := len(m) - syscall.Getpagesize()
	return m[end-size : end : end]
}

// guardedHead returns size bytes that start right after a page with no
// access.
func guardedHead(t *testing.T, size int) []byte {
	m := guardedMapping(t, size)
	page := syscall.Getpagesize()
	return m[page : page+size : page+size]
}

func guardedMapping(t *testing.T, size int) []byte {
	t.Helper()
	page := syscall.Getpagesize()
	dataPages := max(1, (size+page-1)/page)
	m, err := syscall.Mmap(-1, 0, (dataPages+2)*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatalf("mmap guard pages: %v", err)
	}
	t.Cleanup(func() {
		if err := syscall.Munmap(m); err != nil {
			t.Errorf("munmap guard pages: %v", err)
		}
	})
	if err := syscall.Mprotect(m[:page], syscall.PROT_NONE); err != nil {
		t.Fatalf("protect leading page: %v", err)
	}
	end := len(m) - page
	if err := syscall.Mprotect(m[end:], syscall.PROT_NONE); err != nil {
		t.Fatalf("protect trailing page: %v", err)
	}
	return m
}
