//go:build goexperiment.jsonv2

package json

// jsonv2 reports whether encoding/json is built on encoding/json/v2, as it
// is by default from Go 1.27 and with GOEXPERIMENT=jsonv2 from Go 1.25.
// The package then gives the results of that encoding/json.
const jsonv2 = true

// invalidUTF8 is what Marshal writes for a byte that is not part of valid
// UTF-8 in a string.
const invalidUTF8 = "\uFFFD"
