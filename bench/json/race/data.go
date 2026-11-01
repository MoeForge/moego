// Package race compares ojson with gjson, sjson, sonic and encoding/json on
// new-api style request bodies.
package race

import (
	"encoding/base64"
	"fmt"
	"math/rand"
	"strings"
)

// Sizes are the target body sizes of the image request workloads.
var Sizes = []struct {
	Name string
	N    int
}{{"1KiB", 1 << 10}, {"64KiB", 64 << 10}, {"1MiB", 1 << 20}, {"8MiB", 8 << 20}}

// Body returns a chat request with indented formatting whose last user
// message embeds a base64 PNG data URL sized so the body is about n bytes.
// Bodies are deterministic.
func Body(n int) []byte {
	head := `{
  "model": "gpt-4o",
  "group": "default",
  "stream": true,
  "temperature": 0.7,
  "max_tokens": 1024,
  "metadata": {"user_id": "u-42", "trace": {"span": "abc", "tags": ["x", "y"]}},
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": [
      {"type": "text", "text": "What is in this image?"},
      {"type": "image_url", "image_url": {"url": "data:image/png;base64,`
	tail := `", "detail": "high"}}
    ]}
  ],
  "tools": [{"type": "function", "function": {"name": "lookup", "parameters": {"type": "object", "properties": {"q": {"type": "string"}}}}}]
}`
	img := n - len(head) - len(tail)
	if img < 4 {
		img = 4
	}
	raw := make([]byte, img*3/4)
	r := rand.New(rand.NewSource(int64(n)))
	r.Read(raw)
	return []byte(head + base64.StdEncoding.EncodeToString(raw) + tail)
}

// Counts are the numbers of sequential overrides measured.
var Counts = []int{1, 5, 20, 50}

// Override is one parameter override: set a value, or delete when Del is
// true. Parts is Path split at dots, for the map and AST competitors.
type Override struct {
	Path  string
	Parts []string
	Value any
	Del   bool
}

// Overrides returns k sequential overrides. Most set top-level sampling
// parameters, a few touch nested paths, and some delete; later edits may
// overwrite earlier ones, as channel overrides do.
func Overrides(k int) []Override {
	base := []Override{
		{Path: "temperature", Value: 0.2},
		{Path: "max_tokens", Value: 4096},
		{Path: "top_p", Value: 0.9},
		{Path: "metadata.trace.span", Value: "override"},
		{Path: "stream", Del: true},
		{Path: "presence_penalty", Value: 0.1},
		{Path: "frequency_penalty", Value: 0.2},
		{Path: "messages.0.content", Value: "You are terse."},
		{Path: "metadata.user_id", Value: "u-override"},
		{Path: "stream_options.include_usage", Value: true},
	}
	out := make([]Override, 0, k)
	for i := 0; i < k; i++ {
		o := base[i%len(base)]
		if i >= len(base) {
			switch i % 3 {
			case 0:
				o = Override{Path: fmt.Sprintf("extra_%d", i), Value: i}
			case 1:
				o = Override{Path: "temperature", Value: float64(i) / 100}
			default:
				o = Override{Path: fmt.Sprintf("metadata.k%d", i), Value: "v"}
			}
		}
		o.Parts = strings.Split(o.Path, ".")
		out = append(out, o)
	}
	return out
}

// ReadPaths are the read workloads: routing reads model and group; the
// disabled-field check reads six fields, most of them absent; the deep read
// walks into the message list.
var ReadPaths = []struct {
	Name  string
	Paths []string
}{
	{"model+group", []string{"model", "group"}},
	{"six-field", []string{"service_tier", "inference_geo", "store", "safety_identifier", "stream_options.include_obfuscation", "speed"}},
	{"deep", []string{"messages.1.content.1.image_url.detail"}},
}

// SSEFrame returns a small streamed chunk like those relayed per token.
func SSEFrame() []byte {
	return []byte(`{"id":"chatcmpl-9x","object":"chat.completion.chunk","created":1730000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}],"usage":null}`)
}

// SSEPaths are the fields read from each frame.
var SSEPaths = []string{"choices.0.delta.content", "choices.0.finish_reason", "usage"}

// SSEPath1 is the single most frequent read.
const SSEPath1 = "choices.0.delta.content"
