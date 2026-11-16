package deflate

import (
	"bytes"
	stdadler "hash/adler32"
	"math/rand"
	"testing"
)

// FuzzRoundTrip compresses data at a level and checks that compress/zlib
// inflates it back, whatever the Write sizes: split, if not 0, is the
// size of each Write. level picks the level (-1 to 9) and, past the first
// eleven values, a row hint.
func FuzzRoundTrip(f *testing.F) {
	rng := rand.New(rand.NewSource(12))
	for _, seed := range [][]byte{nil, {0}, []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaab"), text(rng, 3000), filteredRows(rng, 4, 50, 40), bytes.Repeat([]byte{1, 2, 3}, 400)} {
		for _, level := range []uint8{0, 2, 7, 9, 10, 18, 21} {
			f.Add(seed, level, uint16(0))
			f.Add(seed, level, uint16(7))
		}
	}
	f.Fuzz(func(t *testing.T, data []byte, level uint8, split uint16) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		lv := int(level)%11 - 1
		var buf bytes.Buffer
		w := NewWriter(&buf, lv)
		if hint := int(level) / 11; hint > 0 {
			bpp := 1 + hint%8
			w.SetHint(bpp, 1+bpp*(1+int(split>>4)))
		}
		write := func(w *Writer) {
			for p := data; len(p) > 0; {
				n := len(p)
				if split != 0 {
					n = min(n, int(split))
				}
				if k, err := w.Write(p[:n]); k != n || err != nil {
					t.Fatalf("Write = %d, %v", k, err)
				}
				p = p[n:]
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
		}
		write(w)
		z := bytes.Clone(buf.Bytes())
		checkDecode(t, z, data)
		// The same stream again after Reset, written whole.
		buf.Reset()
		w.Reset(&buf, lv)
		if hint := int(level) / 11; hint > 0 {
			bpp := 1 + hint%8
			w.SetHint(bpp, 1+bpp*(1+int(split>>4)))
		}
		split = 0
		write(w)
		if !bytes.Equal(buf.Bytes(), z) {
			t.Fatal("output depends on the Write sizes or on Reset")
		}
	})
}

// FuzzAdler32 compares adler32 with the portable code and hash/adler32 on
// data repeated to at least n bytes, from the running value init.
func FuzzAdler32(f *testing.F) {
	f.Add([]byte(nil), uint32(1), uint16(0))
	f.Add([]byte{0xff}, uint32(0xfff0fff0), uint16(40000))
	f.Add([]byte("moego"), uint32(1), uint16(100))
	f.Add(bytes.Repeat([]byte{0x80, 0x7f}, 50), uint32(0x00010000), uint16(1025))
	f.Fuzz(func(t *testing.T, data []byte, init uint32, n uint16) {
		p := data
		if len(data) > 0 {
			for len(p) < int(n) {
				p = append(p, data...)
			}
		}
		init = (init>>16%adlerMod)<<16 | init&0xffff%adlerMod
		want := adler32Generic(init, p)
		if got := adler32(init, p); got != want {
			t.Fatalf("from %08x, length %d: %08x, want %08x", init, len(p), got, want)
		}
		if init == 1 {
			if std := stdadler.Checksum(p); std != want {
				t.Fatalf("length %d: %08x, hash/adler32 %08x", len(p), want, std)
			}
		}
		// In two pieces.
		k := int(n) % (len(p) + 1)
		if got := adler32(adler32(init, p[:k]), p[k:]); got != want {
			t.Fatalf("split at %d of %d: %08x, want %08x", k, len(p), got, want)
		}
	})
}
