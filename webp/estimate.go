package webp

import "math/bits"

// estimator predicts the coded size of an image for choosing between
// transforms without encoding each choice: a greedy LZ77 pass with one hash
// candidate and the two cheap distances (the left pixel and the one above),
// whose literals, lengths and distances are then priced by their entropy.
// It sees the repeats that a residual entropy alone misses, so it can tell
// when prediction breaks up matches in flat or repetitive images.
type estimator struct {
	head [1 << estHashBits]int32
	lit  hist4
	lens [24]uint32
	dist [48]uint32
	// extra counts the extra bits of the length and distance codes.
	extra float64
	cache [1 << estCacheBits]uint32
	hits  [1 << estCacheBits]uint32
}

const (
	estHashBits  = 15
	estCacheBits = 8
	estMinMatch  = 3
	estMaxMatch  = 4096
)

// prefixBits returns the prefix code of v >= 1 (a length or a distance
// code) and the number of its extra bits, as RFC 9649 section 3.6.2.2
// defines them.
func prefixBits(v int) (code int, extra int) {
	if v <= 4 {
		return v - 1, 0
	}
	v--
	hb := bits.Len32(uint32(v)) - 1
	second := v >> (hb - 1) & 1
	return 2*hb + second, hb - 1
}

// bits returns the estimated size of img (w x h) in bits. Images of more
// than estSample pixels are estimated on bands of rows spread over the
// image, about estSample pixels in all, and scaled up.
func (es *estimator) bits(img []uint32, w, h int) float64 {
	for i := range es.head {
		es.head[i] = -1
	}
	es.lit = hist4{}
	clear(es.lens[:])
	clear(es.dist[:])
	clear(es.hits[:])
	clear(es.cache[:])
	es.extra = 0
	if w*h <= estSample {
		es.scan(img, w, 0, h)
		return es.total()
	}
	// Bands of estBand rows, every step-th band.
	bands := (h + estBand - 1) / estBand
	want := max(1, estSample/(w*estBand))
	step := max(1, bands/want)
	rows := 0
	for b := 0; b < bands; b += step {
		y0, y1 := b*estBand, min(h, b*estBand+estBand)
		es.scan(img, w, y0, y1)
		rows += y1 - y0
	}
	// Scale the data, not the code headers, which do not grow with it.
	return es.total() * float64(h) / float64(rows)
}

// Sampling of large images.
var (
	estSample = 1 << 19
	estBand   = 16
)

// scan adds rows y0 to y1 of img to the statistics. Matches stay inside
// the band but may reach back into the rows above it.
func (es *estimator) scan(img []uint32, w, y0, y1 int) {
	n := y1 * w
	img = img[:n]
	// match returns the length of the match of a with b < a, which is at
	// least have+1 only if img[a+have] == img[b+have]: that pixel is
	// checked first.
	match := func(a, b, have int) int {
		if have >= estMaxMatch || a+have >= n || img[a+have] != img[b+have] {
			return 0
		}
		l := 0
		end := min(n-a, estMaxMatch)
		for l < end && img[a+l] == img[b+l] {
			l++
		}
		return l
	}
	hash := func(i int) uint32 {
		return (img[i]*0x1e35a7bd ^ img[i+1]*0x9e3779b1) >> (32 - estHashBits)
	}
	for i := y0 * w; i < n; {
		bestLen, bestDist := 0, 0
		if i > 0 {
			if l := match(i, i-1, 0); l > 0 {
				bestLen, bestDist = l, 1
			}
		}
		if i >= w && w > 1 {
			if l := match(i, i-w, bestLen); l > bestLen {
				bestLen, bestDist = l, w
			}
		}
		if i+1 < n {
			hh := hash(i)
			if c := int(es.head[hh]); c >= 0 && i-c != 1 && i-c != w {
				if l := match(i, c, bestLen+1); l > bestLen+1 {
					bestLen, bestDist = l, i-c
				}
			}
			es.head[hh] = int32(i)
		}
		if bestLen >= estMinMatch {
			lc, le := prefixBits(bestLen)
			es.lens[lc]++
			// The two neighbourhood distances have the shortest codes;
			// others pay their offset past the 120 neighbourhood codes.
			var dc, de int
			switch bestDist {
			case 1:
				dc = 1
			case w:
				dc = 0
			default:
				dc, de = prefixBits(bestDist + 120)
			}
			es.dist[dc]++
			es.extra += float64(le + de)
			for k := i + 1; k < i+bestLen && k+1 < n; k++ {
				es.head[hash(k)] = int32(k)
			}
			for _, p := range img[i : i+bestLen] {
				es.cache[p*0x1e35a7bd>>(32-estCacheBits)] = p
			}
			i += bestLen
			continue
		}
		p := img[i]
		ci := p * 0x1e35a7bd >> (32 - estCacheBits)
		if es.cache[ci] == p {
			es.hits[ci]++
		} else {
			es.cache[ci] = p
			es.lit.add(p)
		}
		i++
	}
}

// total returns the estimate for the statistics collected.
func (es *estimator) total() float64 {
	// The green alphabet holds literals, lengths and cache hits.
	var g [256 + 24 + 1<<estCacheBits]uint32
	copy(g[:], es.lit[1][:])
	copy(g[256:], es.lens[:])
	copy(g[280:], es.hits[:])
	b := histBits(g[:]) + histBits(es.lit[0][:]) + histBits(es.lit[2][:]) + histBits(es.lit[3][:]) + histBits(es.dist[:])
	b += es.extra
	// The codes: a guess of a few bits per used symbol.
	for _, h := range [][]uint32{g[:], es.lit[0][:], es.lit[2][:], es.lit[3][:], es.dist[:]} {
		used := 0
		for _, c := range h {
			if c != 0 {
				used++
			}
		}
		if used > 1 {
			b += 3 * float64(used)
		}
	}
	return b
}
