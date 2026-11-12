//go:build !goexperiment.jsonv2 || !go1.27

package codec

// v2Engines is empty: this toolchain has no encoding/json/v2, or it is
// older than Go 1.27, where encoding/json/v2 is experimental.
var v2Engines []Engine
