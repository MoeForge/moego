//go:build arm64 && darwin && !ios && !purego

package jsonsimd

// All Apple Silicon arm64 CPUs support ARMv8 FEAT_AES.
func cpuHasAES() bool { return true }
