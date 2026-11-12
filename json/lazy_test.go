package json

import (
	"strings"
	"testing"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// TestLazyNeedsCopy checks that a long plain string becomes a lazy
// reference only where CopyPlainBlocks can check it as result copies it.
// Elsewhere result would find it unchecked, escape it and start over, once
// for each such string.
func TestLazyNeedsCopy(t *testing.T) {
	s := strings.Repeat("abcdefghijklmnopqrstuvwxyz012345", lazyMin/32+1)
	e := getEncodeState(false)
	b := e.str(e.buf[:0], s)
	lazy := 0
	for _, r := range e.refs {
		if r.lazy {
			lazy++
		}
	}
	putEncodeState(e, b)
	switch {
	case !jsonsimd.LazyCopy && lazy != 0:
		t.Fatalf("%d lazy references without LazyCopy", lazy)
	case jsonsimd.LazyCopy && jsonsimd.HaveAVX2 && lazy != 1:
		t.Fatalf("%d lazy references with AVX2, want 1", lazy)
	}
	if out, err := Marshal([]string{s, s}); err != nil || string(out) != `["`+s+`","`+s+`"]` {
		t.Fatalf("Marshal = %.40q..., %v", out, err)
	}
}
