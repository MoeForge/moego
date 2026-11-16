package deflate

const adlerMod = 65521

// adlerBlock is the most bytes summed before the sums must be reduced:
// the largest n with 255*n*(n+1)/2 + (n+1)*(adlerMod-1) < 1<<32.
const adlerBlock = 5552

// adler32Generic updates the Adler-32 checksum adler with p.
func adler32Generic(adler uint32, p []byte) uint32 {
	s1, s2 := adler&0xffff, adler>>16
	for len(p) > 0 {
		n := min(len(p), adlerBlock)
		q := p[:n]
		for len(q) >= 8 {
			s1 += uint32(q[0])
			s2 += s1
			s1 += uint32(q[1])
			s2 += s1
			s1 += uint32(q[2])
			s2 += s1
			s1 += uint32(q[3])
			s2 += s1
			s1 += uint32(q[4])
			s2 += s1
			s1 += uint32(q[5])
			s2 += s1
			s1 += uint32(q[6])
			s2 += s1
			s1 += uint32(q[7])
			s2 += s1
			q = q[8:]
		}
		for _, c := range q {
			s1 += uint32(c)
			s2 += s1
		}
		s1 %= adlerMod
		s2 %= adlerMod
		p = p[n:]
	}
	return s2<<16 | s1
}
