package vp8l

import "math"

// Bit costs for the parse are in 1/16 bit.
const (
	costBits = 4
	costOne  = 1 << costBits
	costInf  = math.MaxUint32 >> 1
)

// costModel holds the cost of every symbol of one prefix code group,
// extra bits included.
type costModel struct {
	lit    [4][numLiteral]uint32     // green, red, blue, alpha
	length [numLengthCodes]uint32    // by length prefix code
	dist   [numDistCodes]uint32      // by distance prefix code
	cache  [1 << maxCacheBits]uint32 // by cache index
	// byLen holds the cost of every length 1 to 4096 at index l-1, for
	// the parse.
	byLen [maxLength]uint32
}

// fillLengths sets byLen from length.
func (m *costModel) fillLengths() {
	for i, c := range lengthPrefix {
		m.byLen[i] = m.length[c]
	}
}

func (m *costModel) litCost(p uint32) uint32 {
	return m.lit[0][p>>8&0xff] + m.lit[1][p>>16&0xff] + m.lit[2][p&0xff] + m.lit[3][p>>24]
}

// lenCost returns the cost of length l.
func (m *costModel) lenCost(l int) uint32 { return m.byLen[l-1] }

// codeCost returns the cost of distance code c.
func (m *costModel) codeCost(c uint32) uint32 {
	p, _, _ := prefixOf(c)
	return m.dist[p]
}

// log2Cost returns -log2(f/total) in 1/16 bit.
func log2Cost(f, total float64) uint32 {
	return uint32(math.Log2(total/f)*costOne + 0.5)
}

// fromPixels sets literal costs from the channel counts of the pixels,
// and guesses the rest: lengths and distances by their extra bits and a
// spread typical of images, cache hits as half a literal.
func (m *costModel) fromPixels(argb []uint32, h *histogram) {
	h.clear()
	countLiterals(argb, h)
	total := float64(len(argb)) + 128
	for ch := range m.lit {
		for v, f := range h[alphabetStart[ch] : alphabetStart[ch]+numLiteral] {
			m.lit[ch][v] = log2Cost(float64(f)+0.5, total)
		}
	}
	var sum uint64
	for ch := range m.lit {
		for v, f := range h[alphabetStart[ch] : alphabetStart[ch]+numLiteral] {
			sum += uint64(f) * uint64(m.lit[ch][v])
		}
	}
	avg := uint32(sum / uint64(max(len(argb), 1)))
	for c := range m.length {
		m.length[c] = (3+uint32(c)/4+uint32(prefixExtra(uint32(c))))*costOne + costOne/2
	}
	for c := range m.dist {
		m.dist[c] = uint32(2+c/6+int(prefixExtra(uint32(c)))) * costOne
	}
	for k := range m.cache {
		m.cache[k] = avg/2 + costOne
	}
	m.fillLengths()
}

// unusedCost is what the parse charges for a symbol c has no code for: a
// bit more than its longest code, so later passes can still use it.
func unusedCost(c *prefixCode) uint32 {
	if c.nused <= 1 {
		return 8 * costOne
	}
	m := uint32(0)
	for _, t := range c.tab {
		m = max(m, t&15)
	}
	return min(m+1, maxCodeLen) * costOne
}

// codeCosts fills dst with the costs of the symbols from..from+len(dst)
// of c. A one-leaf code writes its symbol with no bits.
func codeCosts(dst []uint32, c *prefixCode, from int) {
	if c.nused <= 1 {
		for i := range dst {
			if from+i == int(c.sym[0]) {
				dst[i] = 0
			} else {
				dst[i] = 8 * costOne
			}
		}
		return
	}
	unused := unusedCost(c)
	for i, t := range c.tab[from : from+len(dst)] {
		if l := t & 15; l != 0 {
			dst[i] = l * costOne
		} else {
			dst[i] = unused
		}
	}
}

// fromGroup sets the costs from the codes of g, for a color cache of
// cacheBits.
func (m *costModel) fromGroup(g *group, cacheBits int) {
	for ch := range m.lit {
		codeCosts(m.lit[ch][:], &g.codes[ch], 0)
	}
	codeCosts(m.length[:], &g.codes[0], numLiteral)
	for c := range m.length {
		m.length[c] += uint32(prefixExtra(uint32(c))) * costOne
	}
	codeCosts(m.dist[:], &g.codes[4], 0)
	for c := range m.dist {
		m.dist[c] += uint32(prefixExtra(uint32(c))) * costOne
	}
	if cacheBits > 0 {
		codeCosts(m.cache[:1<<cacheBits], &g.codes[0], greenBase)
	}
	m.fillLengths()
}

// countLiterals adds the four channels of every pixel of argb to h. Alpha
// mostly repeats, so it is counted by runs: the count of one symbol over
// and over would wait on its own last store every pixel.
func countLiterals(argb []uint32, h *histogram) {
	if len(argb) == 0 {
		return
	}
	a, run := argb[0]>>24, uint32(0)
	for _, p := range argb {
		h[p>>8&0xff]++
		h[symRed+p>>16&0xff]++
		h[symBlue+p&0xff]++
		if p>>24 != a {
			h[symAlpha+a] += run
			a, run = p>>24, 0
		}
		run++
	}
	h[symAlpha+a] += run
}
