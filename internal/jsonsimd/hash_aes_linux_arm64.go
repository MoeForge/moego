//go:build arm64 && linux && !purego

package jsonsimd

import (
	"encoding/binary"
	"os"
)

func cpuHasAES() bool {
	const (
		atNull  = 0
		atHWCap = 16
		hwAES   = 1 << 3
	)
	b, err := os.ReadFile("/proc/self/auxv")
	if err != nil {
		return false
	}
	for i := 0; i+16 <= len(b); i += 16 {
		tag := binary.LittleEndian.Uint64(b[i:])
		if tag == atNull {
			break
		}
		if tag == atHWCap {
			return binary.LittleEndian.Uint64(b[i+8:])&hwAES != 0
		}
	}
	return false
}
