//go:build arm64 && !purego

package base64

import (
	"bytes"
	std "encoding/base64"
	"testing"
)

func TestARM64EncodeBlocksBounds(t *testing.T) {
	for n := 0; n <= 400; n++ {
		src := make([]byte, n)
		for i := range src {
			src[i] = byte(i*17 + 31)
		}
		for capacity := 0; capacity <= 600; capacity++ {
			out := bytes.Repeat([]byte{0xcc}, capacity+16)
			consumed := encodeBlocks(out[:capacity:capacity], src, &StdEncoding.alphabet)
			groups := min(n/12, capacity/16)
			if consumed != groups*12 {
				t.Fatalf("src=%d dst=%d consumed=%d want=%d", n, capacity, consumed, groups*12)
			}
			want := std.StdEncoding.EncodeToString(src[:consumed])
			if string(out[:len(want)]) != want {
				t.Fatalf("src=%d dst=%d output differs", n, capacity)
			}
			for _, v := range out[len(want):] {
				if v != 0xcc {
					t.Fatalf("src=%d dst=%d write after complete group", n, capacity)
				}
			}
		}
	}
}
