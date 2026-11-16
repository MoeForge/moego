//go:build (!amd64 && !arm64) || purego

package jsonsimd

import "strings"

// HaveAVX2 is false: the portable code runs.
const HaveAVX2 = false

// Per-feature SIMD capabilities are all false in portable builds.
const (
	HaveQuote       = false
	HaveStrSkip     = false
	HaveValidBlocks = false
	HaveCount       = false
	HaveCopyShort   = false
	HaveEscape      = false
	HaveZero        = false
)

// LimitCPU does nothing but check drop: no kernel here needs the features
// it names.
func LimitCPU(drop string) (undo func()) {
	for _, f := range strings.Split(drop, ",") {
		switch strings.TrimSpace(f) {
		case "", "avx2", "avx", "aes":
		default:
			panic("jsonsimd: LimitCPU: unknown feature " + f)
		}
	}
	return func() {}
}
