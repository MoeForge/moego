//go:build !goexperiment.jsonv2

package ojson

// invalidUTF8 is what encoding/json, and so sjson, writes for a byte that
// is not part of valid UTF-8 in a string: the escape, or U+FFFD itself
// when encoding/json is built on encoding/json/v2.
const invalidUTF8 = `\ufffd`
