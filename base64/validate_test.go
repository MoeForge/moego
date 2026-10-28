package base64

import (
	"bytes"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

func TestValidateStringDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(5381))
	lengths := []int{0, 1, 2, 3, 46, 47, 48, 49, 63, 64, 65, 95, 96, 97, 191, 192, 193, 255, 256, 257, 767, 768, 769, 1023, 1024, 1025, 1535, 1536, 1537, 4095, 4096, 4097}
	for range 32 {
		lengths = append(lengths, rng.Intn(8192))
	}
	for _, pair := range encodingPairs() {
		t.Run(pair.name, func(t *testing.T) {
			for _, size := range lengths {
				payload := make([]byte, size)
				rng.Read(payload)
				encoded := pair.ref.EncodeToString(payload)
				compareValidation(t, pair, encoded)
				for _, suffix := range []string{"\r\n", "!", "=", "\xff"} {
					compareValidation(t, pair, encoded+suffix)
				}
				compareValidation(t, pair, "\r\n"+encoded)
				for _, offset := range []int{0, 1, 63, 64, 127, 128, 255, 256, 1023, 1024, 1025, 2047, 2048, len(encoded) - 1} {
					if offset < 0 || offset >= len(encoded) {
						continue
					}
					compareValidation(t, pair, encoded[:offset]+"!"+encoded[offset+1:])
					compareValidation(t, pair, encoded[:offset]+"\r\n"+encoded[offset:])
					compareValidation(t, pair, encoded[:offset])
				}
			}
			for _, prefix := range []string{"", pair.ref.EncodeToString(bytes.Repeat([]byte{0x19, 0x7f, 0x80}, 512))} {
				for _, suffix := range []string{"A", "AA", "AAA", "AA=", "AA==", "A===", "=AAA", "AAAA=", "AA==A", "AA=\n=\r\n", "AA=\nA", "AB==", "AAB=", "AB", "AAB", "AA==\r\n!", "A\r\n", " AAAA", "AAAA\tAAAA", "AAAA\x00AAAA", "AAAA\xffAAAA"} {
					compareValidation(t, pair, prefix+suffix)
				}
			}
		})
	}
}

func TestValidateStringLargeAndConcurrent(t *testing.T) {
	payload := bytes.Repeat([]byte{0, 0x7f, 0x80, 0xff}, 512<<10)
	for _, pair := range encodingPairs()[:4] {
		compareValidation(t, pair, pair.ref.EncodeToString(payload))
	}
	input := StdEncoding.EncodeToString(payload[:8192])
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 16 {
				count, err := StdEncoding.ValidateString(input)
				if count != 8192 || err != nil {
					t.Errorf("concurrent ValidateString: count %d, error %v", count, err)
					return
				}
			}
		}()
	}
	workers.Wait()
}

func compareValidation(t testing.TB, pair encodingPair, input string) {
	t.Helper()
	original := strings.Clone(input)
	want, wantErr := pair.ref.DecodeString(input)
	count, err := pair.fast.ValidateString(input)
	if count != len(want) || !sameError(err, wantErr) {
		t.Fatalf("%s ValidateString input length %d: count %d/%d, error %v/%v", pair.name, len(input), count, len(want), err, wantErr)
	}
	if input != original {
		t.Fatal("ValidateString modified input")
	}
}
