// Package complex compares ojson with gjson and sjson on query paths:
// wildcards, '#' maps and queries, pipes and multipaths.
package complex

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/MoeForge/moego/bench/json/race"
	"github.com/MoeForge/moego/ojson"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// docs are the bodies measured: chat requests with an image (race.Body) and
// a chat completion list with many choices.
var docs = []struct {
	name string
	body []byte
}{
	{"req1KiB", race.Body(1 << 10)},
	{"req64KiB", race.Body(64 << 10)},
	{"req1MiB", race.Body(1 << 20)},
	{"list200", list(200)},
}

// list returns a response with n choices, each with a message and logprobs.
func list(n int) []byte {
	var b bytes.Buffer
	b.WriteString(`{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-4o","choices":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		role := "assistant"
		if i%7 == 3 {
			role = "tool"
		}
		fmt.Fprintf(&b, `{"index":%d,"message":{"role":%q,"content":"answer number %d with some text"},"finish_reason":"stop","logprobs":{"tokens":["a","b","c"],"score":%d.5}}`, i, role, i, i%10)
	}
	b.WriteString(`],"usage":{"prompt_tokens":12,"completion_tokens":34,"total_tokens":46}}`)
	return b.Bytes()
}

// getPaths are the query paths read from every document. Paths that do
// not apply to a document still run: a miss costs a full search.
var getPaths = []struct{ name, path string }{
	{"map", "messages.#.role"},
	{"query", `messages.#(role=="user").content.#(type=="image_url").image_url.detail`},
	{"queryAll", `messages.#(role=="system")#.content`},
	{"wild", "metadata.tr*.span"},
	{"multi", "{model,group,stream,temperature}"},
	{"pipe", "metadata|trace.tags.1"},
	{"choices", "choices.#.message.content"},
	{"choicesQuery", `choices.#(message.role=="tool")#.index`},
	{"count", "choices.#"},
}

// setPaths are the query paths set in every document, with the value set.
var setPaths = []struct {
	name, path string
	value      any
}{
	{"query", `messages.#(role=="system").content`, "Be brief."},
	{"map", "tools.#.type", "function"},
	{"queryAll", `choices.#(message.role=="tool")#.finish_reason`, "tool_calls"},
}

func TestSame(t *testing.T) {
	for _, d := range docs {
		s := string(d.body)
		for _, p := range getPaths {
			w, o := gjson.Get(s, p.path), ojson.Get(s, p.path)
			if w.Raw != o.Raw || w.Index != o.Index || fmt.Sprint(w.Indexes) != fmt.Sprint(o.Indexes) {
				t.Errorf("%s %s: got %.80q %d %v, want %.80q %d %v", d.name, p.name, o.Raw, o.Index, o.Indexes, w.Raw, w.Index, w.Indexes)
			}
		}
		for _, p := range setPaths {
			w, werr := sjson.SetBytes(d.body, p.path, p.value)
			o, oerr := ojson.SetBytes(d.body, p.path, p.value)
			if !bytes.Equal(w, o) || (werr == nil) != (oerr == nil) {
				t.Errorf("%s set %s: outputs differ", d.name, p.name)
			}
		}
	}
}

func BenchmarkGet(b *testing.B) {
	for _, d := range docs {
		s := string(d.body)
		for _, p := range getPaths {
			b.Run(d.name+"/"+p.name+"/ojson", func(b *testing.B) {
				b.SetBytes(int64(len(s)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					sink = ojson.Get(s, p.path).Raw
				}
			})
			b.Run(d.name+"/"+p.name+"/gjson", func(b *testing.B) {
				b.SetBytes(int64(len(s)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					sink = gjson.Get(s, p.path).Raw
				}
			})
		}
	}
}

func BenchmarkGetBytes(b *testing.B) {
	for _, d := range docs {
		for _, p := range getPaths {
			b.Run(d.name+"/"+p.name+"/ojson", func(b *testing.B) {
				b.SetBytes(int64(len(d.body)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					sink = ojson.GetBytes(d.body, p.path).Raw
				}
			})
			b.Run(d.name+"/"+p.name+"/gjson", func(b *testing.B) {
				b.SetBytes(int64(len(d.body)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					sink = gjson.GetBytes(d.body, p.path).Raw
				}
			})
		}
	}
}

func BenchmarkSet(b *testing.B) {
	for _, d := range docs {
		for _, p := range setPaths {
			b.Run(d.name+"/"+p.name+"/ojson", func(b *testing.B) {
				b.SetBytes(int64(len(d.body)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					out, _ := ojson.SetBytes(d.body, p.path, p.value)
					sinkB = out
				}
			})
			b.Run(d.name+"/"+p.name+"/sjson", func(b *testing.B) {
				b.SetBytes(int64(len(d.body)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					out, _ := sjson.SetBytes(d.body, p.path, p.value)
					sinkB = out
				}
			})
		}
	}
}

var (
	sink  string
	sinkB []byte
)
