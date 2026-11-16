package codec

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	mjson "github.com/MoeForge/moego/json"
)

type smallChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Content string `json:"content"`
	Done    bool   `json:"done"`
}

// largeNonFused has sizeof > 4096 bytes (fusedTargetMax) so Unmarshal takes
// the validateBlocks table-building path instead of the fused path.
type largeNonFused struct {
	ID      string     `json:"id"`
	Model   string     `json:"model"`
	Content string     `json:"content"`
	Items   []string   `json:"items"`
	Pad     [520]int64 `json:"-"`
}

func makeRawSlice(n int) []json.RawMessage {
	base := []byte(`{"id": 1, "role": "assistant", "content": "Hello\n\"world\"", "ok": true, "tags": ["a", "b", "c"]}`)
	out := make([]json.RawMessage, n)
	for i := range out {
		cp := make([]byte, len(base))
		copy(cp, base)
		out[i] = cp
	}
	return out
}

func makeEscapedText(target int) string {
	const unit = "Line 1: \"hello\" \\ path\\to\\file\n中文测试：这是一个包含换行、引号和反斜杠的段落。\t结束\n"
	var b strings.Builder
	b.Grow(target + len(unit))
	for b.Len() < target {
		b.WriteString(unit)
	}
	return b.String()
}

func makeLargeNonFusedBody() []byte {
	v := largeNonFused{
		ID:      "req-large-001",
		Model:   "gpt-4o",
		Content: makeEscapedText(4 << 10),
		Items:   make([]string, 32),
	}
	for i := range v.Items {
		v.Items[i] = "item-value-0123456789abcdef"
	}
	b, _ := json.Marshal(&v)
	return b
}

func BenchmarkValidExtra(b *testing.B) {
	cases := []body{
		{"300B", Chunks[1].Body},
		{"64KiB", RequestBody(64 << 10)},
		{"1MiB", RequestBody(1 << 20)},
	}
	for _, c := range cases {
		for _, e := range Engines[:2] {
			b.Run(c.Name+"/"+e.Name, func(b *testing.B) {
				b.SetBytes(int64(len(c.Body)))
				run(b, func() error {
					if !e.Valid(c.Body) {
						return errNotValid
					}
					return nil
				})
			})
		}
	}
}

func BenchmarkCompactIndentExtra(b *testing.B) {
	body64 := RequestBody(64 << 10)
	for _, e := range Engines[:2] {
		b.Run("compact/64KiB/"+e.Name, func(b *testing.B) {
			var dst bytes.Buffer
			b.SetBytes(int64(len(body64)))
			run(b, func() error {
				dst.Reset()
				return e.Compact(&dst, body64)
			})
		})
		b.Run("indent/64KiB/"+e.Name, func(b *testing.B) {
			var dst bytes.Buffer
			b.SetBytes(int64(len(body64)))
			run(b, func() error {
				dst.Reset()
				return e.Indent(&dst, body64, "", "  ")
			})
		})
	}
}

func BenchmarkMarshalExtra(b *testing.B) {
	rawSlice := makeRawSlice(64)
	rawBytes, _ := json.Marshal(rawSlice)
	for _, e := range Engines[:2] {
		b.Run("RawMessageSlice/"+e.Name, func(b *testing.B) {
			b.SetBytes(int64(len(rawBytes)))
			run(b, func() error {
				_, err := e.Marshal(rawSlice)
				return err
			})
		})
	}

	for _, sz := range []struct {
		name string
		n    int
	}{{"64KiB", 64 << 10}, {"1MiB", 1 << 20}} {
		raw := LongChatBody(sz.n)
		var req GeneralOpenAIRequest
		Use = Engines[1]
		if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
			b.Fatal(errUnmarshal)
		}
		for _, e := range Engines[:2] {
			b.Run("LongChat/"+sz.name+"/"+e.Name, func(b *testing.B) {
				Use = e
				b.SetBytes(int64(len(raw)))
				run(b, func() error {
					_, err := e.Marshal(&req)
					return err
				})
			})
		}
	}

	escStr := makeEscapedText(64 << 10)
	for _, e := range Engines[:2] {
		b.Run("EscapedString/64KiB/"+e.Name, func(b *testing.B) {
			b.SetBytes(int64(len(escStr)))
			run(b, func() error {
				_, err := e.Marshal(escStr)
				return err
			})
		})
	}
}

func BenchmarkUnmarshalExtra(b *testing.B) {
	decEngines := []Engine{Engines[0], NoCopy, Engines[1]}

	smallBody := []byte(`{"id":"chatcmpl-123456","object":"chat.completion.chunk","created":1741569952,"model":"gpt-4o-mini","content":"Hello world!","done":false}`)
	for _, e := range decEngines {
		b.Run("SmallStruct/~150B/"+e.Name, func(b *testing.B) {
			b.SetBytes(int64(len(smallBody)))
			run(b, func() error {
				var v smallChunk
				return e.Unmarshal(smallBody, &v)
			})
		})
	}

	largeBody := makeLargeNonFusedBody()
	for _, e := range decEngines {
		b.Run("LargeStruct/>4KiB/"+e.Name, func(b *testing.B) {
			b.SetBytes(int64(len(largeBody)))
			run(b, func() error {
				var v largeNonFused
				return e.Unmarshal(largeBody, &v)
			})
		})
	}

	mapBody := MapBody(64 << 10)
	for _, e := range decEngines {
		b.Run("MapAny/64KiB/"+e.Name, func(b *testing.B) {
			b.SetBytes(int64(len(mapBody)))
			run(b, func() error {
				var m map[string]any
				return e.Unmarshal(mapBody, &m)
			})
		})
	}

	escJSON, _ := json.Marshal(makeEscapedText(64 << 10))
	for _, e := range decEngines {
		b.Run("EscapedString/64KiB/"+e.Name, func(b *testing.B) {
			b.SetBytes(int64(len(escJSON)))
			run(b, func() error {
				var s string
				return e.Unmarshal(escJSON, &s)
			})
		})
	}
}

func BenchmarkDecoderExtra(b *testing.B) {
	mapBody := MapBody(64 << 10)
	longBody := LongChatBody(256 << 10)

	decRunners := []struct {
		name      string
		newDec    func(r io.Reader) interface{ Decode(v any) error }
		newNumDec func(r io.Reader) interface{ Decode(v any) error }
	}{
		{
			name: "moego",
			newDec: func(r io.Reader) interface{ Decode(v any) error } {
				return mjson.NewDecoder(r)
			},
			newNumDec: func(r io.Reader) interface{ Decode(v any) error } {
				d := mjson.NewDecoder(r)
				d.UseNumber()
				return d
			},
		},
		{
			name: "encjson",
			newDec: func(r io.Reader) interface{ Decode(v any) error } {
				return json.NewDecoder(r)
			},
			newNumDec: func(r io.Reader) interface{ Decode(v any) error } {
				d := json.NewDecoder(r)
				d.UseNumber()
				return d
			},
		},
	}

	for _, dr := range decRunners {
		b.Run("UseNumber/64KiB/"+dr.name, func(b *testing.B) {
			var r bytes.Reader
			b.SetBytes(int64(len(mapBody)))
			run(b, func() error {
				r.Reset(mapBody)
				var v any
				return dr.newNumDec(&r).Decode(&v)
			})
		})
		b.Run("BytesReader/256KiB/"+dr.name, func(b *testing.B) {
			var r bytes.Reader
			if dr.name == "moego" {
				Use = Engines[0]
			} else {
				Use = Engines[1]
			}
			b.SetBytes(int64(len(longBody)))
			run(b, func() error {
				r.Reset(longBody)
				var req GeneralOpenAIRequest
				return dr.newDec(&r).Decode(&req)
			})
		})
		longStr := string(longBody)
		b.Run("StringsReader/256KiB/"+dr.name, func(b *testing.B) {
			var r strings.Reader
			if dr.name == "moego" {
				Use = Engines[0]
			} else {
				Use = Engines[1]
			}
			b.SetBytes(int64(len(longStr)))
			run(b, func() error {
				r.Reset(longStr)
				var req GeneralOpenAIRequest
				return dr.newDec(&r).Decode(&req)
			})
		})
	}
}
