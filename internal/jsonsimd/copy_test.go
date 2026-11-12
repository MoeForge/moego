package jsonsimd

import (
	"bytes"
	"math/rand"
	"testing"
	"unsafe"
)

// TestCopyLong checks CopyLong at lengths around its block and chunk sizes
// and at every alignment of dst, and that it writes nothing past the copy.
func TestCopyLong(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	src := make([]byte, 300<<10+200)
	r.Read(src)
	sizes := []int{0, 1, 31, 127, 128, 4095, 4096, 4097, 4096 + 127, 4096 + 128, 65 << 10,
		128<<10 - 1, 128 << 10, 128<<10 + 1, 128<<10 + 159, 256<<10 + 31, 300 << 10}
	for range 20 {
		sizes = append(sizes, 4096+r.Intn(200<<10))
	}
	buf := make([]byte, len(src)+64)
	for _, n := range sizes {
		for off := range 33 {
			so := r.Intn(64)
			if so+n > len(src) {
				so = len(src) - n
			}
			for i := range buf {
				buf[i] = 0xa5
			}
			s := string(src[so : so+n])
			CopyLong(buf[off:off+n:off+n], s)
			if string(buf[off:off+n]) != s {
				t.Fatalf("CopyLong of %d bytes at offset %d: copy differs", n, off)
			}
			if bytes.Count(buf[:off], []byte{0xa5}) != off || bytes.Count(buf[off+n:], []byte{0xa5}) != len(buf)-off-n {
				t.Fatalf("CopyLong of %d bytes at offset %d: wrote outside dst", n, off)
			}
		}
	}
}

func TestZeroAVX2(t *testing.T) {
	if !HaveAVX2 {
		t.Skip("no AVX2")
	}
	buf := make([]byte, 700)
	for n := 32; n <= 600; n++ {
		for off := 1; off < 40; off += 13 {
			p := unsafe.Pointer(&buf[off])
			// The bytes around the n are not read.
			buf[off-1], buf[off+n] = 1, 1
			if !ZeroAVX2(p, uintptr(n)) {
				t.Fatalf("n %d at %d: not zero", n, off)
			}
			for k := 0; k < n; k++ {
				buf[off+k] = 0x80 >> (k % 8)
				if ZeroAVX2(p, uintptr(n)) {
					t.Fatalf("n %d at %d: byte %d set, zero", n, off, k)
				}
				buf[off+k] = 0
			}
			buf[off-1], buf[off+n] = 0, 0
		}
	}
}
