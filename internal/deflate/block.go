package deflate

// A block collects tokens, and writes those of one DEFLATE block, with
// their symbol counts in litFreq and distFreq, as the cheapest of the
// three block types.
type block struct {
	tokens   []token
	litFreq  [numLitLen]uint32
	distFreq [numDist]uint32

	huff     huffman
	litLens  [numLitLen]uint8
	distLens [numDist]uint8
	lit      codeTable
	dist     codeTable

	// The dynamic header: the run-length coded code lengths (symbol in
	// bits 0-7, repeat count extra bits in 8-15) and the code that sends
	// them.
	nlit, ndist, nclen int
	rle                [numLitLen + numDist]uint16
	nrle               int
	clFreq             [numCodeLen]uint32
	clLens             [numCodeLen]uint8
	cl                 codeTable

	// Write tables for the chosen code: per literal, then per length-3,
	// the code (for a length with its extra bits above it) and the bit
	// count in bits 24-31; per distance symbol the code, its length in
	// bits 16-23 and the extra bit count in bits 24-31.
	litLen   [512]uint32
	distCode [32]uint32
}

// maxBlockTokens bounds a block; larger blocks rarely pay for themselves
// and the cost must fit the counters.
const maxBlockTokens = 1 << 16

// bufTokens is how many tokens the fast and chain engines collect before
// planning blocks (tuning knob, at most maxBlockTokens). The last planned
// block stays collected, so a full buffer does not end a block.
var bufTokens = 1 << 16

func (b *block) reset() {
	b.tokens = b.tokens[:0]
}

// literal appends the literal c.
func (b *block) literal(c byte) {
	b.tokens = append(b.tokens, token(c))
}

// match appends a match of length bytes at distance dist.
func (b *block) match(length, dist int) {
	b.tokens = append(b.tokens, matchToken(length, dist))
}

// dynamicHeader run-length codes the code lengths of litLens and distLens
// and builds their code. It returns the header size in bits, after the
// three bits that start every block.
func (b *block) dynamicHeader() int {
	nlit := numLitLen
	for nlit > 257 && b.litLens[nlit-1] == 0 {
		nlit--
	}
	ndist := numDist
	for ndist > 1 && b.distLens[ndist-1] == 0 {
		ndist--
	}
	b.nlit, b.ndist = nlit, ndist
	var seq [numLitLen + numDist]uint8
	copy(seq[:], b.litLens[:nlit])
	copy(seq[nlit:], b.distLens[:ndist])
	n := nlit + ndist

	b.clFreq = [numCodeLen]uint32{}
	k := 0
	for i := 0; i < n; {
		v := seq[i]
		run := 1
		for i+run < n && seq[i+run] == v {
			run++
		}
		i += run
		if v == 0 {
			for run >= 11 {
				r := min(run, 138)
				b.rle[k] = 18 | uint16(r-11)<<8
				k++
				run -= r
			}
			if run >= 3 {
				b.rle[k] = 17 | uint16(run-3)<<8
				k++
				run = 0
			}
		} else {
			b.rle[k] = uint16(v)
			k++
			run--
			for run >= 3 {
				r := min(run, 6)
				b.rle[k] = 16 | uint16(r-3)<<8
				k++
				run -= r
			}
		}
		for ; run > 0; run-- {
			b.rle[k] = uint16(v)
			k++
		}
	}
	b.nrle = k
	for _, r := range b.rle[:k] {
		b.clFreq[r&0xff]++
	}
	b.huff.build(b.clFreq[:], b.clLens[:], maxCodeLenBits)
	nclen := numCodeLen
	for nclen > 4 && b.clLens[codeLenOrder[nclen-1]] == 0 {
		nclen--
	}
	b.nclen = nclen
	b.cl.build(b.clLens[:])
	bits := 5 + 5 + 4 + 3*nclen + 2*int(b.clFreq[16]) + 3*int(b.clFreq[17]) + 7*int(b.clFreq[18])
	for s, f := range b.clFreq {
		bits += int(f) * int(b.clLens[s])
	}
	return bits
}

// prepare fills litLen and distCode from the codes lit and dist.
func (b *block) prepare(lit, dist *codeTable) {
	for c := 0; c < 256; c++ {
		b.litLen[c] = lit.code[c]&0xffff | lit.code[c]>>16<<24
	}
	for x := 0; x < 256; x++ {
		s := lenSymbol[x]
		c := lit.code[257+int(s)]
		l := c >> 16
		b.litLen[256+x] = c&0xffff | uint32(x-int(lenBase[s]))<<l | (l+uint32(lenExtra[s]))<<24
	}
	for s := 0; s < numDist; s++ {
		b.distCode[s] = dist.code[s] | uint32(distExtra[s])<<24
	}
}

// storedBits returns the size of n bytes as stored blocks, starting at bit
// offset nbits within a byte.
func storedBits(n int, nbits uint) int {
	if n <= 0xffff {
		return 3 + int((8-(nbits+3)&7)&7) + 32 + 8*n
	}
	pieces := (n + 0xfffe) / 0xffff
	return int((8-(nbits+3)&7)&7) + pieces*(3+32) + (pieces-1)*5 + 8*n
}

// cost returns the bits of a block with these counts as the cheaper of a
// dynamic and a fixed-code block, leaving out the extra bits, which do not
// depend on the code. It uses the block's code scratch.
func (b *block) cost(lit *[numLitLen]uint32, dist *[numDist]uint32) int {
	b.huff.build(lit[:], b.litLens[:], maxCodeBits)
	b.huff.build(dist[:], b.distLens[:], maxCodeBits)
	dynamic := b.dynamicHeader()
	fixed := 0
	for s, f := range lit {
		dynamic += int(f) * int(b.litLens[s])
		fixed += int(f) * int(fixedLit.lens[s])
	}
	for s, f := range dist {
		dynamic += int(f) * int(b.distLens[s])
		fixed += int(f) * 5
	}
	return 3 + min(dynamic, fixed)
}
