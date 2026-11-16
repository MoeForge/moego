package vp8ltest

import (
	"encoding/binary"
	"strings"
	"testing"
)

// sw writes test streams bit by bit.
type sw struct {
	b []byte
	n int
}

func (w *sw) put(v, n int) {
	for i := 0; i < n; i++ {
		if w.n%8 == 0 {
			w.b = append(w.b, 0)
		}
		w.b[w.n/8] |= byte(v>>i&1) << (w.n % 8)
		w.n++
	}
}

// code writes a prefix code, first bit first.
func (w *sw) code(c, l int) {
	for i := l - 1; i >= 0; i-- {
		w.put(c>>i&1, 1)
	}
}

func (w *sw) header(width, height int) {
	w.put(0x2f, 8)
	w.put(width-1, 14)
	w.put(height-1, 14)
	w.put(0, 1)
	w.put(0, 3)
}

// simple writes a simple code of one or two symbols.
func (w *sw) simple(s ...int) {
	w.put(1, 1)
	w.put(len(s)-1, 1)
	if s[0] < 2 {
		w.put(0, 1)
		w.put(s[0], 1)
	} else {
		w.put(1, 1)
		w.put(s[0], 8)
	}
	if len(s) == 2 {
		w.put(s[1], 8)
	}
}

// canon returns the canonical codes of lens.
func canon(lens []int) []int {
	var count [16]int
	for _, l := range lens {
		if l != 0 {
			count[l]++
		}
	}
	var next [16]int
	c := 0
	for l := 1; l < 16; l++ {
		c = (c + count[l-1]) << 1
		next[l] = c
	}
	codes := make([]int, len(lens))
	for s, l := range lens {
		if l != 0 {
			codes[s] = next[l]
			next[l]++
		}
	}
	return codes
}

// normal writes a normal code: the code length code clLens, max_symbol
// (none if negative), then tokens of {symbol, extra}.
func (w *sw) normal(clLens [19]int, maxSym int, tokens [][2]int) {
	w.put(0, 1)
	ncl := 19
	for ncl > 4 && clLens[codeLenOrder[ncl-1]] == 0 {
		ncl--
	}
	w.put(ncl-4, 4)
	for _, s := range codeLenOrder[:ncl] {
		w.put(clLens[s], 3)
	}
	if maxSym < 0 {
		w.put(0, 1)
	} else {
		w.put(1, 1)
		w.put(7, 3) // 16 bits
		w.put(maxSym-2, 16)
	}
	codes := canon(clLens[:])
	for _, t := range tokens {
		s := t[0]
		w.code(codes[s], clLens[s])
		switch s {
		case 16:
			w.put(t[1], 2)
		case 17:
			w.put(t[1], 3)
		case 18:
			w.put(t[1], 7)
		}
	}
}

// lensCode is a code length code over the lengths 0 to 3, two bits each.
var lensCode = [19]int{0: 2, 1: 2, 2: 2, 3: 2}

// lensTokens returns literal tokens for lens, which are at most 3.
func lensTokens(lens []int) [][2]int {
	t := make([][2]int, len(lens))
	for i, l := range lens {
		t[i] = [2]int{l, 0}
	}
	return t
}

// zeroPixelStream is a valid 1x1 stream of one zero pixel; its parts can be
// replaced by the tests.
func zeroPixelStream(mod func(w *sw, part string) bool) []byte {
	w := &sw{}
	if !mod(w, "header") {
		w.header(1, 1)
	}
	if !mod(w, "transforms") {
		w.put(0, 1)
	}
	if !mod(w, "cache") {
		w.put(0, 1)
	}
	if !mod(w, "meta") {
		w.put(0, 1)
	}
	for _, p := range []string{"green", "red", "blue", "alpha", "dist"} {
		if !mod(w, p) {
			w.simple(0)
		}
	}
	mod(w, "data")
	return w.b
}

func none(*sw, string) bool { return false }

func TestDecodeMinimal(t *testing.T) {
	w, h, px, err := Decode(zeroPixelStream(none))
	if err != nil || w != 1 || h != 1 || len(px) != 1 || px[0] != 0 {
		t.Fatalf("got %d %d %x %v", w, h, px, err)
	}
	// A two-symbol green code from a normal header: 0 and 256 (copy of
	// one pixel, distance code 2 = one pixel left).
	b := zeroPixelStream(func(w *sw, part string) bool {
		switch part {
		case "header":
			w.header(3, 1)
		case "green":
			lens := make([]int, 280)
			lens[0], lens[256] = 1, 1
			w.normal(lensCode, -1, lensTokens(lens))
		case "dist":
			w.simple(1)
		case "data":
			w.code(0, 1) // literal 0
			w.code(1, 1) // copy 1
			w.code(1, 1) // copy 1
		default:
			return false
		}
		return true
	})
	w, h, px, err = Decode(b)
	if err != nil || w != 3 || h != 1 || px[2] != 0 {
		t.Fatalf("got %d %d %x %v", w, h, px, err)
	}
}

func TestDecodeRejects(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		mod        func(w *sw, part string) bool
	}{
		{"signature", "signature", func(w *sw, p string) bool {
			if p == "header" {
				w.put(0x2e, 8)
				w.put(0, 32)
				return true
			}
			return false
		}},
		{"version", "version", func(w *sw, p string) bool {
			if p == "header" {
				w.put(0x2f, 8)
				w.put(0, 29)
				w.put(1, 3)
				return true
			}
			return false
		}},
		{"cache0", "cache", func(w *sw, p string) bool {
			if p == "cache" {
				w.put(1, 1)
				w.put(0, 4)
				return true
			}
			return false
		}},
		{"cache12", "cache", func(w *sw, p string) bool {
			if p == "cache" {
				w.put(1, 1)
				w.put(12, 4)
				return true
			}
			return false
		}},
		{"twice", "twice", func(w *sw, p string) bool {
			if p == "transforms" {
				w.put(1, 1)
				w.put(subtractGreen, 2)
				w.put(1, 1)
				w.put(subtractGreen, 2)
				w.put(0, 1)
				return true
			}
			return false
		}},
		{"incomplete", "incomplete", func(w *sw, p string) bool {
			if p == "red" {
				lens := make([]int, 256)
				lens[0], lens[1] = 1, 2
				w.normal(lensCode, -1, lensTokens(lens))
				return true
			}
			return false
		}},
		{"oversubscribed", "over-subscribed", func(w *sw, p string) bool {
			if p == "red" {
				lens := make([]int, 256)
				lens[0], lens[1], lens[2] = 1, 1, 1
				w.normal(lensCode, -1, lensTokens(lens))
				return true
			}
			return false
		}},
		{"empty", "empty", func(w *sw, p string) bool {
			if p == "blue" {
				w.normal(lensCode, -1, lensTokens(make([]int, 256)))
				return true
			}
			return false
		}},
		{"leaf2", "one-symbol", func(w *sw, p string) bool {
			if p == "blue" {
				lens := make([]int, 256)
				lens[7] = 2
				w.normal(lensCode, -1, lensTokens(lens))
				return true
			}
			return false
		}},
		{"clIncomplete", "incomplete", func(w *sw, p string) bool {
			if p == "blue" {
				cl := [19]int{0: 1, 1: 2}
				w.normal(cl, -1, nil)
				return true
			}
			return false
		}},
		{"maxSymbol", "max_symbol", func(w *sw, p string) bool {
			if p == "dist" {
				w.normal(lensCode, 41, nil)
				return true
			}
			return false
		}},
		{"repeatPast", "past the alphabet", func(w *sw, p string) bool {
			if p == "dist" {
				cl := [19]int{1: 1, 18: 1}
				w.normal(cl, -1, [][2]int{{1, 0}, {1, 0}, {18, 127}})
				return true
			}
			return false
		}},
		{"simpleSymbol", "simple code symbol", func(w *sw, p string) bool {
			if p == "dist" {
				w.simple(200)
				return true
			}
			return false
		}},
		{"distBefore", "distance", func(w *sw, p string) bool {
			switch p {
			case "header":
				w.header(2, 1)
			case "green":
				lens := make([]int, 280)
				lens[0], lens[256] = 1, 1
				w.normal(lensCode, -1, lensTokens(lens))
			case "data":
				w.code(1, 1) // a copy at the first pixel
			default:
				return false
			}
			return true
		}},
		{"copyPast", "runs past", func(w *sw, p string) bool {
			switch p {
			case "header":
				w.header(2, 1)
			case "green":
				lens := make([]int, 280)
				lens[0], lens[257] = 1, 1
				w.normal(lensCode, -1, lensTokens(lens))
			case "dist":
				w.simple(1)
			case "data":
				w.code(0, 1) // literal
				w.code(1, 1) // copy of 2 at pixel 1
			default:
				return false
			}
			return true
		}},
		{"predictorMode", "predictor mode", func(w *sw, p string) bool {
			switch p {
			case "header":
				w.header(2, 2)
			case "transforms":
				w.put(1, 1)
				w.put(predictorTransform, 2)
				w.put(0, 3) // 4x4 blocks: one
				w.put(0, 1) // no cache
				w.simple(14)
				for i := 0; i < 4; i++ {
					w.simple(0)
				}
				w.put(0, 1)
			default:
				return false
			}
			return true
		}},
		{"short", "ends early", func(w *sw, p string) bool {
			if p == "dist" {
				w.put(0, 1)
				w.put(15, 4) // 19 code length code lengths follow
				return true
			}
			return false
		}},
		{"trailing", "after the stream", func(w *sw, p string) bool {
			if p == "data" {
				w.put(0, 16)
				w.b = append(w.b, 0)
				return true
			}
			return false
		}},
		{"padding", "padding", func(w *sw, p string) bool {
			if p == "data" {
				w.put(1, 1)
				return true
			}
			return false
		}},
	} {
		_, _, _, err := Decode(zeroPixelStream(tc.mod))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want one about %q", tc.name, err, tc.want)
		}
	}
}

func TestDecodeWebPContainer(t *testing.T) {
	// An odd payload, so the chunk is padded.
	payload := zeroPixelStream(func(w *sw, p string) bool {
		if p == "green" {
			w.simple(5) // 7 bits more than simple(0): 70 bits in all
			return true
		}
		return false
	})
	good := WebP(payload)
	if _, _, _, err := DecodeWebP(good); err != nil {
		t.Fatal(err)
	}
	if len(payload)%2 != 1 || len(good) != 20+len(payload)+1 {
		t.Fatalf("file of %d bytes for a payload of %d", len(good), len(payload))
	}
	mod := func(f func(b []byte) []byte) []byte { return f(append([]byte(nil), good...)) }
	for name, b := range map[string][]byte{
		"riff":     mod(func(b []byte) []byte { b[0] = 'X'; return b }),
		"webp":     mod(func(b []byte) []byte { b[8] = 'X'; return b }),
		"vp8x":     mod(func(b []byte) []byte { b[15] = 'X'; return b }),
		"riffSize": mod(func(b []byte) []byte { binary.LittleEndian.PutUint32(b[4:], uint32(len(b))); return b }),
		"chunk":    mod(func(b []byte) []byte { binary.LittleEndian.PutUint32(b[16:], uint32(len(payload)+2)); return b }),
		"trailing": mod(func(b []byte) []byte { return append(b, 0, 0) }),
		"pad":      mod(func(b []byte) []byte { b[len(b)-1] = 1; return b }),
		"short":    good[:len(good)-2],
	} {
		if _, _, _, err := DecodeWebP(b); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// TestTransformsByHand round-trips the hand-written transforms through a
// literal-only writer, so the decoder's inverse transforms are checked
// against the forward ones independently of any encoder.
func TestTransformsByHand(t *testing.T) {
	for _, c := range Cases(5, false) {
		if len(c.TS) == 0 && c.W*c.H > 5000 {
			continue
		}
		out := Encode(&literalWriter{}, 0, c.ARGB, c.W, c.H, c.TS)
		w, h, px, err := Decode(out)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if w != c.W || h != c.H {
			t.Fatalf("%s: %dx%d", c.Name, w, h)
		}
		for i := range px {
			if px[i] != c.ARGB[i] {
				t.Fatalf("%s: pixel %d is %08x, want %08x", c.Name, i, px[i], c.ARGB[i])
			}
		}
	}
}

// literalWriter writes every pixel as a literal under codes of 8 bits per
// channel: the simplest valid encoder.
type literalWriter struct{ w sw }

func (l *literalWriter) Reset(int)                  { l.w = sw{} }
func (l *literalWriter) Header(w, h int, a bool)    { l.w.header(w, h) }
func (l *literalWriter) WriteBits(v uint32, n uint) { l.w.put(int(v), int(n)) }
func (l *literalWriter) Finish() []byte             { return l.w.b }
func (l *literalWriter) SubImage(px []uint32, w, h int) {
	l.image(px, false)
}
func (l *literalWriter) MainImage(px []uint32, w, h int) {
	l.image(px, true)
}

func (l *literalWriter) image(px []uint32, main bool) {
	w := &l.w
	w.put(0, 1) // no cache
	if main {
		w.put(0, 1)
	}
	// Every literal 8 bits: lengths of 8 for 0..255, written with a code
	// length code holding 0 and 8.
	cl := [19]int{0: 1, 8: 1}
	for _, n := range []int{280, 256, 256, 256} {
		toks := make([][2]int, n)
		for s := range toks {
			if s < 256 {
				toks[s] = [2]int{8, 0}
			}
		}
		w.normal(cl, -1, toks)
	}
	w.simple(0)
	for _, p := range px {
		w.code(int(p>>8&0xff), 8)
		w.code(int(p>>16&0xff), 8)
		w.code(int(p&0xff), 8)
		w.code(int(p>>24), 8)
	}
}
