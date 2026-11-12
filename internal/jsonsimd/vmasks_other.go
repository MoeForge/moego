//go:build (!amd64 && !arm64) || purego

package jsonsimd

// plainMasks is false: Validate reads blocks only with AVX2.
const plainMasks = false

// validMasks is not called: Validate reads blocks only with AVX2.
func validMasks(s string, i int, out *[validBatch]vmask, tail *[64]byte, hi *[validBatch]uint64) int {
	panic("jsonsimd: validMasks without AVX2")
}
