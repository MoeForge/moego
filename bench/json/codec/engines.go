// Package codec compares moego/json with encoding/json, encoding/json/v2
// (where the toolchain has it), sonic and goccy/go-json on new-api request
// and response types.
package codec

import (
	"bytes"
	"encoding/json"

	"github.com/bytedance/sonic"
	gojson "github.com/goccy/go-json"

	mjson "github.com/MoeForge/moego/json"
)

func init() {
	if sonic.APIKind != sonic.UseSonicJSON {
		panic("codec: sonic is not using its JIT implementation")
	}
}

// An Engine is one JSON implementation under test.
type Engine struct {
	Name      string
	Marshal   func(v any) ([]byte, error)
	Unmarshal func(data []byte, v any) error
	// Std is true when the engine promises encoding/json's output bytes.
	Std bool
	// Valid, Compact and Indent are nil when the engine has none.
	Valid   func(data []byte) bool
	Compact func(dst *bytes.Buffer, src []byte) error
	Indent  func(dst *bytes.Buffer, src []byte, prefix, indent string) error
}

// Engines are the implementations compared, moego first and encoding/json
// second. encoding/json/v2 comes last when the toolchain builds it.
var Engines = append([]Engine{
	{"moego", mjson.Marshal, mjson.Unmarshal, true, mjson.Valid, mjson.Compact, mjson.Indent},
	{"encjson", json.Marshal, json.Unmarshal, true, json.Valid, json.Compact, json.Indent},
	{"sonic-std", sonic.ConfigStd.Marshal, sonic.ConfigStd.Unmarshal, true, sonic.ConfigStd.Valid, nil, nil},
	{"sonic-default", sonic.ConfigDefault.Marshal, sonic.ConfigDefault.Unmarshal, false, sonic.ConfigDefault.Valid, nil, nil},
	{"goccy", gojson.Marshal, gojson.Unmarshal, true, gojson.Valid, gojson.Compact, gojson.Indent},
}, v2Engines...)

// NoCopy is moego's UnmarshalNoCopy, whose strings view the input as
// sonic Default's do. It has rows of its own in the decode workloads.
var NoCopy = Engine{"moego-nocopy", mjson.Marshal, mjson.UnmarshalNoCopy, true, nil, nil, nil}

// DecodeEngines are Engines and NoCopy.
var DecodeEngines = append(Engines[:len(Engines):len(Engines)], NoCopy)

// Use is the engine that the types' own MarshalJSON and UnmarshalJSON
// methods call.
var Use = Engines[1]
