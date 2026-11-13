//go:build !amd64 || purego

package jsonsimd

const hashKernel = hashNone

func hashBlocks(key *[24]uint64, p *byte, n int, tail *[128]byte, total int) uint64 {
	panic("jsonsimd: no AES")
}
