package webp

import "math"

// Predictor search. Each tile gets the mode whose residuals cost the
// fewest bits under a model of the residual statistics: per channel, the
// code length -log2(p) of every byte value. The first pass builds the
// model as it goes, from a weak prior plus the residuals of the tiles
// already chosen, refreshed every tile row; later passes choose again
// under the histogram of the whole previous pass. A mode that differs
// from both the left and the top tile's costs a little more, which keeps
// the predictor image cheap.

// model holds the bit cost of each byte value of each channel, indexed by
// byte position in the pixel (0 is blue, 3 alpha), in fixed point with
// costOne per bit, so that sums are exact and independent of their order.
type model struct {
	cost [4][256]uint32
	// For prior, pc[v] is prior*priorCount[v] and lp[v] the log2 of the
	// count of an unseen value v: log2(pc[v]), or of 1/2 without a prior.
	prior  float32
	pcOK   bool
	pc, lp [256]float32
}

// costShift sets the fixed point of the search costs: costOne is one bit.
const (
	costShift = 12
	costOne   = 1 << costShift
)

// fixCost converts a cost in bits to fixed point.
func fixCost(b float32) uint32 {
	if b <= 0 {
		return 0
	}
	return uint32(b*costOne + 0.5)
}

// hist4 counts byte values per channel, indexed like model.
type hist4 [4][256]uint32

func (h *hist4) add(p uint32) {
	h[0][p&0xff]++
	h[1][p>>8&0xff]++
	h[2][p>>16&0xff]++
	h[3][p>>24]++
}

// searchHist is the search's histogram: two copies of the counts, which
// the residuals of a row fill in turn, so that a run of one value does not
// wait on its own increments. model.set adds them up.
type searchHist [2]hist4

// addScored counts the channels of set alone in row: the search never
// reads the costs of the others.
func (h *searchHist) addScored(row []uint32, set int) {
	a, b := &h[0], &h[1]
	k := 0
	switch set {
	case scoreGreen:
		for ; k+1 < len(row); k += 2 {
			p, q := row[k], row[k+1]
			a[1][p>>8&0xff]++
			b[1][q>>8&0xff]++
		}
	case scoreRGB:
		for ; k+1 < len(row); k += 2 {
			p, q := row[k], row[k+1]
			a[0][p&0xff]++
			b[0][q&0xff]++
			a[1][p>>8&0xff]++
			b[1][q>>8&0xff]++
			a[2][p>>16&0xff]++
			b[2][q>>16&0xff]++
		}
	default:
		for ; k+1 < len(row); k += 2 {
			p, q := row[k], row[k+1]
			a[0][p&0xff]++
			b[0][q&0xff]++
			a[1][p>>8&0xff]++
			b[1][q>>8&0xff]++
			a[2][p>>16&0xff]++
			b[2][q>>16&0xff]++
			a[3][p>>24]++
			b[3][q>>24]++
		}
	}
	if k < len(row) {
		p := row[k]
		switch set {
		case scoreGreen:
			a[1][p>>8&0xff]++
		case scoreRGB:
			a[0][p&0xff]++
			a[1][p>>8&0xff]++
			a[2][p>>16&0xff]++
		default:
			a.add(p)
		}
	}
}

// scored reports whether set scores channel c.
func scored(set, c int) bool {
	switch set {
	case scoreGreen:
		return c == 1
	case scoreRGB:
		return c < 3
	}
	return true
}

// priorCount is the weak prior of the first pass: a count for each byte
// value that falls off with its magnitude as a signed residual.
var priorCount = func() (p [256]float32) {
	for v := range p {
		s := float64(int8(uint8(v)))
		p[v] = float32(4 / (1 + math.Abs(s)))
	}
	return p
}()

// set builds the costs of the channels of set from counts h plus prior
// times priorCount.
func (m *model) set(h *searchHist, prior float32, set int) {
	if !m.pcOK || m.prior != prior {
		m.prior, m.pcOK = prior, true
		for v := range m.pc {
			f := prior * priorCount[v]
			m.pc[v] = f
			if f <= 0 {
				// Unseen without a prior: as if it had count 1/2.
				f = 0.5
			}
			m.lp[v] = log2f(f)
		}
	}
	pc, lp := &m.pc, &m.lp
	for c := range m.cost {
		if !scored(set, c) {
			continue
		}
		h0, h1 := &h[0][c], &h[1][c]
		var n float32
		for v := range h0 {
			n += float32(h0[v]+h1[v]) + pc[v]
		}
		if n == 0 {
			clear(m.cost[c][:])
			continue
		}
		ln := log2f(n)
		cost := &m.cost[c]
		for v := range h0 {
			l := lp[v]
			if k := h0[v] + h1[v]; k != 0 {
				l = log2f(float32(k) + pc[v])
			}
			cost[v] = fixCost(ln - l)
		}
	}
}

// log2f is log2 for positive x, within 1e-4: the exponent plus the log
// of the mantissa from a table, interpolated linearly.
func log2f(x float32) float32 {
	b := math.Float32bits(x)
	e := float32(int32(b>>23&0xff) - 127)
	i := b >> (23 - mantBits) & (1<<mantBits - 1)
	f := float32(b&(1<<(23-mantBits)-1)) * (1.0 / (1 << (23 - mantBits)))
	return e + mantLog[i] + f*(mantLog[i+1]-mantLog[i])
}

const mantBits = 7

// mantLog[i] is log2(1 + i/2^mantBits).
var mantLog = func() (t [1<<mantBits + 1]float32) {
	for i := range t {
		t[i] = float32(math.Log2(1 + float64(i)/(1<<mantBits)))
	}
	return t
}()

// Channel sets scored by the search. A channel that holds black's value
// (0 for red, green and blue, 255 for alpha) in every pixel has the same
// residual, 0, under every mode, so it adds the same cost to every
// choice and is left out.
const (
	scoreAll   = iota // every channel
	scoreRGB          // alpha is 255 everywhere
	scoreGreen        // only green varies: an index image
)

// foldCols adds to tc[t] the costs of tile t's columns, col[x] being the
// cost of the residuals in column x.
func foldCols(tc []uint64, col []uint32, tile int) {
	switch tile {
	case 4:
		full := len(col) / 4
		for t := 0; t < full; t++ {
			c := col[4*t : 4*t+4 : 4*t+4]
			tc[t] += uint64(c[0]) + uint64(c[1]) + uint64(c[2]) + uint64(c[3])
		}
		if full < len(tc) {
			var v uint64
			for _, c := range col[4*full:] {
				v += uint64(c)
			}
			tc[full] += v
		}
	default:
		for t := range tc {
			var v uint64
			for _, c := range col[t*tile : min(len(col), (t+1)*tile)] {
				v += uint64(c)
			}
			tc[t] += v
		}
	}
}

// pixelCostsGeneric adds to out[k] the cost of residual k of res, scoring
// the channels of set.
func (m *model) pixelCostsGeneric(out, res []uint32, set int) {
	out = out[:len(res)]
	c0, c1, c2, c3 := &m.cost[0], &m.cost[1], &m.cost[2], &m.cost[3]
	switch set {
	case scoreGreen:
		for k, p := range res {
			out[k] += c1[p>>8&0xff]
		}
	case scoreRGB:
		for k, p := range res {
			out[k] += c0[p&0xff] + c1[p>>8&0xff] + c2[p>>16&0xff]
		}
	default:
		for k, p := range res {
			out[k] += c0[p&0xff] + c1[p>>8&0xff] + c2[p>>16&0xff] + c3[p>>24]
		}
	}
}

// predParams sets the search effort.
type predParams struct {
	bits   int     // tile size 1<<bits
	passes int     // 1: adaptive model only; more: re-choose under the previous pass's histogram
	bias   float32 // extra bits for a mode unlike the left and the top tile's
	prior  float32 // weight of the first pass's prior
	modes  []uint8 // the modes tried, nil for searchModes
	step   int     // score every step-th row of a tile; 0 or 1 for all
	set    int     // the channels scored: scoreAll, scoreRGB or scoreGreen
}

// searchModes are the modes DefaultCompression and BestCompression try.
// Modes 0 (black) and 10 are left out: over the corpus and a hold-out set
// leaving them out changes the output by less than 0.03%, and the search
// is a seventh faster.
var searchModes = []uint8{1, 2, 3, 4, 5, 6, 7, 8, 9, 11, 12, 13}

// predChunk is the most residuals the search keeps at once: those of a run
// of tiles of one tile row under every mode tried, so that the model
// scores them and the histogram counts the chosen ones without computing
// them again.
var predChunk = 1 << 14

// maxChunkTiles is the most tiles of a chunk.
const maxChunkTiles = 1 << 12

// searchPredictors chooses a mode per tile of img (w x h) and returns them
// in rows of subSize(w, bits).
func (e *encoder) searchPredictors(img []uint32, w, h int, pp predParams) []uint8 {
	bits := pp.bits
	tw, th := subSize(w, bits), subSize(h, bits)
	tile := 1 << bits
	e.modes = growU8(e.modes, tw*th)
	modes := e.modes
	for i := range modes {
		modes[i] = 11
	}
	cands := pp.modes
	if cands == nil {
		cands = searchModes
	}
	if w == 1 || h == 1 {
		return modes
	}
	nc := len(cands)
	// index[m] is the position of mode m in cands, or -1.
	var index [nModes]int8
	for i := range index {
		index[i] = -1
	}
	for i, m := range cands {
		index[m] = int8(i)
	}
	step := max(1, pp.step)
	// rows is the most rows of a tile row that are scored; a chunk of
	// ct tiles keeps their residuals under every mode in e.resid.
	rows := (tile + step - 1) / step
	var chosen [maxChunkTiles]uint8
	ct := min(len(chosen), max(1, predChunk/(nc*rows*tile)))
	cw := ct * tile
	e.resid = growU32(e.resid, nc*rows*cw)
	e.tcost = growU64(e.tcost, nc*ct)
	e.quads = growU32(e.quads, cw)
	bias := uint64(fixCost(pp.bias))
	var hist searchHist
	var md model
	for pass := 0; pass < pp.passes; pass++ {
		adaptive := pass == 0
		if adaptive {
			hist = searchHist{}
			// The first row and column, whose predictions are fixed.
			h0 := &hist[0]
			h0.add(subPixels(img[0], 0xff000000))
			for x := 1; x < w; x++ {
				h0.add(subPixels(img[x], img[x-1]))
			}
			for y := 1; y < h; y++ {
				h0.add(subPixels(img[y*w], img[(y-1)*w]))
			}
			md.set(&hist, pp.prior, pp.set)
		} else {
			e.residualHist(&hist, img, w, h, bits, modes, pp.set)
			md.set(&hist, 0, pp.set)
		}
		for ty := 0; ty < th; ty++ {
			y0, y1 := max(ty*tile, 1), min(h, ty*tile+tile)
			nr := (y1 - y0 + step - 1) / step
			mrow := modes[ty*tw : ty*tw+tw]
			for c0 := 0; c0 < tw; c0 += ct {
				c1 := min(tw, c0+ct)
				nt := c1 - c0
				x0, x1 := c0*tile, min(w, c1*tile)
				n := x1 - x0
				// The costs of the mi-th mode are tcost[mi*nt : mi*nt+nt].
				tcost := e.tcost[:nc*nt]
				clear(tcost)
				// The costs of a column add up over the rows of the
				// tile row: up to 512 residuals stay within 32 bits.
				q := e.quads[:n]
				for mi, m := range cands {
					tc := tcost[mi*nt : mi*nt+nt]
					clear(q)
					for r := 0; r < nr; r++ {
						y := y0 + r*step
						res := e.resid[(mi*rows+r)*cw:][:n]
						if x0 == 0 {
							// Column 0 is predicted by the pixel
							// above whatever the mode: a 0 costs
							// every mode the same.
							res[0] = 0
							residuals(int(m), res[1:], img, y*w+1, w)
						} else {
							residuals(int(m), res, img, y*w+x0, w)
						}
						md.pixelCosts(q, res, pp.set)
					}
					foldCols(tc, q, tile)
				}
				for tx := c0; tx < c1; tx++ {
					t := tx - c0
					left, top := -1, -1
					if tx > 0 {
						left = int(mrow[tx-1])
					}
					if ty > 0 {
						top = int(modes[(ty-1)*tw+tx])
					}
					// Every mode pays the bias but the left and
					// the top tile's: the cheapest mode with the
					// bias against those two without.
					best, bestCost := 0, uint64(math.MaxUint64)
					for mi := range cands {
						if c := tcost[mi*nt+t]; c < bestCost {
							best, bestCost = mi, c
						}
					}
					if m := int(cands[best]); m != left && m != top {
						bestCost += bias
						for _, nb := range [2]int{left, top} {
							if nb < 0 || index[nb] < 0 {
								continue
							}
							mi := int(index[nb])
							if c := tcost[mi*nt+t]; c < bestCost || c == bestCost && mi < best {
								best, bestCost = mi, c
							}
						}
					}
					mrow[tx] = cands[best]
					chosen[t] = uint8(best)
				}
				if !adaptive {
					continue
				}
				for t := 0; t < nt; t++ {
					lo, hi := t*tile, min(n, t*tile+tile)
					if x0 == 0 && t == 0 {
						lo = 1
					}
					mi := int(chosen[t])
					for r := 0; r < nr; r++ {
						hist.addScored(e.resid[(mi*rows+r)*cw:][lo:hi], pp.set)
					}
				}
			}
			if adaptive {
				md.set(&hist, pp.prior, pp.set)
			}
		}
	}
	return modes
}

// residualHist counts the residuals of img under modes, in the channels
// of set and in the first row and column.
func (e *encoder) residualHist(hist *searchHist, img []uint32, w, h, bits int, modes []uint8, set int) {
	*hist = searchHist{}
	h0 := &hist[0]
	e.row = growU32(e.row, w)
	row := e.row[:w]
	tw := subSize(w, bits)
	tile := 1 << bits
	h0.add(subPixels(img[0], 0xff000000))
	for x := 1; x < w; x++ {
		h0.add(subPixels(img[x], img[x-1]))
	}
	for y := 1; y < h; y++ {
		h0.add(subPixels(img[y*w], img[(y-1)*w]))
		mrow := modes[(y>>bits)*tw : (y>>bits)*tw+tw]
		// One call per run of tiles with the same mode.
		for t := 0; t < tw; {
			m := mrow[t]
			u := t + 1
			for u < tw && mrow[u] == m {
				u++
			}
			x, end := max(1, t*tile), min(w, u*tile)
			if x < end {
				seg := row[:end-x]
				residuals(int(m), seg, img, y*w+x, w)
				hist.addScored(seg, set)
			}
			t = u
		}
	}
}

func growU8(b []uint8, n int) []uint8 {
	if cap(b) < n {
		return make([]uint8, n)
	}
	return b[:n]
}

func growU64(b []uint64, n int) []uint64 {
	if cap(b) < n {
		return make([]uint64, n)
	}
	return b[:n]
}
