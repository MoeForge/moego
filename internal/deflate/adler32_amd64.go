//go:build amd64 && !purego

package deflate

// haveAVX2 reports whether the AVX2 kernel runs: the CPU has AVX2 and the
// operating system saves YMM state.
var haveAVX2 = cpuHasAVX2()

//go:noescape
func cpuHasAVX2() bool

func adler32(adler uint32, p []byte) uint32 {
	if !haveAVX2 || len(p) < 64 {
		return adler32Generic(adler, p)
	}
	return adler32Vec(adler, p)
}
