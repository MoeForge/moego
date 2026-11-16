package png

import (
	"math"

	"github.com/MoeForge/moego/internal/deflate"
)

// Filter strategies: how filterRows picks each row's filter. Strategies
// below stMinSum use that filter type on every row.
const (
	stNone    = ftNone
	stSub     = ftSub
	stUp      = ftUp
	stAverage = ftAverage
	stPaeth   = ftPaeth
	// stMinSum picks the filter with the least sum of the magnitudes of
	// its residuals as signed bytes, libpng's heuristic.
	stMinSum = 5
	// stMinSumUP is stMinSum over Up and Paeth only.
	stMinSumUP = 6
	// stEntropy picks the filter whose residual bytes have the least
	// Shannon entropy.
	stEntropy = 7
	// stModel picks the filter whose residual bytes cost the fewest bits
	// under the byte frequencies of the rows already written, a stand-in
	// for the Huffman codes those rows will share.
	stModel = 8
	nStrats = 9
)

// strategy returns the filter strategy for the level and layout. Images
// with few colors compress best unfiltered, like palette images: on the
// benchmark corpus with image/png's color types, None on the truecolor
// images of at most 256 colors saves 0.4% at DefaultCompression.
func (e *encoder) strategy() uint8 {
	if e.level == NoCompression || e.ct == ctPalette || e.depth < 8 || e.few {
		return stNone
	}
	return stMinSum
}

// bandRows is the height of the row bands that strategy trials sample.
const bandRows = 16

// The strategies BestCompression tries, and the sampling of its trials on
// images of more than 16 bands: one band in bestSample. Paeth alone wins
// on one corpus image in 97 and minsum's choice is close to entropy's; on
// sampled trials both mislead more often than they help. Against all
// eight strategies on one band in four, this costs 0.02% and saves 60%
// of the trial time.
var (
	bestCands  = []uint8{stEntropy, stModel, stNone, stSub, stUp, stAverage}
	bestSample = 8
)

// bestStrategy compresses the image with each candidate strategy at
// BestSpeed and returns the one with the smallest output. Over the
// benchmark corpus the result is within 0.3% of the best strategy per
// image at BestCompression and 1.8% smaller than stMinSum on every image.
func (e *encoder) bestStrategy() uint8 {
	sample := 1
	if e.src.h > 16*bandRows {
		sample = bestSample
		if e.tune.sample != 0 {
			sample = e.tune.sample
		}
	}
	best, bestN := bestCands[0], -1
	for _, st := range bestCands {
		n := e.trialSize(st, sample, deflate.BestSpeed)
		if e.err != nil {
			return best
		}
		e.trialSizes[st] = n
		if bestN < 0 || n < bestN {
			best, bestN = st, n
		}
	}
	return best
}

// countWriter counts the bytes written to it.
type countWriter int

func (c *countWriter) Write(p []byte) (int, error) {
	*c += countWriter(len(p))
	return len(p), nil
}

// pick returns the filter for row cur below prev.
func (e *encoder) pick(strat uint8, cur, prev []byte) uint8 {
	switch strat {
	case stMinSum:
		c := filterCosts(cur, prev, e.bpp)
		f := pickFilter(&c, 1<<ftNone|1<<ftSub|1<<ftUp|1<<ftAverage|1<<ftPaeth)
		e.costSum += c[f]
		e.costBytes += len(cur)
		return f
	case stMinSumUP:
		c := filterCosts(cur, prev, e.bpp)
		return pickFilter(&c, 1<<ftUp|1<<ftPaeth)
	case stEntropy:
		return e.pickEntropy(cur, prev)
	case stModel:
		return e.pickModel(cur, prev)
	}
	return strat
}

// pickModel returns the filter whose residuals cost the fewest bits under
// the model, then adds the chosen residuals to the model.
func (e *encoder) pickModel(cur, prev []byte) uint8 {
	n := len(cur)
	m := &e.model
	if m.rows == 0 {
		clear(m.count[:])
		m.total = 0
	}
	if m.rows == 0 || m.dirty {
		// cost[b] = -log2((count[b]+1)/(total+256)) in 1/16 bits.
		lt := math.Log2(float64(m.total + 256))
		for b, c := range m.count {
			m.cost[b] = uint32(16 * (lt - math.Log2(float64(c+1))))
		}
		m.dirty = false
	}
	for f := range e.fbuf {
		e.fbuf[f] = grow(e.fbuf[f], n)
		filterRow(e.fbuf[f], cur, prev, e.bpp, uint8(f+1))
	}
	best, bf := ^uint64(0), uint8(0)
	if m.total < modelSeed {
		// Too little data: a model this young picks None and then learns
		// raw pixels from it. Start from libpng's heuristic.
		c := filterCosts(cur, prev, e.bpp)
		bf = pickFilter(&c, 0x1f)
	} else {
		for f := uint8(0); f < 5; f++ {
			row := cur
			if f > 0 {
				row = e.fbuf[f-1]
			}
			var s uint64
			for _, b := range row {
				s += uint64(m.cost[b])
			}
			if s < best {
				best, bf = s, f
			}
		}
	}
	row := cur
	if bf > 0 {
		row = e.fbuf[bf-1]
	}
	for _, b := range row {
		m.count[b]++
	}
	m.total += uint32(n)
	m.rows++
	m.dirty = true
	if m.total > modelSpan {
		// Halve the counts: the model follows the recent rows, as
		// the compressor's blocks do.
		m.total = 0
		for b, c := range m.count {
			m.count[b] = c / 2
			m.total += c / 2
		}
	}
	return bf
}

// modelSpan is how many residual bytes the model weighs fully; modelSeed
// how many it needs before it chooses.
const modelSpan, modelSeed = 1 << 17, 1 << 13

// byteModel holds the residual byte frequencies for stModel.
type byteModel struct {
	count [256]uint32
	cost  [256]uint32
	total uint32
	rows  int
	dirty bool
}

// pickEntropy returns the filter whose residuals have the least entropy.
func (e *encoder) pickEntropy(cur, prev []byte) uint8 {
	n := len(cur)
	for f := range e.fbuf {
		e.fbuf[f] = grow(e.fbuf[f], n)
		filterRow(e.fbuf[f], cur, prev, e.bpp, uint8(f+1))
	}
	best, bf := math.Inf(1), uint8(0)
	for f := uint8(0); f < 5; f++ {
		row := cur
		if f > 0 {
			row = e.fbuf[f-1]
		}
		h := &e.hist[f]
		clear(h[:])
		for _, b := range row {
			h[b]++
		}
		// n*H = n*log2(n) - sum c*log2(c); n is the same for every filter.
		s := 0.0
		for _, c := range h {
			if c > 1 {
				s -= float64(c) * math.Log2(float64(c))
			}
		}
		if s < best {
			best, bf = s, f
		}
	}
	return bf
}

// paletteOrTruecolor compares the palette chosen for an image of 17 to
// 256 colors with the truecolor type altCT by compressing sampled row
// bands of each at DefaultCompression, and keeps the smaller. (Trials at
// BestSpeed misjudge it: its greedy matching favors the index rows.)
// Images of at most 16 colors keep the palette, packed to 4 bits or less.
func (e *encoder) paletteOrTruecolor() {
	if e.depth < 8 {
		return
	}
	sample := 1
	if e.src.h > 16*bandRows {
		sample = 4
	}
	pct, pdepth := e.ct, e.depth
	pal := e.trialSize(stNone, sample, deflate.DefaultCompression)
	e.ct, e.depth, e.few = e.altCT, 8, false
	e.layout()
	tc := e.trialSize(stMinSum, sample, deflate.DefaultCompression)
	if tc < pal {
		return
	}
	e.ct, e.depth, e.few = pct, pdepth, true
	e.layout()
}

// trialSize returns the size of the image compressed at deflate level
// with strategy strat, sampling one row band in sample. Trials share the
// Writer of the final stream: it keeps its buffers across levels.
func (e *encoder) trialSize(strat uint8, sample, level int) int {
	if e.comp == nil {
		e.comp = deflate.NewWriter(&e.count, level)
	}
	e.count = 0
	e.comp.Reset(&e.count, level)
	e.filterRows(strat, e.comp, sample)
	e.comp.Close()
	return int(e.count)
}
