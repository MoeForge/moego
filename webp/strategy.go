package webp

import (
	"image"

	"github.com/MoeForge/moego/internal/vp8l"
)

// A plan is one way to transform the image before entropy coding.
type plan struct {
	palette bool // color indexing
	order   int  // palette order
	// predict applies the predictor transform: to the pixels, or with
	// palette to the index image.
	predict  bool
	subGreen bool
	ct       bool // color transform of the residuals
	predBits int
	ctBits   int
	passes   int
	modes    []uint8 // predictor modes tried, nil for all
}

// vp8lLevel maps a CompressionLevel to the entropy coder's level.
func vp8lLevel(l CompressionLevel) int {
	switch l {
	case BestSpeed:
		return vp8l.BestSpeed
	case BestCompression:
		return vp8l.BestCompression
	}
	return vp8l.DefaultCompression
}

// encode reads m and returns the VP8L payload; it is valid until the next
// encode.
func (e *encoder) encode(m image.Image) ([]byte, error) {
	if err := e.load(m); err != nil {
		return nil, err
	}
	e.analyze()
	if e.an.ncolors <= 256 {
		e.indexImage()
	}
	e.ncache, e.subGreenOn = 0, false
	level := vp8lLevel(e.level)
	if e.tune.on {
		if e.tune.vp8level != 0 {
			level = e.tune.vp8level
		}
		return e.run(e.tune.plan, level), nil
	}
	var cands [maxCands]plan
	plans := e.plans(cands[:0])
	if len(plans) == 1 {
		return e.run(plans[0], level), nil
	}
	if best := e.rankOnProbe(plans); best >= 0 {
		if e.level != BestCompression || bestTrials == 1 {
			return e.run(plans[best], level), nil
		}
	}
	// Rank the candidates by estimate; encode the best, or at
	// BestCompression the best few, and keep the smallest.
	// The last candidate, the one that wins most often, stays transformed
	// after its estimate: if it wins, its encode does not transform the
	// image again.
	var est [maxCands]float64
	last := len(plans) - 1
	for i := range plans {
		est[i] = e.estimate(plans[i], i == last)
	}
	kept, keptWins := plans[last], true
	for i := range plans[:last] {
		keptWins = keptWins && est[last] < est[i]
	}
	for i := 1; i < len(plans); i++ {
		for j := i; j > 0 && est[j] < est[j-1]; j-- {
			est[j], est[j-1] = est[j-1], est[j]
			plans[j], plans[j-1] = plans[j-1], plans[j]
		}
	}
	trials := 1
	if e.level == BestCompression {
		trials = bestTrials
	}
	if trials == 1 && keptWins {
		return e.runApplied(kept, level), nil
	}
	e.undo(kept)
	if trials == 1 {
		return e.run(plans[0], level), nil
	}
	best := -1
	for i, p := range plans[:min(trials, len(plans))] {
		// Candidates estimated far worse than the best are not tried.
		if i > 0 && est[i] > est[0]*trialSlack {
			break
		}
		out := e.run(p, level)
		if best < 0 || len(out) < best {
			best = len(out)
			e.out = append(e.out[:0], out...)
		}
		e.undo(p)
	}
	return e.out, nil
}

// Probing: an image of at least probeMin pixels whose candidates include
// two or more predictor plans ranks them on a probe image, bands of
// probeBand rows taken every probeStep-th band, instead of on the whole
// image, and searches the whole image for the best candidate only.
var (
	probeMin  = 1 << 16
	probeBand = 32
	probeStep = 6
)

// rankOnProbe returns the index of the candidate estimated smallest on
// the probe image, or -1 when the whole image ranks the candidates. The
// probe's bands are swapped to the top of e.argb for the estimates and
// back after them, so the probe needs no buffer of its own.
func (e *encoder) rankOnProbe(plans []plan) int {
	npred := 0
	for _, p := range plans {
		if p.predict && !p.palette {
			npred++
		}
	}
	bands := e.h / probeBand
	if npred < 2 || probeStep <= 1 || e.w*e.h < probeMin || bands < 2*probeStep {
		return -1
	}
	n := bands / probeStep
	rows := probeBand * e.w
	// Band k*probeStep + probeStep/2 goes to slot k. Every band moved is
	// below its slot, so the slots filled stay put, and undoing the swaps
	// in reverse order restores the image.
	band := func(k int) int { return k*probeStep + probeStep/2 }
	for k := 0; k < n; k++ {
		swapPixels(e.argb[k*rows:(k+1)*rows], e.argb[band(k)*rows:(band(k)+1)*rows])
	}
	full, h := e.argb, e.h
	e.argb, e.h = full[:n*rows], n*probeBand
	best, bestEst := 0, 0.0
	for i := range plans {
		if b := e.estimate(plans[i], false); i == 0 || b < bestEst {
			best, bestEst = i, b
		}
	}
	e.setSubGreen(false)
	e.argb, e.h = full, h
	e.ncache = 0
	for k := n - 1; k >= 0; k-- {
		swapPixels(e.argb[k*rows:(k+1)*rows], e.argb[band(k)*rows:(band(k)+1)*rows])
	}
	return best
}

// swapPixels exchanges the contents of a and b, of equal length.
func swapPixels(a, b []uint32) {
	b = b[:len(a)]
	for i, v := range a {
		a[i], b[i] = b[i], v
	}
}

// predKey identifies a predictor search.
type predKey struct {
	palette, subGreen bool
	order, bits       int
	passes, modes     int
}

// maxCands is the most candidate plans of an image.
const maxCands = 12

// BestCompression encodes up to bestTrials candidates, those estimated
// within trialSlack of the best. A second trial saves 0.12% over the
// corpus for 30% more time.
var (
	bestTrials         = 1
	trialSlack float64 = 1.01
)

// plans returns the candidate plans for the level.
func (e *encoder) plans(dst []plan) []plan {
	few := e.an.ncolors <= 256
	if e.level == BestSpeed {
		if few {
			return append(dst, plan{palette: true, order: orderValue})
		}
		return append(dst, plan{subGreen: true, predict: true, predBits: 5, passes: 1, modes: speedModes})
	}
	if few {
		dst = append(dst, plan{palette: true, order: e.cheapOrder()})
		if e.an.ncolors > 16 {
			dst = append(dst, plan{palette: true, predict: true, order: orderLuma, predBits: 3, passes: 1})
		}
		// With at most 256 colors, prediction on the pixels does not
		// pay: over the corpus and a hold-out set it never beat the
		// palette plans, so only subtract green alone is tried besides.
		return append(dst, plan{subGreen: true})
	}
	if e.level == BestCompression {
		// The best of these four per image is within 0.05% of the best
		// of nine over the corpus.
		return append(dst,
			plan{subGreen: true},
			plan{predict: true, ct: true, predBits: 3, ctBits: 4, passes: 2},
			plan{subGreen: true, predict: true, predBits: 2, passes: 2},
			plan{subGreen: true, predict: true, ct: true, predBits: 2, ctBits: 4, passes: 2},
		)
	}
	// Over the corpus, the best of these three per image is within 0.2%
	// of the best of nine; with two on large images, within 0.5%.
	dst = append(dst, plan{subGreen: true})
	if e.w*e.h <= defBig {
		dst = append(dst, plan{predict: true, ct: true, predBits: 3, ctBits: 4, passes: 1})
	}
	return append(dst, plan{subGreen: true, predict: true, ct: true, predBits: 2, ctBits: 4, passes: 1})
}

// defBig is the size in pixels above which DefaultCompression leaves out
// its plan without subtract green: over the corpus that costs 0.3% and
// saves 13% of the time, most of it on the few largest images.
var defBig = 1 << 20

// speedModes are BestSpeed's predictor modes; its search scores every
// speedStep-th row (every fourth costs 0.2% over every row).
var (
	speedModes = []uint8{1, 2, 11, 12}
	speedStep  = 4
)

// cheapOrder returns the palette order whose delta-coded table is
// estimated smallest, for plans without prediction on the indices, where
// the order changes nothing else.
func (e *encoder) cheapOrder() int {
	best, bestBits := orderValue, 0.0
	for _, k := range []int{orderValue, orderLuma, orderNearest} {
		e.pal.setOrder(&e.an.colors, e.paletteOrder(k, e.order[:]))
		if b := e.pal.deltaBits(); k == orderValue || b < bestBits {
			best, bestBits = k, b
		}
	}
	return best
}

// run writes the stream for plan p at vp8l level and returns the payload.
func (e *encoder) run(p plan, level int) []byte {
	return e.runImage(p, level, false)
}

// runApplied is run for a plan that e.argb already holds the transforms
// of: it writes the transform data without transforming the pixels again.
func (e *encoder) runApplied(p plan, level int) []byte {
	return e.runImage(p, level, true)
}

func (e *encoder) runImage(p plan, level int, applied bool) []byte {
	vp := &e.vp
	vp.Reset(level)
	vp.Header(e.w, e.h, !e.an.opaque)
	img, w, h := e.apply(p, vp, applied)
	vp.WriteBits(0, 1)
	if !e.tune.side {
		vp.MainImage(img, w, h)
	}
	return vp.Finish()
}

// estimate returns the estimated size in bits of plan p's stream. With
// keep, the image stays transformed by p; undo(p) restores it.
func (e *encoder) estimate(p plan, keep bool) float64 {
	e.estBits = 0
	img, w, h := e.apply(p, nil, false)
	b := e.estBits + e.est.bits(img, w, h)
	if !keep {
		e.undo(p)
	}
	return b
}

// apply transforms the image by plan p and returns the main image. With vp
// set it writes the transforms to vp; without, it adds the estimated size
// of the transform data to e.estBits.
//
// The transforms work in place on e.argb (or e.work, the index image of
// the palette plans): subtract green, the predictor and the color
// transform all invert exactly, so undo restores the pixels after an
// estimate. The predictor modes and color transform elements of each plan
// are kept, so a plan's final encode repeats only the cheap passes, not
// the searches.
//
// With applied, e.argb already holds plan p's transforms (see estimate):
// only the transform data is written.
func (e *encoder) apply(p plan, vp *vp8l.Encoder, applied bool) (img []uint32, w, h int) {
	w, h = e.w, e.h
	// The index image of the palette plans is built again every time.
	applied = applied && !p.palette
	sub := func(kind uint32, bits int, px []uint32, sw, sh int) {
		if vp == nil {
			e.estBits += 3 + e.est.bits(px, sw, sh)
			return
		}
		vp.WriteBits(1, 1)
		vp.WriteBits(kind, 2)
		switch kind {
		case tColorIndexing:
			vp.WriteBits(uint32(sw-1), 8)
		default:
			vp.WriteBits(uint32(bits-2), 3)
		}
		vp.SubImage(px, sw, sh)
	}
	if p.palette {
		pal := &e.pal
		pal.setOrder(&e.an.colors, e.paletteOrder(p.order, e.order[:]))
		e.sub = growU32(e.sub, 256)
		sub(tColorIndexing, 0, pal.deltas(e.sub), pal.n, 1)
		wb := bundleBits(pal.n)
		e.work = e.packIndices(e.work, pal, wb)
		w = subSize(w, wb)
		img = e.work
	} else {
		if p.subGreen && vp != nil {
			vp.WriteBits(1, 1)
			vp.WriteBits(tSubtractGreen, 2)
		}
		e.setSubGreen(p.subGreen)
		img = e.argb
	}
	if !p.predict || w == 1 || h == 1 {
		return img, w, h
	}
	tw, th := subSize(w, p.predBits), subSize(h, p.predBits)
	pk := predKey{palette: p.palette, order: p.order, subGreen: p.subGreen, bits: p.predBits, passes: p.passes, modes: len(p.modes)}
	pc := e.cached(pk, 0)
	if !pc.ok {
		pp := predParams{bits: p.predBits, passes: p.passes, bias: predBias, prior: predPrior, modes: p.modes, set: e.scoreSet()}
		if p.palette {
			pp.set = scoreGreen
		}
		if e.level == BestSpeed {
			pp.step = speedStep
		} else {
			pp.step = predStep
		}
		pc.modes = append(pc.modes[:0], e.searchPredictors(img, w, h, pp)...)
		pc.ok = true
	}
	e.sub = growU32(e.sub, tw*th)
	for i, m := range pc.modes {
		e.sub[i] = 0xff000000 | uint32(m)<<8
	}
	sub(tPredictor, p.predBits, e.sub[:tw*th], tw, th)
	if !applied {
		e.row = growU32(e.row, w)
		applyPredictor(img, w, h, p.predBits, pc.modes, e.row)
	}
	if p.ct {
		tw, th := subSize(w, p.ctBits), subSize(h, p.ctBits)
		cc := e.cached(pk, p.ctBits)
		if !cc.ok {
			elems := e.searchColorTransform(img, w, h, ctParams{bits: p.ctBits, wide: e.level == BestCompression, bias: ctBias})
			cc.elems = append(cc.elems[:0], elems...)
			cc.ok = true
		}
		e.sub = growU32(e.sub, tw*th)
		for i, c := range cc.elems {
			e.sub[i] = c.pixel()
		}
		sub(tColor, p.ctBits, e.sub[:tw*th], tw, th)
		if !applied {
			applyColorTransform(img, w, h, p.ctBits, cc.elems)
		}
	}
	return img, w, h
}

// undo restores e.argb after apply(p): the predictor and the color
// transform are inverted in place. Subtract green stays; setSubGreen
// switches it when a plan needs the other state. The index image of the
// palette plans is rebuilt by every apply.
func (e *encoder) undo(p plan) {
	w, h := e.w, e.h
	if p.palette || !p.predict || w == 1 || h == 1 {
		return
	}
	pk := predKey{palette: p.palette, order: p.order, subGreen: p.subGreen, bits: p.predBits, passes: p.passes, modes: len(p.modes)}
	if p.ct {
		undoColorTransform(e.argb, w, h, p.ctBits, e.cached(pk, p.ctBits).elems)
	}
	undoPredictor(e.argb, w, h, p.predBits, e.cached(pk, 0).modes)
}

// scoreSet returns the channels the predictor search scores on the
// pixels.
func (e *encoder) scoreSet() int {
	if e.an.opaque {
		return scoreRGB
	}
	return scoreAll
}

// setSubGreen applies subtract green to e.argb, or undoes it.
func (e *encoder) setSubGreen(on bool) {
	if e.subGreenOn == on {
		return
	}
	if on {
		subtractGreenRow(e.argb, e.argb)
	} else {
		addGreenRow(e.argb, e.argb)
	}
	e.subGreenOn = on
}

// addGreenRowGeneric undoes subtract green on src into dst.
func addGreenRowGeneric(dst, src []uint32) {
	src = src[:len(dst)]
	for i, p := range src {
		dst[i] = addGreenPixel(p)
	}
}

// addGreenPixel undoes subtractGreen.
func addGreenPixel(p uint32) uint32 {
	g := p >> 8 & 0xff
	return p&0xff00ff00 | ((p&0x00ff00ff)+(g|g<<16))&0x00ff00ff
}

// searchCache is the result of one predictor search (ctBits 0) or one
// color transform search on its residuals.
type searchCache struct {
	key    predKey
	ctBits int
	ok     bool
	modes  []uint8
	elems  []ctElem
}

// cached returns the cache entry for a search, claiming a free one if
// there is none yet.
func (e *encoder) cached(k predKey, ctBits int) *searchCache {
	for i := range e.cache[:e.ncache] {
		if c := &e.cache[i]; c.key == k && c.ctBits == ctBits {
			return c
		}
	}
	c := &e.cache[e.ncache]
	e.ncache++
	c.key, c.ctBits, c.ok = k, ctBits, false
	return c
}

// Search knobs.
var (
	predStep          = 1
	predBias  float32 = 6
	predPrior float32 = 1
	ctBias    float32 = 4
)
