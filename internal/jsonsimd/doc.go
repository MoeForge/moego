// Package jsonsimd holds the JSON scanning kernels that moego's JSON
// packages share: CPU feature detection, the validator that also records
// container and long-string positions, and the AVX2 blocks for quoting
// strings. On amd64 without the purego tag the kernels use AVX2 when the
// CPU has it; elsewhere portable code gives the same results.
package jsonsimd
