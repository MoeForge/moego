package bench

import (
	"bytes"
	std "encoding/base64"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	moego "github.com/MoeForge/moego/base64"
	cloudwego "github.com/cloudwego/base64x"
	cristalhq "github.com/cristalhq/base64"
	emmansun "github.com/emmansun/base64"
	segmentio "github.com/segmentio/asm/base64"
)

// All implementations use the same call boundary. The preallocated benchmarks
// measure the codec itself; the string benchmarks also measure allocation and GC.
type codec struct {
	name           string
	encodedLen     func(int) int
	decodedLen     func(int) int
	encode         func([]byte, []byte)
	decode         func([]byte, []byte) (int, error)
	encodeToString func([]byte) string
	decodeString   func(string) ([]byte, error)
}

type encoding interface {
	EncodedLen(int) int
	DecodedLen(int) int
	Encode([]byte, []byte)
	Decode([]byte, []byte) (int, error)
	EncodeToString([]byte) string
	DecodeString(string) ([]byte, error)
}

func wrap(name string, enc encoding) codec {
	return codec{name, enc.EncodedLen, enc.DecodedLen, enc.Encode, enc.Decode, enc.EncodeToString, enc.DecodeString}
}

func codecs() []codec {
	cloudwegoName := "Cloudwego"
	if runtime.GOARCH != "amd64" {
		cloudwegoName = "CloudwegoFallback"
	}
	list := []codec{
		wrap("Stdlib", std.StdEncoding),
		wrap("Segmentio", segmentio.StdEncoding),
		wrap("Emmansun", emmansun.StdEncoding),
		wrap("Cristalhq", cristalhq.StdEncoding),
		wrap(cloudwegoName, cloudwego.StdEncoding),
		wrap("Moego", moego.StdEncoding),
	}
	if offset, err := strconv.Atoi(os.Getenv("MOEGO_BENCH_ROTATE")); err == nil {
		offset = (offset%len(list) + len(list)) % len(list)
		list = append(list[offset:], list[:offset]...)
	}
	return list
}

var sizes = []struct {
	name string
	n    int
}{
	{"1KiB", 1 << 10},
	{"32KiB", 32 << 10},
	{"256KiB", 256 << 10},
	{"1MiB", 1 << 20},
	{"4MiB", 4 << 20},
}

func payload(n int) []byte {
	p := make([]byte, n)
	state := uint64(0x6a09e667f3bcc909)
	for i := range p {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		p[i] = byte(state)
	}
	return p
}

var sinkBytes []byte
var sinkString string

func checkBytes(tb testing.TB, got, want []byte, err error) {
	tb.Helper()
	if err != nil || !bytes.Equal(got, want) {
		tb.Fatalf("result mismatch: got %d bytes, want %d, err=%v", len(got), len(want), err)
	}
}

// TestCompetitors verifies the exact operations used by the benchmarks against
// the standard library, including all tail lengths and SIMD block boundaries.
// It deliberately does not claim equivalent handling of malformed input.
func TestCompetitors(t *testing.T) {
	for _, c := range codecs() {
		t.Run(c.name, func(t *testing.T) {
			for _, n := range []int{0, 1, 2, 3, 15, 16, 17, 31, 32, 33, 47, 48, 49, 63, 64, 65, 127, 128, 129, 1023, 1024, 1025, 32768, 262144, 1048576, 4194304} {
				src := payload(n)
				want := std.StdEncoding.EncodeToString(src)
				encoded := make([]byte, c.encodedLen(n))
				c.encode(encoded, src)
				checkBytes(t, encoded, []byte(want), nil)
				if got := c.encodeToString(src); got != want {
					t.Fatalf("EncodeToString length %d differs", n)
				}
				decoded := make([]byte, c.decodedLen(len(encoded)))
				nn, err := c.decode(decoded, encoded)
				checkBytes(t, decoded[:nn], src, err)
				got, err := c.decodeString(want)
				checkBytes(t, got, src, err)
				if got := dataURLOneAllocation(c, "image/png", src); got != "data:image/png;base64,"+want {
					t.Fatalf("data URL length %d differs", n)
				}
			}
		})
	}
}

func BenchmarkCodec(b *testing.B) {
	for _, op := range []string{"Encode", "Decode"} {
		b.Run(op, func(b *testing.B) {
			for _, size := range sizes {
				b.Run(size.name, func(b *testing.B) {
					src := payload(size.n)
					encoded := []byte(std.StdEncoding.EncodeToString(src))
					for _, c := range codecs() {
						b.Run(c.name, func(b *testing.B) {
							b.ReportAllocs()
							// Both directions report original binary payload throughput.
							b.SetBytes(int64(len(src)))
							if op == "Encode" {
								dst := make([]byte, c.encodedLen(len(src)))
								c.encode(dst, src)
								checkBytes(b, dst, encoded, nil)
								b.ResetTimer()
								for i := 0; i < b.N; i++ {
									c.encode(dst, src)
								}
								b.StopTimer()
								checkBytes(b, dst, encoded, nil)
								sinkBytes = dst
							} else {
								// Some competitors require one extra byte for wide stores.
								// Give every codec the same sufficiently sized destination.
								dst := make([]byte, std.StdEncoding.DecodedLen(len(encoded))+8)
								n, err := c.decode(dst, encoded)
								checkBytes(b, dst[:n], src, err)
								b.ResetTimer()
								for i := 0; i < b.N; i++ {
									n, err = c.decode(dst, encoded)
								}
								b.StopTimer()
								checkBytes(b, dst[:n], src, err)
								sinkBytes = dst[:n]
							}
						})
					}
				})
			}
		})
	}
}

func BenchmarkNewAPI(b *testing.B) {
	for _, op := range []string{"EncodeToString", "DecodeString", "DataURLConcat", "DataURLDecode"} {
		for _, size := range sizes {
			for _, c := range codecs() {
				b.Run(fmt.Sprintf("%s/%s/%s", op, size.name, c.name), func(b *testing.B) {
					src := payload(size.n)
					encoded := std.StdEncoding.EncodeToString(src)
					// Image and audio inputs follow the same base64 path. Vary the
					// MIME label without implying these synthetic bytes are a file.
					mime := "image/png"
					if size.n <= 32<<10 {
						mime = "audio/wav"
					}
					url := "data:" + mime + ";base64," + encoded
					b.ReportAllocs()
					b.SetBytes(int64(len(src)))
					var gotString string
					var gotBytes []byte
					var err error
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						switch op {
						case "EncodeToString":
							gotString = c.encodeToString(src)
						case "DecodeString":
							gotBytes, err = c.decodeString(encoded)
						case "DataURLConcat":
							gotString = "data:" + mime + ";base64," + c.encodeToString(src)
						case "DataURLDecode":
							_, data, _ := strings.Cut(url, ",")
							gotBytes, err = c.decodeString(data)
						}
					}
					b.StopTimer()
					if op == "EncodeToString" && gotString != encoded || op == "DataURLConcat" && gotString != url {
						b.Fatal("encoded output differs")
					}
					if op == "DecodeString" || op == "DataURLDecode" {
						checkBytes(b, gotBytes, src, err)
					}
					sinkString, sinkBytes = gotString, gotBytes
				})
			}
		}
	}
}

// This optimized baseline gives every competitor the same one-allocation
// construction as moego.EncodeDataURL. It avoids attributing allocation savings
// to SIMD speed. The new byte slice is exclusively owned by the returned string.
func dataURLOneAllocation(c codec, mime string, src []byte) string {
	buf := make([]byte, len("data:")+len(mime)+len(";base64,")+c.encodedLen(len(src)))
	appendDataURL(c, buf[:0], mime, src)
	return unsafe.String(unsafe.SliceData(buf), len(buf))
}

func appendDataURL(c codec, dst []byte, mime string, src []byte) []byte {
	dst = append(dst, "data:"...)
	dst = append(dst, mime...)
	dst = append(dst, ";base64,"...)
	start := len(dst)
	dst = dst[:start+c.encodedLen(len(src))]
	c.encode(dst[start:], src)
	return dst
}

func BenchmarkDataURL(b *testing.B) {
	for _, op := range []string{"OneAllocation", "AppendReuse"} {
		for _, size := range sizes {
			for _, c := range codecs() {
				b.Run(fmt.Sprintf("%s/%s/%s", op, size.name, c.name), func(b *testing.B) {
					src := payload(size.n)
					const mime = "image/png"
					want := "data:" + mime + ";base64," + std.StdEncoding.EncodeToString(src)
					b.ReportAllocs()
					b.SetBytes(int64(len(src)))
					if op == "OneAllocation" {
						var got string
						encode := func(src []byte) string { return dataURLOneAllocation(c, mime, src) }
						if c.name == "Moego" {
							encode = func(src []byte) string { return moego.EncodeDataURL(mime, src) }
						}
						b.ResetTimer()
						for i := 0; i < b.N; i++ {
							got = encode(src)
						}
						b.StopTimer()
						if got != want {
							b.Fatal("data URL differs")
						}
						sinkString = got
					} else {
						dst := make([]byte, 0, len(want))
						appendURL := func(dst, src []byte) []byte { return appendDataURL(c, dst, mime, src) }
						if c.name == "Moego" {
							appendURL = func(dst, src []byte) []byte { return moego.AppendDataURL(dst, mime, src) }
						}
						b.ResetTimer()
						for i := 0; i < b.N; i++ {
							dst = appendURL(dst[:0], src)
						}
						b.StopTimer()
						checkBytes(b, dst, []byte(want), nil)
						sinkBytes = dst
					}
				})
			}
		}
	}
}

// Validation models new-api audio paths that only need the decoded length.
// Reuse also gives competitors a preallocated output to separate allocation
// savings from the cost of decoding into a full payload buffer.
func BenchmarkValidation(b *testing.B) {
	for _, op := range []string{"Allocate", "Reuse"} {
		for _, size := range sizes {
			for _, c := range codecs() {
				b.Run(fmt.Sprintf("%s/%s/%s", op, size.name, c.name), func(b *testing.B) {
					src := payload(size.n)
					encoded := std.StdEncoding.EncodeToString(src)
					encodedBytes := []byte(encoded)
					dst := make([]byte, std.StdEncoding.DecodedLen(len(encoded))+8)
					var validate func() (int, error)
					if c.name == "Moego" {
						validate = func() (int, error) { return moego.StdEncoding.ValidateString(encoded) }
					} else if op == "Reuse" {
						validate = func() (int, error) { return c.decode(dst, encodedBytes) }
					} else {
						validate = func() (int, error) {
							decoded, err := c.decodeString(encoded)
							return len(decoded), err
						}
					}
					b.ReportAllocs()
					b.SetBytes(int64(len(src)))
					b.ResetTimer()
					var n int
					var err error
					for i := 0; i < b.N; i++ {
						n, err = validate()
					}
					b.StopTimer()
					if err != nil || n != len(src) {
						b.Fatalf("validation: length=%d err=%v", n, err)
					}
				})
			}
		}
	}
}
