package codec

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type body struct {
	Name string
	Body []byte
}

// The bodies of W1t, W2, W5, W6 and W8.
var (
	textChat        = TextChatBody()
	longChats       = longChatBodies()
	claudeBodies    = []body{{"1KiB", ClaudeBody(1 << 10)}, {"1MiB", ClaudeBody(1 << 20)}}
	responsesBodies = []body{{"1KiB", ResponsesBody(1 << 10)}, {"1MiB", ResponsesBody(1 << 20)}}
	rawHeavyBodies  = []body{{"4KiB", RawHeavyBody(4 << 10)}, {"16KiB", RawHeavyBody(16 << 10)}}
	embeddingBodies = []body{{"1x1536", EmbeddingBody(1)}, {"16x1536", EmbeddingBody(16)}}
	formatBodies    = []body{{"1KiB", RequestBody(1 << 10)}, {"1MiB", RequestBody(1 << 20)}}
)

func longChatBodies() []body {
	var out []body
	for _, c := range LongChats {
		out = append(out, body{c.Name, LongChatBody(c.N)})
	}
	return out
}

// TestDumpLongChats writes the long-context bodies into the directory
// MOEGO_DUMP_LONG names, for tokens.py to count. MOEGO_DUMP_LONG_SIZES, a
// comma-separated list of byte sizes, writes bodies of those sizes instead.
func TestDumpLongChats(t *testing.T) {
	dir := os.Getenv("MOEGO_DUMP_LONG")
	if dir == "" {
		t.Skip("set MOEGO_DUMP_LONG to a directory")
	}
	bodies := longChats
	if sizes := os.Getenv("MOEGO_DUMP_LONG_SIZES"); sizes != "" {
		bodies = nil
		for _, f := range strings.Split(sizes, ",") {
			n, err := strconv.Atoi(f)
			if err != nil {
				t.Fatal(err)
			}
			bodies = append(bodies, body{fmt.Sprint(n), LongChatBody(n)})
		}
	}
	for _, b := range bodies {
		if err := os.WriteFile(filepath.Join(dir, "W1t-"+b.Name+".json"), b.Body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// checkDecode checks that every engine decodes body into a T equal to the
// one encoding/json decodes, and when encode is set, that it encodes that
// value as encoding/json does.
func checkDecode[T any](t *testing.T, name string, body []byte, encode bool) {
	t.Helper()
	std := Engines[1]
	want := decodeWith[T](t, std, body)
	var wantOut []byte
	if encode {
		wantOut = encodeWith(t, std, &want)
	}
	for _, e := range DecodeEngines {
		got := decodeWith[T](t, e, body)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s %s: decoded value differs", name, e.Name)
		}
		if encode {
			checkOutput(t, e, encodeWith(t, e, &want), wantOut)
		}
	}
}

// TestMoreOutputs is TestOutputs for W1t, W2, W5, W6 and W8.
func TestMoreOutputs(t *testing.T) {
	checkDecode[GeneralOpenAIRequest](t, "W1t", textChat, true)
	for _, b := range longChats {
		checkDecode[GeneralOpenAIRequest](t, "W1t "+b.Name, b.Body, true)
	}
	for _, b := range claudeBodies {
		checkDecode[ClaudeRequest](t, "W2 claude "+b.Name, b.Body, false)
	}
	for _, b := range responsesBodies {
		checkDecode[OpenAIResponsesRequest](t, "W2 responses "+b.Name, b.Body, false)
	}
	for _, b := range rawHeavyBodies {
		checkDecode[GeneralOpenAIRequest](t, "W5 "+b.Name, b.Body, true)
	}
	for _, b := range embeddingBodies {
		checkDecode[OpenAIEmbeddingResponse](t, "W6 "+b.Name, b.Body, true)
	}
	std := Engines[1]
	for _, b := range formatBodies {
		var wc, wi bytes.Buffer
		if err := std.Compact(&wc, b.Body); err != nil {
			t.Fatal(err)
		}
		if err := std.Indent(&wi, b.Body, "", "  "); err != nil {
			t.Fatal(err)
		}
		for _, e := range Engines {
			if !e.Valid(b.Body) {
				t.Errorf("W8 %s %s: not valid", b.Name, e.Name)
			}
			if e.Compact != nil {
				var c bytes.Buffer
				if err := e.Compact(&c, b.Body); err != nil || !bytes.Equal(c.Bytes(), wc.Bytes()) {
					t.Errorf("W8 compact %s %s: %v", b.Name, e.Name, err)
				}
			}
			if e.Indent != nil {
				var c bytes.Buffer
				if err := e.Indent(&c, b.Body, "", "  "); err != nil || !bytes.Equal(c.Bytes(), wi.Bytes()) {
					t.Errorf("W8 indent %s %s: %v", b.Name, e.Name, err)
				}
			}
		}
	}
}

// benchDecode measures decoding body into a T.
func benchDecode[T any](b *testing.B, e Engine, body []byte) {
	Use = e
	b.SetBytes(int64(len(body)))
	run(b, func() error {
		var v T
		return e.Unmarshal(body, &v)
	})
}

func BenchmarkW1t(b *testing.B) {
	for _, e := range DecodeEngines {
		b.Run("16KiB/"+e.Name, func(b *testing.B) { benchDecode[GeneralOpenAIRequest](b, e, textChat) })
	}
	for _, body := range longChats {
		for _, e := range DecodeEngines {
			b.Run(body.Name+"/"+e.Name, func(b *testing.B) { benchDecode[GeneralOpenAIRequest](b, e, body.Body) })
		}
	}
}

func BenchmarkW2(b *testing.B) {
	for _, body := range claudeBodies {
		for _, e := range DecodeEngines {
			b.Run("claude/"+body.Name+"/"+e.Name, func(b *testing.B) { benchDecode[ClaudeRequest](b, e, body.Body) })
		}
	}
	for _, body := range responsesBodies {
		for _, e := range DecodeEngines {
			b.Run("responses/"+body.Name+"/"+e.Name, func(b *testing.B) { benchDecode[OpenAIResponsesRequest](b, e, body.Body) })
		}
	}
}

// BenchmarkW5 decodes and encodes again a request whose size is in
// RawMessage members.
func BenchmarkW5(b *testing.B) {
	for _, body := range rawHeavyBodies {
		for _, e := range Engines {
			b.Run(body.Name+"/"+e.Name, func(b *testing.B) {
				Use = e
				b.SetBytes(int64(len(body.Body)))
				run(b, func() error {
					var v GeneralOpenAIRequest
					if err := e.Unmarshal(body.Body, &v); err != nil {
						return err
					}
					_, err := e.Marshal(&v)
					return err
				})
			})
		}
	}
}

func BenchmarkW6(b *testing.B) {
	for _, body := range embeddingBodies {
		var v OpenAIEmbeddingResponse
		Use = Engines[1]
		if err := Engines[1].Unmarshal(body.Body, &v); err != nil {
			b.Fatal(err)
		}
		for _, e := range Engines {
			b.Run("unmarshal/"+body.Name+"/"+e.Name, func(b *testing.B) { benchDecode[OpenAIEmbeddingResponse](b, e, body.Body) })
			b.Run("marshal/"+body.Name+"/"+e.Name, func(b *testing.B) {
				Use = e
				run(b, func() error {
					_, err := e.Marshal(&v)
					return err
				})
			})
		}
	}
}

func BenchmarkW8(b *testing.B) {
	for _, body := range formatBodies {
		for _, e := range Engines {
			b.Run("valid/"+body.Name+"/"+e.Name, func(b *testing.B) {
				b.SetBytes(int64(len(body.Body)))
				run(b, func() error {
					if !e.Valid(body.Body) {
						return errNotValid
					}
					return nil
				})
			})
			if e.Compact != nil {
				b.Run("compact/"+body.Name+"/"+e.Name, func(b *testing.B) {
					var dst bytes.Buffer
					b.SetBytes(int64(len(body.Body)))
					run(b, func() error {
						dst.Reset()
						return e.Compact(&dst, body.Body)
					})
				})
			}
			if e.Indent != nil {
				b.Run("indent/"+body.Name+"/"+e.Name, func(b *testing.B) {
					var dst bytes.Buffer
					b.SetBytes(int64(len(body.Body)))
					run(b, func() error {
						dst.Reset()
						return e.Indent(&dst, body.Body, "", "  ")
					})
				})
			}
		}
	}
}

var errNotValid = errors.New("not valid")
