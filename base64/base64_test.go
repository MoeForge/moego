package base64

import (
	"bytes"
	std "encoding/base64"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

type encodingPair struct {
	name string
	fast *Encoding
	ref  *std.Encoding
}

func encodingPairs() []encodingPair {
	pairs := []encodingPair{
		{"std", StdEncoding, std.StdEncoding},
		{"url", URLEncoding, std.URLEncoding},
		{"rawstd", RawStdEncoding, std.RawStdEncoding},
		{"rawurl", RawURLEncoding, std.RawURLEncoding},
		{"custom-padding", StdEncoding.WithPadding('*'), std.StdEncoding.WithPadding('*')},
		{"byte-padding", URLEncoding.WithPadding(0xff), std.URLEncoding.WithPadding(0xff)},
	}
	custom := "ZYXWVUTSRQPONMLKJIHGFEDCBAzyxwvutsrqponmlkjihgfedcba9876543210+/"
	pairs = append(pairs,
		encodingPair{"custom-alphabet", NewEncoding(custom), std.NewEncoding(custom)},
		encodingPair{"custom-raw", NewEncoding(custom).WithPadding(NoPadding), std.NewEncoding(custom).WithPadding(std.NoPadding)},
	)
	high := make([]byte, 64)
	for i := range high {
		high[i] = byte(0x80 + i)
	}
	pairs = append(pairs, encodingPair{"byte-alphabet", NewEncoding(string(high)), std.NewEncoding(string(high))})
	for _, pair := range pairs {
		pairs = append(pairs, encodingPair{pair.name + "/strict", pair.fast.Strict(), pair.ref.Strict()})
	}
	return pairs
}

func compareDecode(t testing.TB, pair encodingPair, src []byte) {
	t.Helper()
	want, wantErr := pair.ref.DecodeString(string(src))
	input := bytes.Clone(src)
	const guard = 16
	buf := bytes.Repeat([]byte{0xcd}, guard+pair.fast.DecodedLen(len(src))+guard)
	dst := buf[guard : len(buf)-guard : len(buf)-guard]
	n, err := pair.fast.Decode(dst, src)
	if n != len(want) || !bytes.Equal(dst[:n], want) || !sameError(err, wantErr) {
		t.Fatalf("%s Decode input %x: count %d/%d, error %v/%v, output %x/%x", pair.name, abbreviated(src), n, len(want), err, wantErr, abbreviated(dst[:n]), abbreviated(want))
	}
	if !bytes.Equal(buf[:guard], bytes.Repeat([]byte{0xcd}, guard)) || !bytes.Equal(buf[len(buf)-guard:], bytes.Repeat([]byte{0xcd}, guard)) {
		t.Fatalf("%s Decode wrote outside destination", pair.name)
	}
	gotString, stringErr := pair.fast.DecodeString(string(src))
	if !bytes.Equal(gotString, want) || !sameError(stringErr, wantErr) {
		t.Fatalf("%s DecodeString input %x: error %v/%v, length %d/%d", pair.name, abbreviated(src), stringErr, wantErr, len(gotString), len(want))
	}
	if !bytes.Equal(src, input) {
		t.Fatalf("%s Decode modified source", pair.name)
	}
}

func sameError(got, want error) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	g, gok := got.(std.CorruptInputError)
	w, wok := want.(std.CorruptInputError)
	return gok == wok && (!gok || g == w) && got.Error() == want.Error()
}

func abbreviated(data []byte) []byte {
	if len(data) > 128 {
		return data[:128]
	}
	return data
}

func TestEncodingDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(197901))
	lengths := make([]int, 257)
	for i := range lengths {
		lengths[i] = i
	}
	lengths = append(lengths, 511, 512, 513, 1023, 1024, 1025, 4095, 4096, 4097, 65535, 65536, 65537)
	for range 96 {
		lengths = append(lengths, rng.Intn(64<<10))
	}
	for _, pair := range encodingPairs() {
		t.Run(pair.name, func(t *testing.T) {
			for _, size := range lengths {
				src := make([]byte, size)
				rng.Read(src)
				original := bytes.Clone(src)
				want := pair.ref.EncodeToString(src)
				if pair.fast.EncodedLen(size) != len(want) || pair.fast.DecodedLen(len(want)) != pair.ref.DecodedLen(len(want)) {
					t.Fatalf("length calculation for %d", size)
				}
				buf := bytes.Repeat([]byte{0x91}, len(want)+2)
				pair.fast.Encode(buf[1:len(buf)-1:len(buf)-1], src)
				if string(buf[1:len(buf)-1]) != want || buf[0] != 0x91 || buf[len(buf)-1] != 0x91 {
					t.Fatalf("Encode size %d", size)
				}
				if got := pair.fast.EncodeToString(src); got != want {
					t.Fatalf("EncodeToString size %d", size)
				}
				if !bytes.Equal(src, original) {
					t.Fatalf("Encode modified source size %d", size)
				}
				compareDecode(t, pair, []byte(want))
				minimum := make([]byte, len(src))
				n, err := pair.fast.Decode(minimum, []byte(want))
				if n != len(src) || err != nil || !bytes.Equal(minimum, src) {
					t.Fatalf("minimum destination size %d: count %d, error %v", size, n, err)
				}
			}
		})
	}
}

func TestDecodeCorruptionAndNewlines(t *testing.T) {
	for _, pair := range encodingPairs() {
		t.Run(pair.name, func(t *testing.T) {
			for _, input := range []string{"", "\r\n", "A", "AA", "AAA", "AAAA", "AA=", "AA==", "AA===", "A===", "=AAA", "AAAA=", "AA==A", "AA=\n=\r\n", "AA=\nA", "AB==", "AAB=", "AB", "AAB", "AA==\r\n!", "A\r\n", " AAAAAA", "AAAA\tAAAA", "AAAA\x00AAAA", "AAAA\xffAAAA"} {
				compareDecode(t, pair, []byte(input))
			}
			payload := bytes.Repeat([]byte{0, 0x7f, 0xff, 0xa5, 0x32}, 61)
			encoded := []byte(pair.ref.EncodeToString(payload))
			for position := 0; position <= len(encoded); position++ {
				for _, separator := range []string{"\r", "\n", "\r\n"} {
					withNewline := append(bytes.Clone(encoded[:position]), separator...)
					withNewline = append(withNewline, encoded[position:]...)
					compareDecode(t, pair, withNewline)
				}
			}
			for _, position := range []int{0, 1, 2, 3, 15, 16, 31, 32, 47, 48, 62, 63, 64, 65, 95, 96, 126, 127, 128, 129, 191, 192, 255, 256, 383, len(encoded) - 2, len(encoded) - 1} {
				for _, bad := range []byte{0, '!', ' ', '\t', '=', '*', '+', '/', '-', '_', 0x80, 0xff} {
					corrupt := bytes.Clone(encoded)
					corrupt[position] = bad
					compareDecode(t, pair, corrupt)
				}
			}
			for keep := 0; keep < len(encoded); keep++ {
				compareDecode(t, pair, encoded[:keep])
			}
		})
	}
}

func TestAppendDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(1518))
	for _, pair := range encodingPairs() {
		for _, size := range []int{0, 1, 2, 3, 47, 48, 49, 63, 64, 65, 127, 128, 129, 4097} {
			src := make([]byte, size)
			rng.Read(src)
			encoded := pair.ref.EncodeToString(src)
			for _, reserve := range []int{0, len(encoded) + 128} {
				prefix := []byte("existing-prefix:")
				dst := make([]byte, len(prefix), len(prefix)+reserve)
				copy(dst, prefix)
				got := pair.fast.AppendEncode(dst, src)
				want := pair.ref.AppendEncode(bytes.Clone(prefix), src)
				if !bytes.Equal(got, want) {
					t.Fatalf("%s AppendEncode size %d reserve %d", pair.name, size, reserve)
				}
				for _, input := range []string{encoded, encoded + "!", "\r\n" + encoded + "\r\n", encoded + "=", encoded + "\xff"} {
					dst = make([]byte, len(prefix), len(prefix)+reserve)
					copy(dst, prefix)
					got, gotErr := pair.fast.AppendDecode(dst, []byte(input))
					want, wantErr := pair.ref.AppendDecode(bytes.Clone(prefix), []byte(input))
					if !bytes.Equal(got, want) || !sameError(gotErr, wantErr) {
						t.Fatalf("%s AppendDecode size %d reserve %d error %v/%v", pair.name, size, reserve, gotErr, wantErr)
					}
				}
			}
		}
	}
}

func TestLargePayloadAndUnalignedBuffers(t *testing.T) {
	rng := rand.New(rand.NewSource(913))
	payload := make([]byte, 2<<20)
	rng.Read(payload)
	for _, pair := range encodingPairs()[:4] {
		encoded := pair.ref.EncodeToString(payload)
		if pair.fast.EncodeToString(payload) != encoded {
			t.Fatalf("%s 2 MiB encode", pair.name)
		}
		compareDecode(t, pair, []byte(encoded))
		for offset := 0; offset < 64; offset++ {
			const size = 258
			src := make([]byte, offset+size)
			copy(src[offset:], payload[:size])
			want := pair.ref.EncodeToString(src[offset:])
			buf := bytes.Repeat([]byte{0xab}, offset+len(want)+1)
			pair.fast.Encode(buf[offset:len(buf)-1:len(buf)-1], src[offset:])
			if string(buf[offset:len(buf)-1]) != want || buf[len(buf)-1] != 0xab {
				t.Fatalf("%s unaligned encode offset %d", pair.name, offset)
			}
			decoded := bytes.Repeat([]byte{0xcd}, offset+size+1)
			n, err := pair.fast.Decode(decoded[offset:len(decoded)-1:len(decoded)-1], buf[offset:len(buf)-1])
			if err != nil || n != size || !bytes.Equal(decoded[offset:offset+n], payload[:size]) || decoded[len(decoded)-1] != 0xcd {
				t.Fatalf("%s unaligned decode offset %d", pair.name, offset)
			}
		}
	}
}

func TestConcurrentStringsOwnTheirStorage(t *testing.T) {
	var wg sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, pair := range encodingPairs() {
				src := bytes.Repeat([]byte{0x19, 0xff, 0x80, 0x00}, 1025)
				want := pair.ref.EncodeToString(src)
				got := pair.fast.EncodeToString(src)
				clear(src)
				for range 8 {
					_ = pair.fast.EncodeToString(src)
					decoded, err := pair.fast.DecodeString(got)
					if got != want || err != nil || pair.ref.EncodeToString(decoded) != want {
						t.Errorf("%s string storage changed or concurrent decode failed", pair.name)
						return
					}
					clear(decoded)
				}
			}
		}()
	}
	wg.Wait()
}

func TestStreamingDelegates(t *testing.T) {
	payload := bytes.Repeat([]byte("media\x00\xff\x80\r\n"), 103)
	for _, pair := range encodingPairs() {
		for _, chunk := range []int{1, 2, 3, 7, 64, 1024} {
			var out bytes.Buffer
			encoder := NewEncoder(pair.fast, &out)
			for start := 0; start < len(payload); start += chunk {
				part := payload[start:min(start+chunk, len(payload))]
				if n, err := encoder.Write(part); err != nil || n != len(part) {
					t.Fatalf("%s streaming write: %d, %v", pair.name, n, err)
				}
			}
			if err := encoder.Close(); err != nil || out.String() != pair.ref.EncodeToString(payload) {
				t.Fatalf("%s streaming encode chunk %d: %v", pair.name, chunk, err)
			}
			for _, suffix := range []string{"", "!", "\r\n", "=", "\xff"} {
				encoded := out.String() + suffix
				got, gotErr := io.ReadAll(NewDecoder(pair.fast, &chunkReader{data: []byte(encoded), chunk: chunk}))
				want, wantErr := io.ReadAll(std.NewDecoder(pair.ref, &chunkReader{data: []byte(encoded), chunk: chunk}))
				if !bytes.Equal(got, want) || !sameError(gotErr, wantErr) {
					t.Fatalf("%s streaming decode chunk %d: %v/%v", pair.name, chunk, gotErr, wantErr)
				}
			}
		}
	}
}

type chunkReader struct {
	data  []byte
	chunk int
}

func (r *chunkReader) Read(dst []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(dst, r.data[:min(len(r.data), r.chunk)])
	r.data = r.data[n:]
	return n, nil
}

func TestConfigurationPanics(t *testing.T) {
	for _, alphabet := range []string{"", standardAlphabet[:63], standardAlphabet + "!", strings.Repeat("A", 64), standardAlphabet[:63] + "\n", standardAlphabet[:63] + "\r"} {
		if !didPanic(func() { NewEncoding(alphabet) }) {
			t.Errorf("NewEncoding accepted %q", alphabet)
		}
	}
	for _, padding := range []rune{-2, '\r', '\n', 'A', 256} {
		if !didPanic(func() { StdEncoding.WithPadding(padding) }) {
			t.Errorf("WithPadding accepted %U", padding)
		}
	}
	for _, size := range []int{1, 2, 3, 47, 48, 49, 64, 128} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			if !didPanic(func() { StdEncoding.Encode(make([]byte, StdEncoding.EncodedLen(size)-1), make([]byte, size)) }) {
				t.Fatal("Encode accepted short destination")
			}
		})
	}
}

func TestCustomAlphabetContainingDefaultPadding(t *testing.T) {
	// NewEncoding allows '=' in the alphabet; WithPadding checks overlap.
	alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+="
	pair := encodingPair{name: "equals-alphabet", fast: NewEncoding(alphabet), ref: std.NewEncoding(alphabet)}
	for _, input := range []string{"====", "AAA=", "AA==", "========"} {
		compareDecode(t, pair, []byte(input))
	}
}

func didPanic(fn func()) (panicked bool) {
	defer func() { panicked = recover() != nil }()
	fn()
	return false
}
