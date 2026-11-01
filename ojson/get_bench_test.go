package ojson

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
)

// benchBody is an indented chat request of about n bytes whose last message
// embeds a base64 image, like the bodies of bench/json/race.
func benchBody(n int) []byte {
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
	raw := make([]byte, max(4, n-len(head)-len(tail))*3/4)
	rand.New(rand.NewSource(int64(n))).Read(raw)
	return []byte(head + base64.StdEncoding.EncodeToString(raw) + tail)
}

var benchFrame = []byte(`{"id":"chatcmpl-9x","object":"chat.completion.chunk","created":1730000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}],"usage":null}`)

var (
	benchSSE   = []string{"choices.0.delta.content", "choices.0.finish_reason", "usage"}
	benchSix   = []string{"service_tier", "inference_geo", "store", "safety_identifier", "stream_options.include_obfuscation", "speed"}
	benchDeep  = "messages.1.content.1.image_url.detail"
	benchModel = []string{"model", "group"}
	sinkResult Result
	sinkMany   []Result
)

func BenchmarkGet(b *testing.B) {
	body := benchBody(1 << 10)
	b.Run("deep/bytes", func(b *testing.B) {
		for range b.N {
			sinkResult = GetBytes(body, benchDeep)
		}
	})
	b.Run("deep/nocopy", func(b *testing.B) {
		for range b.N {
			sinkResult = GetBytesNoCopy(body, benchDeep)
		}
	})
	for _, p := range []string{"model", "group", "stream", "temperature", "max_tokens", "metadata", "messages", "messages.1", "messages.1.content", "messages.1.content.1", "messages.1.content.1.image_url", "messages.1.content.1.image_url.url", benchDeep} {
		b.Run("steps/"+p, func(b *testing.B) {
			for range b.N {
				sinkResult = GetBytesNoCopy(body, p)
			}
		})
	}
	var compact bytes.Buffer
	json.Compact(&compact, body)
	b.Run("deep/compact", func(b *testing.B) {
		c := compact.Bytes()
		for range b.N {
			sinkResult = GetBytesNoCopy(c, benchDeep)
		}
	})
	b.Run("sse1/bytes", func(b *testing.B) {
		for range b.N {
			sinkResult = GetBytes(benchFrame, benchSSE[0])
		}
	})
	for _, c := range []struct {
		name  string
		body  []byte
		paths []string
	}{{"sse3", benchFrame, benchSSE}, {"six", body, benchSix}, {"model", body, benchModel}} {
		b.Run(c.name+"/many", func(b *testing.B) {
			for range b.N {
				sinkMany = GetManyBytes(c.body, c.paths...)
			}
		})
		b.Run(c.name+"/nocopy", func(b *testing.B) {
			dst := make([]Result, len(c.paths))
			for range b.N {
				dst = GetManyBytesNoCopy(dst, c.body, c.paths...)
			}
			sinkMany = dst
		})
		b.Run(c.name+"/each", func(b *testing.B) {
			for range b.N {
				for _, p := range c.paths {
					sinkResult = GetBytes(c.body, p)
				}
			}
		})
	}
}

var sinkInt int

func BenchmarkContainerEnd(b *testing.B) {
	body := string(benchBody(1 << 10))
	for _, c := range []struct{ name, at string }{
		{"small", `{"role": "system"`},
		{"metadata", `{"user_id"`},
		{"messages", "[\n    {"},
	} {
		i := strings.Index(body, c.at)
		b.Run(c.name+"/fast", func(b *testing.B) {
			for range b.N {
				sinkInt = containerEndFast(body, i)
			}
		})
		b.Run(c.name+"/scalar", func(b *testing.B) {
			for range b.N {
				sinkInt = containerEndScalar(body, i)
			}
		})
	}
	b.Run("string/scan", func(b *testing.B) {
		i := strings.Index(body, "data:image")
		for range b.N {
			sinkInt, _ = scanString(body, i)
		}
	})
	b.Run("key/scan", func(b *testing.B) {
		i := strings.Index(body, "temperature")
		for range b.N {
			sinkInt, _ = scanString(body, i)
		}
	})
	b.Run("space/skip", func(b *testing.B) {
		i := strings.Index(body, "\n  \"group")
		for range b.N {
			sinkInt = skipSpace(body, i)
		}
	})
	b.Run("path/comps", func(b *testing.B) {
		for range b.N {
			for pi := 0; pi >= 0; {
				_, pi, _ = getComp(benchDeep, pi, pi)
			}
		}
	})
}
