package codec

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MoeForge/moego/bench/json/race"
)

// Sizes are the body sizes of the request workloads.
var Sizes = race.Sizes

// RequestBody returns an image chat request of about n bytes.
func RequestBody(n int) []byte { return race.Body(n) }

// Chunks are stream chunks as upstreams send them: a content delta, a tool
// call delta, and the final chunk with usage.
var Chunks = []struct {
	Name string
	Body []byte
}{
	{"content", []byte(`{"id":"chatcmpl-B9MHDbslfkBeAs8l4bebGdFOJ6PeG","object":"chat.completion.chunk","created":1741569952,"model":"gpt-4o-2024-08-06","system_fingerprint":"fp_f9f4fb6dbf","choices":[{"index":0,"delta":{"content":" the capital of France is Paris, a city"},"logprobs":null,"finish_reason":null}],"usage":null}`)},
	{"tool", []byte(`{"id":"chatcmpl-B9MHDbslfkBeAs8l4bebGdFOJ6PeG","object":"chat.completion.chunk","created":1741569952,"model":"gpt-4o-2024-08-06","system_fingerprint":"fp_f9f4fb6dbf","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_DdmO9pD3xa9XTPNJ32zg2hcA","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Paris, France\"}"}}]},"logprobs":null,"finish_reason":null}],"usage":null}`)},
	{"usage", []byte(`{"id":"chatcmpl-B9MHDbslfkBeAs8l4bebGdFOJ6PeG","object":"chat.completion.chunk","created":1741569952,"model":"gpt-4o-2024-08-06","system_fingerprint":"fp_f9f4fb6dbf","choices":[],"usage":{"prompt_tokens":1117,"completion_tokens":46,"total_tokens":1163,"prompt_tokens_details":{"cached_tokens":1024,"text_tokens":0,"audio_tokens":0,"image_tokens":0},"completion_tokens_details":{"text_tokens":0,"audio_tokens":0,"image_tokens":0,"reasoning_tokens":0},"input_tokens":0,"output_tokens":0,"input_tokens_details":null,"claude_cache_creation_5_m_tokens":0,"claude_cache_creation_1_h_tokens":0}}`)},
}

// MapSizes are the body sizes of the map workloads.
var MapSizes = Sizes[:3]

// MapBody returns a request of about n bytes whose size comes from many
// messages and tool schemas rather than one long string, so map work
// dominates.
func MapBody(n int) []byte {
	var b strings.Builder
	b.WriteString(`{"model":"gpt-4o","stream":true,"temperature":0.7,"max_tokens":1024,"messages":[`)
	for i := 0; b.Len() < n*3/4; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		fmt.Fprintf(&b, `{"role":%q,"content":"Message %d: tell me about the weather in city number %d and include the forecast été for the week.","name":"u%d"}`, role, i, i*7, i)
	}
	b.WriteString(`],"tools":[`)
	for i := 0; b.Len() < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"type":"function","function":{"name":"tool_%d","description":"Looks up item %d.","parameters":{"type":"object","properties":{"q":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":%d}},"required":["q"]}}}`, i, i, 100+i)
	}
	b.WriteString(`],"metadata":{"user_id":"u-42","trace":{"span":"abc","tags":["x","y"]}}}`)
	return []byte(b.String())
}

// awsDropped are the members that the AWS pass-through removes.
var awsDropped = []string{"model", "stream"}

// AWSFlow decodes body into raw members, drops two of them and encodes the
// rest, as new-api's AWS pass-through does.
func AWSFlow(e Engine, body []byte) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := e.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	for _, k := range awsDropped {
		delete(m, k)
	}
	return e.Marshal(m)
}

// AnyRoundTrip decodes body into a map[string]any and encodes it again.
func AnyRoundTrip(e Engine, body []byte) ([]byte, error) {
	var m map[string]any
	if err := e.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	return e.Marshal(m)
}
