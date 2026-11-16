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

// Where the kernels take over from the loops that read eight bytes at a
// time.
const (
	// strEndAfter is the number of words of a string that PlainRun and
	// ScanString read before strEnd looks for its end. The NEON kernel
	// costs more to start than the AVX2 one: it pays from 128 bytes on.
	strEndAfter = 16
	// CopyShortMin is the length of the shortest string that package json
	// copies with CopyShort's kernel. Below 16 bytes the words are faster
	// than its masked store.
	CopyShortMin = 16
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
