//go:build !amd64 || purego

package jsonsimd

func countMasks(s string, i int, out *[validBatch]cmask, tail *[64]byte) int {
	panic("jsonsimd: countMasks without AVX2")
}

func strSkip(s string, i int) (int, bool) { return i, false }

func strSkipHTML(s string, i int) (int, bool) { return i, false }

func strSkipBytes(s string, i int) (int, bool) { return i, false }

func strEnd(s string, i int) (int, bool) { return i, false }

func htmlSkip(s string, i int) int { return i }
