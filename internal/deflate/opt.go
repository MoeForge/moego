package deflate

import (
	"fmt"
	"math"
)

// The best engine (level 9): a binary-tree match finder lists, at every
// position, the nearest match of each length from 3 up; a shortest-path parse over
// bit costs then picks literals and matches. The costs start from the
// byte statistics of the segment and are refined from the code lengths of
// the previous parse.

const (
	btHashBits = 14
	optSeg     = 1 << 17 // positions parsed at once at most
	// optMatches bounds the listed matches of a segment; a segment ends
	// early when they would not fit.
	optMatches = 3 * optSeg
	maxListed  = 64 // matches listed at one position at most
	optRoom    = 64 // room for the matches of one search at the default depth
	markShift  = 8
	// pathLen is the length of the parse's sliding cost buffer: positions
	// up to maxMatch ahead, and room to slide rarely.
	pathLen = 1 << 12

	costBits = 4 // costs are in 1/16 bit
	costInf  = math.MaxUint32 >> 1
)

type btMatcher struct {
	head [1 << btHashBits]int32
	// child holds, per position modulo the window, the subtrees smaller
	// (0) and larger (1) than the node there, each as the distance back
	// from the node to the subtree's root, side by side so one cache line
	// serves both. A node is newer than all nodes below it, so the
	// distance is positive; windowSize or more ends the walk, and noChild
	// is how an empty subtree is stored.
	child [windowSize][2]uint16
}

const noChild = windowSize

// btHash hashes the low three bytes of u. Trees on three bytes find the
// nearest match of length 3 at their root, and filtered image rows are
// full of them (one RGB pixel).
func btHash(u uint32) uint32 { return (u << 8) * 0x9e3779b1 >> (32 - btHashBits) }

func (m *btMatcher) rebase(delta int32) {
	for i, v := range m.head {
		m.head[i] = max(v-delta, 0)
	}
}

// packMatch packs a listed match: distance-1 in bits 0-14, the length in
// bits 15-23 and the distance symbol in bits 24-28.
func packMatch(length, dist int) uint32 {
	return uint32(dist-1) | uint32(length)<<15 | distSymbol(dist-1)<<24
}

// find inserts position i into the tree and appends to out the matches at
// i, by increasing length, each the nearest of its length. limit, at
// least 4, caps the lengths and win[i:i+limit] must be input; the walk
// visits at most depth nodes and ends at a match of length nice. known
// bytes at distance knownDist are known to match. It returns the longest
// length and its distance.
func (m *btMatcher) find(win []byte, i, base, limit, depth, nice, known, knownDist int, out []uint32) ([]uint32, int, int) {
	abs := i + base
	best, bestDist := minMatch-1, 0
	h := btHash(load32(win, i))
	// d is how far back the node visited is.
	d := abs - int(m.head[h])
	m.head[h] = int32(abs)
	// attach[s] is where the next node smaller (1) or larger (0) than the
	// new one goes, owner[s] how far back the node holding it is, and
	// lens[s] how much that side shares with the new one.
	node := &m.child[abs&(windowSize-1)]
	attach := [2]*uint16{&node[1], &node[0]}
	var owner, lens [2]int
	truncated := limit < nice
	nice = min(nice, limit)
	for {
		// A node windowSize back shares its slot with the new one.
		if d >= windowSize || depth == 0 {
			*attach[0], *attach[1] = noChild, noChild
			return out, best, bestDist
		}
		depth--
		c := i - d
		l := min(lens[0], lens[1])
		if d == knownDist {
			l = max(l, min(known, nice))
		}
		if l < nice {
			l += matchLen(win, c+l, i+l, nice-l)
		}
		if l >= nice && !truncated {
			// The tree stops here, but the match goes on.
			l += matchLen(win, c+l, i+l, limit-l)
		}
		if l > best {
			best, bestDist = l, d
			out = append(out, packMatch(l, d))
		}
		cn := &m.child[(abs-d)&(windowSize-1)]
		if l >= nice {
			if truncated {
				// The input ends before the order of the two is known;
				// drop the node rather than guess, so the tree stays
				// sorted once more input arrives.
				*attach[0], *attach[1] = noChild, noChild
			} else {
				// Equal for nice bytes: the new node takes this one's
				// place and children.
				*attach[1] = uint16(min(d+int(cn[0])-owner[1], noChild))
				*attach[0] = uint16(min(d+int(cn[1])-owner[0], noChild))
			}
			return out, best, bestDist
		}
		// s is 1 when the node is smaller: it joins the smaller side,
		// whose next attach point is its larger child.
		s := 0
		if win[c+l] < win[i+l] {
			s = 1
		}
		*attach[s] = uint16(d - owner[s])
		attach[s] = &cn[s]
		owner[s] = d
		lens[s] = l
		d += int(cn[s])
	}
}

type optimizer struct {
	bt   btMatcher
	warm bool // costs hold the last segment's

	segData []byte
	// Per position k of the segment: count[k] matches are listed, from
	// matches[mark[k>>markShift]] on for the first of each run of
	// 1<<markShift positions, and steps[k] holds the last step of the
	// cheapest way there as stepLen(length) | dist-1, the length 1 for a
	// literal.
	matches []uint32
	count   []uint8
	mark    []uint32
	steps   []uint32
	// path holds, while the parse runs, the cheapest ways found so far to
	// the positions just ahead: the cost in the high half, the step in the
	// low half. One min keeps the cheaper of two ways without a branch
	// and, at equal cost, the longer step.
	path [pathLen]uint64

	listed, positions int // totals over the stream, for tuning

	litCost  [256]uint32
	lenCost  [maxMatch + 1]uint32
	distCost [numDist]uint32 // code bits plus extra bits

	litFreq  [numLitLen]uint32
	distFreq [numDist]uint32
	litLens  [numLitLen]uint8
	distLens [numDist]uint8
}

// optLevel holds the effort of the best engine.
type optLevel struct {
	// The tree walk visits depth nodes and stops at a match of nice bytes.
	depth, nice int
	// iters parses run per segment, firstIters on the first segment of a
	// stream, whose costs start from a guess rather than the previous
	// segment's; each block is then parsed reparse more times with the
	// costs of its own code.
	iters, firstIters, reparse int
	// seg is the most positions parsed with one cost model, at most
	// optSeg. Shorter segments need less memory: the matches of a whole
	// segment are listed before it is parsed.
	seg int
}

// minOptLevel is the lowest level that uses the near-optimal parse.
const minOptLevel = 8

// optLevels holds the effort of levels 8 and 9. Level 9: deeper trees and
// more passes each still gain a little on filtered PNG rows, but past
// these the time grows much faster than the gain. Level 8, about twice as
// fast and 3% larger, has a shallow tree, fewer passes on the first
// segment, no block re-parse and half-length segments, which cost 0.04%
// in size and save about 1 MB on a large image.
var optLevels = [10]optLevel{
	8: {depth: 8, nice: maxMatch, iters: 1, firstIters: 3, reparse: 0, seg: optSeg / 2},
	9: {depth: 32, nice: maxMatch, iters: 1, firstIters: 6, reparse: 2, seg: optSeg},
}

// grow sizes the per-position arrays for n positions.
func (o *optimizer) grow(n int) {
	if len(o.steps) > n {
		return
	}
	n = min(max(n+1, 2*len(o.steps)), optSeg+1)
	o.count = make([]uint8, n)
	o.mark = make([]uint32, n>>markShift+1)
	o.steps = make([]uint32, n)
	// Room for three matches per position, which the listing doubles
	// when a short segment needs more. Long segments stop at optMatches.
	o.matches = make([]uint32, 0, min(3*n, optMatches)+optRoom)
}

// compressOpt codes win[pos:end] with the near-optimal parse.
func (w *Writer) compressOpt(final bool) {
	o := w.opt
	win := w.win[:len(w.win)+winSlack]
	p := optLevels[w.level]
	base, end := w.base, w.end
	for start := w.pos; start < end; {
		// List the matches of a segment.
		o.grow(min(end-start, p.seg))
		o.matches = o.matches[:0]
		n := 0
		// The longest match at the previous position, one byte shorter,
		// holds here at the same distance.
		known, knownDist := 0, 0
		for i := start; i < end && n < p.seg && len(o.matches) < optMatches; i++ {
			if n&(1<<markShift-1) == 0 {
				o.mark[n>>markShift] = uint32(len(o.matches))
			}
			o.count[n] = 0
			n++
			limit := min(maxMatch, end-i)
			if limit < 4 {
				continue
			}
			first := len(o.matches)
			if room := max(p.depth, optRoom); cap(o.matches)-first < room {
				m := make([]uint32, first, max(min(2*cap(o.matches), optMatches+room), first+room))
				copy(m, o.matches)
				o.matches = m
			}
			o.matches, known, knownDist = o.bt.find(win, i, base, limit, p.depth, p.nice, known-1, knownDist, o.matches)
			if len(o.matches)-first > maxListed {
				// Keep the longest.
				copy(o.matches[first:], o.matches[len(o.matches)-maxListed:])
				o.matches = o.matches[:first+maxListed]
			}
			o.count[n-1] = uint8(len(o.matches) - first)
		}
		o.listed += len(o.matches)
		o.positions += n
		if verifyMatches {
			o.verify(win, start, n, end)
		}
		o.segData = win[start : start+n]

		iters := p.iters
		if !o.warm {
			o.initCosts(o.segData)
			iters = p.firstIters
		}
		o.warm = true
		for it := 0; ; it++ {
			o.parse(0, n)
			if it+1 >= iters {
				break
			}
			o.countPath(0, n)
			o.setCosts(&w.blk.huff)
		}
		// Plan the blocks on this parse, then parse each block again
		// with the costs of its own code.
		last := final && start+n == end
		planned := o.tokens(0, n)
		sp := &w.split
		ns := w.planBlocks(planned)
		lo, tok := 0, 0
		for b := 0; b < ns; b = sp.next[b] {
			hi := lo + sp.bytes[b]
			// A block's own costs re-parse it; those of the last block
			// also start the parse of the next segment.
			if p.reparse > 0 || sp.next[b] >= ns {
				o.litFreq, o.distFreq = sp.lit[b], sp.dist[b]
				o.setCosts(&w.blk.huff)
			}
			// Without a re-parse the block is the planned tokens.
			toks := planned[tok : tok+sp.toks[b]]
			w.blk.litFreq, w.blk.distFreq = sp.lit[b], sp.dist[b]
			if p.reparse > 0 {
				for it := 0; it < p.reparse; it++ {
					o.parse(lo, hi)
					if it+1 < p.reparse {
						o.countPath(lo, hi)
						o.setCosts(&w.blk.huff)
					}
				}
				o.countPath(lo, hi)
				w.blk.litFreq, w.blk.distFreq = o.litFreq, o.distFreq
				toks = o.tokens(lo, hi)
			}
			w.writeBlock(toks, start+lo, start+hi, last && sp.next[b] >= ns)
			lo, tok = hi, tok+sp.toks[b]
		}
		start += n
	}
}

// parse finds the cheapest path from position lo to hi of the segment
// under the current costs, leaving its steps in steps.
func (o *optimizer) parse(lo, hi int) {
	seg := o.segData
	steps := o.steps[:hi+1]
	// path[k] is the way to position base+k.
	path := o.path[:]
	base := lo
	for k := range path {
		path[k] = costInf << 32
	}
	path[0] = 0
	off := o.offset(lo)
	for i := lo; i < hi; i++ {
		if i-base > pathLen-maxMatch-1 {
			// Slide: keep the positions from i on.
			k := copy(path, path[i-base:])
			for ; k < pathLen; k++ {
				path[k] = costInf << 32
			}
			base = i
		}
		path := path[i-base : i-base+maxMatch+1]
		steps[i] = uint32(path[0])
		c := path[0] >> 32
		path[1] = min(path[1], (c+uint64(o.litCost[seg[i]]))<<32|stepLen(1))
		room := hi - i
		prev := minMatch
		ms := o.matches[off : off+int(o.count[i])]
		off += len(ms)
		for _, mt := range ms {
			l := min(int(mt>>15&0x1ff), room) + 1
			if l <= prev {
				continue
			}
			dc := (c+uint64(o.distCost[mt>>24&31]))<<32 | uint64(mt&0x7fff)
			// Lengths past relaxCap: only the longest. The positions
			// inside are reached from the matches of the next ones.
			top := l
			if l > relaxCap+1 {
				top = max(prev, relaxCap+1)
				path[l-1] = min(path[l-1], dc+(uint64(o.lenCost[l-1])<<32|stepLen(l-1)))
			}
			lc := o.lenCost[prev:top]
			ps := path[prev:top]
			for k := range lc {
				ps[k] = min(ps[k], dc+(uint64(lc[k])<<32|stepLen(prev+k)))
			}
			prev = l
		}
	}
	steps[hi] = uint32(path[hi-base])
}

// offset returns the index in matches of the first match at position k.
func (o *optimizer) offset(k int) int {
	off := int(o.mark[k>>markShift])
	for _, c := range o.count[k&^(1<<markShift-1) : k] {
		off += int(c)
	}
	return off
}

func stepLen(l int) uint64 { return uint64(511-l) << 15 }

// step returns the length and distance-1 of a step.
func step(p uint32) (length, dist1 int) {
	return 511 - int(p>>15), int(p & 0x7fff)
}

// tokens turns the parse from lo to hi into tokens in place and returns
// them: they end up in steps[lo:], which no longer holds the parse.
func (o *optimizer) tokens(lo, hi int) []token {
	steps, seg := o.steps, o.segData
	// The steps link backward from hi: move each, as a token, to where it
	// starts, which holds the step before it.
	cur := steps[hi]
	for k := hi; k > lo; {
		l, d1 := step(cur)
		k -= l
		var t token
		if l == 1 {
			t = token(seg[k])
		} else {
			t = matchToken(l, d1+1)
		}
		steps[k], cur = t, steps[k]
	}
	// Then gather them at the front.
	n := lo
	for k := lo; k < hi; n++ {
		t := steps[k]
		steps[n] = t
		k++
		if t&matchFlag != 0 {
			k += int(t>>15&0xff) + minMatch - 1
		}
	}
	return steps[lo:n]
}

// initCosts sets first-pass costs: literals from the byte counts of the
// segment, matches as if every length and distance symbol were equally
// likely within a typical spread.
func (o *optimizer) initCosts(seg []byte) {
	var count [256]uint32
	for _, c := range seg {
		count[c]++
	}
	// Bytes not seen still get a finite cost: every count gets a half.
	total := float64(len(seg)) + 128
	for c := range o.litCost {
		f := float64(count[c]) + 0.5
		o.litCost[c] = bitCost(math.Log2(total / f))
	}
	for l := minMatch; l <= maxMatch; l++ {
		s := lenSymbol[l-minMatch]
		o.lenCost[l] = bitCost(5 + float64(lenExtra[s]))
	}
	for d := range o.distCost {
		o.distCost[d] = bitCost(5 + float64(distExtra[d]))
	}
}

func bitCost(b float64) uint32 {
	return uint32(b*(1<<costBits) + 0.5)
}

// countPath counts the symbols of the parse from lo to hi.
func (o *optimizer) countPath(lo, hi int) {
	seg := o.segData
	o.litFreq = [numLitLen]uint32{}
	o.distFreq = [numDist]uint32{}
	for k := hi; k > lo; {
		l, d1 := step(o.steps[k])
		if l == 1 {
			o.litFreq[seg[k-1]]++
		} else {
			o.litFreq[257+int(lenSymbol[l-minMatch])]++
			o.distFreq[distSymbol(d1)]++
		}
		k -= l
	}
	o.litFreq[endOfBlock] = 1
}

// setCosts sets costs from the code lengths a Huffman code for litFreq
// and distFreq would have, building it in h. Symbols not counted cost a
// bit more than the longest code, so later passes can still try them.
func (o *optimizer) setCosts(h *huffman) {
	h.build(o.litFreq[:], o.litLens[:], maxCodeBits)
	h.build(o.distFreq[:], o.distLens[:], maxCodeBits)
	unused := func(lens []uint8) uint32 {
		m := uint8(0)
		for _, l := range lens {
			m = max(m, l)
		}
		return uint32(min(m+1, maxCodeBits)) << costBits
	}
	ul, ud := unused(o.litLens[:]), unused(o.distLens[:])
	for c := range o.litCost {
		o.litCost[c] = symbolCost(o.litLens[c], ul)
	}
	for l := minMatch; l <= maxMatch; l++ {
		s := lenSymbol[l-minMatch]
		o.lenCost[l] = symbolCost(o.litLens[257+int(s)], ul) + uint32(lenExtra[s])<<costBits
	}
	for d := range o.distCost {
		o.distCost[d] = symbolCost(o.distLens[d], ud) + uint32(distExtra[d])<<costBits
	}
}

func symbolCost(l uint8, unused uint32) uint32 {
	if l == 0 {
		return unused
	}
	return uint32(l) << costBits
}

// verifyMatches makes compressOpt check every listed match (tests only).
var verifyMatches = false

func (o *optimizer) verify(win []byte, start, n, end int) {
	off := 0
	for k := 0; k < n; k++ {
		p := start + k
		prev := 0
		if o.offset(k) != off {
			panic("deflate: bad match offsets")
		}
		ms := o.matches[off : off+int(o.count[k])]
		off += len(ms)
		for _, mt := range ms {
			l, d := int(mt>>15&0x1ff), int(mt&0x7fff)+1
			if l <= prev || l < minMatch || d >= windowSize || p-d < 0 || p+l > end || string(win[p-d:p-d+l]) != string(win[p:p+l]) {
				panic(fmt.Sprintf("deflate: bad match at %d: length %d distance %d", p, l, d))
			}
			prev = l
		}
	}
}

// relaxCap is the longest length the parse tries at every value; for
// longer matches it tries only the full length.
var relaxCap = 16
