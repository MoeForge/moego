//go:build (!arm64 && !amd64) || purego

package base64

// Without SIMD kernels every input uses encoding/base64.
const encodeMin, decodeMin = maxInt, maxInt

const maxInt = int(^uint(0) >> 1)

func encodeBlocks(dst, src []byte, alphabet *[64]byte) int { return 0 }
func decodeBlocks(dst, src []byte, url bool) int           { return 0 }
