//go:build arm64 && !(darwin && !ios) && !linux && !purego

package jsonsimd

func cpuHasAES() bool { return false }
