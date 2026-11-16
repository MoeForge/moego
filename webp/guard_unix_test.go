//go:build darwin || linux

package webp

import (
	"math/rand"
	"syscall"
	"testing"
	"unsafe"
)

// TestKernelGuardPages runs the kernels on images and outputs that end at,
// or start right after, inaccessible pages: they must not read or write
// outside their slices.
func TestKernelGuardPages(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	for w := 2; w <= 70; w++ {
		img := guardedTail(t, 2*w)
		copy(img, kernelPixels(rng, 2*w))
		checkResiduals(t, img, w, guardedTail(t, w-1))
		img = guardedHead(t, 2*w)
		copy(img, kernelPixels(rng, 2*w))
		checkResiduals(t, img, w, guardedHead(t, w-1))
	}
	var m model
	for c := range m.cost {
		for v := range m.cost[c] {
			m.cost[c][v] = uint32(rng.Intn(1 << 16))
		}
	}
	for n := 1; n <= 70; n++ {
		for _, set := range []int{scoreAll, scoreRGB, scoreGreen} {
			res := guardedTail(t, n)
			copy(res, kernelPixels(rng, n))
			m.pixelCosts(guardedTail(t, n), res, set)
			m.pixelCosts(guardedHead(t, n), guardedHead(t, n), set)
		}
		x := guardedTail(t, (n+3)/4)
		xb := unsafe.Slice((*int8)(unsafe.Pointer(&x[0])), 4*len(x))[4*len(x)-n:]
		y := guardedHead(t, (n+3)/4)
		yb := unsafe.Slice((*int8)(unsafe.Pointer(&y[0])), n)
		ctCostSum(&m.cost[0], xb, yb, -77)
		c := ctElem{-77, 101, -128}
		colorTransformRow(guardedTail(t, n), guardedHead(t, n), c)
		colorTransformRow(guardedHead(t, n), guardedTail(t, n), c)
		undoColorTransformRow(guardedTail(t, n), guardedHead(t, n), c)
		undoColorTransformRow(guardedHead(t, n), guardedTail(t, n), c)
		if n%16 == 0 {
			var sums [5]int32
			g, r, b := make([]int8, n), make([]int8, n), make([]int8, n)
			ctTile(guardedTail(t, n), 0, n, n, 1, g, r, b, &sums)
			ctTile(guardedHead(t, n), 0, n, n, 1, g, r, b, &sums)
			gt, gh := guardedTail(t, (n+3)/4), guardedHead(t, (n+3)/4)
			g8 := unsafe.Slice((*int8)(unsafe.Pointer(&gt[0])), 4*len(gt))[4*len(gt)-n:]
			h8 := unsafe.Slice((*int8)(unsafe.Pointer(&gh[0])), n)
			ctTile(kernelPixels(rng, n), 0, n, n, 1, g8, g8, g8, &sums)
			ctTile(kernelPixels(rng, n), 0, n, n, 1, h8, h8, h8, &sums)
			// Several rows of a wider image: the last row ends at the
			// guard page, the first starts right after one.
			for _, rows := range []int{2, 3, 7} {
				stride := n + 16
				size := (rows-1)*stride + n
				out := make([]int8, n*rows)
				img := guardedTail(t, size)
				copy(img, kernelPixels(rng, size))
				ctTile(img, 0, stride, n, rows, out, out, out, &sums)
				img = guardedHead(t, size)
				copy(img, kernelPixels(rng, size))
				ctTile(img, 0, stride, n, rows, out, out, out, &sums)
				gt, gh := guardedTail(t, (n*rows+3)/4), guardedHead(t, (n*rows+3)/4)
				g8 := unsafe.Slice((*int8)(unsafe.Pointer(&gt[0])), 4*len(gt))[4*len(gt)-n*rows:]
				h8 := unsafe.Slice((*int8)(unsafe.Pointer(&gh[0])), n*rows)
				ctTile(img, 0, stride, n, rows, g8, g8, g8, &sums)
				ctTile(img, 0, stride, n, rows, h8, h8, h8, &sums)
			}
		}
		for _, mode := range []int{0, 2, 3, 4, 8, 9} {
			top := guardedTail(t, n+2)
			copy(top, kernelPixels(rng, n+2))
			addTopRow(mode, guardedTail(t, n), top)
			addTopRow(mode, guardedHead(t, n), guardedHead(t, n+2))
		}
		src := guardedTail(t, n)
		copy(src, kernelPixels(rng, n))
		subtractGreenRow(guardedTail(t, n), src)
		subtractGreenRow(guardedHead(t, n), guardedHead(t, n))
		addGreenRow(guardedTail(t, n), src)
		addGreenRow(guardedHead(t, n), guardedHead(t, n))
		src = guardedTail(t, n)
		b := unsafe.Slice((*byte)(unsafe.Pointer(&src[0])), 4*n)
		fromNRGBA(guardedTail(t, n), b)
		src = guardedHead(t, n)
		b = unsafe.Slice((*byte)(unsafe.Pointer(&src[0])), 4*n)
		fromNRGBA(guardedHead(t, n), b)
	}
}

// guardedTail returns n pixels that end at a page with no access.
func guardedTail(t *testing.T, n int) []uint32 {
	m := guardedMapping(t, 4*n)
	end := len(m) - syscall.Getpagesize()
	b := m[end-4*n : end : end]
	return unsafe.Slice((*uint32)(unsafe.Pointer(&b[0])), n)
}

// guardedHead returns n pixels that start right after a page with no
// access.
func guardedHead(t *testing.T, n int) []uint32 {
	m := guardedMapping(t, 4*n)
	page := syscall.Getpagesize()
	b := m[page : page+4*n : page+4*n]
	return unsafe.Slice((*uint32)(unsafe.Pointer(&b[0])), n)
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
