//go:build (!amd64 && !arm64) || purego

package jsonsimd

func countMasks(s string, i int, out *[validBatch]cmask, tail *[64]byte) int {
	panic("jsonsimd: countMasks without SIMD")
}
