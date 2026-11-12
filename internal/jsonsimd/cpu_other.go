//go:build !amd64 || purego

package jsonsimd

// HaveAVX2 is false: the portable code runs.
const HaveAVX2 = false
