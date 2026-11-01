//go:build !amd64 || purego

package ojson

// validMasks is not called: validate reads blocks only with AVX2.
func validMasks(s string, i int, out *[validBatch]vmask, tail *[64]byte) int {
	panic("ojson: validMasks without AVX2")
}
