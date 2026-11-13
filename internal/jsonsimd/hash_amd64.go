//go:build amd64 && !purego

package jsonsimd

// cpuAES and cpuAVX report what the CPU has: the AES instructions, and AVX
// with OS support for YMM state, without which VEX encodings fault even on
// 128-bit registers.
var cpuAES, cpuAVX = cpuHasAES(), cpuHasAVX()

// hashKernel is the kernel Sum runs from 128 bytes on.
var hashKernel = pickHash(cpuAES, cpuAVX)

// pickHash returns the kernel to run with the AES instructions and AVX, if
// the CPU has them: hashAES, which is VEX-encoded, with both, and
// hashAESSSE, which gives the same sums, with AES alone.
func pickHash(aes, avx bool) int {
	switch {
	case aes && avx:
		return hashVEX
	case aes:
		return hashSSE
	}
	return hashNone
}

//go:noescape
func cpuHasAES() bool

//go:noescape
func cpuHasAVX() bool

//go:noescape
func hashAES(key *[24]uint64, p *byte, n int, tail *[128]byte, total int) uint64

//go:noescape
func hashAESSSE(key *[24]uint64, p *byte, n int, tail *[128]byte, total int) uint64

// hashBlocks runs hashKernel, which is not hashNone.
func hashBlocks(key *[24]uint64, p *byte, n int, tail *[128]byte, total int) uint64 {
	if hashKernel == hashVEX {
		return hashAES(key, p, n, tail, total)
	}
	return hashAESSSE(key, p, n, tail, total)
}
