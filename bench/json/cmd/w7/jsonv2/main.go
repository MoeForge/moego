//go:build goexperiment.jsonv2 && go1.27

// Command jsonv2 runs a W7 scenario with encoding/json/v2, the one JSON
// engine it links, with the options of the matrix's jsonv2 engine. It
// decodes the file with UnmarshalRead. See github.com/MoeForge/moego/bench/json/w7.
package main

import (
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"io"

	"github.com/MoeForge/moego/bench/json/w7"
)

// marshalOptions is the one option that encoding/json/v2 needs to encode
// the values encoding/json does: the types' omitempty members include
// numbers and bools, which encoding/json leaves out when they are zero.
var marshalOptions = json.OmitEmptyWithLegacySemantics(true)

// decoder decodes the file with UnmarshalRead.
type decoder struct{ r io.Reader }

func (d decoder) Decode(v any) error { return jsonv2.UnmarshalRead(d.r, v) }

func main() {
	w7.Main(w7.Engine{
		Name:       "jsonv2",
		Marshal:    func(v any) ([]byte, error) { return jsonv2.Marshal(v, marshalOptions) },
		Unmarshal:  func(data []byte, v any) error { return jsonv2.Unmarshal(data, v) },
		Valid:      func(data []byte) bool { return jsontext.Value(data).IsValid() },
		NewDecoder: func(r io.Reader) w7.Decoder { return decoder{r} },
	})
}
