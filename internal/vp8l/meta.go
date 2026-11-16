package vp8l

import (
	"math"
	"math/bits"
)

// Meta prefix codes. The main image is split into blocks of 1<<b pixels
// squared; each block's symbols are counted, and blocks with alike
// statistics are grouped so that each group gets its own prefix codes.
// The groups start from bins of blocks with similar per-channel entropy,
// pairs of groups are merged while a merge saves estimated bits, and each
// block then moves to the group that codes it cheapest, a few rounds. The
// result is kept only when its exact size, entropy image included, beats
// one group.

const (
	// maxBlocks bounds the blocks of one clustering.
	maxBlocks = 1 << 14
	// bandBudget bounds the dense histograms of one row of blocks.
	bandBudget = 1 << 21
	// entryCountBits is the width of a count in a sparse entry; a block
	// holds at most 1<<18 pixels.
	entryCountBits = 20
	// maxBins bounds the entropy bins per channel of the first clusters.
	maxBins = 6
	// maxMetaBits bounds the block size bits tried: a block's counts
	// fit in a uint16.
	maxMetaBits = 7
)

// blockWalker follows the block of a position moving through an image in
// scan-line order.
type blockWalker struct {
	x, y, w  int
	bits, mw int
	row      int // the first block of the current row of blocks
	ymask    int
}

func (bw *blockWalker) init(w, bits, mw int) {
	*bw = blockWalker{w: w, bits: bits, mw: mw, ymask: 1<<bits - 1}
}

func (bw *blockWalker) block() int { return bw.row + bw.x>>bw.bits }

func (bw *blockWalker) advance(n int) {
	bw.x += n
	for bw.x >= bw.w {
		bw.x -= bw.w
		bw.y++
		if bw.y&bw.ymask == 0 {
			bw.row += bw.mw
		}
	}
}

// clusterer holds the state of the meta prefix code search.
type clusterer struct {
	// Block histograms, sparse: entries[off[k]:off[k+1]] hold block k's
	// counts as sym<<entryCountBits | count.
	entries []uint32
	off     []int32
	band    []uint16   // dense histograms of one row of blocks
	touched [][]uint16 // per block of a row: the symbols counted
	sig     []uint16   // per block: 0, or noSig when it has no symbols
	ent     []float32  // per block: entropies of green, red and blue
	binID   [maxBins * maxBins * maxBins]uint16

	// Clusters: dense histograms of numSyms symbols each, their
	// estimated costs, and per block the cluster it is in.
	hist   []uint32
	cost   []float64
	alive  []bool
	assign []uint16
	nclust int

	// Pair merging: the savings of merging each pair.
	save []float32

	// Reassignment: per symbol the estimated code length in every
	// cluster (lens[sym*nclust+g]), and per cluster a block's cost.
	lens []float32
	sums []float32

	// nz holds per cluster a bit per symbol with a nonzero count.
	nz []uint64

	count []int32  // blocks per cluster
	ids   []uint16 // renumbering of the clusters
	img   []uint32 // the entropy image
	// best holds the assignment of the best candidate so far.
	best []uint16
}

const noSig = math.MaxUint16

// log2Tab[i] is log2(i) for i < 512.
var log2Tab = func() (t [512]float32) {
	for i := 1; i < len(t); i++ {
		t[i] = float32(math.Log2(float64(i)))
	}
	return
}()

// flog returns f*log2(f), close enough for cost estimates.
func flog(f uint32) float64 {
	if f < 512 {
		return float64(f) * float64(log2Tab[f])
	}
	s := uint(bits.Len32(f)) - 9
	return float64(f) * (float64(s) + float64(log2Tab[f>>s]))
}

func log2f(f uint32) float64 {
	if f < 512 {
		return float64(log2Tab[f])
	}
	s := uint(bits.Len32(f)) - 9
	return float64(s) + float64(log2Tab[f>>s])
}

// headerEstimate is the estimated header size of a code with k used
// symbols.
func headerEstimate(k int) float64 {
	switch k {
	case 0, 1:
		return 12
	case 2:
		return 20
	}
	return 30 + 3*float64(k)
}

// alphabetCost estimates the bits of coding the counts h with a prefix
// code of their own, its header included.
func alphabetCost(h []uint32) float64 {
	var n uint32
	var s float64
	k := 0
	for _, f := range h {
		if f != 0 {
			n += f
			s += flog(f)
			k++
		}
	}
	if k <= 1 {
		return headerEstimate(k)
	}
	return max(flog(n)-s, float64(n)) + headerEstimate(k)
}

// groupCost estimates the bits of a group with the counts h.
func groupCost(h []uint32, cacheBits int) float64 {
	c := 0.0
	for a := range alphabetStart {
		c += alphabetCost(h[alphabetStart[a]:alphabetEnd(a, cacheBits)])
	}
	return c
}

// nzWords is the size of a bit set of the symbols.
const nzWords = (numSyms + 63) / 64

// mergedGroupCostNZ estimates groupCost of the sum of a and b, whose
// nonzero counts are marked in the bit sets za and zb: it visits only the
// symbols one of them counts.
func mergedGroupCostNZ(a, b []uint32, za, zb []uint64, cacheBits int) float64 {
	c := 0.0
	for k := range alphabetStart {
		c += mergedCostNZ(a, b, za, zb, alphabetStart[k], alphabetEnd(k, cacheBits))
	}
	return c
}

// mergedCostNZ estimates alphabetCost of the sum of a[lo:hi] and
// b[lo:hi], visiting the symbols marked in za or zb.
func mergedCostNZ(a, b []uint32, za, zb []uint64, lo, hi int) float64 {
	var n uint32
	var s float64
	k := 0
	a, b = a[:numSyms], b[:numSyms]
	za, zb = za[:nzWords], zb[:nzWords]
	last := (hi - 1) >> 6
	for x := lo >> 6; x <= last; x++ {
		w := za[x] | zb[x]
		if x == lo>>6 {
			w &= ^uint64(0) << (lo & 63)
		}
		if x == last {
			w &= ^uint64(0) >> (63 - (hi-1)&63)
		}
		for w != 0 {
			i := x<<6 | bits.TrailingZeros64(w)
			w &= w - 1
			f := a[i] + b[i]
			n += f
			s += flog(f)
			k++
		}
	}
	if k <= 1 {
		return headerEstimate(k)
	}
	return max(flog(n)-s, float64(n)) + headerEstimate(k)
}

// metaPlan is a meta prefix code search setting of a level.
type metaPlan struct {
	sizes  []int // block size bits to try
	bins   int   // entropy bins per channel for the first clusters
	rounds int   // reassignment rounds
	groups int   // most groups
}

// chooseMeta tries meta prefix codes for the main image and keeps the
// best that codes it in fewer than single bits. It returns the bits of the
// image as chosen.
func (c *coder) chooseMeta(e *Encoder, argb []uint32, w, h int, mp *metaPlan, single int) int {
	cl := &c.cl
	bestBits := single
	bestB := 0
	for _, b := range mp.sizes {
		mw, mh := divRoundUp(w, 1<<b), divRoundUp(h, 1<<b)
		nb := mw * mh
		if b > maxMetaBits || nb < 2 || nb > maxBlocks || mw*numSyms > bandBudget {
			continue
		}
		cl.blocks(c, argb, w, b, mw, mh)
		cl.seed(mp.bins, c.cacheBits)
		for r := 0; ; r++ {
			cl.merge(c.cacheBits, mp.groups)
			if r == mp.rounds {
				break
			}
			if !cl.reassign(c.cacheBits) {
				break
			}
		}
		if cl.nclust < 2 {
			continue
		}
		bits := cl.exactBits(e, c, mw, mh)
		if c.cacheBits > 0 {
			bits += 4
		}
		bits += 1 + 1 + 3
		if bits < bestBits {
			bestBits, bestB = bits, b
			cl.best = append(cl.best[:0], cl.assign[:nb]...)
			c.ngroups = cl.nclust
		}
	}
	if bestB == 0 {
		return single
	}
	c.metaBits = bestB
	c.metaW, c.metaH = divRoundUp(w, 1<<bestB), divRoundUp(h, 1<<bestB)
	c.gmap = append(c.gmap[:0], cl.best...)
	cl.img = cl.img[:0]
	for _, g := range c.gmap {
		cl.img = append(cl.img, uint32(g)<<8)
	}
	c.buildGroups(e, argb, w)
	return bestBits
}

// blocks counts the symbols of each block of 1<<b pixels squared, mw
// blocks per row: dense counts for a row of blocks at a time, kept as
// sparse entries per block.
func (cl *clusterer) blocks(c *coder, argb []uint32, w, b, mw, mh int) {
	if cap(cl.band) < mw*numSyms {
		cl.band = make([]uint16, mw*numSyms)
	}
	band := cl.band[:mw*numSyms]
	if need := mw*mh + 1; cap(cl.off) < need {
		cl.off = make([]int32, 0, need)
	}
	// The parse is done: the entries take the scratch buffer, and keep
	// it when they outgrow it.
	cl.entries = c.buf[:0]
	defer func() { c.buf = cl.entries[:0] }()
	cl.off = append(cl.off[:0], 0)
	for len(cl.touched) < mw {
		cl.touched = append(cl.touched, nil)
	}
	touched := cl.touched[:mw]
	for k := range touched {
		touched[k] = touched[k][:0]
	}
	count := func(k int, s uint32) {
		hk := band[k*numSyms:]
		if hk[s] == 0 {
			touched[k] = append(touched[k], uint16(s))
		}
		hk[s]++
	}
	shift := 32 - uint(c.cacheBits)
	lim := hitLimit(c.cacheBits)
	hit := c.hit
	cur := 0 // the row of blocks in band
	x, y := 0, 0
	pos := 0
	for _, t := range c.toks {
		n := int(t&(1<<tokLenBits-1)) + 1
		d := t >> tokLenBits
		if d == 0 {
			hs := hit[pos : pos+n]
			for i, p := range argb[pos : pos+n] {
				k := x >> b
				if hs[i]-1 < lim {
					count(k, greenBase+(p*0x1e35a7bd)>>shift)
					goto next
				}
				count(k, p>>8&0xff)
				count(k, symRed+p>>16&0xff)
				count(k, symBlue+p&0xff)
				count(k, symAlpha+p>>24)
			next:
				if x++; x == w {
					x = 0
					if y++; y>>b != cur {
						cl.flushRow(band, touched)
						cur++
					}
				}
			}
			pos += n
			continue
		}
		k := x >> b
		dc, _, _ := prefixOf(d)
		count(k, uint32(numLiteral+int(lengthPrefix[n-1])))
		count(k, symDist+dc)
		pos += n
		for x += n; x >= w; x -= w {
			if y++; y>>b != cur {
				cl.flushRow(band, touched)
				cur++
			}
		}
	}
	for ; cur < mh; cur++ {
		cl.flushRow(band, touched)
	}
}

// flushRow moves the dense histograms of a row of blocks into entries and
// clears them.
func (cl *clusterer) flushRow(band []uint16, touched [][]uint16) {
	for k, ts := range touched {
		hk := band[k*numSyms : (k+1)*numSyms]
		for _, s := range ts {
			cl.entries = append(cl.entries, uint32(s)<<entryCountBits|uint32(hk[s]))
			hk[s] = 0
		}
		touched[k] = ts[:0]
		cl.off = append(cl.off, int32(len(cl.entries)))
	}
}

// seed makes the first clusters: blocks binned by the entropy of their
// green, red and blue symbols, bins steps per channel.
func (cl *clusterer) seed(bins, cacheBits int) {
	nb := len(cl.off) - 1
	if cap(cl.sig) < nb {
		cl.sig = make([]uint16, nb)
		cl.assign = make([]uint16, nb)
		cl.ent = make([]float32, 3*nb)
	}
	cl.sig, cl.assign, cl.ent = cl.sig[:nb], cl.assign[:nb], cl.ent[:3*nb]
	// Per block the entropy per symbol of three alphabets.
	var lo, hi [3]float64
	for a := range lo {
		lo[a], hi[a] = math.Inf(1), math.Inf(-1)
	}
	for k := 0; k < nb; k++ {
		es := cl.entries[cl.off[k]:cl.off[k+1]]
		if len(es) == 0 {
			cl.sig[k] = noSig
			continue
		}
		cl.sig[k] = 0
		var n [3]uint32
		var s [3]float64
		for _, e := range es {
			sym, f := int(e>>entryCountBits), e&(1<<entryCountBits-1)
			a := 0
			switch {
			case sym >= symAlpha:
				continue
			case sym >= symBlue:
				a = 2
			case sym >= symRed:
				a = 1
			}
			n[a] += f
			s[a] += flog(f)
		}
		for a := range n {
			v := float32(0)
			if n[a] > 0 {
				v = float32((flog(n[a]) - s[a]) / float64(n[a]))
			}
			// The extremes are those of the stored values, so that
			// every value bins between them.
			cl.ent[3*k+a] = v
			lo[a], hi[a] = min(lo[a], float64(v)), max(hi[a], float64(v))
		}
	}
	// Bin numbers: per channel bins steps between the extremes.
	nbins := bins * bins * bins
	for i := range cl.binID[:nbins] {
		cl.binID[i] = noSig
	}
	cl.nclust = 0
	for k := 0; k < nb; k++ {
		if cl.sig[k] == noSig {
			continue
		}
		key := 0
		for a := range lo {
			q := 0
			if hi[a] > lo[a] {
				// In [0, 1]: lo <= v <= hi; the clamps also catch a NaN.
				t := (float64(cl.ent[3*k+a]) - lo[a]) / (hi[a] - lo[a])
				if t > 0 {
					q = min(int(float64(bins)*min(t, 1)), bins-1)
				}
			}
			key = key*bins + q
		}
		if cl.binID[key] == noSig {
			cl.binID[key] = uint16(cl.nclust)
			cl.nclust++
		}
		cl.assign[k] = cl.binID[key]
	}
	cl.rebuild(cacheBits)
}

// rebuild sums the block histograms into their clusters, drops empty
// clusters and computes the clusters' costs.
func (cl *clusterer) rebuild(cacheBits int) {
	k := cl.nclust
	if cap(cl.hist) < k*numSyms {
		cl.hist = make([]uint32, k*numSyms)
	}
	cl.hist = cl.hist[:k*numSyms]
	clear(cl.hist)
	cl.cost = cl.cost[:0]
	cl.alive = cl.alive[:0]
	if cap(cl.count) < k {
		cl.count = make([]int32, k, max(k, 256))
		cl.ids = make([]uint16, k, max(k, 256))
	}
	count, ids := cl.count[:k], cl.ids[:k]
	clear(count)
	for b, g := range cl.assign {
		if cl.sig[b] == noSig {
			continue
		}
		count[g]++
		h := cl.hist[int(g)*numSyms:]
		for _, e := range cl.entries[cl.off[b]:cl.off[b+1]] {
			h[e>>entryCountBits] += e & (1<<entryCountBits - 1)
		}
	}
	// Compact: drop clusters without blocks.
	n := 0
	for g := 0; g < k; g++ {
		if count[g] == 0 {
			continue
		}
		ids[g] = uint16(n)
		if n != g {
			copy(cl.hist[n*numSyms:(n+1)*numSyms], cl.hist[g*numSyms:(g+1)*numSyms])
		}
		n++
	}
	for b, g := range cl.assign {
		if cl.sig[b] != noSig {
			cl.assign[b] = ids[g]
		}
	}
	cl.nclust = n
	cl.hist = cl.hist[:n*numSyms]
	if cap(cl.nz) < n*nzWords {
		cl.nz = make([]uint64, n*nzWords)
	}
	cl.nz = cl.nz[:n*nzWords]
	for g := 0; g < n; g++ {
		h := cl.hist[g*numSyms : (g+1)*numSyms]
		z := cl.nz[g*nzWords : (g+1)*nzWords]
		clear(z)
		for i, f := range h {
			if f != 0 {
				z[i>>6] |= 1 << (i & 63)
			}
		}
		cl.cost = append(cl.cost, groupCost(h, cacheBits))
		cl.alive = append(cl.alive, true)
	}
}

// merge joins pairs of clusters while a merge saves estimated bits, or
// while there are more than most clusters, then renumbers them.
func (cl *clusterer) merge(cacheBits, most int) {
	k := cl.nclust
	if k < 2 {
		return
	}
	if cap(cl.save) < k*k {
		cl.save = make([]float32, k*k)
	}
	save := cl.save[:k*k]
	hist := func(g int) []uint32 { return cl.hist[g*numSyms : (g+1)*numSyms] }
	nz := func(g int) []uint64 { return cl.nz[g*nzWords : (g+1)*nzWords] }
	pair := func(i, j int) float32 {
		return float32(cl.cost[i] + cl.cost[j] - mergedGroupCostNZ(hist(i), hist(j), nz(i), nz(j), cacheBits))
	}
	for i := 0; i < k; i++ {
		for j := i + 1; j < k; j++ {
			save[i*k+j] = pair(i, j)
		}
	}
	live := k
	for live > 1 {
		bi, bj := -1, -1
		best := float32(math.Inf(-1))
		for i := 0; i < k; i++ {
			if !cl.alive[i] {
				continue
			}
			for j := i + 1; j < k; j++ {
				if cl.alive[j] && save[i*k+j] > best {
					best, bi, bj = save[i*k+j], i, j
				}
			}
		}
		if bi < 0 || best <= 0 && live <= most {
			break
		}
		hi, hj := hist(bi), hist(bj)
		for s, f := range hj {
			hi[s] += f
		}
		zi, zj := nz(bi), nz(bj)
		for x, v := range zj {
			zi[x] |= v
		}
		cl.cost[bi] = groupCost(hi, cacheBits)
		cl.alive[bj] = false
		live--
		for b, g := range cl.assign {
			if int(g) == bj && cl.sig[b] != noSig {
				cl.assign[b] = uint16(bi)
			}
		}
		for j := 0; j < k; j++ {
			if j == bi || !cl.alive[j] {
				continue
			}
			if j < bi {
				save[j*k+bi] = pair(j, bi)
			} else {
				save[bi*k+j] = pair(bi, j)
			}
		}
	}
	cl.rebuild(cacheBits)
}

// reassign moves each block to the cluster whose estimated code lengths
// code it in the fewest bits, and rebuilds the clusters. It reports
// whether any block moved.
func (cl *clusterer) reassign(cacheBits int) bool {
	k := cl.nclust
	if k < 2 {
		return false
	}
	if cap(cl.lens) < k*numSyms {
		cl.lens = make([]float32, k*numSyms)
	}
	lens := cl.lens[:k*numSyms]
	for g := 0; g < k; g++ {
		h := cl.hist[g*numSyms : (g+1)*numSyms]
		for a := range alphabetStart {
			lo, hi := alphabetStart[a], alphabetEnd(a, cacheBits)
			var n uint32
			for _, f := range h[lo:hi] {
				n += f
			}
			ln := log2f(max(n, 1))
			for s, f := range h[lo:hi] {
				v := float32(ln + 2)
				if f != 0 {
					v = float32(ln - log2f(f))
				}
				lens[(lo+s)*k+g] = v
			}
		}
	}
	if cap(cl.sums) < k {
		cl.sums = make([]float32, k, max(k, 256))
	}
	sums := cl.sums[:k]
	moved := false
	for b := range cl.assign {
		if cl.sig[b] == noSig {
			continue
		}
		// The cost of the block in every cluster, adding its symbols in
		// the same order for each.
		clear(sums)
		for _, e := range cl.entries[cl.off[b]:cl.off[b+1]] {
			f := float32(e & (1<<entryCountBits - 1))
			sym := int(e >> entryCountBits)
			addScaled(sums, lens[sym*k:sym*k+k], f)
		}
		best, bg := float32(math.Inf(1)), 0
		for g, s := range sums {
			if s < best {
				best, bg = s, g
			}
		}
		if bg != int(cl.assign[b]) {
			cl.assign[b] = uint16(bg)
			moved = true
		}
	}
	if moved {
		cl.rebuild(cacheBits)
	}
	return moved
}

// addScaled adds f*l[g] to s[g] for every g.
func addScaled(s, l []float32, f float32) {
	l = l[:len(s)]
	for g, v := range l {
		s[g] += f * v
	}
}

// exactBits fills the empty blocks, numbers the clusters by first
// appearance, and returns the exact bits of the groups' codes and data and
// of the entropy image.
func (cl *clusterer) exactBits(e *Encoder, c *coder, mw, mh int) int {
	nb := mw * mh
	// Empty blocks take the group of the block before them (the first
	// group for those before every other).
	first := 0
	for first < nb && cl.sig[first] == noSig {
		first++
	}
	prev := cl.assign[first]
	for b := 0; b < nb; b++ {
		if cl.sig[b] == noSig {
			cl.assign[b] = prev
		}
		prev = cl.assign[b]
	}
	ids := cl.ids[:cl.nclust]
	for i := range ids {
		ids[i] = noSig
	}
	next := uint16(0)
	cl.img = cl.img[:0]
	for b := 0; b < nb; b++ {
		g := cl.assign[b]
		if ids[g] == noSig {
			ids[g] = next
			next++
		}
		cl.assign[b] = ids[g]
		cl.img = append(cl.img, uint32(ids[g])<<8)
	}
	// Cluster histograms in the new numbering.
	old := cl.hist
	bits := 0
	for g := 0; g < cl.nclust; g++ {
		if ids[g] == noSig {
			continue
		}
		h := (*histogram)(old[g*numSyms : (g+1)*numSyms])
		bits += c.groupBits(e, h, c.cacheBits)
	}
	bits += e.sub.plan(e, cl.img, mw, mh, false)
	return bits
}

// buildGroups counts the symbols of each group under gmap and builds the
// groups' codes.
func (c *coder) buildGroups(e *Encoder, argb []uint32, w int) {
	k := c.ngroups
	c.growGroups(k)
	cl := &c.cl
	if cap(cl.hist) < k*numSyms {
		cl.hist = make([]uint32, k*numSyms)
	}
	hs := cl.hist[:k*numSyms]
	clear(hs)
	var bw blockWalker
	bw.init(w, c.metaBits, c.metaW)
	shift := 32 - uint(c.cacheBits)
	lim := hitLimit(c.cacheBits)
	hit := c.hit
	pos := 0
	for _, t := range c.toks {
		n := int(t&(1<<tokLenBits-1)) + 1
		d := t >> tokLenBits
		if d == 0 {
			ht := hit[pos : pos+n]
			for i, p := range argb[pos : pos+n] {
				h := hs[int(c.gmap[bw.block()])*numSyms:]
				bw.advance(1)
				if ht[i]-1 < lim {
					h[greenBase+(p*0x1e35a7bd)>>shift]++
					continue
				}
				h[p>>8&0xff]++
				h[symRed+p>>16&0xff]++
				h[symBlue+p&0xff]++
				h[symAlpha+p>>24]++
			}
			pos += n
			continue
		}
		h := hs[int(c.gmap[bw.block()])*numSyms:]
		h[numLiteral+int(lengthPrefix[n-1])]++
		dc, _, _ := prefixOf(d)
		h[symDist+dc]++
		pos += n
		bw.advance(n)
	}
	for g := 0; g < k; g++ {
		c.buildGroup(e, (*histogram)(hs[g*numSyms:(g+1)*numSyms]), &c.groups[g])
	}
}

func divRoundUp(n, d int) int { return (n + d - 1) / d }
