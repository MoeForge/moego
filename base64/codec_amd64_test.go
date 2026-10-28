//go:build amd64 && !purego

package base64

import (
	"bytes"
	std "encoding/base64"
	"math/rand"
	"os"
	"testing"
)

func requireAVX2(t *testing.T) {
	t.Helper()
	if !haveAVX2 {
		if os.Getenv("MOEGO_REQUIRE_AVX2") == "1" {
			t.Fatal("MOEGO_REQUIRE_AVX2=1 but CPU/OS AVX2 support is unavailable")
		}
		t.Skip("AVX2 unavailable on this host")
	}
}

func TestAMD64BlockCodec(t *testing.T) {
	requireAVX2(t)
	rng := rand.New(rand.NewSource(8128))
	for _, pair := range []struct {
		e *Encoding
		s *std.Encoding
	}{{StdEncoding, std.StdEncoding}, {URLEncoding, std.URLEncoding}} {
		for n := 0; n < 20000; n++ {
			src := make([]byte, n)
			rng.Read(src)
			want := pair.s.EncodeToString(src)
			got := make([]byte, pair.e.EncodedLen(n))
			pair.e.Encode(got, src)
			if string(got) != want {
				t.Fatalf("encode length %d", n)
			}
			dst := make([]byte, pair.e.DecodedLen(len(got)))
			count, err := pair.e.Decode(dst, got)
			if err != nil || !bytes.Equal(dst[:count], src) {
				t.Fatalf("decode length %d: %v", n, err)
			}
		}
	}
}

func TestAMD64BlockValidation(t *testing.T) {
	requireAVX2(t)
	for _, pair := range []struct {
		e   *Encoding
		s   *std.Encoding
		url bool
	}{{StdEncoding, std.StdEncoding, false}, {URLEncoding, std.URLEncoding, true}} {
		src := bytes.Repeat([]byte("A"), 128)
		dst := bytes.Repeat([]byte{0xcc}, 96)
		for pos := 0; pos < 128; pos++ {
			for b := 0; b < 256; b++ {
				src[pos] = byte(b)
				for i := range dst {
					dst[i] = 0xcc
				}
				n := decodeBlocks(dst, src, pair.url)
				valid := false
				for _, c := range pair.e.alphabet {
					if c == byte(b) {
						valid = true
						break
					}
				}
				want := 128
				if !valid {
					want = (pos / 32) * 32
				}
				if n != want {
					t.Fatalf("position %d byte %d url %v: consumed %d want %d", pos, b, pair.url, n, want)
				}
				for i := n / 4 * 3; i < len(dst); i++ {
					if dst[i] != 0xcc {
						t.Fatalf("write after returned prefix")
					}
				}
				if valid {
					w, err := pair.s.DecodeString(string(src))
					if err != nil || !bytes.Equal(dst, w) {
						t.Fatalf("mapping position %d byte %d url %v", pos, b, pair.url)
					}
				}
				src[pos] = 'A'
			}
		}
	}
}
