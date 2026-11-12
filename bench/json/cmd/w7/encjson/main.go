// Command encjson runs a W7 scenario with encoding/json, the one JSON
// engine it links. See github.com/MoeForge/moego/bench/json/w7.
package main

import (
	"encoding/json"
	"io"

	"github.com/MoeForge/moego/bench/json/w7"
)

func main() {
	w7.Main(w7.Engine{
		Name:       "encjson",
		Marshal:    json.Marshal,
		Unmarshal:  json.Unmarshal,
		Valid:      json.Valid,
		NewDecoder: func(r io.Reader) w7.Decoder { return json.NewDecoder(r) },
	})
}
