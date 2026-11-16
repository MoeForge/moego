package vp8l

import (
	"encoding/binary"
	"testing"

	"github.com/MoeForge/moego/internal/vp8l/vp8ltest"
)

// FuzzEncode encodes pixels taken from data at a level and checks that
// vp8ltest decodes them back. w picks the width; the low bits of level
// the level, the high bits a hand-written transform.
func FuzzEncode(f *testing.F) {
	for _, s := range [][]byte{nil, {1, 2, 3, 4}, make([]byte, 4000)} {
		for _, l := range []uint8{0, 1, 6, 9, 16 + 6, 32 + 9, 48 + 2} {
			f.Add(s, uint16(7), l)
		}
	}
	f.Add([]byte("aaaaaaaabbbbbbbbaaaaaaaabbbbbbbb"), uint16(2), uint8(9))
	var e Encoder
	f.Fuzz(func(t *testing.T, data []byte, w uint16, level uint8) {
		n := len(data) / 4
		if n == 0 || n > 1<<16 {
			return
		}
		px := make([]uint32, n)
		for i := range px {
			px[i] = binary.LittleEndian.Uint32(data[4*i:])
		}
		width := 1 + int(w)%min(n, 1<<14)
		h := n / width
		px = px[:width*h]
		var ts []vp8ltest.Transform
		switch level >> 4 & 3 {
		case 1:
			ts = []vp8ltest.Transform{{Kind: vp8ltest.SubtractGreen}}
		case 2:
			modes := make([]uint32, ((width+3)/4)*((h+3)/4))
			for i := range modes {
				modes[i] = uint32(i % 14)
			}
			ts = []vp8ltest.Transform{{Kind: vp8ltest.Predictor, Bits: 2, Data: modes}}
		case 3:
			// The most common colors as a table, when there are few.
			seen := map[uint32]bool{}
			var pal []uint32
			for _, p := range px {
				if !seen[p] {
					seen[p] = true
					pal = append(pal, p)
				}
			}
			if len(pal) <= 256 {
				ts = []vp8ltest.Transform{{Kind: vp8ltest.ColorIndexing, Data: pal}}
			}
		}
		lv := int(level&15) % 10
		out := vp8ltest.Encode(&e, lv, px, width, h, ts)
		gw, gh, got, err := vp8ltest.Decode(out)
		if err != nil {
			t.Fatalf("level %d %dx%d: %v", lv, width, h, err)
		}
		if gw != width || gh != h {
			t.Fatalf("decoded %dx%d, want %dx%d", gw, gh, width, h)
		}
		for i := range px {
			if got[i] != px[i] {
				t.Fatalf("level %d %dx%d: pixel %d is %08x, want %08x", lv, width, h, i, got[i], px[i])
			}
		}
	})
}
