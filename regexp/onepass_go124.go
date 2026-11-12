//go:build go1.24

package regexp

// From Go 1.24 on, a one-pass program without alternations may end without
// \z.
const onePassEndOnly = false
