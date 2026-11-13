//go:build amd64 && !purego

package jsonsimd

// haveAES reports whether hashAES runs: the CPU has AES with OS support for
// XMM state, and AVX, which its VEX-encoded instructions need. AES alone is
// not enough: some SKUs ship AES-NI with AVX fused off, and hashAES starts
// with a VEX-encoded VMOVDQU.
var haveAES = cpuHasAES()

//go:noescape
func cpuHasAES() bool

//go:noescape
func hashAES(key *[24]uint64, p *byte, n int, tail *[128]byte, total int) uint64
