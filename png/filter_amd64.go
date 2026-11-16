//go:build amd64 && !purego

package png

// haveAVX2 reports whether the AVX2 kernels run: the CPU has AVX2 and the
// operating system saves YMM state.
var haveAVX2 = cpuHasAVX2()

//go:noescape
func cpuHasAVX2() bool

//go:noescape
func costsAVX2(cur, prev *byte, n, bpp int, c *[5]uint64) int

//go:noescape
func filterAVX2(dst, cur, prev *byte, n, bpp, f int) int

// filterCosts returns the cost of each filter for row cur below prev.
func filterCosts(cur, prev []byte, bpp int) (c [5]uint64) {
	n := len(cur)
	prev = prev[:n]
	if !haveAVX2 || n < bpp+32 {
		costRange(cur, prev, bpp, 0, n, &c)
		return c
	}
	end := costsAVX2(&cur[0], &prev[0], n, bpp, &c)
	costRange(cur, prev, bpp, 0, bpp, &c)
	costRange(cur, prev, bpp, end, n, &c)
	return c
}

// filterRow writes filter f of row cur below prev into dst.
func filterRow(dst, cur, prev []byte, bpp int, f uint8) {
	n := len(cur)
	dst, prev = dst[:n], prev[:n]
	if !haveAVX2 || n < bpp+32 || f == ftNone {
		filterRange(dst, cur, prev, bpp, 0, n, f)
		return
	}
	end := filterAVX2(&dst[0], &cur[0], &prev[0], n, bpp, int(f))
	filterRange(dst, cur, prev, bpp, 0, bpp, f)
	filterRange(dst, cur, prev, bpp, end, n, f)
}
