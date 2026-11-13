//go:build amd64 && !purego

package base64

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

// codecResults is what the codec gives for one source and one encoded
// input.
type codecResults struct {
	encoded, appended, toString string
	decoded, appendDecoded      []byte
	decodedString               []byte
	n, validN                   int
	err, appendErr, stringErr   error
	validErr                    error
}

// codecRun runs every encode and decode of e on src and on in, writing
// into buffers with guard bytes around them, which must stay unchanged.
func codecRun(t *testing.T, e *Encoding, src, in []byte) codecResults {
	t.Helper()
	const guard = 8
	var r codecResults
	buf := bytes.Repeat([]byte{0xa5}, guard+e.EncodedLen(len(src))+guard)
	e.Encode(buf[guard:len(buf)-guard:len(buf)-guard], src)
	r.encoded = string(buf[guard : len(buf)-guard])
	r.appended = string(e.AppendEncode([]byte("prefix"), src))
	r.toString = e.EncodeToString(src)
	dbuf := bytes.Repeat([]byte{0xa5}, guard+e.DecodedLen(len(in))+guard)
	r.n, r.err = e.Decode(dbuf[guard:len(dbuf)-guard:len(dbuf)-guard], in)
	r.decoded = bytes.Clone(dbuf[guard : guard+r.n])
	for _, b := range [][]byte{buf[:guard], buf[len(buf)-guard:], dbuf[:guard], dbuf[len(dbuf)-guard:]} {
		if !bytes.Equal(b, bytes.Repeat([]byte{0xa5}, guard)) {
			t.Fatalf("write outside the destination for source %d bytes, input %q", len(src), abbreviated(in))
		}
	}
	r.appendDecoded, r.appendErr = e.AppendDecode([]byte("prefix"), in)
	r.decodedString, r.stringErr = e.DecodeString(string(in))
	r.validN, r.validErr = e.ValidateString(string(in))
	return r
}

// corruptions returns enc changed in the ways decoders must report or
// skip: bytes outside the alphabet, padding inside, CR and LF, a missing
// tail and bytes after the end.
func corruptions(rng *rand.Rand, enc []byte) [][]byte {
	if len(enc) == 0 {
		return [][]byte{[]byte("="), []byte("\r\n"), []byte("A")}
	}
	const bad = "!*#\x00\x7f\x80\xff -_+/=.\t"
	var out [][]byte
	edit := func(f func(b []byte) []byte) {
		out = append(out, f(bytes.Clone(enc)))
	}
	for range 3 {
		i, c := rng.Intn(len(enc)), bad[rng.Intn(len(bad))]
		edit(func(b []byte) []byte { b[i] = c; return b })
	}
	for _, k := range []int{1, 2, 3} {
		k := min(k, len(enc))
		edit(func(b []byte) []byte { return b[:len(b)-k] })
	}
	i := rng.Intn(len(enc) + 1)
	edit(func(b []byte) []byte { return append(b[:i:i], append([]byte("\r\n"), enc[i:]...)...) })
	edit(func(b []byte) []byte {
		for range 1 + rng.Intn(4) {
			j := rng.Intn(len(b) + 1)
			b = append(b[:j:j], append([]byte{"\r\n"[rng.Intn(2)]}, b[j:]...)...)
		}
		return b
	})
	edit(func(b []byte) []byte { return append(b, '=') })
	edit(func(b []byte) []byte { return append(b, "QQ=="...) })
	edit(func(b []byte) []byte { return append(b, "\n"...) })
	return out
}

// TestCodecWithoutAVX2 compares what the codec gives with the AVX2 kernels
// and without them, as on CPUs without AVX2, for every encoding of the
// tests, valid and corrupt input of many lengths: the encoded text, the
// decoded bytes, the counts, and the errors with their offsets.
func TestCodecWithoutAVX2(t *testing.T) {
	requireAVX2(t)
	rng := rand.New(rand.NewSource(20261113))
	step := 1
	if raceEnabled {
		step = 7
	}
	var lengths []int
	for n := 0; n <= 300; n += step {
		lengths = append(lengths, n)
	}
	for range randomIterations(150) {
		lengths = append(lengths, 300+rng.Intn(1800))
	}
	lengths = append(lengths, 4095, 4096, 4097, 64<<10+1)
	for _, pair := range encodingPairs() {
		t.Run(pair.name, func(t *testing.T) {
			for _, size := range lengths {
				src := make([]byte, size)
				rng.Read(src)
				enc := []byte(pair.ref.EncodeToString(src))
				inputs := append([][]byte{enc}, corruptions(rng, enc)...)
				if size > 4096 && raceEnabled {
					inputs = inputs[:2]
				}
				for _, in := range inputs {
					fast := codecRun(t, pair.fast, src, in)
					undo := limitCPU("avx2")
					slow := codecRun(t, pair.fast, src, in)
					undo()
					if d := codecDiff(fast, slow); d != "" {
						t.Fatalf("source %d bytes, input %q: %s with AVX2, %s without", size, abbreviated(in), d, codecDiff(slow, fast))
					}
					// Both must also be what encoding/base64 gives.
					want, werr := pair.ref.DecodeString(string(in))
					if !bytes.Equal(fast.decoded, want) || !sameError(fast.err, werr) || fast.encoded != string(enc) {
						t.Fatalf("source %d bytes, input %q: decoded %d bytes, %v; want %d bytes, %v", size, abbreviated(in), len(fast.decoded), fast.err, len(want), werr)
					}
				}
			}
		})
	}
}

// codecDiff describes the first result of a that differs from b, or
// returns "".
func codecDiff(a, b codecResults) string {
	switch {
	case a.encoded != b.encoded:
		return fmt.Sprintf("Encode %.80q", a.encoded)
	case a.appended != b.appended:
		return fmt.Sprintf("AppendEncode %.80q", a.appended)
	case a.toString != b.toString:
		return fmt.Sprintf("EncodeToString %.80q", a.toString)
	case a.n != b.n || !bytes.Equal(a.decoded, b.decoded) || !sameError(a.err, b.err):
		return fmt.Sprintf("Decode %d, %v, %x", a.n, a.err, abbreviated(a.decoded))
	case !bytes.Equal(a.appendDecoded, b.appendDecoded) || !sameError(a.appendErr, b.appendErr):
		return fmt.Sprintf("AppendDecode %v, %x", a.appendErr, abbreviated(a.appendDecoded))
	case !bytes.Equal(a.decodedString, b.decodedString) || !sameError(a.stringErr, b.stringErr):
		return fmt.Sprintf("DecodeString %v, %x", a.stringErr, abbreviated(a.decodedString))
	case a.validN != b.validN || !sameError(a.validErr, b.validErr):
		return fmt.Sprintf("ValidateString %d, %v", a.validN, a.validErr)
	}
	return ""
}

// TestDataURLWithoutAVX2 compares the data URLs made with the AVX2 kernels
// and without them.
func TestDataURLWithoutAVX2(t *testing.T) {
	requireAVX2(t)
	rng := rand.New(rand.NewSource(7))
	for _, n := range []int{0, 1, 23, 24, 25, 47, 48, 100, 1000, 70000} {
		src := make([]byte, n)
		rng.Read(src)
		fast, fastApp := EncodeDataURL("image/png", src), AppendDataURL([]byte("x"), "image/png", src)
		undo := limitCPU("avx2")
		slow, slowApp := EncodeDataURL("image/png", src), AppendDataURL([]byte("x"), "image/png", src)
		undo()
		if fast != slow || !bytes.Equal(fastApp, slowApp) {
			t.Fatalf("data URL of %d bytes differs without AVX2", n)
		}
	}
}

// TestLimitCPU checks that MOEGO_TEST_CPU names turn the kernels off and
// that the function limitCPU returns turns them back on.
func TestLimitCPU(t *testing.T) {
	prev := haveAVX2
	for _, drop := range []string{"avx2", "avx", "avx,aes", " avx2 , aes"} {
		undo := limitCPU(drop)
		if haveAVX2 {
			t.Errorf("limitCPU(%q) left the AVX2 kernels on", drop)
		}
		undo()
		if haveAVX2 != prev {
			t.Fatalf("undo of limitCPU(%q) did not restore the kernels", drop)
		}
	}
	undo := limitCPU("aes")
	if haveAVX2 != prev {
		t.Errorf("limitCPU(\"aes\") changed the AVX2 kernels")
	}
	undo()
	defer func() {
		if recover() == nil {
			t.Error("limitCPU of an unknown feature did not panic")
		}
	}()
	limitCPU("sse5")
}
