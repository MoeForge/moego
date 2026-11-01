//go:build amd64 && !purego && linux

package ojson

import (
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// TestContainerEndPages places documents and paths against unmapped pages:
// the reads past the end of the input that skipAVX2 and pathMasksAVX2 make
// must stay in mapped memory.
func TestContainerEndPages(t *testing.T) {
	const page = 4096
	mem, err := syscall.Mmap(-1, 0, 3*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		t.Skip(err)
	}
	defer syscall.Munmap(mem)
	if err := syscall.Mprotect(mem[:page], syscall.PROT_NONE); err != nil {
		t.Skip(err)
	}
	if err := syscall.Mprotect(mem[2*page:], syscall.PROT_NONE); err != nil {
		t.Skip(err)
	}
	data := mem[page : 2*page]
	bodies := []string{
		`{}`, `[1]`, `{"a":"]"}`, `["` + strings.Repeat(`\\`, 9) + `"]`,
		`{"a":[1,{"b":"x\"y"}],"c":"` + strings.Repeat("z", 150) + `"}`,
		`[` + strings.Repeat(`"s",`, 40) + `0]`, `{"a":`, `["` + strings.Repeat("q", 100),
	}
	for _, body := range bodies {
		for pad := 0; pad < 130; pad++ {
			doc := strings.Repeat(" ", pad) + body
			if len(doc) > page {
				continue
			}
			for _, at := range []int{0, page - len(doc)} {
				b := data[at : at+len(doc)]
				copy(b, doc)
				s := unsafe.String(&b[0], len(b))
				for i := 0; i < len(s); i++ {
					if s[i] == '{' || s[i] == '[' {
						if got, want := containerEndFast(s, i), containerEndScalar(s, i); got != want {
							t.Fatalf("containerEndFast(%q, %d) at offset %d = %d, want %d", s, i, at, got, want)
						}
					}
				}
			}
		}
	}
	// Reads, whose member scan loads 32 bytes at a time.
	docs := []string{
		`{"a":1,"b":"` + strings.Repeat("y", 70) + `","c":{"d":true},"e":[1,2],"f":  null}`,
		`{"model": "gpt-4o",` + "\n  " + `"x": 12345, "y": "\"q", "zz": [], "f": 1}`,
		`{"a"`, `{"a":`, `{"a": "`, `{"a":1`, `{"a":1,`, `{"` + strings.Repeat("k", 40) + `":1,"f":2}`,
	}
	for _, body := range docs {
		for pad := 0; pad < 70; pad++ {
			doc := body[:len(body)-pad%len(body)]
			for _, at := range []int{0, page - len(doc)} {
				b := data[at : at+len(doc)]
				copy(b, doc)
				s := unsafe.String(&b[0], len(b))
				for _, p := range []string{"a", "b", "c.d", "e.1", "f", "x", "y", "zz", "model", "q"} {
					if got, want := Get(s, p), getRef(s, p); !sameResult(got, want) {
						t.Fatalf("Get(%q, %q) at offset %d = %+v, want %+v", s, p, at, got, want)
					}
				}
			}
		}
	}
	for n := 1; n < 64; n++ {
		path := strings.Repeat("ab.", 22)[:n]
		for _, at := range []int{0, page - n} {
			b := data[at : at+n]
			copy(b, path)
			s := unsafe.String(&b[0], n)
			if got, want := pathDots(s), pathDotsRef(s); got != want {
				t.Fatalf("pathDots(%q) at offset %d = %b, want %b", s, at, got, want)
			}
		}
	}
}
