// Package jsonsimd holds the JSON scanning kernels that moego's JSON
// packages share: CPU feature detection, the validator that also records
// container and long-string positions, and the blocks for quoting
// strings. Without the purego tag the kernels use AVX2 on amd64 when the
// CPU has it, and NEON on arm64; elsewhere portable code gives the same
// results.
package jsonsimd
