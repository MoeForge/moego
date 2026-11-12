//go:build !go1.27

package json

// go127 reports whether the toolchain is Go 1.27 or later. Its
// encoding/json/v2 reports some syntax errors of a Decoder one byte later
// than the experiment in Go 1.25 and 1.26.
const go127 = false
