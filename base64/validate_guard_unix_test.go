//go:build darwin || linux

package base64

import (
	"bytes"
	"fmt"
	"syscall"
	"testing"
	"unsafe"
)

func TestValidateStringGuardPages(t *testing.T) {
	lengths := []int{0, 1, 2, 3, 15, 16, 17, 63, 64, 65, 127, 128, 129, 191, 192, 193, 255, 256, 257, 319, 320, 321, 511, 512, 513, 767, 768, 769, 1023, 1024, 1025, 4095, 4096, 4097}
	for _, pair := range encodingPairs() {
		if pair.fast.mode == 0 {
			continue
		}
		for _, length := range lengths {
			for _, suffix := range []string{"", "!", "\r\n", "\xff", "=", "AB=="} {
				t.Run(fmt.Sprintf("%s/%d/%x", pair.name, length, suffix), func(t *testing.T) {
					payload := append(bytes.Repeat([]byte("A"), length), suffix...)
					guarded := readOnlyGuardedInput(t, payload)
					input := unsafe.String(unsafe.SliceData(guarded), len(guarded))
					compareValidation(t, pair, input)
					consumed := validBlocks(guarded, pair.fast.mode == 2)
					if consumed < 0 || consumed%64 != 0 || consumed > len(payload)/64*64 {
						t.Fatalf("validBlocks returned invalid prefix %d", consumed)
					}
					if _, err := pair.ref.DecodeString(string(guarded[:consumed])); err != nil {
						t.Fatalf("validBlocks accepted a corrupt prefix: %v", err)
					}
				})
			}
		}
	}
}

// readOnlyGuardedInput makes source writes and reads past its end fault. The
// unsafe string view in the caller keeps the source against the protected page.
func readOnlyGuardedInput(t *testing.T, data []byte) []byte {
	t.Helper()
	page := syscall.Getpagesize()
	dataPages := max(1, (len(data)+page-1)/page)
	mapping, err := syscall.Mmap(-1, 0, (dataPages+2)*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatalf("mmap validation source: %v", err)
	}
	t.Cleanup(func() {
		if err := syscall.Munmap(mapping); err != nil {
			t.Errorf("munmap validation source: %v", err)
		}
	})
	end := len(mapping) - page
	copy(mapping[end-len(data):end], data)
	if err := syscall.Mprotect(mapping, syscall.PROT_NONE); err != nil {
		t.Fatalf("protect validation guard pages: %v", err)
	}
	if err := syscall.Mprotect(mapping[page:end], syscall.PROT_READ); err != nil {
		t.Fatalf("make validation source read-only: %v", err)
	}
	return mapping[end-len(data) : end : end]
}
