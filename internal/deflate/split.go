package deflate

import "math"

// Block splitting. The engines collect up to bufTokens tokens; the
// splitter cuts them into segments of segTokens, then merges neighbors
// greedily while one code for both is estimated to cost less than two:
// the estimate is the entropy of each alphabet plus a header guess. Each
// resulting block is then written as the cheapest of the three types.

const maxSegs = maxBlockTokens / 1024

// segTokens is the segment size; headerBase and headerPerSym the header
// guess (tuning knobs).
var (
	exactSplit     = true
	exactSegTokens = 1024
	segTokens      = 4096
	headerBase     = 80.0
	headerPerSym   = 4.0
)

type splitter struct {
	// The counts of each segment, for as many segments as a level uses.
	lit   [][numLitLen]uint32
	dist  [][numDist]uint32
	bytes [maxSegs]int
	toks  [maxSegs]int
	cost  [maxSegs]float64
	delta [maxSegs]float64 // gain of merging block i with the next
	next  [maxSegs]int     // the block after block i, or n
	mlit  [numLitLen]uint32
	mdist [numDist]uint32

	// exact, if set, computes block costs with real codes.
	exact *block
}

// noSplit turns block splitting off (tuning only).
var noSplit = false

// xlog2x[f] is f*log2(f).
var xlog2x [1 << 12]float32

func init() {
	for f := 1; f < len(xlog2x); f++ {
		xlog2x[f] = float32(float64(f) * math.Log2(float64(f)))
	}
}

func flog(f uint32) float64 {
	if f < uint32(len(xlog2x)) {
		return float64(xlog2x[f])
	}
	return float64(f) * math.Log2(float64(f))
}

// estimate returns the estimated size in bits of a dynamic block with
// these counts, leaving out extra bits.
func estimate(lit []uint32, dist []uint32) float64 {
	var n, sum float64
	used := 0
	for _, f := range lit {
		if f != 0 {
			n += float64(f)
			sum += flog(f)
			used++
		}
	}
	bits := flog32(n) - sum
	n, sum = 0, 0
	for _, f := range dist {
		if f != 0 {
			n += float64(f)
			sum += flog(f)
			used++
		}
	}
	bits += flog32(n) - sum
	// The header: about four bits per code length sent, plus the fixed
	// fields and the code length code.
	return bits + headerBase + headerPerSym*float64(used)
}

func flog32(n float64) float64 {
	if n == 0 {
		return 0
	}
	return n * math.Log2(n)
}

// count fills the histograms and byte count of segment s from tokens.
func (sp *splitter) count(s int, tokens []token) {
	lit, dist := &sp.lit[s], &sp.dist[s]
	*lit = [numLitLen]uint32{}
	*dist = [numDist]uint32{}
	n := 0
	for _, t := range tokens {
		if t&matchFlag == 0 {
			lit[uint8(t)]++
			n++
			continue
		}
		l := uint8(t >> 15)
		lit[257+int(lenSymbol[l])]++
		dist[(t>>23)&31]++
		n += int(l) + minMatch
	}
	lit[endOfBlock] = 1
	sp.bytes[s] = n
	sp.toks[s] = len(tokens)
}

// mergeCost returns the estimate for blocks a and b as one block.
func (sp *splitter) mergeCost(a, b int) float64 {
	for i := range sp.mlit {
		sp.mlit[i] = sp.lit[a][i] + sp.lit[b][i]
	}
	sp.mlit[endOfBlock] = 1
	for i := range sp.mdist {
		sp.mdist[i] = sp.dist[a][i] + sp.dist[b][i]
	}
	return sp.estimate(&sp.mlit, &sp.mdist)
}

// estimate returns the estimated or, with exact set, the computed bits
// of a block with these counts.
func (sp *splitter) estimate(lit *[numLitLen]uint32, dist *[numDist]uint32) float64 {
	if sp.exact != nil {
		return float64(sp.exact.cost(lit, dist))
	}
	return estimate(lit[:], dist[:])
}

// flushTokens writes the collected tokens, which cover win[start:end], as
// one or more blocks. Unless final, the last block stays collected when it
// has at most half a buffer of tokens and carry bytes, so that the next
// tokens can still join it. It returns where the collected tokens start.
func (w *Writer) flushTokens(start, end int, final bool, carry int) int {
	b := &w.blk
	sp := &w.split
	n := w.planBlocks(b.tokens)
	pos, tok := start, 0
	for s := 0; s < n; s = sp.next[s] {
		last := sp.next[s] >= n
		if last && !final && s > 0 && 2*sp.toks[s] <= bufTokens && sp.bytes[s] <= carry {
			b.tokens = b.tokens[:copy(b.tokens, b.tokens[tok:])]
			return pos
		}
		b.litFreq, b.distFreq = sp.lit[s], sp.dist[s]
		w.writeBlock(b.tokens[tok:tok+sp.toks[s]], pos, pos+sp.bytes[s], final && last)
		pos += sp.bytes[s]
		tok += sp.toks[s]
	}
	if pos != end {
		panic("deflate: block split lost bytes")
	}
	b.tokens = b.tokens[:0]
	return end
}

// endChunk ends a chunk whose tokens since win[start] are not written:
// they are, if the chunk is the last, and otherwise as far as needed to
// keep at most maxPend bytes of them, which wait for the next chunk; so
// the end of a chunk does not end a block.
func (w *Writer) endChunk(start int, final bool) {
	if final || w.end-start > maxPend {
		start = w.flushTokens(start, w.end, final, maxPend)
	}
	w.pend = start
}

// planBlocks groups tokens into blocks: block s, starting at segment
// 0 and then at next[s] while that is below the returned count, has
// toks[s] tokens covering bytes[s] input bytes with the counts lit[s]
// and dist[s].
func (w *Writer) planBlocks(tokens []token) int {
	sp := &w.split
	seg := segTokens
	sp.exact = nil
	if w.level >= minOptLevel && exactSplit {
		seg = exactSegTokens
		sp.exact = &w.blk
	}
	seg = max(seg, (len(tokens)+maxSegs-1)/maxSegs)
	n := (len(tokens) + seg - 1) / seg
	if len(sp.lit) < max(n, 1) {
		// As many segments as a full buffer of tokens at this level has.
		m := max(n, 1, bufTokens/seg)
		if sp.exact != nil {
			m = maxSegs
		}
		sp.lit = make([][numLitLen]uint32, m)
		sp.dist = make([][numDist]uint32, m)
	}
	if n <= 1 || noSplit {
		sp.count(0, tokens)
		sp.next[0] = 1
		return 1
	}
	for s := 0; s < n; s++ {
		sp.count(s, tokens[s*seg:min((s+1)*seg, len(tokens))])
		sp.cost[s] = sp.estimate(&sp.lit[s], &sp.dist[s])
		sp.next[s] = s + 1
	}
	for s := 0; s+1 < n; s++ {
		sp.delta[s] = sp.mergeCost(s, s+1) - sp.cost[s] - sp.cost[s+1]
	}
	for {
		best, bestDelta := -1, 0.0
		for s := 0; s < n; s = sp.next[s] {
			if sp.next[s] < n && sp.delta[s] < bestDelta {
				best, bestDelta = s, sp.delta[s]
			}
		}
		if best < 0 {
			break
		}
		// Merge best with its successor into best.
		s, t := best, sp.next[best]
		for i := range sp.lit[s] {
			sp.lit[s][i] += sp.lit[t][i]
		}
		sp.lit[s][endOfBlock] = 1
		for i := range sp.dist[s] {
			sp.dist[s][i] += sp.dist[t][i]
		}
		sp.bytes[s] += sp.bytes[t]
		sp.toks[s] += sp.toks[t]
		sp.cost[s] += sp.cost[t] + sp.delta[s]
		sp.next[s] = sp.next[t]
		if u := sp.next[s]; u < n {
			sp.delta[s] = sp.mergeCost(s, u) - sp.cost[s] - sp.cost[u]
		}
		for p := 0; p < s; p = sp.next[p] {
			if sp.next[p] == s {
				sp.delta[p] = sp.mergeCost(p, s) - sp.cost[p] - sp.cost[s]
				break
			}
		}
	}
	return n
}
