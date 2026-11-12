// Command sonic-default runs a W7 scenario with sonic's ConfigDefault, the one JSON
// engine it links. See github.com/MoeForge/moego/bench/json/w7.
package main

import (
	"io"

	"github.com/bytedance/sonic"

	"github.com/MoeForge/moego/bench/json/w7"
)

func main() {
	if sonic.APIKind != sonic.UseSonicJSON {
		panic("sonic is not using its JIT implementation")
	}
	api := sonic.ConfigDefault
	w7.Main(w7.Engine{
		Name:       "sonic-default",
		Marshal:    api.Marshal,
		Unmarshal:  api.Unmarshal,
		Valid:      api.Valid,
		NewDecoder: func(r io.Reader) w7.Decoder { return api.NewDecoder(r) },
	})
}
