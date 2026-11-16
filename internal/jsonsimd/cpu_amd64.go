//go:build amd64 && !purego

package jsonsimd

import "strings"

// HaveAVX2 reports whether the AVX2 kernels run: the CPU has AVX2 with OS
// support for YMM state, and the BMI1, BMI2, POPCNT and PCLMULQDQ
// instructions the kernels also use.
var HaveAVX2 = cpuHasAVX2()

// Per-feature SIMD capabilities on amd64, all gated on HaveAVX2.
var (
	HaveQuote       = HaveAVX2
	HaveStrSkip     = HaveAVX2
	HaveValidBlocks = HaveAVX2
	HaveCount       = HaveAVX2
	HaveCopyShort   = HaveAVX2
	HaveEscape      = HaveAVX2
	HaveZero        = HaveAVX2
)

// Where the kernels take over from the loops that read eight bytes at a
// time.
const (
	// strEndAfter is the number of words of a string that PlainRun and
	// ScanString read before strEnd looks for its end.
	strEndAfter = 2
	// CopyShortMin is the length of the shortest string that package json
	// copies with CopyShort's kernel.
	CopyShortMin = 1
)

// useAES and useAVX are cpuAES and cpuAVX less what LimitCPU dropped.
var useAES, useAVX = cpuAES, cpuAVX

// LimitCPU makes the kernels run as on a CPU without the features named in
// drop, a list of "avx2", "avx" and "aes" separated by commas, as well as
// those that calls not yet undone dropped, and returns a function that
// undoes it. A CPU without AVX has no AVX2 either. It is for tests of the
// paths such CPUs take, and must not run with them.
func LimitCPU(drop string) (undo func()) {
	var noAVX2, noAVX, noAES bool
	for _, f := range strings.Split(drop, ",") {
		switch strings.TrimSpace(f) {
		case "":
		case "avx2":
			noAVX2 = true
		case "avx":
			noAVX2, noAVX = true, true
		case "aes":
			noAES = true
		default:
			panic("jsonsimd: LimitCPU: unknown feature " + f)
		}
	}
	avx2, aes, avx := HaveAVX2, useAES, useAVX
	setAVX2 := func(v bool) {
		HaveAVX2 = v
		HaveQuote = v
		HaveStrSkip = v
		HaveValidBlocks = v
		HaveCount = v
		HaveCopyShort = v
		HaveEscape = v
		HaveZero = v
	}
	setAVX2(HaveAVX2 && !noAVX2)
	useAES, useAVX = useAES && !noAES, useAVX && !noAVX
	hashKernel = pickHash(useAES, useAVX)
	return func() {
		setAVX2(avx2)
		useAES, useAVX = aes, avx
		hashKernel = pickHash(aes, avx)
	}
}

//go:noescape
func cpuHasAVX2() bool
