//go:build (amd64 || arm64) && !purego

package deflate

// The vector kernels sum strides of 32 bytes. For n bytes x[0..n) after
// the state (s1, s2):
//
//	s1' = s1 + sum x[k]
//	s2' = s2 + n*s1 + sum (n-k)*x[k]
//
// Write k = 32t + r: n-k = 32*(strides after t) + (32-r). A kernel keeps
// per lane the byte sums (part of sum x), before each stride their running
// total added up (whose sum, times 32, is the first part of the weighted
// sum) and the bytes times 32-r (the second part).

// adlerVecChunk is the most bytes one kernel call sums: 1024 strides keep
// every 32-bit lane below 1<<32.
const adlerVecChunk = 1 << 15

// adlerFold adds the lane sums of a kernel call over n bytes to the state.
// lanes holds eight lanes each of byte sums, running totals and weighted
// sums.
func adlerFold(s1, s2 uint32, n int, lanes *[24]uint32) (uint32, uint32) {
	var sa, sp, sb uint64
	for k := 0; k < 8; k++ {
		sa += uint64(lanes[k])
		sp += uint64(lanes[8+k])
		sb += uint64(lanes[16+k])
	}
	a := uint64(s1) + sa
	b := uint64(s2) + uint64(n)*uint64(s1) + 32*sp + sb
	return uint32(a % adlerMod), uint32(b % adlerMod)
}

// adlerSums sums n bytes at p, n a positive multiple of 32 up to
// adlerVecChunk, into lanes as adlerFold reads them: AVX2 on amd64, NEON
// on arm64 (which writes the first four lanes of each group).
//
//go:noescape
func adlerSums(p *byte, n int, lanes *[24]uint32)

// adler32Vec updates adler with p using adlerSums.
func adler32Vec(adler uint32, p []byte) uint32 {
	s1, s2 := adler&0xffff, adler>>16
	var lanes [24]uint32
	for len(p) >= 32 {
		n := min(len(p), adlerVecChunk) &^ 31
		adlerSums(&p[0], n, &lanes)
		s1, s2 = adlerFold(s1, s2, n, &lanes)
		p = p[n:]
	}
	return adler32Generic(s2<<16|s1, p)
}
