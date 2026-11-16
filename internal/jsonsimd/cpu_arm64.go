//go:build arm64 && !purego

package jsonsimd

import "strings"

// HaveAVX2 is false: this is not amd64.
const HaveAVX2 = false

// Per-feature NEON capabilities on arm64.
const (
	HaveQuote       = true
	HaveStrSkip     = true
	HaveValidBlocks = true
	HaveCount       = true
	HaveCopyShort   = true
	HaveEscape      = true
	HaveZero        = false
)

// useAES is cpuAES less what LimitCPU dropped.
var useAES = cpuAES

// LimitCPU checks drop and updates AES capability on arm64 for testing.
func LimitCPU(drop string) (undo func()) {
	var noAES bool
	for _, f := range strings.Split(drop, ",") {
		switch strings.TrimSpace(f) {
		case "", "avx2", "avx":
		case "aes":
			noAES = true
		default:
			panic("jsonsimd: LimitCPU: unknown feature " + f)
		}
	}
	aes := useAES
	useAES = useAES && !noAES
	hashKernel = pickHashARM64(useAES)
	return func() {
		useAES = aes
		hashKernel = pickHashARM64(aes)
	}
}
