package base64

import (
	"bytes"
	std "encoding/base64"
	"testing"
)

func TestDataURL(t *testing.T) {
	for _, mediaType := range []string{"", "image/png", "image/jpeg", "audio/pcm", "application/pdf", "text/plain;charset=utf-8"} {
		for _, size := range []int{0, 1, 2, 3, 47, 48, 49, 63, 64, 65, 1024, 65537} {
			src := make([]byte, size)
			for i := range src {
				src[i] = byte(i)
			}
			original := bytes.Clone(src)
			want := "data:" + mediaType + ";base64," + std.StdEncoding.EncodeToString(src)
			got := EncodeDataURL(mediaType, src)
			if got != want {
				t.Fatalf("EncodeDataURL %q size %d", mediaType, size)
			}
			for _, reserve := range []int{0, len(want) + 128} {
				prefix := []byte("url=")
				dst := make([]byte, len(prefix), len(prefix)+reserve)
				copy(dst, prefix)
				out := AppendDataURL(dst, mediaType, src)
				if string(out) != string(prefix)+want {
					t.Fatalf("AppendDataURL %q size %d reserve %d", mediaType, size, reserve)
				}
			}
			if !bytes.Equal(src, original) {
				t.Fatal("DataURL encoding modified source")
			}
			clear(src)
			_ = EncodeDataURL(mediaType, src)
			if got != want {
				t.Fatal("DataURL string changed after source mutation")
			}
		}
	}
}
