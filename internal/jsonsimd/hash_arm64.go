//go:build arm64 && !purego

package jsonsimd

// cpuAES reports whether the CPU supports ARMv8 AES instructions.
var cpuAES = cpuHasAES()

// hashKernel is the kernel Sum runs from 128 bytes on.
var hashKernel = pickHashARM64(cpuAES)

func pickHashARM64(aes bool) int {
	if aes {
		return hashNEON
	}
	return hashNone
}

//go:noescape
func hashAESNEON(key *[24]uint64, p *byte, n int, tail *[128]byte, total int) uint64

// hashBlocks runs the ARMv8 AES kernel when hashKernel != hashNone.
func hashBlocks(key *[24]uint64, p *byte, n int, tail *[128]byte, total int) uint64 {
	return hashAESNEON(key, p, n, tail, total)
}
