//go:build amd64 && !purego

package jsonsimd

// HaveAVX2 reports whether the AVX2 kernels run: the CPU has AVX2 with OS
// support for YMM state, and the BMI1, BMI2, POPCNT and PCLMULQDQ
// instructions the kernels also use.
var HaveAVX2 = cpuHasAVX2()

//go:noescape
func cpuHasAVX2() bool
