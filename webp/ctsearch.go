package webp

import "math"

// Color transform search. For each tile, the multipliers start at the
// least-squares fit of red on green and of blue on green and red (the
// residuals as signed bytes), and are refined by the bit cost of the
// transformed red and blue under a model of the image's statistics so far:
// the histogram of the tiles already transformed, plus a weak prior, so
// tiles agree on values the entropy coder has already seen. A candidate
// unlike both the left and the top tile's value costs a little more, which
// keeps the color transform image cheap.

type ctParams struct {
	bits int
	// wide refines over more candidates around the least-squares start.
	wide bool
	bias float32
}

// ctSteps are the offsets tried around the least-squares start.
var (
	ctStepsNarrow = []int{-1, 1}
	ctStepsWide   = []int{-6, -4, -3, -2, -1, 1, 2, 3, 4, 6}
)

// searchColorTransform chooses an element per tile of img (w x h, the
// predictor residuals) and returns them in rows of subSize(w, bits).
func (e *encoder) searchColorTransform(img []uint32, w, h int, cp ctParams) []ctElem {
	bits := cp.bits
	tw, th := subSize(w, bits), subSize(h, bits)
	tile := 1 << bits
	if cap(e.elems) < tw*th {
		e.elems = make([]ctElem, tw*th)
	}
	elems := e.elems[:tw*th]
	clear(elems)
	cs := &e.cts
	cs.reset()
	steps := ctStepsNarrow
	if cp.wide {
		steps = ctStepsWide
	}
	e.tileG = growI8(e.tileG, tile*tile)
	e.tileR = growI8(e.tileR, tile*tile)
	e.tileB = growI8(e.tileB, tile*tile)
	cs.cx = growI8(cs.cx, tile*tile)
	cs.cy = growI8(cs.cy, tile*tile)
	for ty := 0; ty < th; ty++ {
		y0, y1 := ty*tile, min(h, ty*tile+tile)
		for tx := 0; tx < tw; tx++ {
			x0, x1 := tx*tile, min(w, tx*tile+tile)
			n, nz := 0, 0
			tg, tr, tb := e.tileG, e.tileR, e.tileB
			cx, cy := cs.cx, cs.cy
			var sgg, sgr, srr, sgb, srb int64
			var sums [5]int32
			done := ctTile(img, y0*w+x0, w, x1-x0, y1-y0, tg, tr, tb, &sums)
			if done {
				n = (x1 - x0) * (y1 - y0)
				sgg, sgr, srr, sgb, srb = int64(sums[0]), int64(sums[1]), int64(sums[2]), int64(sums[3]), int64(sums[4])
				// The red of a pixel with green 0 costs the same
				// under every green_to_red: only the others are
				// compared.
				r8 := tr[:n]
				for k, g := range tg[:n] {
					cx[nz], cy[nz] = r8[k], g
					nz += nonzero(g)
				}
			}
			for y := y0; y < y1 && !done; y++ {
				// Products of two int8 fit 15 bits: a row of up to
				// 512 sums in an int32.
				var gg, gr, rr, gb, rb int32
				row := img[y*w+x0 : y*w+x1]
				g8, r8, b8 := tg[n:n+len(row)], tr[n:n+len(row)], tb[n:n+len(row)]
				for k, p := range row {
					g, r, b := int8(p>>8), int8(p>>16), int8(p)
					g8[k], r8[k], b8[k] = g, r, b
					// The red of a pixel with green 0 costs the
					// same under every green_to_red: only the
					// others are compared.
					cx[nz], cy[nz] = r, g
					nz += nonzero(g)
					gi, ri, bi := int32(g), int32(r), int32(b)
					gg += gi * gi
					gr += gi * ri
					rr += ri * ri
					gb += gi * bi
					rb += ri * bi
				}
				n += len(row)
				sgg += int64(gg)
				sgr += int64(gr)
				srr += int64(rr)
				sgb += int64(gb)
				srb += int64(rb)
			}
			cs.tg, cs.tr, cs.tb = tg[:n], tr[:n], tb[:n]
			cs.nz = nz
			var left, top ctElem
			cs.hasLeft, cs.hasTop = tx > 0, ty > 0
			if cs.hasLeft {
				left = elems[ty*tw+tx-1]
			}
			if cs.hasTop {
				top = elems[(ty-1)*tw+tx]
			}
			cs.bias = fixCost(cp.bias)

			// Least squares: r ~ k*g, b ~ kg*g + kr*r, with k in 1/32.
			lsR := fitMult(sgr, sgg)
			var lsG, lsRB int8
			if det := sgg*srr - sgr*sgr; det > 0 {
				lsG = clampMult(32 * float64(sgb*srr-srb*sgr) / float64(det))
				lsRB = clampMult(32 * float64(srb*sgg-sgb*sgr) / float64(det))
			} else {
				lsG = fitMult(sgb, sgg)
			}

			cs.which = 0
			g2r := cs.search(lsR, left.g2r, top.g2r, steps)
			cs.setFixed(1, lsRB)
			g2b := cs.search(lsG, left.g2b, top.g2b, steps)
			cs.setFixed(2, g2b)
			r2b := cs.search(lsRB, left.r2b, top.r2b, steps)
			if r2b != lsRB {
				cs.setFixed(1, r2b)
				g2b = cs.search(g2b, left.g2b, top.g2b, ctStepsNarrow)
			}
			c := ctElem{g2r: g2r, g2b: g2b, r2b: r2b}
			elems[ty*tw+tx] = c
			cs.add(c)
		}
		cs.update()
	}
	return elems
}

func fitMult(sxy, sxx int64) int8 {
	if sxx == 0 {
		return 0
	}
	return clampMult(32 * float64(sxy) / float64(sxx))
}

func clampMult(v float64) int8 {
	return int8(math.Round(max(-128, min(127, v))))
}

// ctSearch is the state of one tile's search.
type ctSearch struct {
	accR, accB      [2][256]uint32 // two copies, see searchHist
	costR, costB    [256]uint32    // fixed point, costOne per bit
	tg, tr, tb      []int8
	hasLeft, hasTop bool
	bias            uint32
	which           int // 0: green_to_red, 1: green_to_blue, 2: red_to_blue
	// The channel searched is cx[i] - m*cy[i]/32 for i < nz: the pixels
	// whose term changes with the multiplier m, those with cy[i] != 0.
	// For green_to_red, cx is the red and cy the green; for the blue
	// multipliers, cx is the blue minus the other multiplier's term.
	cx, cy          []int8
	nz              int
	tried           [256]uint32
	gen             uint32
	leftVal, topVal int8
	bestVal         int8
	bestCost        uint32
	haveBest        bool
}

func (cs *ctSearch) reset() {
	cs.accR = [2][256]uint32{}
	cs.accB = [2][256]uint32{}
	cs.update()
}

// priorLog[v] is log2(priorCount[v]).
var priorLog = func() (t [256]float64) {
	for v := range t {
		t[v] = math.Log2(float64(priorCount[v]))
	}
	return t
}()

// update rebuilds the costs from the counts so far.
func (cs *ctSearch) update() {
	set := func(cost *[256]uint32, acc *[2][256]uint32) {
		a0, a1 := &acc[0], &acc[1]
		var n float64
		for v := range a0 {
			n += float64(a0[v]+a1[v]) + float64(priorCount[v])
		}
		ln := math.Log2(n)
		for v := range a0 {
			l := priorLog[v]
			if k := a0[v] + a1[v]; k != 0 {
				l = math.Log2(float64(k) + float64(priorCount[v]))
			}
			cost[v] = fixCost(float32(ln - l))
		}
	}
	set(&cs.costR, &cs.accR)
	set(&cs.costB, &cs.accB)
}

// add counts the tile's red and blue under c.
func (cs *ctSearch) add(c ctElem) {
	tg := cs.tg
	tr, tb := cs.tr[:len(tg)], cs.tb[:len(tg)]
	g2r, g2b, r2b := int32(c.g2r), int32(c.g2b), int32(c.r2b)
	r0, r1 := &cs.accR[0], &cs.accR[1]
	b0, b1 := &cs.accB[0], &cs.accB[1]
	i := 0
	for ; i+1 < len(tg); i += 2 {
		g, r := int32(tg[i]), int32(tr[i])
		h, s := int32(tg[i+1]), int32(tr[i+1])
		r0[uint8(r-g2r*g>>5)]++
		r1[uint8(s-g2r*h>>5)]++
		b0[uint8(int32(tb[i])-g2b*g>>5-r2b*r>>5)]++
		b1[uint8(int32(tb[i+1])-g2b*h>>5-r2b*s>>5)]++
	}
	if i < len(tg) {
		g, r := int32(tg[i]), int32(tr[i])
		r0[uint8(r-g2r*g>>5)]++
		b0[uint8(int32(tb[i])-g2b*g>>5-r2b*r>>5)]++
	}
}

// cost returns the bits of the tile's channel under multiplier v, plus the
// bias when v is unlike the neighbors'.
func (cs *ctSearch) cost(v int8) uint32 {
	tab := &cs.costB
	if cs.which == 0 {
		tab = &cs.costR
	}
	s := ctCostSum(tab, cs.cx[:cs.nz], cs.cy[:cs.nz], int32(v))
	switch {
	case !cs.hasLeft && !cs.hasTop:
		if v != 0 {
			s += cs.bias
		}
	case cs.hasLeft && v == cs.leftVal, cs.hasTop && v == cs.topVal:
	default:
		s += cs.bias
	}
	return s
}

// ctCostGeneric returns the sum of tab[uint8(x[i] - m*y[i]>>5)].
func ctCostGeneric(tab *[256]uint32, x, y []int8, m int32) uint32 {
	x = x[:len(y)]
	var s0, s1 uint32
	i := 0
	for ; i+1 < len(y); i += 2 {
		s0 += tab[uint8(int32(x[i])-m*int32(y[i])>>5)]
		s1 += tab[uint8(int32(x[i+1])-m*int32(y[i+1])>>5)]
	}
	if i < len(y) {
		s0 += tab[uint8(int32(x[i])-m*int32(y[i])>>5)]
	}
	return s0 + s1
}

// setFixed sets the other blue multiplier for which = 1 (k is red_to_blue)
// or which = 2 (k is green_to_blue): cx becomes the blue minus its term,
// for the pixels whose green (which 1) or red (which 2) is not 0.
func (cs *ctSearch) setFixed(which int, k int8) {
	cs.which = which
	src, mul := cs.tr, cs.tg
	if which == 2 {
		src, mul = cs.tg, cs.tr
	}
	n := len(cs.tg)
	src, mul = src[:n], mul[:n]
	cx, cy := cs.cx[:n], cs.cy[:n]
	m := int32(k)
	nz := 0
	for i, b := range cs.tb[:n] {
		y := mul[i]
		cx[nz], cy[nz] = int8(int32(b)-m*int32(src[i])>>5), y
		nz += nonzero(y)
	}
	cs.nz = nz
}

// search returns the cheapest multiplier among the start, the steps
// around it, zero and the neighbors' values.
func (cs *ctSearch) search(start, left, top int8, steps []int) int8 {
	cs.leftVal, cs.topVal = left, top
	cs.gen++
	cs.haveBest = false
	try := func(v int) {
		if v < -128 || v > 127 || cs.tried[uint8(v)] == cs.gen {
			return
		}
		cs.tried[uint8(v)] = cs.gen
		if c := cs.cost(int8(v)); !cs.haveBest || c < cs.bestCost {
			cs.bestVal, cs.bestCost, cs.haveBest = int8(v), c, true
		}
	}
	try(int(start))
	for _, d := range steps {
		try(int(start) + d)
	}
	try(0)
	if cs.hasLeft {
		try(int(left))
	}
	if cs.hasTop {
		try(int(top))
	}
	// One more step from a better point at the edge of the steps.
	for moved := true; moved; {
		b := cs.bestVal
		try(int(b) - 1)
		try(int(b) + 1)
		moved = cs.bestVal != b
	}
	return cs.bestVal
}

// nonzero returns 1 if v is not 0, else 0, without a branch.
func nonzero(v int8) int {
	u := uint8(v)
	return int((u | -u) >> 7)
}

func growI8(b []int8, n int) []int8 {
	if cap(b) < n {
		return make([]int8, n)
	}
	return b[:n]
}
