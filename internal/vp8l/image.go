package vp8l

// A token is one coded item of an image: dist<<12 | (n-1). dist is a
// distance code (1 to maxDistCode) for a copy of n pixels, or 0 for n
// literal pixels, each of which is written as a color cache index when the
// cache holds it.
type token = uint32

const tokLenBits = 12

func litRun(n int) token                { return token(n - 1) }
func copyTok(dcode uint32, n int) token { return dcode<<tokLenBits | token(n-1) }

// The symbols of a prefix code group are numbered across its five
// alphabets: green (with lengths and cache indices), red, blue, alpha and
// distance.
const (
	symRed   = maxGreen
	symBlue  = symRed + numLiteral
	symAlpha = symBlue + numLiteral
	symDist  = symAlpha + numLiteral
	numSyms  = symDist + numDistCodes
)

// alphabetStart is where each alphabet starts in the numbering.
var alphabetStart = [5]int{0, symRed, symBlue, symAlpha, symDist}

// alphabetEnd returns the end of alphabet a's symbols under a color cache
// of cacheBits.
func alphabetEnd(a, cacheBits int) int {
	switch a {
	case 0:
		return greenBase + cacheSize(cacheBits)
	case 4:
		return numSyms
	}
	return alphabetStart[a] + numLiteral
}

func cacheSize(bits int) int {
	if bits == 0 {
		return 0
	}
	return 1 << bits
}

// histogram counts the symbols of a prefix code group.
type histogram [numSyms]uint32

func (h *histogram) clear() { *h = histogram{} }

// addLiteral counts the four channels of p.
func (h *histogram) addLiteral(p uint32) {
	h[p>>8&0xff]++
	h[symRed+p>>16&0xff]++
	h[symBlue+p&0xff]++
	h[symAlpha+p>>24]++
}

// group is a prefix code group: green (with lengths and cache indices),
// red, blue, alpha and distance.
type group struct {
	codes [5]prefixCode
}

// coder codes one image. Its buffers are kept between images.
type coder struct {
	toks      []token
	cacheBits int
	// hit holds per pixel the smallest color cache size the level tries
	// at which the pixel hits the cache, or noHit; see findHits. hits
	// tells whether it is set for the image.
	hit  []uint8
	hits bool
	// counted tells that the parse counted the tokens for the color
	// cache choice (see runSim).
	counted bool
	// cache is the color cache of writeData.
	cache [1 << maxCacheBits]uint32

	// The match finder and the parse.
	dc  distCoder
	ch  chains
	rn  runs
	buf []uint32 // scratch, see scratch
	// The matches listed for the shortest-path parse: per position their
	// count, and in order as copy tokens.
	mcount []uint8
	ml     matchList
	last   int // the position of the last group of a compact list
	// cost is the cost model of the parse.
	cost costModel

	// Meta prefix codes: metaBits is 0 for one group, else the block
	// size bits; gmap holds the group of each block, metaW blocks per
	// row; groups holds the codes of the ngroups groups.
	metaBits, metaW, metaH int
	ngroups                int
	gmap                   []uint16
	groups                 []group
	cl                     clusterer

	// hist holds the counts of the chosen color cache once plan has
	// chosen it, else a parse's counts.
	hist histogram
	grp  group
}

// scratch is the Encoder's working memory of a single call: the main and
// the sub-image coder take turns with it.
type scratch struct {
	path [pathLen]uint64 // the costs of the shortest-path parse
	lens [2 * maxRelax]uint64
	// Color cache choice: a histogram per cache size (0 to 11 bits).
	hists [maxCacheBits + 1]histogram
	cs    cacheSim
}

// encode writes argb as an entropy-coded image; main adds the meta prefix
// codes of the spatially coded image.
func (c *coder) encode(e *Encoder, argb []uint32, w, h int, main bool) {
	bits := c.plan(e, argb, w, h, main)
	e.bw.reserve(bits)
	c.write(e, argb, w, main)
}

// plan parses argb and chooses its color cache and prefix codes.
func (c *coder) plan(e *Encoder, argb []uint32, w, h int, main bool) int {
	ef := &efforts[e.level]
	c.metaBits = 0
	// The levels that parse greedily into one group count the cache
	// sizes as they simulate them, and need no hit table.
	c.hits = ef.opt > 0 || ef.meta != nil
	if c.hits {
		c.findHits(e, argb, ef.cache)
	}
	sizes := c.parse(e, argb, w, ef)
	c.cacheBits = c.chooseCache(e, argb, sizes)
	// The meta search plans the entropy image with the sub-image coder,
	// which reuses e.tmp: keep the chosen counts.
	c.hist = e.tmp.hists[c.cacheBits]
	bits := 1 + c.groupBits(e, &c.hist, c.cacheBits)
	if c.cacheBits > 0 {
		bits += 4
	}
	if main {
		bits++
		if ef.meta != nil {
			bits = c.chooseMeta(e, argb, w, h, ef.meta, bits)
		}
	}
	if c.metaBits == 0 {
		c.ngroups = 1
		c.growGroups(1)
		c.buildGroup(e, &c.hist, &c.groups[0])
	}
	return bits
}

// write writes the image as planned.
func (c *coder) write(e *Encoder, argb []uint32, w int, main bool) {
	b := &e.bw
	if c.cacheBits > 0 {
		b.put(1, 1)
		b.put(uint32(c.cacheBits), 4)
	} else {
		b.put(0, 1)
	}
	if main {
		if c.metaBits == 0 {
			b.put(0, 1)
		} else {
			b.put(1, 1)
			b.put(uint32(c.metaBits-2), 3)
			e.sub.encode(e, c.cl.img, c.metaW, c.metaH, false)
		}
	}
	for g := range c.groups[:c.ngroups] {
		c.writeGroup(e, &c.groups[g])
	}
	c.writeData(b, argb, w)
}

func (c *coder) growGroups(n int) {
	if len(c.groups) < n {
		c.groups = append(c.groups, make([]group, n-len(c.groups))...)
	}
}

// parse fills toks for argb as the level asks, and returns the color
// cache sizes worth trying on them.
func (c *coder) parse(e *Encoder, argb []uint32, w int, ef *effort) uint16 {
	c.cacheBits = 0
	if ef.opt == 0 {
		c.greedy(e, argb, w, ef)
		return ef.cache
	}
	c.counted = false
	c.listMatches(argb, w, ef)
	c.cost.fromPixels(argb, &c.hist)
	c.optimal(e, argb, ef, true)
	for pass := 1; pass < ef.opt; pass++ {
		c.refine(e, argb, ef)
	}
	if ef.cacheParse {
		// Parse again knowing which pixels the best cache would hold;
		// the parse that sees the cache prefers a size close to it.
		if cb := c.chooseCache(e, argb, ef.cache&ef.cacheFirst); cb > 0 {
			c.cacheBits = cb
			c.refine(e, argb, ef)
			c.cacheBits = 0
			near := uint16(1)
			for b := max(cb-1, 1); b <= min(cb+1, maxCacheBits); b++ {
				near |= 1 << b
			}
			return near & ef.cache
		}
	}
	return ef.cache
}

// refine parses again with the costs of the codes of the last parse.
func (c *coder) refine(e *Encoder, argb []uint32, ef *effort) {
	c.hist.clear()
	c.count(argb, &c.hist, c.cacheBits)
	c.buildGroup(e, &c.hist, &c.grp)
	c.cost.fromGroup(&c.grp, c.cacheBits)
	c.optimal(e, argb, ef, false)
}

// chooseCache counts the tokens under every color cache size in sizes (a
// bit set of cache bits, all of them sizes findHits looked at) in one
// pass, leaving the counts in hists, and returns the size whose codes
// code the image in the fewest bits.
//
// The pass counts the symbols without a cache and, per tried size, the
// literals whose smallest hit among the tried sizes it is; since a hit at
// b bits is a hit at every larger size, the counts of every size follow
// from those.
func (c *coder) chooseCache(e *Encoder, argb []uint32, sizes uint16) int {
	h0 := &e.tmp.hists[0]
	h0.clear()
	var bs [maxCacheBits]int // the sizes tried, ascending
	nb := 0
	for b := 1; b <= maxCacheBits; b++ {
		if sizes&(1<<b) != 0 {
			bs[nb] = b
			nb++
		}
	}
	if nb == 0 {
		c.count(argb, h0, 0)
		return 0
	}
	top := uint(bs[nb-1])
	// bucket[h] is the first tried size at or above h: a pixel whose
	// smallest hit is h hits the tried sizes from there on (nb: none).
	var bucket [noHit + 1]uint8
	for h, j := 1, 0; h <= noHit; h++ {
		for j < nb && bs[j] < h {
			j++
		}
		bucket[h] = uint8(j)
	}
	// Each literal is counted under its bucket, the literals that hit no
	// size under bucket nb; the counts without a cache are their sum.
	cs := &e.tmp.cs
	if c.counted && nb == 2 {
		// The parse counted them, for these two sizes; it left the
		// lengths and distances in c.hist.
		copy(h0[numLiteral:greenBase], c.hist[numLiteral:greenBase])
		copy(h0[symDist:], c.hist[symDist:])
	} else {
		for j := 0; j <= nb; j++ {
			clear(cs.lit[j][:])
			clear(cs.idx[j][:1<<top])
		}
		if c.hits {
			c.countBuckets(e, argb, h0, &bucket, top)
		} else {
			c.simulateBuckets(e, argb, h0, bs[:nb], top)
		}
	}
	c.counted = false
	for j := 0; j <= nb; j++ {
		l := &cs.lit[j]
		for i := 0; i < numLiteral; i++ {
			h0[i] += l[i]
			h0[symRed+i] += l[numLiteral+i]
			h0[symBlue+i] += l[2*numLiteral+i]
			h0[symAlpha+i] += l[3*numLiteral+i]
		}
	}
	// The counts of size bs[j]: the literals less those that hit at
	// bs[j] or below, and those hits as cache symbols.
	var cumLit [4 * numLiteral]uint32
	cumIdx := cs.cum[:1<<top]
	clear(cumIdx)
	best, bestBits := 0, c.groupBits(e, h0, 0)
	for j := 0; j < nb; j++ {
		b := bs[j]
		hb := &e.tmp.hists[b]
		hits := uint32(0)
		for i, f := range cs.lit[j][:numLiteral] {
			cumLit[i] += f
			hits += f
		}
		for i, f := range cs.lit[j][numLiteral:] {
			cumLit[numLiteral+i] += f
		}
		for i, f := range cs.idx[j][:1<<top] {
			cumIdx[i] += f
		}
		*hb = *h0
		for i := 0; i < numLiteral; i++ {
			hb[i] -= cumLit[i]
			hb[symRed+i] -= cumLit[numLiteral+i]
			hb[symBlue+i] -= cumLit[2*numLiteral+i]
			hb[symAlpha+i] -= cumLit[3*numLiteral+i]
		}
		total := uint32(0)
		for i, f := range cumIdx {
			hb[greenBase+i>>(int(top)-b)] += f
			total += f
		}
		if total == 0 {
			continue
		}
		if bits := c.groupBits(e, hb, b) + 4; bits < bestBits {
			best, bestBits = b, bits
		}
	}
	return best
}

// countBuckets counts the copies of toks into h and each literal into
// the bucket of its smallest hit among the tried sizes, by the hit table.
func (c *coder) countBuckets(e *Encoder, argb []uint32, h *histogram, bucket *[noHit + 1]uint8, top uint) {
	cs := &e.tmp.cs
	hit := c.hit
	tshift := 32 - top
	pos := 0
	for _, t := range c.toks {
		n := int(t&(1<<tokLenBits-1)) + 1
		if d := t >> tokLenBits; d != 0 {
			h[numLiteral+int(lengthPrefix[n-1])]++
			dc, _, _ := prefixOf(d)
			h[symDist+dc]++
			pos += n
			continue
		}
		hs := hit[pos : pos+n]
		for k, p := range argb[pos : pos+n] {
			j := bucket[hs[k]]
			l := &cs.lit[j]
			l[p>>8&0xff]++
			l[numLiteral+p>>16&0xff]++
			l[2*numLiteral+p&0xff]++
			l[3*numLiteral+p>>24]++
			cs.idx[j][(p*0x1e35a7bd)>>tshift]++
		}
		pos += n
	}
}

// simulateBuckets counts as countBuckets does, simulating the caches of
// the sizes bs (ascending) as it goes, the way findHits does.
func (c *coder) simulateBuckets(e *Encoder, argb []uint32, h *histogram, bs []int, top uint) {
	if len(bs) == 2 {
		// The greedy parse left the channel counts of every pixel in
		// c.hist.
		c.simulate2(e, argb, h, &c.hist, bs[0], bs[1])
		return
	}
	cs := &e.tmp.cs
	var off, shift [maxCacheBits]uint32
	nb := len(bs)
	for j, b := range bs {
		off[j], shift[j] = 1<<b, uint32(32-b)
		clear(cs.caches[1<<b : 2<<b])
	}
	const mask = 2<<maxCacheBits - 1
	caches := &cs.caches
	last := nb - 1
	tshift := 32 - top
	prev := ^argb[0]
	pos := 0
	for _, t := range c.toks {
		n := int(t&(1<<tokLenBits-1)) + 1
		px := argb[pos : pos+n]
		pos += n
		if d := t >> tokLenBits; d != 0 {
			h[numLiteral+int(lengthPrefix[n-1])]++
			dc, _, _ := prefixOf(d)
			h[symDist+dc]++
			for _, p := range px {
				if p != prev {
					prev = p
					hv := p * 0x1e35a7bd
					for j := 0; j < nb; j++ {
						caches[(off[j]+hv>>shift[j])&mask] = p
					}
				}
			}
			continue
		}
		for _, p := range px {
			j := 0 // the bucket: hits every size
			if p != prev {
				prev = p
				hv := p * 0x1e35a7bd
				if caches[(off[last]+hv>>shift[last])&mask] != p {
					for k := 0; k < nb; k++ {
						caches[(off[k]+hv>>shift[k])&mask] = p
					}
					j = nb
				} else {
					for ; j < nb; j++ {
						s := &caches[(off[j]+hv>>shift[j])&mask]
						if *s == p {
							break
						}
						*s = p
					}
				}
			}
			l := &cs.lit[j]
			l[p>>8&0xff]++
			l[numLiteral+p>>16&0xff]++
			l[2*numLiteral+p&0xff]++
			l[3*numLiteral+p>>24]++
			cs.idx[j][(p*0x1e35a7bd)>>tshift]++
		}
	}
}

// simulate2 is simulateBuckets for two sizes, small and large bits; all
// holds the channel counts of every pixel. The copies only store into the
// caches; a run of equal pixels hits both, so within a copy only a pixel
// unlike the one before stores. The literals that miss both, most of them
// in photos, are not counted one by one: their counts are those of all
// pixels less the copies' and the hits'.
func (c *coder) simulate2(e *Encoder, argb []uint32, h, all *histogram, small, large int) {
	cs := &e.tmp.cs
	cA := cs.caches[1<<small : 2<<small]
	cB := cs.caches[1<<large : 2<<large]
	clear(cA)
	clear(cB)
	sA, sB := uint(32-small), uint(32-large)
	lit0, lit1, miss := &cs.lit[0], &cs.lit[1], &cs.lit[2]
	idx0, idx1 := &cs.idx[0], &cs.idx[1]
	for i := 0; i < numLiteral; i++ {
		miss[i] = all[i]
		miss[numLiteral+i] = all[symRed+i]
		miss[2*numLiteral+i] = all[symBlue+i]
		miss[3*numLiteral+i] = all[symAlpha+i]
	}
	// sub takes n copies of p out of the counts of the misses.
	sub := func(p uint32, n uint32) {
		miss[p>>8&0xff] -= n
		miss[numLiteral+p>>16&0xff] -= n
		miss[2*numLiteral+p&0xff] -= n
		miss[3*numLiteral+p>>24] -= n
	}
	prev := ^argb[0]
	pos := 0
	for _, t := range c.toks {
		n := int(t&(1<<tokLenBits-1)) + 1
		px := argb[pos : pos+n]
		pos += n
		if d := t >> tokLenBits; d != 0 {
			h[numLiteral+int(lengthPrefix[n-1])]++
			dc, _, _ := prefixOf(d)
			h[symDist+dc]++
			// Equal pixels in a row come off the counts at once.
			run := uint32(0)
			for _, p := range px {
				if p != prev {
					if run > 0 {
						sub(prev, run)
						run = 0
					}
					prev = p
					hv := p * 0x1e35a7bd
					cA[hv>>sA&(1<<small-1)] = p
					cB[hv>>sB&(1<<large-1)] = p
				}
				run++
			}
			sub(prev, run)
			continue
		}
		for _, p := range px {
			hv := p * 0x1e35a7bd
			kB := hv >> sB & (1<<large - 1)
			if p == prev {
				// Hits both: bucket 0.
				lit0[p>>8&0xff]++
				lit0[numLiteral+p>>16&0xff]++
				lit0[2*numLiteral+p&0xff]++
				lit0[3*numLiteral+p>>24]++
				idx0[kB]++
				continue
			}
			prev = p
			kA := hv >> sA & (1<<small - 1)
			if cB[kB] == p {
				l, idx := lit1, idx1
				if cA[kA] == p {
					l, idx = lit0, idx0
				}
				l[p>>8&0xff]++
				l[numLiteral+p>>16&0xff]++
				l[2*numLiteral+p&0xff]++
				l[3*numLiteral+p>>24]++
				idx[kB]++
			}
			cA[kA] = p
			cB[kB] = p
		}
	}
	for i := range miss {
		miss[i] -= lit0[i] + lit1[i]
	}
}

// noHit marks a pixel that hits no color cache size tried.
const noHit = maxCacheBits + 1

// findHits sets c.hit[i] to the smallest color cache size in sizes (a bit
// set of cache bits) at which pixel i hits the cache, or noHit. Every
// pixel enters the cache, whether it is coded as a literal or within a
// copy, so the hits depend on the pixels alone: one pass serves every
// parse, count and write of the image after it, and those keep no cache.
//
// A pixel that hits the cache of b bits hits every larger one too: the
// colors stored in its slot of the larger cache are some of those stored
// in its slot of the smaller one, and the latest of those is the pixel
// itself. So the cache of cb bits holds pixel i exactly when hit[i] <= cb,
// for cb in sizes.
func (c *coder) findHits(e *Encoder, argb []uint32, sizes uint16) {
	n := len(argb)
	if cap(c.hit) < n {
		c.hit = make([]uint8, n)
	}
	c.hit = c.hit[:n]
	hit := c.hit
	// The sizes tried, ascending; caches holds the cache of b bits at
	// offset 1<<b.
	var off, shift [maxCacheBits]uint32
	var val [maxCacheBits]uint8
	nb := 0
	caches := &e.tmp.cs.caches
	for b := 1; b <= maxCacheBits; b++ {
		if sizes&(1<<b) != 0 {
			off[nb], shift[nb], val[nb] = 1<<b, uint32(32-b), uint8(b)
			clear(caches[1<<b : 2<<b])
			nb++
		}
	}
	if nb == 0 {
		return
	}
	const mask = 2<<maxCacheBits - 1
	// A miss at the largest size is a miss at every size, so it only
	// stores the pixel; a hit at one size is a hit at every larger one,
	// whose slot holds the pixel already, so the first hit ends the walk
	// up from the smallest. A pixel equal to the one before hits every
	// size and leaves the caches as they are.
	small, last := val[0], nb-1
	prev := ^argb[0]
	for i, p := range argb {
		if p == prev {
			hit[i] = small
			continue
		}
		prev = p
		hv := p * 0x1e35a7bd
		if caches[(off[last]+hv>>shift[last])&mask] != p {
			for j := 0; j < nb; j++ {
				caches[(off[j]+hv>>shift[j])&mask] = p
			}
			hit[i] = noHit
			continue
		}
		m := uint8(noHit)
		for j := 0; j < nb; j++ {
			s := &caches[(off[j]+hv>>shift[j])&mask]
			if *s == p {
				m = val[j]
				break
			}
			*s = p
		}
		hit[i] = m
	}
}

// hitLimit returns the bound under which hit-1 (as a uint8) marks a pixel
// that the cache of cb bits holds: none for no cache, whatever hit holds
// then.
func hitLimit(cb int) uint8 { return uint8(cb) }

// cacheSim is the scratch of the color cache choice: the caches of every
// size side by side (the one of b bits at offset 1<<b), and per size tried
// the literals that first hit it and their slots in the largest cache,
// then those that hit none.
type cacheSim struct {
	caches [2 << maxCacheBits]uint32
	lit    [maxCacheBits + 1][4 * numLiteral]uint32
	idx    [maxCacheBits + 1][1 << maxCacheBits]uint32
	cum    [1 << maxCacheBits]uint32
}

// groupBits returns the bits of coding the symbols of h with their own
// prefix code group, headers and extra bits included, under a cache of
// cacheBits.
func (c *coder) groupBits(e *Encoder, h *histogram, cacheBits int) int {
	saved := c.cacheBits
	c.cacheBits = cacheBits
	c.buildGroup(e, h, &c.grp)
	c.cacheBits = saved
	bits := 0
	for a := range c.grp.codes {
		p := &c.grp.codes[a]
		bits += e.hw.prepare(&e.huff, p)
		bits += p.cost(h[alphabetStart[a]:alphabetEnd(a, cacheBits)])
	}
	return bits + extraBits(h)
}

// extraBits returns the extra bits of the lengths and distances counted
// in h.
func extraBits(h *histogram) int {
	bits := 0
	for i, f := range h[numLiteral:greenBase] {
		bits += int(f) * int(prefixExtra(uint32(i)))
	}
	for i, f := range h[symDist:] {
		bits += int(f) * int(prefixExtra(uint32(i)))
	}
	return bits
}

// count adds the symbols of toks under a color cache of cb bits to h.
func (c *coder) count(argb []uint32, h *histogram, cb int) {
	if cb == 0 {
		c.count0(argb, h)
		return
	}
	shift := 32 - uint(cb)
	lim := hitLimit(cb)
	hit := c.hit
	pos := 0
	for _, t := range c.toks {
		n := int(t&(1<<tokLenBits-1)) + 1
		if d := t >> tokLenBits; d != 0 {
			h[numLiteral+int(lengthPrefix[n-1])]++
			dc, _, _ := prefixOf(d)
			h[symDist+dc]++
			pos += n
			continue
		}
		hs := hit[pos : pos+n]
		for k, p := range argb[pos : pos+n] {
			if hs[k]-1 < lim {
				h[greenBase+(p*0x1e35a7bd)>>shift]++
				continue
			}
			h.addLiteral(p)
		}
		pos += n
	}
}

// count0 adds the symbols of toks without a color cache to h.
func (c *coder) count0(argb []uint32, h *histogram) {
	pos := 0
	for _, t := range c.toks {
		n := int(t&(1<<tokLenBits-1)) + 1
		if d := t >> tokLenBits; d != 0 {
			h[numLiteral+int(lengthPrefix[n-1])]++
			dc, _, _ := prefixOf(d)
			h[symDist+dc]++
		} else {
			for _, p := range argb[pos : pos+n] {
				h.addLiteral(p)
			}
		}
		pos += n
	}
}

// buildGroup builds the codes of g from h.
func (c *coder) buildGroup(e *Encoder, h *histogram, g *group) {
	for a := range g.codes {
		g.codes[a].build(&e.huff, h[alphabetStart[a]:alphabetEnd(a, c.cacheBits)], e.codes[:], maxCodeLen)
	}
}

// writeGroup writes the five headers of g.
func (c *coder) writeGroup(e *Encoder, g *group) {
	for a := range g.codes {
		p := &g.codes[a]
		e.hw.prepare(&e.huff, p)
		e.hw.write(&e.bw, p)
	}
}

// writeData writes the tokens under the planned groups.
func (c *coder) writeData(b *bitWriter, argb []uint32, w int) {
	cb := c.cacheBits
	shift := 32 - uint(cb)
	lim := hitLimit(cb)
	hit := c.hit
	// The hits come from the hit table, or else from a simulated cache.
	table := cb > 0 && c.hits
	sim := cb > 0 && !c.hits
	cache := c.cache[:1<<cb]
	if sim {
		clear(cache)
	}
	g := &c.groups[0]
	meta := c.metaBits != 0
	var bw blockWalker
	if meta {
		bw.init(w, c.metaBits, c.metaW)
	}
	pos := 0
	for _, t := range c.toks {
		n := int(t&(1<<tokLenBits-1)) + 1
		d := t >> tokLenBits
		if d == 0 {
			var hs []uint8
			if table {
				hs = hit[pos : pos+n]
			}
			for k, p := range argb[pos : pos+n] {
				if meta {
					g = &c.groups[c.gmap[bw.block()]]
					bw.advance(1)
				}
				if sim {
					if k := (p * 0x1e35a7bd) >> shift; cache[k] == p {
						b.sym(&g.codes[0], greenBase+int(k))
						continue
					} else {
						cache[k] = p
					}
				} else if table && hs[k]-1 < lim {
					b.sym(&g.codes[0], greenBase+int((p*0x1e35a7bd)>>shift))
					continue
				}
				// The four codes at once: at most 60 bits.
				tg, tr := g.codes[0].tab[p>>8&0xff], g.codes[1].tab[p>>16&0xff]
				tb, ta := g.codes[2].tab[p&0xff], g.codes[3].tab[p>>24]
				v := uint64(tg >> 4)
				nb := uint(tg & 15)
				v |= uint64(tr>>4) << nb
				nb += uint(tr & 15)
				v |= uint64(tb>>4) << nb
				nb += uint(tb & 15)
				v |= uint64(ta>>4) << nb
				nb += uint(ta & 15)
				b.put64(v, nb)
			}
			pos += n
			continue
		}
		if meta {
			g = &c.groups[c.gmap[bw.block()]]
			bw.advance(n)
		}
		lc, ln, lx := prefixOf(uint32(n))
		b.sym(&g.codes[0], numLiteral+int(lc))
		b.put(lx, ln)
		dc, dn, dx := prefixOf(d)
		b.sym(&g.codes[4], int(dc))
		b.put(dx, dn)
		if sim {
			for _, p := range argb[pos : pos+n] {
				cache[(p*0x1e35a7bd)>>shift] = p
			}
		}
		pos += n
	}
}
