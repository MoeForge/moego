//go:build amd64 && !purego && linux

package jsonsimd

import (
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// TestCopyShortPages places strings right before an unmapped page: the
// 32-byte reads of CopyShort must stay in mapped memory.
func TestCopyShortPages(t *testing.T) {
	const page = 4096
	mem, err := syscall.Mmap(-1, 0, 2*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		t.Skip(err)
	}
	defer syscall.Munmap(mem)
	if err := syscall.Mprotect(mem[page:], syscall.PROT_NONE); err != nil {
		t.Skip(err)
	}
	dst := make([]byte, 64)
	for n := 1; n < 64; n++ {
		for _, c := range []string{"a", "\""} {
			body := strings.Repeat("x", n-1) + c
			for end := page - 40; end <= page; end++ {
				if end < n {
					continue
				}
				b := mem[end-n : end]
				copy(b, body)
				s := unsafe.String(&b[0], n)
				if got, want := CopyShort(dst, s, true), plainRef(s, true); got && !want {
					t.Fatalf("CopyShort(%q) ending at %d = true", s, end)
				}
			}
		}
	}
}

// TestStrEndPages places strings right before an unmapped page: the reads
// of strEnd must stay in mapped memory.
func TestStrEndPages(t *testing.T) {
	if !HaveAVX2 {
		t.Skip("no AVX2")
	}
	const page = 4096
	mem, err := syscall.Mmap(-1, 0, 2*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		t.Skip(err)
	}
	defer syscall.Munmap(mem)
	if err := syscall.Mprotect(mem[page:], syscall.PROT_NONE); err != nil {
		t.Skip(err)
	}
	for n := 1; n < 200; n++ {
		for _, c := range []string{"a", "\"", "\xff"} {
			body := strings.Repeat("x", n-1) + c
			for end := page - 70; end <= page; end++ {
				b := mem[end-n : end]
				copy(b, body)
				s := unsafe.String(&b[0], n)
				for i := 0; i <= n; i += 1 + i/8 {
					checkStrEnd(t, s, i)
				}
			}
		}
	}
}
