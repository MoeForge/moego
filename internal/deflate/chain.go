package deflate

import "math/bits"

const (
	chainHashBits = 15
	chainMask     = windowSize - 1
)

// chainMatcher finds matches through hash chains: head holds the latest
// position (plus base) modulo 1<<16 for each hash of four bytes, and prev,
// per position modulo the window, the distance back to the previous
// position with the same hash, 0 ending the chain.
type chainMatcher struct {
	_    [0]uint64 // sweep reads head as words
	head [1 << chainHashBits]uint16
	prev [windowSize]uint16
}

// headSweep is how often, in positions, sweep runs (Writer.swept holds the
// last time): rarely enough to be cheap, often enough that no entry gets
// near 1<<16 positions old, so its 16 bits give its distance.
const headSweep = 1<<15 - 1<<10

// reset empties the chains for a stream starting at abs.
func (m *chainMatcher) reset(abs int) {
	stale := uint16(abs - windowSize - 1)
	for i := range m.head {
		m.head[i] = stale
	}
}

// probe returns the length of the match at distance d, at most limit.
// win[i:i+limit] must be input and d <= i.
func probe(win []byte, i, d, limit int) int {
	if x := load64(win, i-d) ^ load64(win, i); x != 0 {
		return min(bits.TrailingZeros64(x)>>3, limit)
	}
	return min(8+matchLen(win, i-d+8, i+8, max(limit-8, 0)), limit)
}

func chainHash(u uint32) uint32 { return u * 0x1e35a7bd >> (32 - chainHashBits) }

// chainLevel holds the search effort of a level.
type chainLevel struct {
	depth int // most chain entries tried at a position
	nice  int // a match this long ends the search
	good  int // with a pending match this long, the lazy search tries depth/4
	lazy  bool
}

// Measured on filtered PNG rows: each level about halves the remaining
// size gap to the next, at the cost of speed.
var chainLevels = [10]chainLevel{
	2: {depth: 1, nice: 258, good: 1, lazy: true},
	3: {depth: 2, nice: 258, good: 2, lazy: true},
	4: {depth: 4, nice: 258, good: 4, lazy: true},
	5: {depth: 8, nice: 258, good: 8, lazy: true},
	6: {depth: 20, nice: 258, good: 8, lazy: true},
	7: {depth: 32, nice: 258, good: 16, lazy: true},
}

// insert adds position i to the chains. win[i:i+4] must be input.
func (m *chainMatcher) insert(win []byte, i, base int) {
	abs := i + base
	h := chainHash(load32(win, i))
	d := int(uint16(abs) - m.head[h])
	m.head[h] = uint16(abs)
	if d > windowSize {
		d = 0
	}
	m.prev[abs&chainMask] = uint16(d)
}

// matchScore weighs a match: about the bits it saves over literals, in
// units where a byte is worth scoreW and the distance code grows with its
// bit length.
func matchScore(length, dist int) int {
	return length*scoreW - bits.Len32(uint32(dist))
}

// Tuning knobs: scoreW is the weight of a byte against one bit of
// distance, minScoreAdj raises the score a match needs, lazyBonus is what
// the next position's match must beat the current one by, and walkDist
// ends the chain walk.
var (
	scoreW      = 6
	minScoreAdj = 0
	lazyBonus   = 0
	walkDist    = windowSize
)

// find inserts position i and returns the best scoring match at i scoring
// more than minScore, trying at most depth candidates, or dist 0 if there
// is none. limit, at least 4, caps the length; win[i:i+limit] must be
// input.
func (m *chainMatcher) find(win []byte, i, base, limit, minScore, depth, nice int) (length, dist, score int) {
	abs := i + base
	u := load32(win, i)
	h := chainHash(u)
	d := int(uint16(abs) - m.head[h])
	m.head[h] = uint16(abs)
	if d > windowSize {
		d = 0
	}
	m.prev[abs&chainMask] = uint16(d)
	score = minScore
	// A candidate must be at least this long to be considered.
	need := minMatch
	for d != 0 {
		cand := i - d
		if load32(win, cand+need-3) == load32(win, i+need-3) && load32(win, cand) == u {
			l := 4 + matchLen(win, cand+4, i+4, limit-4)
			if sc := matchScore(l, d); sc > score {
				length, dist, score = l, d, sc
				if l >= nice || l == limit {
					break
				}
				need = l
			}
		}
		if depth--; depth == 0 {
			break
		}
		next := int(m.prev[(abs-d)&chainMask])
		if next == 0 {
			break
		}
		if d += next; d > walkDist {
			break
		}
	}
	return length, dist, score
}

// compressChain codes win[pos:end] with hash chains and one-step lazy
// matching: a match is taken only if the next position has no better one.
func (w *Writer) compressChain(final bool) {
	win := w.win[:len(w.win)+winSlack]
	m := w.chain
	p := chainLevels[w.level]
	b := &w.blk
	base, end := w.base, w.end
	i, start := w.pos, w.pend
	last := end - 4
	minScore := matchScore(minMatch, windowSize) + minScoreAdj
	insert := func(from, to int) {
		for j := from; j < to && j <= last; j++ {
			m.insert(win, j, base)
		}
	}
	curLen, curDist, curScore := 0, 0, 0
	searched := false
	// The head is swept again at position sweepAt.
	sweepAt := w.swept + headSweep - base
	for i <= last {
		if len(b.tokens) >= bufTokens-1 {
			start = w.flushTokens(start, i, false, maxPend)
		}
		if i >= sweepAt {
			m.sweep(i + base)
			w.swept = i + base
			sweepAt = i + headSweep
		}
		if !searched {
			curLen, curDist, curScore = m.find(win, i, base, min(maxMatch, end-i), minScore, p.depth, p.nice)
		}
		searched = false
		if curDist == 0 {
			b.literal(win[i])
			i++
			continue
		}
		if p.lazy && curLen < p.nice && i+1 <= last {
			depth := p.depth
			if curLen >= p.good {
				depth = max(depth/4, 1)
			}
			if l, d, sc := m.find(win, i+1, base, min(maxMatch, end-i-1), curScore+lazyBonus, depth, p.nice); d != 0 {
				b.literal(win[i])
				i++
				curLen, curDist, curScore = l, d, sc
				searched = true
				continue
			}
			b.match(curLen, curDist)
			insert(i+2, i+curLen)
		} else {
			b.match(curLen, curDist)
			insert(i+1, i+curLen)
		}
		i += curLen
	}
	for ; i < end; i++ {
		if len(b.tokens) >= bufTokens-1 {
			start = w.flushTokens(start, i, false, maxPend)
		}
		b.literal(win[i])
	}
	w.endChunk(start, final)
}
