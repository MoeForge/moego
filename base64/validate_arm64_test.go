//go:build arm64 && !purego

package base64

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestARM64ValidBlocksEveryByteAndPosition(t *testing.T) {
	for _, url := range []bool{false, true} {
		src := bytes.Repeat([]byte("A"), 512)
		unchanged := bytes.Clone(src)
		for position := range len(src) {
			for value := range 256 {
				src[position] = byte(value)
				consumed := validBlocks(src, url)
				if isBase64AlphabetByte(byte(value), url) {
					if consumed != len(src) {
						t.Fatalf("url=%v position=%d byte=%#02x: consumed %d, want %d", url, position, value, consumed, len(src))
					}
				} else if consumed < 0 || consumed%64 != 0 || consumed > position {
					t.Fatalf("url=%v position=%d byte=%#02x: invalid prefix %d", url, position, value, consumed)
				}
				if src[position] != byte(value) || !bytes.Equal(src[:position], unchanged[:position]) || !bytes.Equal(src[position+1:], unchanged[position+1:]) {
					t.Fatalf("url=%v position=%d byte=%#02x: source modified", url, position, value)
				}
			}
			src[position] = 'A'
		}
	}
}

func TestARM64ValidBlocksLengthsAndAlignment(t *testing.T) {
	lengths := make([]int, 321)
	for i := range lengths {
		lengths[i] = i
	}
	lengths = append(lengths, 511, 512, 513, 767, 768, 769, 1023, 1024, 1025, 4095, 4096, 4097)
	for _, url := range []bool{false, true} {
		for offset := range 64 {
			storage := bytes.Repeat([]byte("A"), offset+4097)
			for _, length := range lengths {
				src := storage[offset : offset+length : offset+length]
				whole := length / 64 * 64
				if consumed := validBlocks(src, url); consumed != whole {
					t.Fatalf("url=%v offset=%d length=%d: consumed %d, want %d", url, offset, length, consumed, whole)
				}
				for _, position := range []int{0, length / 2, length - 1} {
					if position < 0 || position >= length {
						continue
					}
					src[position] = 0xff
					consumed := validBlocks(src, url)
					if position >= whole {
						if consumed != whole {
							t.Fatalf("url=%v offset=%d length=%d: unprocessed tail changed prefix to %d", url, offset, length, consumed)
						}
					} else if consumed < 0 || consumed%64 != 0 || consumed > position {
						t.Fatalf("url=%v offset=%d length=%d invalid=%d: prefix %d", url, offset, length, position, consumed)
					}
					src[position] = 'A'
				}
			}
		}
	}
}

func TestARM64ValidateStringByteBoundaries(t *testing.T) {
	for _, pair := range encodingPairs() {
		if pair.fast.mode == 0 {
			continue
		}
		t.Run(pair.name, func(t *testing.T) {
			src := bytes.Repeat([]byte("A"), 512)
			for _, position := range []int{0, 1, 15, 16, 63, 64, 127, 128, 191, 192, 255, 256, 257, 319, 320, 383, 384, 447, 448, 510, 511} {
				for value := range 256 {
					src[position] = byte(value)
					input := string(src)
					want, wantErr := pair.ref.DecodeString(input)
					decoded, decodeErr := pair.fast.DecodeString(input)
					count, err := pair.fast.ValidateString(input)
					if count != len(want) || count != len(decoded) || !sameError(err, wantErr) || !sameError(err, decodeErr) {
						t.Fatalf("position=%d byte=%#02x: count=%d/%d/%d error=%v/%v/%v", position, value, count, len(want), len(decoded), err, wantErr, decodeErr)
					}
				}
				src[position] = 'A'
			}
			for _, length := range []int{60, 64, 124, 128, 188, 192, 252, 256, 316, 320, 508, 512, 1020, 1024} {
				prefix := strings.Repeat("A", length)
				for _, suffix := range []string{"", "A", "AA", "AB", "AAA", "AAB", "AA==", "AB==", "AAB=", "AA=\r\n=", "AA==\r\n", "AA==A", "\r\nAAAA", "\r\nAB==", "\xffAAAA"} {
					t.Run(fmt.Sprintf("tail-%d-%x", length, suffix), func(t *testing.T) {
						compareValidation(t, pair, prefix+suffix)
					})
				}
			}
		})
	}
}

func isBase64AlphabetByte(value byte, url bool) bool {
	if value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9' {
		return true
	}
	if url {
		return value == '-' || value == '_'
	}
	return value == '+' || value == '/'
}
