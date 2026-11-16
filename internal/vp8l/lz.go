package vp8l

// Backward references. Matches are searched in hash chains over pairs of
// pixels, plus two fixed distances that are cheap to code and common in
// images: the pixel to the left (a run) and the one above.

const (
	windowBits = 20 // the chains reach back at most this far, and maxDist
)

// distCoder maps plain distances to distance codes for one image width:
// the smallest neighbourhood code that names the distance, else the
// distance plus 120.
type distCoder struct {
	near []uint8 // near[d] is the code for distance d, or 0
	w    int
}

func (dc *distCoder) init(w int) {
	n := 7*w + 9 // (8, 7) is the farthest neighbour
	if cap(dc.near) < n {
		dc.near = make([]uint8, n)
	}
	dc.near = dc.near[:n]
	clear(dc.near)
	for c := numNeighbours; c >= 1; c-- {
		o := neighbours[c-1]
		d := max(int(o[0])+int(o[1])*w, 1)
		dc.near[d] = uint8(c)
	}
	dc.w = w
}

func (dc *distCoder) code(d int) uint32 {
	if d < len(dc.near) {
		if c := dc.near[d]; c != 0 {
			return uint32(c)
		}
	}
	return uint32(d + numNeighbours)
}

// matchLen returns how many pixels from a[i] on equal those from a[j] on,
// at most limit; j < i and i+limit <= len(a).
func matchLen(a []uint32, j, i, limit int) int {
	x, y := a[i:i+limit], a[j:j+limit]
	for k := range x {
		if x[k] != y[k] {
			return k
		}
	}
	return limit
}

// chains is a hash-chain match finder.
type chains struct {
	head []uint32 // per hash: the latest position + 1, 0 when none
	own  []uint32 // the buffer of head, unless head is bucketed
	// prev holds per position (modulo the window, for images larger
	// than it) the previous one + 1.
	prev []uint32
	bits uint
	mask int
	// bucketed: head holds per hash the latest bucketWays positions + 1,
	// latest first, and there are no links. Walking a chain to depth
	// bucketWays visits the same positions; a bucket has them all at
	// once rather than one link after the other.
	bucketed bool
}

// bucketWays is the depth of a bucketed match finder.
const bucketWays = 4

// init sets the chains up for n positions with a hash table of at most
// 1<<maxBits heads; without chained, only the heads (for latest); with
// bucketed, buckets. The links live in c's scratch buffer.
func (m *chains) init(n int, maxBits uint, chained, bucketed bool, c *coder) {
	hb := uint(8)
	for hb < maxBits && 1<<hb < n {
		hb++
	}
	m.bits = hb
	m.bucketed = bucketed
	if bucketed {
		// The buckets take the scratch buffer, as the links would, and
		// at least the n+1 entries the parse steps need next.
		size := bucketWays << hb
		m.head = c.scratch(max(size, n+1))[:size]
		clear(m.head)
		return
	}
	if cap(m.own) < 1<<hb {
		m.own = make([]uint32, 1<<hb)
	}
	m.head = m.own[:1<<hb]
	clear(m.head)
	if !chained {
		return
	}
	// prev is written before it is read: no clearing needed. It takes
	// the n+1 entries the parse steps need next, so the buffer is sized
	// once.
	if n <= 1<<windowBits {
		m.prev = c.scratch(n + 1)[:n]
		m.mask = -1
	} else {
		m.prev = c.scratch(n + 1)[:1<<windowBits]
		m.mask = 1<<windowBits - 1
	}
}

// hash2 hashes the pixels at i and i+1.
func (m *chains) hash2(a []uint32, i int) uint32 {
	v := uint64(a[i]) | uint64(a[i+1])<<32
	return uint32((v * 0x9e3779b97f4a7c15) >> (64 - m.bits))
}

// insert adds position i, which has a pixel after it.
func (m *chains) insert(a []uint32, i int) {
	h := m.hash2(a, i)
	if m.bucketed {
		b := m.head[h*bucketWays : h*bucketWays+bucketWays]
		b[3], b[2], b[1], b[0] = b[2], b[1], b[0], uint32(i+1)
		return
	}
	m.prev[i&m.mask] = m.head[h]
	m.head[h] = uint32(i + 1)
}

// A cand is a match: length<<32 | distance.
type cand = uint64

func candOf(l, d int) cand { return uint64(l)<<32 | uint64(d) }
func candLen(c cand) int   { return int(c >> 32) }
func candDist(c cand) int  { return int(uint32(c)) }

// matchSet collects the matches at one position that are worth keeping:
// each is longer than every match with a smaller distance code. They are
// kept by increasing length, so by increasing code: for any length, the
// first match at least that long has the smallest code.
type matchSet struct {
	m    [maxListed]cand
	code [maxListed]uint32
	n    int
}

const maxListed = 16

// add offers a match of length l at distance d with distance code c.
func (s *matchSet) add(l, d int, c uint32) {
	// Dominated: a match at least as long with a code no larger.
	k := s.n
	for k > 0 && candLen(s.m[k-1]) >= l {
		k--
	}
	// s.m[k:] are at least as long; the first has the smallest code
	// among them.
	if k < s.n && s.code[k] <= c {
		return
	}
	// Drop the shorter ones whose codes are no smaller.
	j := k
	for j > 0 && s.code[j-1] >= c {
		j--
	}
	if k < s.n && candLen(s.m[k]) == l {
		k++ // same length, larger code: replaced
	}
	tail := s.n - k
	if j+1+tail > maxListed {
		// Full: drop the shortest, which may be the new one.
		if j == 0 {
			return
		}
		copy(s.m[:], s.m[1:j])
		copy(s.code[:], s.code[1:j])
		j--
	}
	copy(s.m[j+1:], s.m[k:s.n])
	copy(s.code[j+1:], s.code[k:s.n])
	s.m[j], s.code[j] = candOf(l, d), c
	s.n = j + 1 + tail
}

// latest is the match finder of depth 1: the hash table holds the latest
// position of each hash, without chains. It returns the match at i with
// that position if it is longer than min, and makes i the latest.
func (m *chains) latest(a []uint32, i, limit, min int) (int, int) {
	h := m.hash2(a, i)
	cand := int(m.head[h]) - 1
	m.head[h] = uint32(i + 1)
	if cand < 0 || min >= limit || i-cand > maxDist {
		return 0, 0
	}
	x, y := a[i:i+limit], a[cand:cand+limit]
	if y[min] != x[min] {
		return 0, 0
	}
	l := matchLen(a, cand, i, limit)
	if l <= min {
		return 0, 0
	}
	return l, i - cand
}

// setLatest makes i the latest position of its hash; i+1 < len(a).
func (m *chains) setLatest(a []uint32, i int) {
	m.head[m.hash2(a, i)] = uint32(i + 1)
}
