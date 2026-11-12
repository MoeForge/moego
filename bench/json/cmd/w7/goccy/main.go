// Command goccy runs a W7 scenario with goccy/go-json, the one JSON engine
// it links. See github.com/MoeForge/moego/bench/json/w7.
package main

import (
	"io"

	gojson "github.com/goccy/go-json"

	"github.com/MoeForge/moego/bench/json/w7"
)

func main() {
	w7.Main(w7.Engine{
		Name:       "goccy",
		Marshal:    gojson.Marshal,
		Unmarshal:  gojson.Unmarshal,
		Valid:      gojson.Valid,
		NewDecoder: func(r io.Reader) w7.Decoder { return gojson.NewDecoder(r) },
	})
}
