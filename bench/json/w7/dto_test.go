package w7

import (
	"bytes"
	"os"
	"testing"
)

// TestDTO checks that dto.go is codec's dto.go, so that W7 decodes and
// encodes the types the matrix does. Run go generate to copy it again.
func TestDTO(t *testing.T) {
	src, err := os.ReadFile("../codec/dto.go")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("dto.go")
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte("// Code generated from ../codec/dto.go by go generate. DO NOT EDIT.\n\n"),
		bytes.Replace(src, []byte("package codec\n"), []byte("package w7\n"), 1)...)
	if !bytes.Equal(got, want) {
		t.Fatal("dto.go differs from ../codec/dto.go: run go generate")
	}
}
