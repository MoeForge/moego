//go:build amd64 && !purego

package jsonsimd

// haveAES reports whether the CPU has the AES instructions of hashAES.
var haveAES = cpuHasAES()

//go:noescape
func cpuHasAES() bool

//go:noescape
func hashAES(key *[24]uint64, p *byte, n int, tail *[128]byte, total int) uint64
