package vp8l

// The parse turns an image into tokens. Levels 0 to 3 parse greedily,
// higher levels find the cheapest path through the image under bit costs
// taken from the previous pass's prefix codes.

// effort is the search effort of a level.
type effort struct {
	depth int // hash chain nodes visited per position
	nice  int // a match this long ends the search
	// opt is the number of shortest-path passes; 0 parses greedily.
	opt int
	// lazy, for the greedy parse, checks the next position for a longer
	// match before taking one shorter than this.
	lazy int
	// relax is the longest length tried at every value by the
	// shortest-path parse; longer matches are tried at their full length.
	relax int
	// cache lists the color cache sizes tried, as a bit set of cache
	// bits 0 (none) to 11.
	cache uint16
	// cacheParse re-parses with the costs of the cache chosen among the
	// sizes in cacheFirst; the final choice then tries the sizes next to
	// it.
	cacheParse bool
	cacheFirst uint16
	// compact lists only the matches the chains find and rebuilds the
	// others (runs, the row above, the matches of the position before)
	// in every pass: a smaller list for a slower parse.
	compact bool
	// meta is the meta prefix code search, nil for one group.
	meta *metaPlan
	// relax1 is relax for the first shortest-path pass, whose costs
	// are guesses (0: relax).
	relax1 int
	// hashBits bounds the hash table of the chains: a small one stays
	// in cache, a large one has shorter chains. Beyond 16 bits the
	// corpus gains nothing at these depths (2026-11-15: 20 bits coded
	// it 0.01% larger at level 6) and the table costs memory.
	hashBits uint
}

const (
	allCaches  = 1<<12 - 1
	everyOther = 1 | 1<<2 | 1<<4 | 1<<6 | 1<<8 | 1<<10
)

var (
	metaLow  = &metaPlan{sizes: []int{5}, bins: 3, rounds: 0, groups: 64}
	metaMid  = &metaPlan{sizes: []int{4, 5}, bins: 3, rounds: 1, groups: 128}
	metaHigh = &metaPlan{sizes: []int{3, 4, 5, 6}, bins: 4, rounds: 2, groups: 256}
	metaBest = &metaPlan{sizes: []int{2, 3, 4, 5, 6, 7}, bins: 6, rounds: 4, groups: 256}
)

var efforts = [10]effort{
	0: {depth: 0, nice: 16, cache: 1 << 10, hashBits: 14},
	1: {depth: 0, nice: 32, cache: 1<<6 | 1<<10, hashBits: 14},
	2: {depth: 4, nice: 64, lazy: 8, cache: allCaches, meta: metaLow, hashBits: 16},
	3: {depth: 8, nice: 128, lazy: 32, cache: allCaches, meta: metaLow, hashBits: 16},
	4: {depth: 4, nice: 128, opt: 1, relax: 16, cache: allCaches, meta: metaMid, hashBits: 16},
	5: {depth: 4, nice: 128, opt: 2, relax: 16, cache: allCaches, cacheParse: true, cacheFirst: everyOther, meta: metaMid, hashBits: 16},
	6: {depth: 4, nice: 64, opt: 2, relax: 8, relax1: 2, cache: allCaches, cacheParse: true, cacheFirst: everyOther, meta: metaMid, hashBits: 16},
	7: {depth: 16, nice: 256, opt: 2, relax: 32, cache: allCaches, cacheParse: true, cacheFirst: everyOther, compact: true, meta: metaHigh, hashBits: 16},
	8: {depth: 32, nice: 1024, opt: 3, relax: 48, cache: allCaches, cacheParse: true, cacheFirst: everyOther, compact: true, meta: metaBest, hashBits: 16},
	9: {depth: 64, nice: maxLength, opt: 3, relax: 64, cache: allCaches, cacheParse: true, cacheFirst: everyOther, compact: true, meta: metaBest, hashBits: 16},
}

// runs tracks how far the copies at distances 1 and w reach, so that their
// lengths cost O(1) per position: each scan covers pixels not scanned
// before.
type runs struct {
	end1, endW int // first position past i where the copy breaks
	// start1 is where the run of equal pixels through end1 starts, or
	// later when the scan began inside it.
	start1 int
}

// at returns the lengths of the copies at distances 1 and w (0 when there
// is none) at position i, at most limit.
func (r *runs) at(a []uint32, i, w, limit int) (int, int) {
	l1, lw := 0, 0
	if i >= 1 && a[i] == a[i-1] {
		if r.end1 <= i {
			k := i + 1
			for k < len(a) && a[k] == a[k-1] {
				k++
			}
			r.end1, r.start1 = k, i-1
		}
		l1 = min(r.end1-i, limit)
	}
	if w > 1 && i >= w && a[i] == a[i-w] {
		if r.endW <= i {
			k := i + 1
			for k < len(a) && a[k] == a[k-w] {
				k++
			}
			r.endW = k
		}
		lw = min(r.endW-i, limit)
	}
	return l1, lw
}

// prepare sets up the match finder for argb of width w.
func (c *coder) prepare(argb []uint32, w int, ef *effort) {
	c.dc.init(w)
	if ef.depth > 0 {
		// The shortest-path parse walks every chain to its depth: when
		// that is shallow, buckets serve it.
		c.ch.init(len(argb), ef.hashBits, ef.depth > 1, ef.opt > 0 && ef.depth <= bucketWays, c)
	}
	c.rn = runs{}
}

// scratch returns n uint32s of the coder's scratch buffer, which serves
// one user at a time: the chain links while matches are searched, the
// steps of the shortest-path parse, then the block entries of the entropy
// image search.
func (c *coder) scratch(n int) []uint32 {
	if cap(c.buf) < n {
		c.buf = make([]uint32, n)
	}
	return c.buf[:n]
}

// greedy parses argb into c.toks, taking at each position the longest
// match when it costs less than literals under costs guessed from the
// pixels. Below the level's lazy length it first checks whether the next
// position starts a longer match, and if so writes a literal instead.
func (c *coder) greedy(e *Encoder, argb []uint32, w int, ef *effort) {
	c.counted = false
	if ef.depth == 0 {
		c.greedyRuns(e, argb, w, ef.cache)
		return
	}
	c.prepare(argb, w, ef)
	cm := &c.cost
	cm.fromPixels(argb, &c.hist)
	// worth reports whether a copy of l pixels at distance d costs less
	// than the pixels as literals; long copies always do.
	worth := func(i, l, d int) bool {
		if l >= 8 {
			return true
		}
		mc := cm.lenCost(l) + cm.codeCost(c.dc.code(d))
		lc := uint32(0)
		for _, p := range argb[i : i+l] {
			lc += cm.litCost(p)
		}
		return mc < lc
	}
	n := len(argb)
	toks := c.toks[:0]
	lits := 0 // literals not yet in a token
	pendL, pendD := 0, 0
	for i := 0; i < n; {
		var bl, bd int
		if pendL != 0 {
			bl, bd, pendL = pendL, pendD, 0
		} else {
			bl, bd = c.longest(argb, i, w, min(maxLength, n-i), ef)
		}
		if bl == 0 || !worth(i, bl, bd) {
			lits++
			if lits == maxLength {
				toks = append(toks, litRun(lits))
				lits = 0
			}
			i++
			continue
		}
		covered := i + 1 // positions up to here are in the chains
		if bl < ef.lazy && i+1 < n {
			nl, nd := c.longest(argb, i+1, w, min(maxLength, n-i-1), ef)
			covered = i + 2
			if nl > bl && worth(i+1, nl, nd) {
				lits++
				if lits == maxLength {
					toks = append(toks, litRun(lits))
					lits = 0
				}
				pendL, pendD = nl, nd
				i++
				continue
			}
		}
		if lits > 0 {
			toks = append(toks, litRun(lits))
			lits = 0
		}
		toks = append(toks, copyTok(c.dc.code(bd), bl))
		// Insert the positions the match covers, but only the first
		// ones of a long match.
		if ef.depth > 1 {
			end := min(i+min(bl, ef.nice), n-1)
			for j := covered; j < end; j++ {
				c.ch.insert(argb, j)
			}
		} else if ef.depth == 1 && i+bl+1 < n {
			// One slot per hash: the last position of the match.
			c.ch.setLatest(argb, i+bl-1)
		}
		i += bl
	}
	if lits > 0 {
		toks = append(toks, litRun(lits))
	}
	c.toks = toks
}

// greedyRuns is greedy without chains: the only matches are the copies
// at distances 1 and w. Between them, a pixel equal to neither the one
// before nor the one above is a literal, found in a tight loop.
//
// When sizes holds two color cache sizes, it also counts the tokens for
// the cache choice as it goes (see simulate2), and sets c.counted.
func (c *coder) greedyRuns(e *Encoder, argb []uint32, w int, sizes uint16) {
	c.dc.init(w)
	cm := &c.cost
	cm.fromPixels(argb, &c.hist)
	code1, codeW := c.dc.code(1), c.dc.code(w)
	cost1, costW := cm.codeCost(code1), cm.codeCost(codeW)
	var rs runSim
	sim := rs.init(e, &c.hist, sizes)
	c.counted = sim
	n := len(argb)
	toks := c.toks[:0]
	lits := 0 // the first pixel not in a token
	if sim {
		rs.literal(argb[0])
	}
	for i := 1; i < n; {
		p := argb[i]
		up := w > 1 && i >= w && p == argb[i-w]
		if p != argb[i-1] && !up {
			if sim {
				rs.literal(p)
			}
			i++
			continue
		}
		limit := min(maxLength, n-i)
		l1, lw := 0, 0
		if p == argb[i-1] {
			k := i + 1
			for k < i+limit && argb[k] == argb[k-1] {
				k++
			}
			l1 = k - i
		}
		if up {
			k := i + 1
			for k < i+limit && argb[k] == argb[k-w] {
				k++
			}
			lw = k - i
		}
		// The longer copy, the one above on a tie: its code is smaller.
		l, code, dcost := l1, code1, cost1
		if lw >= l1 {
			l, code, dcost = lw, codeW, costW
		}
		if l < 8 {
			// A short copy must cost less than its pixels as literals.
			lc := uint32(0)
			for _, q := range argb[i : i+l] {
				lc += cm.litCost(q)
			}
			if cm.lenCost(l)+dcost >= lc {
				if sim {
					if p == argb[i-1] {
						rs.repeat(p)
					} else {
						rs.literal(p)
					}
				}
				i++
				continue
			}
		}
		for lits < i {
			k := min(i-lits, maxLength)
			toks = append(toks, litRun(k))
			lits += k
		}
		toks = append(toks, copyTok(code, l))
		if sim {
			c.hist[numLiteral+int(lengthPrefix[l-1])]++
			dc, _, _ := prefixOf(code)
			c.hist[symDist+dc]++
			rs.copied(argb[i:i+l], argb[i-1])
		}
		i += l
		lits = i
	}
	for lits < n {
		k := min(n-lits, maxLength)
		toks = append(toks, litRun(k))
		lits += k
	}
	c.toks = toks
	if sim {
		rs.finish()
	}
}

// runSim counts the tokens of a greedy parse for the choice between two
// color cache sizes as the parse makes them, as simulate2 does after a
// parse: the literals that hit both caches (bucket 0) or only the larger
// (bucket 1) one by one, and those that hit neither as all pixels less
// the copies and the hits.
type runSim struct {
	cA, cB         []uint32
	sA, sB         uint
	mA, mB         uint32
	lit0, lit1, ms *[4 * numLiteral]uint32
	idx0, idx1     *[1 << maxCacheBits]uint32
}

// init sets s up for the two sizes in sizes, starting the misses from
// the counts of all pixels in all, and reports whether sizes holds two.
func (s *runSim) init(e *Encoder, all *histogram, sizes uint16) bool {
	var bs [2]int
	nb := 0
	for b := 1; b <= maxCacheBits; b++ {
		if sizes&(1<<b) != 0 {
			if nb == 2 {
				return false
			}
			bs[nb] = b
			nb++
		}
	}
	if nb != 2 {
		return false
	}
	cs := &e.tmp.cs
	s.cA, s.cB = cs.caches[1<<bs[0]:2<<bs[0]], cs.caches[1<<bs[1]:2<<bs[1]]
	clear(s.cA)
	clear(s.cB)
	s.sA, s.sB = uint(32-bs[0]), uint(32-bs[1])
	s.mA, s.mB = 1<<bs[0]-1, 1<<bs[1]-1
	s.lit0, s.lit1, s.ms = &cs.lit[0], &cs.lit[1], &cs.lit[2]
	s.idx0, s.idx1 = &cs.idx[0], &cs.idx[1]
	clear(s.lit0[:])
	clear(s.lit1[:])
	clear(s.idx0[:1<<bs[1]])
	clear(s.idx1[:1<<bs[1]])
	for i := 0; i < numLiteral; i++ {
		s.ms[i] = all[i]
		s.ms[numLiteral+i] = all[symRed+i]
		s.ms[2*numLiteral+i] = all[symBlue+i]
		s.ms[3*numLiteral+i] = all[symAlpha+i]
	}
	return true
}

// literal counts literal p, unlike the pixel before it.
func (s *runSim) literal(p uint32) {
	hv := p * 0x1e35a7bd
	kA, kB := hv>>s.sA&s.mA, hv>>s.sB&s.mB
	if s.cB[kB] == p {
		l, idx := s.lit1, s.idx1
		if s.cA[kA] == p {
			l, idx = s.lit0, s.idx0
		}
		l[p>>8&0xff]++
		l[numLiteral+p>>16&0xff]++
		l[2*numLiteral+p&0xff]++
		l[3*numLiteral+p>>24]++
		idx[kB]++
	}
	s.cA[kA] = p
	s.cB[kB] = p
}

// repeat counts literal p, equal to the pixel before it: a hit in both.
func (s *runSim) repeat(p uint32) {
	l := s.lit0
	l[p>>8&0xff]++
	l[numLiteral+p>>16&0xff]++
	l[2*numLiteral+p&0xff]++
	l[3*numLiteral+p>>24]++
	s.idx0[(p*0x1e35a7bd)>>s.sB&s.mB]++
}

// copied takes the pixels px of a copy, after prev, out of the misses and
// stores them in the caches.
func (s *runSim) copied(px []uint32, prev uint32) {
	ms := s.ms
	run := uint32(0)
	for _, p := range px {
		if p != prev {
			if run > 0 {
				ms[prev>>8&0xff] -= run
				ms[numLiteral+prev>>16&0xff] -= run
				ms[2*numLiteral+prev&0xff] -= run
				ms[3*numLiteral+prev>>24] -= run
				run = 0
			}
			prev = p
			hv := p * 0x1e35a7bd
			s.cA[hv>>s.sA&s.mA] = p
			s.cB[hv>>s.sB&s.mB] = p
		}
		run++
	}
	ms[prev>>8&0xff] -= run
	ms[numLiteral+prev>>16&0xff] -= run
	ms[2*numLiteral+prev&0xff] -= run
	ms[3*numLiteral+prev>>24] -= run
}

// finish takes the hits out of the misses.
func (s *runSim) finish() {
	for i := range s.ms {
		s.ms[i] -= s.lit0[i] + s.lit1[i]
	}
}

// longest returns the longest match at i, inserting i into the chains.
func (c *coder) longest(argb []uint32, i, w, limit int, ef *effort) (int, int) {
	l1, lw := c.rn.at(argb, i, w, limit)
	bl, bd := l1, 1
	if lw >= bl && lw > 0 {
		bl, bd = lw, w // above has the smaller code
	}
	if i+1 >= len(argb) {
		return bl, bd
	}
	if ef.depth == 0 {
		return bl, bd
	}
	if bl >= ef.nice {
		c.ch.insert(argb, i)
		return bl, bd
	}
	if ef.depth == 1 {
		l, d := c.ch.latest(argb, i, limit, bl)
		if l > bl {
			return l, d
		}
		return bl, bd
	}
	l, d := c.ch.longest(argb, i, limit, ef.depth, bl)
	if l > bl {
		return l, d
	}
	return bl, bd
}

// longest inserts i and returns the longest match at i among depth nodes
// of i's chain that is longer than min; its length is 0 when there is
// none. i+1 < len(a).
func (m *chains) longest(a []uint32, i, limit, depth, min int) (int, int) {
	h := m.hash2(a, i)
	cand := int(m.head[h]) - 1
	m.prev[i&m.mask] = m.head[h]
	m.head[h] = uint32(i + 1)
	bl, bd := min, 0
	if bl >= limit {
		return 0, 0
	}
	x := a[i : i+limit]
	for ; cand >= 0 && depth > 0; depth-- {
		d := i - cand
		if d > maxDist {
			break
		}
		y := a[cand : cand+limit]
		if y[bl] == x[bl] && y[0] == x[0] {
			l := matchLen(a, cand, i, limit)
			if l > bl {
				bl, bd = l, d
				if l == limit {
					break
				}
			}
		}
		next := int(m.prev[cand&m.mask]) - 1
		if next >= cand {
			break
		}
		cand = next
	}
	if bd == 0 {
		return 0, 0
	}
	return bl, bd
}

// maxRelax bounds effort.relax; the parse tries longer ones at the bound.
const maxRelax = 64

// pathLen is the length of the sliding cost buffer of the shortest-path
// parse: positions up to maxLength ahead, and room to slide rarely.
const pathLen = 1 << 14

// listMatches finds the matches worth trying at every position of argb
// and keeps them for the passes of the shortest-path parse: count[i]
// matches of position i, in order in the list. A compact list holds only
// the matches from the chains, in groups: markers (literal tokens whose
// lengths add up to the distance from the group before) and the group's
// matches as distance<<tokLenBits | length-1.
func (c *coder) listMatches(argb []uint32, w int, ef *effort) {
	c.prepare(argb, w, ef)
	n := len(argb)
	ml := &c.ml
	ml.n = 0
	cur := ml.next()
	var sa, sb matchSet
	prev, set := &sa, &sb
	if ef.compact {
		c.last = -1
		for i := 0; i < n; {
			c.search(argb, i, w, min(maxLength, n-i), ef, prev, set)
			prev, set = set, prev
			i++
		}
		return
	}
	if cap(c.mcount) < n {
		c.mcount = make([]uint8, n)
	}
	c.mcount = c.mcount[:n]
	for i := 0; i < n; {
		c.search(argb, i, w, min(maxLength, n-i), ef, prev, set)
		if len(cur)+set.n > listChunk {
			ml.close(cur)
			cur = ml.next()
		}
		for k := 0; k < set.n; k++ {
			cur = append(cur, copyTok(set.code[k], candLen(set.m[k])))
		}
		c.mcount[i] = uint8(set.n)
		prev, set = set, prev
		i++
	}
	ml.close(cur)
}

// listChunk is the size of a chunk of the match list.
const listChunk = 1 << 14

// A matchList holds the listed matches in chunks, so that it takes no
// more memory than it holds and never copies. In the full list the
// matches of a position never span two chunks.
type matchList struct {
	chunks [][]token
	n      int // chunks in use
}

// next returns an empty chunk at the end of the list.
func (ml *matchList) next() []token {
	if ml.n == len(ml.chunks) {
		ml.chunks = append(ml.chunks, make([]token, 0, listChunk))
	}
	ml.n++
	ml.chunks[ml.n-1] = ml.chunks[ml.n-1][:0]
	return ml.chunks[ml.n-1]
}

// close keeps the filled chunk cur as the last.
func (ml *matchList) close(cur []token) { ml.chunks[ml.n-1] = cur }

// push appends t to the list, in a new chunk when the last is full.
func (ml *matchList) push(t token) {
	ch := ml.chunks[ml.n-1]
	if len(ch) == cap(ch) {
		ch = ml.next()
	}
	ml.chunks[ml.n-1] = append(ch, t)
}

// len returns the number of tokens listed.
func (ml *matchList) len() int {
	t := 0
	for _, ch := range ml.chunks[:ml.n] {
		t += len(ch)
	}
	return t
}

// record adds to the compact list the match of l pixels at distance d at
// position i, after the markers that lead to i from the last group.
func (c *coder) record(i, l, d int) {
	if i != c.last {
		for gap := i - c.last; gap > 0; gap -= maxLength {
			c.ml.push(litRun(min(gap, maxLength)))
		}
		c.last = i
	}
	c.ml.push(copyTok(uint32(d), l))
}

// A listReader reads a compact list.
type listReader struct {
	chunks [][]token
	cur    []token
}

func (r *listReader) peek() (token, bool) {
	for len(r.cur) == 0 {
		if len(r.chunks) == 0 {
			return 0, false
		}
		r.cur, r.chunks = r.chunks[0], r.chunks[1:]
	}
	return r.cur[0], true
}

// seek reads the markers after the group at last and returns the
// position of the next group, or -1 when there is none.
func (r *listReader) seek(last int) int {
	at := last
	for {
		t, ok := r.peek()
		if !ok {
			return -1
		}
		if t>>tokLenBits != 0 {
			return at
		}
		at += int(t&(1<<tokLenBits-1)) + 1
		r.cur = r.cur[1:]
	}
}

// group adds the matches of the group at the reader to set.
func (r *listReader) group(c *coder, set *matchSet) {
	for {
		t, ok := r.peek()
		if !ok || t>>tokLenBits == 0 {
			return
		}
		r.cur = r.cur[1:]
		d := int(t >> tokLenBits)
		set.add(int(t&(1<<tokLenBits-1))+1, d, c.dc.code(d))
	}
}

// optimal finds the cheapest parse of argb under c.cost with the listed
// matches, with the color cache of c.cacheBits, and leaves it in c.toks;
// first tells the first pass, under guessed costs.
func (c *coder) optimal(e *Encoder, argb []uint32, ef *effort, first bool) {
	n := len(argb)
	// The matches are listed, so the chain links are dead: the steps
	// take over their buffer.
	steps := c.scratch(n + 1)
	path := e.tmp.path[:]
	// The parse reads no further than position n until the path slides,
	// and a slide sets every entry.
	for k := range path[:min(n+1, pathLen)] {
		path[k] = costInf << 32
	}
	path[0] = 0
	cm := &c.cost
	relax := min(ef.relax, maxRelax)
	if first && ef.relax1 > 0 {
		relax = min(ef.relax1, relax)
	}
	// lens[j] is the cost of length j and its value in a step, for the
	// lengths every match tries.
	lens := &e.tmp.lens
	for j := 1; j <= relax; j++ {
		lens[j] = uint64(cm.lenCost(j))<<32 | uint64(j-1)
	}
	var base int
	if ef.compact {
		base = c.optimalCompact(argb, steps, path, relax, lens)
	} else {
		base = c.optimalFull(argb, steps, path, relax, lens)
	}
	steps[n] = uint32(path[n-base])
	c.toks = append(c.toks[:0], c.tokens(steps, n)...)
}

// slide moves the path entries from position i on (at offset i-base) to
// the front, with costs relative to the cheapest of them so they stay
// small, and returns the new base, i.
func slide(path []uint64, i, base int) int {
	k := copy(path, path[i-base:])
	low := uint64(costInf)
	for _, v := range path[:k] {
		low = min(low, v>>32)
	}
	for j, v := range path[:k] {
		if v>>32 < costInf {
			path[j] = v - low<<32
		}
	}
	for ; k < pathLen; k++ {
		path[k] = costInf << 32
	}
	return i
}

// optimalFull is the parse loop of optimal over the full match list. It
// fills steps[:n] and returns the base of path.
func (c *coder) optimalFull(argb []uint32, steps []uint32, path []uint64, relax int, lens *[2 * maxRelax]uint64) int {
	n := len(argb)
	steps = steps[:n]
	relax = min(relax, maxRelax)
	cm := &c.cost
	shift := 32 - uint(c.cacheBits)
	lim := hitLimit(c.cacheBits)
	hit := c.hit[:n]
	mcount := c.mcount[:n]
	chunks := c.ml.chunks[:c.ml.n]
	list := chunks[0]
	base := 0
	for i := 0; i < n; i++ {
		if i-base > pathLen-maxLength-1 {
			base = slide(path, i, base)
		}
		pp := path[i-base : i-base+maxLength+1]
		steps[i] = uint32(pp[0])
		cost := pp[0] >> 32
		p := argb[i]
		lc := cm.litCost(p)
		if hit[i]-1 < lim {
			lc = cm.cache[(p*0x1e35a7bd)>>shift]
		}
		pp[1] = min(pp[1], (cost+uint64(lc))<<32|uint64(litRun(1)))
		k := int(mcount[i])
		if k > len(list) {
			// This chunk is used up: the matches are in the next.
			chunks = chunks[1:]
			list = chunks[0]
		}
		if k == 0 {
			continue
		}
		ms := list[:k]
		list = list[k:]
		relaxMatches(pp, ms, cost, cm, lens, relax)
	}
	return base
}

// relaxMatches offers the copies ms, listed at a position whose cheapest
// way costs cost, to the path entries pp of the positions after it: every
// length up to relax, and longer ones at their full length.
func relaxMatches(pp []uint64, ms []token, cost uint64, cm *costModel, lens *[2 * maxRelax]uint64, relax int) {
	pp = pp[:maxLength+1]
	relax = min(relax, maxRelax)
	prevLen := 0
	for _, m := range ms {
		l := int(m&(1<<tokLenBits-1)) + 1
		// The step of the copy at length 0: the length's cost and value
		// add to it without carries.
		v := (cost+uint64(cm.codeCost(m>>tokLenBits)))<<32 | uint64(m&^(1<<tokLenBits-1))
		if top := min(l, relax); prevLen < top {
			q := pp[prevLen+1 : top+1]
			ls := lens[prevLen+1 : top+1]
			ls = ls[:len(q)]
			for j, x := range q {
				q[j] = min(x, v+ls[j])
			}
		}
		if l > relax {
			pp[l] = min(pp[l], v+(uint64(cm.lenCost(l))<<32|uint64(l-1)))
		}
		prevLen = l
	}
}

// optimalCompact is the parse loop of optimal over a compact match list,
// which it completes position by position. It fills steps[:n] and returns
// the base of path.
func (c *coder) optimalCompact(argb []uint32, steps []uint32, path []uint64, relax int, lens *[2 * maxRelax]uint64) int {
	n := len(argb)
	cm := &c.cost
	shift := 32 - uint(c.cacheBits)
	lim := hitLimit(c.cacheBits)
	hit := c.hit[:n]
	// The next group at, and the matches of the position and the one
	// before it.
	rd := listReader{chunks: c.ml.chunks[:c.ml.n]}
	at := rd.seek(-1)
	c.rn = runs{}
	var sa, sb matchSet
	prev, set := &sa, &sb
	var tmp [maxListed]token
	w := c.dc.w
	base := 0
	for i := 0; i < n; i++ {
		if i-base > pathLen-maxLength-1 {
			base = slide(path, i, base)
		}
		pp := path[i-base : i-base+maxLength+1]
		steps[i] = uint32(pp[0])
		cost := pp[0] >> 32
		p := argb[i]
		lc := cm.litCost(p)
		if hit[i]-1 < lim {
			lc = cm.cache[(p*0x1e35a7bd)>>shift]
		}
		pp[1] = min(pp[1], (cost+uint64(lc))<<32|uint64(litRun(1)))
		c.base(argb, i, w, min(maxLength, n-i), prev, set)
		if i == at {
			rd.group(c, set)
			at = rd.seek(i)
		}
		for k := 0; k < set.n; k++ {
			tmp[k] = copyTok(set.code[k], candLen(set.m[k]))
		}
		relaxMatches(pp, tmp[:set.n], cost, cm, lens, relax)
		prev, set = set, prev
	}
	return base
}

// search fills set with the matches at i worth trying: those of base,
// and from the hash chain those longer than all of these, which a compact
// list records.
func (c *coder) search(a []uint32, i, w, limit int, ef *effort, prev, set *matchSet) {
	best := c.base(a, i, w, limit, prev, set)
	if i+1 >= len(a) {
		return
	}
	if best >= ef.nice || best >= limit || ef.depth == 0 {
		c.ch.insert(a, i)
		return
	}
	if c.ch.bucketed {
		c.searchBucket(a, i, limit, ef, best, set)
		return
	}
	h := c.ch.hash2(a, i)
	cand := int(c.ch.head[h]) - 1
	c.ch.prev[i&c.ch.mask] = c.ch.head[h]
	c.ch.head[h] = uint32(i + 1)
	depth := ef.depth
	if c.rn.end1-i >= 2 && a[i] == a[i-1] {
		// Inside a run: the chain starts with the run's positions from
		// i-1 back to its start, each one link to the next, and their
		// matches are as long as the run's own, with a larger code.
		// Pass them without looking.
		s := c.rn.start1
		if i-s >= depth {
			return
		}
		depth -= i - s
		cand = int(c.ch.prev[s&c.ch.mask]) - 1
	}
	x := a[i : i+limit]
	bl := max(best, 1)
	for ; cand >= 0 && depth > 0; depth-- {
		d := i - cand
		if d > maxDist {
			break
		}
		y := a[cand : cand+limit]
		if y[bl] == x[bl] && y[0] == x[0] {
			l := matchLen(a, cand, i, limit)
			if l > bl {
				bl = l
				set.add(l, d, c.dc.code(d))
				if ef.compact {
					c.record(i, l, d)
				}
				if l >= ef.nice || l == limit {
					break
				}
			}
		}
		next := int(c.ch.prev[cand&c.ch.mask]) - 1
		if next >= cand {
			break
		}
		cand = next
	}
}

// searchBucket is the chain search of search with a bucketed match
// finder: it inserts i and adds to set the matches longer than best (and
// than each other) among the latest ef.depth positions of i's hash.
func (c *coder) searchBucket(a []uint32, i, limit int, ef *effort, best int, set *matchSet) {
	m := &c.ch
	h := m.hash2(a, i)
	b := m.head[h*bucketWays : h*bucketWays+bucketWays]
	c0, c1, c2, c3 := b[0], b[1], b[2], b[3]
	b[3], b[2], b[1], b[0] = c2, c1, c0, uint32(i+1)
	cands := [bucketWays]uint32{c0, c1, c2, c3}
	k := 0
	if c.rn.end1-i >= 2 && a[i] == a[i-1] {
		// Inside a run: the latest positions are the run's from i-1
		// back to its start; their matches are as long as the run's own,
		// with a larger code. Pass them without looking.
		k = i - c.rn.start1
	}
	x := a[i : i+limit]
	bl := max(best, 1)
	for depth := min(ef.depth, bucketWays); k < depth; k++ {
		cand := int(cands[k]) - 1
		if cand < 0 {
			break
		}
		d := i - cand
		if d > maxDist {
			break
		}
		y := a[cand : cand+limit]
		if y[bl] == x[bl] && y[0] == x[0] {
			l := matchLen(a, cand, i, limit)
			if l > bl {
				bl = l
				set.add(l, d, c.dc.code(d))
				if l >= ef.nice || l == limit {
					break
				}
			}
		}
	}
}

// base fills set with the matches at i that need no chain: the copies at
// distances 1 and w, and those of i-1 (in prev) one pixel shorter. It
// returns the longest length.
//
// It builds the set that adding these to an empty set would build:
// distance w has code 1 and distance 1 code 2, below the codes of every
// other distance, and the matches of prev are by increasing length and
// code, so each is kept when it is longer than all before it.
func (c *coder) base(a []uint32, i, w, limit int, prev, set *matchSet) int {
	l1, lw := c.rn.at(a, i, w, limit)
	n := 0
	if lw > 0 {
		set.m[0], set.code[0] = candOf(lw, w), c.dc.code(w)
		n = 1
	}
	if l1 > lw {
		set.m[n], set.code[n] = candOf(l1, 1), c.dc.code(1)
		n++
	}
	best := max(l1, lw)
	for k := 0; k < prev.n; k++ {
		l, d := candLen(prev.m[k])-1, candDist(prev.m[k])
		if l < 2 || d == 1 || d == w {
			continue
		}
		if l+1 == maxLength && l < limit {
			// The match at i-1 stopped at the longest length: it may go
			// on.
			l += matchLen(a, i-d+l, i+l, limit-l)
		}
		if l <= best {
			continue
		}
		if n == maxListed {
			// Full: drop the shortest.
			copy(set.m[:], set.m[1:])
			copy(set.code[:], set.code[1:])
			n--
		}
		set.m[n], set.code[n] = candOf(l, d), prev.code[k]
		n++
		best = l
	}
	set.n = n
	return best
}

// tokens turns the parse in steps (the last step of the cheapest way to
// each position) into tokens, in place, merging literals into runs.
func (c *coder) tokens(steps []uint32, n int) []token {
	// The steps link backward from n: move each to where it starts.
	cur := steps[n]
	for k := n; k > 0; {
		// Steps of one pixel (mostly literals) take a branch that runs
		// of them predict well, rather than wait for the step's load.
		if cur&(1<<tokLenBits-1) == 0 {
			k--
		} else {
			k -= int(cur&(1<<tokLenBits-1)) + 1
		}
		steps[k], cur = cur, steps[k]
	}
	// Gather them at the front.
	out := 0
	lits := 0
	for k := 0; k < n; {
		t := steps[k]
		l := int(t&(1<<tokLenBits-1)) + 1
		if t>>tokLenBits == 0 {
			lits++
			if lits == maxLength {
				steps[out] = litRun(lits)
				out++
				lits = 0
			}
		} else {
			if lits > 0 {
				steps[out] = litRun(lits)
				out++
				lits = 0
			}
			steps[out] = t
			out++
		}
		k += l
	}
	if lits > 0 {
		steps[out] = litRun(lits)
		out++
	}
	return steps[:out]
}
