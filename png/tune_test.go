package png

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// MOEGO_PNG_BESTSAMPLE and MOEGO_PNG_BESTCANDS (strategy numbers, comma
// separated) override BestCompression's trials, for timing alternatives.
func init() {
	if v, err := strconv.Atoi(os.Getenv("MOEGO_PNG_BESTSAMPLE")); err == nil {
		bestSample = v
	}
	if v := os.Getenv("MOEGO_PNG_BESTCANDS"); v != "" {
		bestCands = nil
		for _, f := range strings.Split(v, ",") {
			n, _ := strconv.Atoi(f)
			bestCands = append(bestCands, uint8(n))
		}
	}
}

// requireTune skips the tuning measurements unless MOEGO_PNG_TUNE=1 and
// the corpus variables of benchImages are set.
func requireTune(t *testing.T) {
	if os.Getenv("MOEGO_PNG_TUNE") != "1" || os.Getenv("MOEGO_PNG_CORPUS") == "" {
		t.Skip("set MOEGO_PNG_TUNE=1, MOEGO_PNG_CORPUS and MOEGO_PNG_LIST")
	}
}

// TestStrategySizes prints the corpus size of every filter strategy at
// the deflate levels in $MOEGO_PNG_ZLEVELS (default "1,6,9"). It runs only
// with $MOEGO_PNG_CORPUS and $MOEGO_PNG_LIST set; $MOEGO_PNG_KEEP=1 keeps
// image/png's color types.
func TestStrategySizes(t *testing.T) {
	requireTune(t)
	b := &testing.B{}
	ms := benchImages(b)
	zl := os.Getenv("MOEGO_PNG_ZLEVELS")
	if zl == "" {
		zl = "1,6,9"
	}
	keep := os.Getenv("MOEGO_PNG_KEEP") == "1"
	names := []string{"none", "sub", "up", "avg", "paeth", "minsum", "minsumUP", "entropy", "model"}
	strats := os.Getenv("MOEGO_PNG_STRATS")
	for _, z := range strings.Split(zl, ",") {
		zlevel, _ := strconv.Atoi(z)
		for st := uint8(0); st < nStrats; st++ {
			if strats != "" && !strings.Contains(","+strats+",", ","+names[st]+",") {
				continue
			}
			var mu sync.Mutex
			total, work := 0, make(chan *image.NRGBA)
			var wg sync.WaitGroup
			start := time.Now()
			for w := 0; w < runtime.GOMAXPROCS(0); w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					e := &encoder{}
					var buf bytes.Buffer
					for m := range work {
						buf.Reset()
						e.tune = tuning{strat: st, zlevel: zlevel}
						if err := e.encode(&buf, m, &Encoder{KeepColorType: keep}); err != nil {
							t.Error(err)
						}
						mu.Lock()
						total += buf.Len()
						mu.Unlock()
					}
				}()
			}
			for _, m := range ms {
				work <- m
			}
			close(work)
			wg.Wait()
			fmt.Printf("zlevel %s strategy %-9s %10d bytes  %v\n", z, names[st], total, time.Since(start).Round(time.Millisecond))
		}
	}
}

// TestStrategyOracle compares, per image, every strategy at the final
// deflate level ($MOEGO_PNG_ZLEVEL, default 9) with the choice of trials
// at deflate level 1 that sample one row band in every s, for s in
// $MOEGO_PNG_SAMPLES (default "1"), over all strategies and over subsets
// of them. It runs only with the corpus variables set.
func TestStrategyOracle(t *testing.T) {
	requireTune(t)
	ms := benchImages(&testing.B{})
	final := 9
	if v := os.Getenv("MOEGO_PNG_ZLEVEL"); v != "" {
		final, _ = strconv.Atoi(v)
	}
	samples := []int{1}
	if v := os.Getenv("MOEGO_PNG_SAMPLES"); v != "" {
		samples = nil
		for _, f := range strings.Split(v, ",") {
			n, _ := strconv.Atoi(f)
			samples = append(samples, n)
		}
	}
	keep := os.Getenv("MOEGO_PNG_KEEP") == "1"
	strats := []uint8{stMinSum, stEntropy, stModel, stPaeth, stNone, stSub, stUp, stAverage}
	// Trials over every strategy; the subsets are picked from them below.
	defer func(c []uint8) { bestCands = c }(bestCands)
	bestCands = strats
	names := map[uint8]string{stMinSum: "minsum", stEntropy: "entropy", stModel: "model", stPaeth: "paeth", stNone: "none", stSub: "sub", stUp: "up", stAverage: "avg"}
	type res struct {
		final [nStrats]int
		trial [8][nStrats]int
		ns    [8]time.Duration
	}
	out := make([]res, len(ms))
	var wg sync.WaitGroup
	work := make(chan int)
	for w := 0; w < runtime.GOMAXPROCS(0); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := &encoder{}
			var buf bytes.Buffer
			for i := range work {
				for _, st := range strats {
					buf.Reset()
					e.tune = tuning{strat: st, zlevel: final}
					if err := e.encode(&buf, ms[i], &Encoder{KeepColorType: keep}); err != nil {
						t.Error(err)
					}
					out[i].final[st] = buf.Len()
				}
				for k, sm := range samples {
					t0 := time.Now()
					e.tune = tuning{strat: tuneOff, sample: sm, trialOnly: true}
					if err := e.encode(io.Discard, ms[i], &Encoder{CompressionLevel: BestCompression, KeepColorType: keep}); err != nil {
						t.Error(err)
					}
					out[i].ns[k] = time.Since(t0)
					out[i].trial[k] = e.trialSizes
				}
			}
		}()
	}
	for i := range ms {
		work <- i
	}
	close(work)
	wg.Wait()
	sums := map[uint8]int{}
	oracle := 0
	pick := func(cands []uint8, by func(uint8) int) uint8 {
		b := cands[0]
		for _, st := range cands {
			if by(st) < by(b) {
				b = st
			}
		}
		return b
	}
	wins := map[string]int{}
	trial := make([]int, len(samples))
	ns := make([]time.Duration, len(samples))
	for _, r := range out {
		for _, st := range strats {
			sums[st] += r.final[st]
		}
		ob := pick(strats, func(st uint8) int { return r.final[st] })
		wins[names[ob]]++
		oracle += r.final[ob]
		for k := range samples {
			tr := r.trial[k]
			trial[k] += r.final[pick(strats, func(st uint8) int { return tr[st] })]
			ns[k] += r.ns[k]
		}
	}
	for _, st := range strats {
		fmt.Printf("level %d %-8s %10d\n", final, names[st], sums[st])
	}
	fmt.Printf("oracle %d, wins %v\n", oracle, wins)
	for k, sm := range samples {
		fmt.Printf("trials sampling 1 band in %d: %d (%.4f of oracle), trial time %v\n", sm, trial[k], float64(trial[k])/float64(oracle), ns[k])
	}
	// Trials over fewer candidates, from the same trial sizes.
	for _, sub := range [][]uint8{
		{stMinSum, stEntropy, stModel, stNone, stSub, stUp, stAverage},
		{stEntropy, stModel, stNone, stSub, stUp, stAverage},
		{stEntropy, stModel, stNone, stSub, stUp},
		{stEntropy, stNone, stSub, stUp},
		{stEntropy, stModel, stNone},
	} {
		var ns []string
		for _, st := range sub {
			ns = append(ns, names[st])
		}
		for k, sm := range samples {
			n := 0
			for _, r := range out {
				tr := r.trial[k]
				n += r.final[pick(sub, func(st uint8) int { return tr[st] })]
			}
			fmt.Printf("trials over %s, 1 band in %d: %d (%.4f of oracle)\n", strings.Join(ns, ","), sm, n, float64(n)/float64(oracle))
		}
	}
}

// TestNoneRule checks, at deflate level $MOEGO_PNG_ZLEVEL (default 6),
// how filtering images of at most 256 colors with None compares with
// stMinSum and with the per-image best of the two.
func TestNoneRule(t *testing.T) {
	requireTune(t)
	ms := benchImages(&testing.B{})
	zl := 6
	if v := os.Getenv("MOEGO_PNG_ZLEVEL"); v != "" {
		zl, _ = strconv.Atoi(v)
	}
	var mu sync.Mutex
	minsum, rule, best := 0, 0, 0
	few, fewNoneWins, manyNoneWins := 0, 0, 0
	var wg sync.WaitGroup
	work := make(chan *image.NRGBA)
	for w := 0; w < runtime.GOMAXPROCS(0); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := &encoder{}
			var buf bytes.Buffer
			for m := range work {
				size := func(st uint8) int {
					buf.Reset()
					e.tune = tuning{strat: st, zlevel: zl}
					if err := e.encode(&buf, m, &Encoder{KeepColorType: true}); err != nil {
						t.Error(err)
					}
					return buf.Len()
				}
				a, b := size(stMinSum), size(stNone)
				e.src.classify(m)
				e.sbuf[0] = grow(e.sbuf[0], 8*e.src.w)
				e.analyze()
				isFew := e.set.n <= 256
				mu.Lock()
				minsum += a
				best += min(a, b)
				if isFew {
					rule += b
					few++
					if b < a {
						fewNoneWins++
					}
				} else {
					rule += a
					if b < a {
						manyNoneWins++
					}
				}
				mu.Unlock()
			}
		}()
	}
	for _, m := range ms {
		work <- m
	}
	close(work)
	wg.Wait()
	fmt.Printf("level %d: minsum %d, none if <=256 colors %d, best of two %d; %d images <=256 colors, None wins on %d of them and on %d others\n",
		zl, minsum, rule, best, few, fewNoneWins, manyNoneWins)
}

// TestLevelTable writes $MOEGO_PNG_TABLE, a CSV of every corpus image's
// size and encode time at deflate levels $MOEGO_PNG_ZLEVELS with the
// Default strategy and image/png's color types, with the mean minsum
// filter cost per byte, for choosing a deflate level per image.
func TestLevelTable(t *testing.T) {
	requireTune(t)
	out := os.Getenv("MOEGO_PNG_TABLE")
	if out == "" {
		t.Skip("set MOEGO_PNG_TABLE")
	}
	ms := benchImages(&testing.B{})
	var levels []int
	for _, f := range strings.Split(os.Getenv("MOEGO_PNG_ZLEVELS"), ",") {
		n, _ := strconv.Atoi(f)
		levels = append(levels, n)
	}
	rows := make([]string, len(ms))
	var wg sync.WaitGroup
	work := make(chan int)
	for w := 0; w < runtime.GOMAXPROCS(0); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := &encoder{}
			for i := range work {
				m := ms[i]
				row := fmt.Sprintf("%d,%d", i, m.Rect.Dx()*m.Rect.Dy())
				// Mean minsum cost per byte, from a stored encode.
				e.tune = tuning{strat: stMinSum, zlevel: 0}
				e.costSum, e.costBytes = 0, 0
				e.encode(io.Discard, m, &Encoder{KeepColorType: true})
				row += fmt.Sprintf(",%.3f", float64(e.costSum)/float64(max(1, e.costBytes)))
				for _, zl := range levels {
					var best time.Duration
					n := 0
					for r := 0; r < 3; r++ {
						var buf bytes.Buffer
						e.tune = tuning{strat: tuneOff, zlevel: zl, levelOnly: true}
						t0 := time.Now()
						e.encode(&buf, m, &Encoder{KeepColorType: true})
						if d := time.Since(t0); r == 0 || d < best {
							best = d
						}
						n = buf.Len()
					}
					row += fmt.Sprintf(",%d,%d", n, best.Nanoseconds())
				}
				rows[i] = row
			}
		}()
	}
	for i := range ms {
		work <- i
	}
	close(work)
	wg.Wait()
	head := "image,pixels,cost"
	for _, zl := range levels {
		head += fmt.Sprintf(",bytes%d,ns%d", zl, zl)
	}
	if err := os.WriteFile(out, []byte(head+"\n"+strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
