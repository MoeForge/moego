//go:build amd64 && !purego

package regexp

// limitCPU makes the matchers run as on a CPU without the features that
// drop names, and returns a function that undoes it. Compiled expressions
// choose some of their scans when they are compiled, so it empties the
// compile cache, and so does the function it returns: expressions
// compiled after either call take the scans of the CPU it sets.
func limitCPU(drop string) (undo func()) {
	avx2, pair, span := haveAVX2, hasPairKernel, hasSpanKernel
	if dropsAVX2(drop) {
		haveAVX2, hasPairKernel, hasSpanKernel = false, false, false
		SetCacheSize(SetCacheSize(0))
	}
	return func() {
		if haveAVX2 != avx2 || hasPairKernel != pair || hasSpanKernel != span {
			haveAVX2, hasPairKernel, hasSpanKernel = avx2, pair, span
			SetCacheSize(SetCacheSize(0))
		}
	}
}
