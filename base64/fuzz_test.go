package base64

import (
	"bytes"
	"testing"
)

func FuzzCodecDifferential(f *testing.F) {
	for _, seed := range [][]byte{nil, []byte("f"), []byte("fo"), []byte("foo"), {0, 255, 128}, bytes.Repeat([]byte{0x81, 0x00, 0xff}, 65), bytes.Repeat([]byte("media"), 1024)} {
		f.Add(seed, uint8(0))
		f.Add(seed, uint8(3))
		f.Add(seed, uint8(9))
	}
	pairs := encodingPairs()
	f.Fuzz(func(t *testing.T, src []byte, encoding uint8) {
		if len(src) > 2<<20 {
			t.Skip()
		}
		pair := pairs[int(encoding)%len(pairs)]
		want := pair.ref.EncodeToString(src)
		if got := pair.fast.EncodeToString(src); got != want {
			t.Fatalf("%s EncodeToString differs for %d bytes", pair.name, len(src))
		}
		compareDecode(t, pair, []byte(want))
		compareDecode(t, pair, src)
		prefix := []byte("prefix:")
		got, gotErr := pair.fast.AppendDecode(bytes.Clone(prefix), src)
		wantDecoded, wantErr := pair.ref.AppendDecode(bytes.Clone(prefix), src)
		if !bytes.Equal(got, wantDecoded) || !sameError(gotErr, wantErr) {
			t.Fatalf("%s AppendDecode differs: %v/%v", pair.name, gotErr, wantErr)
		}
	})
}
