package vp8l

// A prefixCode is one prefix code of a group, ready to write: per symbol
// its header length and the code and length written for it.
type prefixCode struct {
	n     int      // alphabet size
	lens  []uint8  // code lengths as the header gives them
	tab   []uint32 // per symbol: bit-reversed code<<4 | bits written
	nused int      // symbols with a code (0 counts as 1: symbol 0)
	sym   [2]uint16
}

func (c *prefixCode) init(n int) {
	c.n = n
	if cap(c.lens) < n {
		// Only the green alphabet varies, with the cache size: room for
		// the largest.
		k := n
		if n > numLiteral {
			k = maxGreen
		}
		c.lens = make([]uint8, n, k)
		c.tab = make([]uint32, n, k)
	}
	c.lens, c.tab = c.lens[:n], c.tab[:n]
}

// build makes c the optimal code for freq with lengths at most limit. A
// code with one used symbol, or none, is a one-leaf code: its symbol is
// written with no bits.
func (c *prefixCode) build(h *huffman, freq []uint32, codes []uint16, limit int) {
	c.init(len(freq))
	used := h.build(freq, c.lens, limit)
	if used < 2 {
		s := 0
		if used == 1 {
			for i, f := range freq {
				if f != 0 {
					s = i
					break
				}
			}
		}
		clear(c.tab)
		c.lens[s] = 1
		c.nused, c.sym[0] = 1, uint16(s)
		return
	}
	c.nused = used
	if used == 2 {
		k := 0
		for i, l := range c.lens {
			if l != 0 {
				c.sym[k] = uint16(i)
				k++
			}
		}
	}
	codes = codes[:len(freq)]
	canonical(c.lens, codes)
	for i, l := range c.lens {
		c.tab[i] = uint32(codes[i])<<4 | uint32(l)
	}
}

// cost returns the bits of coding freq with c, without the header.
func (c *prefixCode) cost(freq []uint32) int {
	s := 0
	for i, f := range freq {
		s += int(f) * int(c.tab[i]&15)
	}
	return s
}

// put writes symbol s.
func (b *bitWriter) sym(c *prefixCode, s int) {
	t := c.tab[s]
	b.put(t>>4, uint(t&15))
}

// headerWriter writes prefix code headers: the simple form for one or two
// symbols below 256, otherwise the code lengths run-length coded under a
// code length code, with the count of written lengths (max_symbol) when
// that saves bits.
type headerWriter struct {
	// tokens holds the code length symbols, each sym | extra<<5.
	tokens [maxGreen]uint16
	ntok   int
	// end is the number of tokens up to the last nonzero length.
	end int

	freq  [numCodeLenSyms]uint32
	lens  [numCodeLenSyms]uint8
	codes [numCodeLenSyms]uint16
	cl    prefixCode

	// The choice made by prepare.
	simple    bool
	trimmed   bool
	trimBits  uint // length_nbits of max_symbol
	ncl       int  // code length code lengths written
	writeToks int  // tokens written
}

// simpleCode reports whether c can use the simple header.
func simpleCode(c *prefixCode) bool {
	switch c.nused {
	case 1:
		return c.sym[0] < 256
	case 2:
		return c.sym[1] < 256 // sym is sorted
	}
	return false
}

// simpleBits is the size of the simple header of c.
func simpleBits(c *prefixCode) int {
	// num_symbols, is_first_8bits, the first symbol.
	n := 2 + 8
	if c.sym[0] < 2 {
		n = 2 + 1
	}
	if c.nused == 2 {
		n += 8
	}
	return n
}

// tokenize run-length codes the lengths of c: 16 repeats the previous
// nonzero length (8 before the first) 3 to 6 times, 17 and 18 code 3 to
// 10 and 11 to 138 zeros.
func (hw *headerWriter) tokenize(lens []uint8) {
	n := len(lens)
	last := n - 1
	for last >= 0 && lens[last] == 0 {
		last--
	}
	k := 0
	prev := uint8(8)
	end := 0
	for i := 0; i < n; {
		l := lens[i]
		run := 1
		for i+run < n && lens[i+run] == l {
			run++
		}
		i += run
		if l == 0 {
			for run >= 11 {
				r := min(run, 138)
				if run-r > 0 && run-r < 3 && r > 11 {
					// Leave three zeros for a 17 rather than one or
					// two literal zeros.
					r = max(11, run-3)
				}
				hw.tokens[k] = 18 | uint16(r-11)<<5
				k++
				run -= r
			}
			if run >= 3 {
				hw.tokens[k] = 17 | uint16(run-3)<<5
				k++
				run = 0
			}
			for ; run > 0; run-- {
				hw.tokens[k] = 0
				k++
			}
		} else {
			if l != prev {
				hw.tokens[k] = uint16(l)
				k++
				run--
				prev = l
			}
			for run >= 3 {
				r := min(run, 6)
				if run-r > 0 && run-r < 3 && r > 3 {
					r = max(3, run-3)
				}
				hw.tokens[k] = 16 | uint16(r-3)<<5
				k++
				run -= r
			}
			for ; run > 0; run-- {
				hw.tokens[k] = uint16(l)
				k++
			}
		}
		if i > last && end == 0 {
			end = k
		}
	}
	hw.ntok, hw.end = k, end
}

// tokenBits is the extra bits of each code length symbol.
var tokenBits = [numCodeLenSyms]uint8{16: 2, 17: 3, 18: 7}

// clCost builds the code length code for the first m tokens and returns
// the bits of the header after the simple bit and num_code_lengths, and
// the count of code lengths written.
func (hw *headerWriter) clCost(h *huffman, m int) (int, int) {
	hw.freq = [numCodeLenSyms]uint32{}
	for _, t := range hw.tokens[:m] {
		hw.freq[t&31]++
	}
	hw.cl.build(h, hw.freq[:], hw.codes[:], maxCodeLenLen)
	ncl := numCodeLenSyms
	for ncl > 4 && hw.cl.lens[codeLenOrder[ncl-1]] == 0 {
		ncl--
	}
	bits := 3 * ncl
	for s, f := range hw.freq {
		bits += int(f) * (int(hw.cl.tab[s]&15) + int(tokenBits[s]))
	}
	return bits, ncl
}

// trimNbits returns length_nbits for max_symbol m.
func trimNbits(m int) uint {
	nb := uint(2)
	for m-2 >= 1<<nb {
		nb += 2
	}
	return nb
}

// prepare chooses the header form of c and returns its size in bits.
func (hw *headerWriter) prepare(h *huffman, c *prefixCode) int {
	if simpleCode(c) {
		hw.simple = true
		return 1 + simpleBits(c)
	}
	hw.simple = false
	hw.tokenize(c.lens)
	full, ncl := hw.clCost(h, hw.ntok)
	full += 1 + 4 + 1
	best := full
	hw.trimmed, hw.ncl, hw.writeToks = false, ncl, hw.ntok
	if hw.end >= 2 && hw.end < hw.ntok {
		nb := trimNbits(hw.end)
		t, tncl := hw.clCost(h, hw.end)
		t += 1 + 4 + 1 + 3 + int(nb)
		if t < best {
			best = t
			hw.trimmed, hw.trimBits, hw.ncl, hw.writeToks = true, nb, tncl, hw.end
			return best
		}
		// Rebuild the code for all tokens.
		hw.clCost(h, hw.ntok)
	}
	return best
}

// write writes the header of c as chosen by the last prepare.
func (hw *headerWriter) write(b *bitWriter, c *prefixCode) {
	if hw.simple {
		b.put(1, 1)
		// sym is sorted, so a first symbol below 2 takes one bit.
		s0, s1 := c.sym[0], c.sym[1]
		b.put(uint32(c.nused-1), 1)
		if s0 < 2 {
			b.put(0, 1)
			b.put(uint32(s0), 1)
		} else {
			b.put(1, 1)
			b.put(uint32(s0), 8)
		}
		if c.nused == 2 {
			b.put(uint32(s1), 8)
		}
		return
	}
	b.put(0, 1)
	b.put(uint32(hw.ncl-4), 4)
	for _, s := range codeLenOrder[:hw.ncl] {
		b.put(uint32(hw.cl.lens[s]), 3)
	}
	if hw.trimmed {
		b.put(1, 1)
		b.put(uint32(hw.trimBits-2)/2, 3)
		b.put(uint32(hw.writeToks-2), hw.trimBits)
	} else {
		b.put(0, 1)
	}
	for _, t := range hw.tokens[:hw.writeToks] {
		s := int(t & 31)
		b.sym(&hw.cl, s)
		if nb := tokenBits[s]; nb != 0 {
			b.put(uint32(t>>5), uint(nb))
		}
	}
}
