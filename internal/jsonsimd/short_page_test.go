//go:build (amd64 || arm64) && !purego && (linux || darwin)

package jsonsimd

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

func guardPage(t *testing.T) ([]byte, int) {
	t.Helper()
	page := os.Getpagesize()
	mem, errMmap := syscall.Mmap(-1, 0, 2*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if errMmap != nil {
		t.Skip(errMmap)
	}
	t.Cleanup(func() {
		if errMunmap := syscall.Munmap(mem); errMunmap != nil {
			t.Errorf("Munmap: %v", errMunmap)
		}
	})
	if errMprotect := syscall.Mprotect(mem[page:], syscall.PROT_NONE); errMprotect != nil {
		t.Skip(errMprotect)
	}
	return mem, page
}

// TestCopyShortPages places strings right before an unmapped page: the
// 32-byte reads of CopyShort must stay in mapped memory.
func TestCopyShortPages(t *testing.T) {
	if !HaveCopyShort {
		t.Skip("no CopyShort")
	}
	mem, page := guardPage(t)
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
	if !HaveStrSkip {
		t.Skip("no StrSkip")
	}
	mem, page := guardPage(t)
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

// TestEscapeUnescapePages places source and destination buffers right before an
// unmapped page to ensure strSkip, EscapeBlocks, and UnescapeBlocks never read
// or write past their slice bounds.
func TestEscapeUnescapePages(t *testing.T) {
	srcMem, page := guardPage(t)
	dstMem, _ := guardPage(t)

	if HaveStrSkip {
		for _, body := range []string{
			strings.Repeat("a", 180),
			strings.Repeat("a", 120) + "\u00e9" + strings.Repeat("b", 80),
			strings.Repeat("a", 95) + `"` + strings.Repeat("b", 70),
		} {
			n := len(body)
			b := srcMem[page-n : page]
			copy(b, body)
			s := unsafe.String(&b[0], n)
			for i := 0; i <= n; i += 17 {
				strSkip(s, i)
				strSkipHTML(s, i)
			}
		}
	}

	if HaveEscape {
		escSrc := strings.Repeat("abc\n\"\\<\x00\u4e2d", 25)
		b := srcMem[page-len(escSrc) : page]
		copy(b, escSrc)
		s := unsafe.String(&b[0], len(escSrc))
		// Check destination guard page with constrained dn.
		for dn := 40; dn <= 360; dn += 19 {
			out := dstMem[page-dn : page]
			i, w := EscapeBlocks(out, s, 0, 0, true)
			if i < 0 || i > len(s) || w < 0 || w > dn {
				t.Fatalf("EscapeBlocks(%d): out of range (%d, %d)", dn, i, w)
			}
		}
		// Check source guard page with ample output space across varying tails.
		bigOut := make([]byte, 6*len(escSrc)+128)
		for n := 32; n <= len(escSrc); n += 7 {
			sub := srcMem[page-n : page]
			subS := unsafe.String(&sub[0], n)
			i, w := EscapeBlocks(bigOut, subS, 0, 0, true)
			if i <= 0 || i > n || w <= 0 || w > len(bigOut) {
				t.Fatalf("EscapeBlocks(n=%d): out of range (%d, %d)", n, i, w)
			}
		}

		unescSrc := strings.Repeat(`abc\n\"\\\u0026\u4e2d中文`, 16)
		ub := srcMem[page-len(unescSrc) : page]
		copy(ub, unescSrc)
		us := unsafe.String(&ub[0], len(unescSrc))
		for dn := 32; dn <= 320; dn += 17 {
			out := dstMem[page-dn : page]
			i, w, _ := UnescapeBlocks(out, us, 0, 0)
			if i < 0 || i > len(us) || w < 0 || w > dn {
				t.Fatalf("UnescapeBlocks(%d): out of range (%d, %d)", dn, i, w)
			}
		}
		for n := 34; n <= len(unescSrc); n += 7 {
			sub := srcMem[page-n : page]
			subS := unsafe.String(&sub[0], n)
			i, w, _ := UnescapeBlocks(bigOut, subS, 0, 0)
			if i < 0 || i > n || w < 0 || w > len(bigOut) {
				t.Fatalf("UnescapeBlocks(n=%d): out of range (%d, %d)", n, i, w)
			}
		}
	}
}
