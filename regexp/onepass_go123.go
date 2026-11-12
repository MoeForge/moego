//go:build !go1.24

package regexp

// Before Go 1.24, a one-pass program reaches its match only through \z.
const onePassEndOnly = true
