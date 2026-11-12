package codec

import (
	"encoding/base64"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
)

// image returns base64 text of about n bytes, as an inline image.
func image(n int) string {
	raw := make([]byte, max(n, 4)*3/4)
	rand.New(rand.NewSource(int64(n))).Read(raw)
	return base64.StdEncoding.EncodeToString(raw)
}

// weatherTool is a tool schema as clients send it.
const weatherTool = `{"type":"object","properties":{"location":{"type":"string","description":"City and country, e.g. Paris, France"},"unit":{"type":"string","enum":["celsius","fahrenheit"]},"days":{"type":"integer","minimum":1,"maximum":14}},"required":["location"],"additionalProperties":false}`

// TextChatBody returns a text-only chat request of 20 turns with tools, of
// about 16 KiB.
func TextChatBody() []byte {
	var b strings.Builder
	b.WriteString(`{"model":"gpt-4o","stream":true,"temperature":0.3,"max_tokens":2048,"messages":[{"role":"system","content":"You are a careful assistant. Answer briefly and cite sources when you can."}`)
	for i := 0; i < 20; i++ {
		role := "user"
		text := fmt.Sprintf("Question %d: what changed in the release notes for version 1.%d, and does it affect \"streaming\" clients?\nList the items.", i, i)
		if i%2 == 1 {
			role = "assistant"
			text = fmt.Sprintf("In 1.%d the server now flushes each chunk at once, retries upstream timeouts twice, and reports usage in the last chunk.\n- Streaming clients see the first token sooner.\n- Long responses no longer stall at the 60 s proxy limit.\n- Token counts include cached prompt tokens (été, 東京).\nNo client change is needed unless you parse the \"usage\" field yourself; then read prompt_tokens_details as well.", i)
		}
		fmt.Fprintf(&b, `,{"role":%q,"content":%s}`, role, strconv.Quote(text))
	}
	b.WriteString(`],"tools":[`)
	for i := 0; i < 6; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"type":"function","function":{"name":"tool_%d","description":"Looks up release %d and returns its notes as Markdown.","parameters":%s}}`, i, i, weatherTool)
	}
	b.WriteString(`],"tool_choice":"auto","stream_options":{"include_usage":true}}`)
	return []byte(b.String())
}

// ClaudeBody returns a Claude Messages request of about n bytes, the size
// coming from an inline image.
func ClaudeBody(n int) []byte {
	head := `{"model":"claude-sonnet-4-5","max_tokens":4096,"stream":true,"system":[{"type":"text","text":"You are a vision assistant. Describe what you see.","cache_control":{"type":"ephemeral"}}],"thinking":{"type":"enabled","budget_tokens":2048},"metadata":{"user_id":"u-42"},"tools":[{"name":"get_weather","description":"Gets the weather.","input_schema":` + weatherTool + `}],"messages":[{"role":"user","content":[{"type":"text","text":"What is in this image?"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"`
	tail := `"}}]}]}`
	return []byte(head + image(n-len(head)-len(tail)) + tail)
}

// ResponsesBody returns an OpenAI Responses request of about n bytes, the
// size coming from an inline image.
func ResponsesBody(n int) []byte {
	head := `{"model":"gpt-4.1","instructions":"You are a vision assistant.","reasoning":{"effort":"medium","summary":"auto"},"stream":true,"store":false,"metadata":{"user_id":"u-42"},"text":{"format":{"type":"text"}},"tools":[{"type":"function","name":"get_weather","parameters":` + weatherTool + `}],"input":[{"role":"user","content":[{"type":"input_text","text":"What is in this image?"},{"type":"input_image","image_url":"data:image/png;base64,`
	tail := `"}]}]}`
	return []byte(head + image(n-len(head)-len(tail)) + tail)
}

// RawHeavyBody returns a chat request of about n bytes whose size is in
// members kept as RawMessage: tool schemas, a response schema, metadata
// and extra_body.
func RawHeavyBody(n int) []byte {
	var b strings.Builder
	b.WriteString(`{"model":"gpt-4o","messages":[{"role":"user","content":"Plan a trip."}],"metadata":{"user_id":"u-42","trace":{"span":"abc","tags":["x","y"]}},"extra_body":{"provider":{"order":["a","b"],"allow_fallbacks":true},"transforms":["middle-out"]},"response_format":{"type":"json_schema","json_schema":{"name":"plan","strict":true,"schema":` + weatherTool + `}},"tools":[`)
	for i := 0; b.Len() < n-64; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"type":"function","function":{"name":"tool_%d","description":"Tool %d.","parameters":{"type":"object","properties":{`, i, i)
		for k := 0; k < 6; k++ {
			if k > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `"p%d":{"type":"string","description":"Parameter %d of tool %d."}`, k, k, i)
		}
		b.WriteString(`},"required":["p0"]}}}`)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

// EmbeddingBody returns an embedding response of k vectors of 1536 floats,
// written as float32 values are.
func EmbeddingBody(k int) []byte {
	r := rand.New(rand.NewSource(int64(k)))
	var b strings.Builder
	b.WriteString(`{"object":"list","data":[`)
	for i := 0; i < k; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"object":"embedding","index":%d,"embedding":[`, i)
		for j := 0; j < 1536; j++ {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.FormatFloat(float64(float32(r.NormFloat64()*0.03)), 'g', -1, 32))
		}
		b.WriteString(`]}`)
	}
	b.WriteString(`],"model":"text-embedding-3-small","usage":{"prompt_tokens":8,"total_tokens":8}}`)
	return []byte(b.String())
}
