// Command moego runs a W7 scenario with moego/json, the one JSON engine
// it links. See github.com/MoeForge/moego/bench/json/w7.
package main

import (
	"io"

	"github.com/MoeForge/moego/bench/json/w7"
	mjson "github.com/MoeForge/moego/json"
)

func main() {
	w7.Main(w7.Engine{
		Name:       "moego",
		Marshal:    mjson.Marshal,
		Unmarshal:  mjson.Unmarshal,
		Valid:      mjson.Valid,
		NewDecoder: func(r io.Reader) w7.Decoder { return mjson.NewDecoder(r) },
	})
}
