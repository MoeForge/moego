package webp

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Corpus measurements for tuning, off unless MOEGO_WEBP_TUNE=1 with
// MOEGO_WEBP_CORPUS (directory) and MOEGO_WEBP_LIST (lines of
// "sha256  relpath") set. MOEGO_WEBP_ONLY keeps images whose path contains
// one of its comma-separated substrings.

var estSampleF, slackF, trialsF, speedStepF, defBigF, stepF, probeStepF, probeMinF, probeBandF, chunkF float32

func init() {
	defer func() {
		if estSampleF != 0 {
			estSample = int(estSampleF)
		}
		if slackF != 0 {
			trialSlack = float64(slackF)
		}
		if defBigF != 0 {
			defBig = int(defBigF)
		}
		if speedStepF != 0 {
			speedStep = int(speedStepF)
		}
		if trialsF != 0 {
			bestTrials = int(trialsF)
		}
		if stepF != 0 {
			predStep = int(stepF)
		}
		if probeStepF != 0 {
			probeStep = int(probeStepF)
		}
		if probeMinF != 0 {
			probeMin = int(probeMinF)
		}
		if probeBandF != 0 {
			probeBand = int(probeBandF)
		}
		if chunkF != 0 {
			predChunk = int(chunkF)
		}
	}()
	// MOEGO_WEBP_MODES limits the predictor modes of every search
	// ("1.2.11"); MOEGO_WEBP_CTSTEPS sets the narrow color transform
	// steps ("-1.1").
	ints := func(s string) []int {
		var out []int
		for _, f := range strings.Split(s, ".") {
			v, err := strconv.Atoi(f)
			if err != nil {
				panic(s + ": " + err.Error())
			}
			out = append(out, v)
		}
		return out
	}
	if s := os.Getenv("MOEGO_WEBP_MODES"); s != "" {
		searchModes = nil
		for _, v := range ints(s) {
			searchModes = append(searchModes, uint8(v))
		}
	}
	if s := os.Getenv("MOEGO_WEBP_CTSTEPS"); s != "" {
		ctStepsNarrow = ints(s)
	}
	for _, k := range []struct {
		env string
		v   *float32
	}{{"MOEGO_WEBP_PREDBIAS", &predBias}, {"MOEGO_WEBP_ESTSAMPLE", &estSampleF}, {"MOEGO_WEBP_SLACK", &slackF}, {"MOEGO_WEBP_TRIALS", &trialsF}, {"MOEGO_WEBP_PREDPRIOR", &predPrior}, {"MOEGO_WEBP_CTBIAS", &ctBias}, {"MOEGO_WEBP_SPEEDSTEP", &speedStepF}, {"MOEGO_WEBP_DEFBIG", &defBigF}, {"MOEGO_WEBP_STEP", &stepF}, {"MOEGO_WEBP_PROBESTEP", &probeStepF}, {"MOEGO_WEBP_PROBEMIN", &probeMinF}, {"MOEGO_WEBP_PROBEBAND", &probeBandF}, {"MOEGO_WEBP_CHUNK", &chunkF}} {
		if s := os.Getenv(k.env); s != "" {
			f, err := strconv.ParseFloat(s, 32)
			if err != nil {
				panic(k.env + ": " + err.Error())
			}
			*k.v = float32(f)
		}
	}
}

type corpusImage struct {
	rel, category string
	m             *image.NRGBA
	raw           int // raw pixel bytes: 3 per pixel, 4 with alpha
}

var corpusOnce = sync.OnceValues(func() ([]corpusImage, error) {
	dir, list := os.Getenv("MOEGO_WEBP_CORPUS"), os.Getenv("MOEGO_WEBP_LIST")
	f, err := os.Open(list)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var only []string
	if s := os.Getenv("MOEGO_WEBP_ONLY"); s != "" {
		only = strings.Split(s, ",")
	}
	var out []corpusImage
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		rel := fields[1]
		if only != nil {
			keep := false
			for _, o := range only {
				keep = keep || strings.Contains(rel, o)
			}
			if !keep {
				continue
			}
		}
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			return nil, err
		}
		src, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %v", rel, err)
		}
		m := image.NewNRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
		draw.Draw(m, m.Rect, src, src.Bounds().Min, draw.Src)
		per := 3
		for i := 3; i < len(m.Pix); i += 4 {
			if m.Pix[i] != 0xff {
				per = 4
				break
			}
		}
		out = append(out, corpusImage{rel, strings.SplitN(rel, "/", 2)[0], m, per * len(m.Pix) / 4})
	}
	return out, sc.Err()
})

func requireTune(t *testing.T) []corpusImage {
	t.Helper()
	if os.Getenv("MOEGO_WEBP_TUNE") != "1" || os.Getenv("MOEGO_WEBP_CORPUS") == "" {
		t.Skip("set MOEGO_WEBP_TUNE=1, MOEGO_WEBP_CORPUS and MOEGO_WEBP_LIST")
	}
	ms, err := corpusOnce()
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

// parsePlan reads a plan spec: "speed", "default" or "best" for a level's
// own choice, else '+'-separated parts: "pal" or "pal@ORDER" (color
// indexing), "sg" (subtract green), "pN" (predictor, tile bits N), "cN"
// (color transform, tile bits N), "nN" (search passes), "zN" (vp8l level),
// "mM1.M2..." (predictor modes).
func parsePlan(s string) (level CompressionLevel, tu tuning, err error) {
	switch s {
	case "speed":
		return BestSpeed, tuning{}, nil
	case "default":
		return DefaultCompression, tuning{}, nil
	case "best":
		return BestCompression, tuning{}, nil
	}
	tu.on = true
	tu.plan.passes = 1
	for _, part := range strings.Split(s, "+") {
		if part == "none" {
			continue
		}
		num := func(p string) int {
			v, e := strconv.Atoi(strings.TrimPrefix(part, p))
			if e != nil {
				err = fmt.Errorf("plan %q: bad part %q", s, part)
			}
			return v
		}
		switch {
		case part == "pal":
			tu.plan.palette = true
		case strings.HasPrefix(part, "pal@"):
			tu.plan.palette = true
			tu.plan.order = num("pal@")
		case part == "sg":
			tu.plan.subGreen = true
		case strings.HasPrefix(part, "p"):
			tu.plan.predict = true
			tu.plan.predBits = num("p")
		case strings.HasPrefix(part, "c"):
			tu.plan.ct = true
			tu.plan.ctBits = num("c")
		case strings.HasPrefix(part, "n"):
			tu.plan.passes = num("n")
		case strings.HasPrefix(part, "z"):
			tu.vp8level = num("z")
		case strings.HasPrefix(part, "m"):
			for _, f := range strings.Split(part[1:], ".") {
				v, e := strconv.Atoi(f)
				if e != nil {
					err = fmt.Errorf("plan %q: bad modes", s)
				}
				tu.plan.modes = append(tu.plan.modes, uint8(v))
			}
		default:
			err = fmt.Errorf("plan %q: unknown part %q", s, part)
		}
	}
	return DefaultCompression, tu, err
}

// TestTunePlans prints the corpus size per category of every plan in
// MOEGO_WEBP_PLANS (space-separated specs of parsePlan). A palette plan is
// used only on images of at most 256 colors; the others fall back to the
// level's own choice, so totals stay comparable. MOEGO_WEBP_PERIMAGE=FILE
// appends "plan image bytes ns" lines.
func TestTunePlans(t *testing.T) {
	ms := requireTune(t)
	specs := strings.Fields(os.Getenv("MOEGO_WEBP_PLANS"))
	if len(specs) == 0 {
		specs = []string{"speed", "default", "best"}
	}
	var per *os.File
	if p := os.Getenv("MOEGO_WEBP_PERIMAGE"); p != "" {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		per = f
	}
	cats := map[string]int{}
	for _, c := range ms {
		cats[c.category] += c.raw
	}
	var catNames []string
	for c := range cats {
		catNames = append(catNames, c)
	}
	sort.Strings(catNames)
	for _, spec := range specs {
		level, tu, err := parsePlan(spec)
		if err != nil {
			t.Fatal(err)
		}
		sizes := make([]int, len(ms))
		times := make([]time.Duration, len(ms))
		work := make(chan int)
		var wg sync.WaitGroup
		for k := 0; k < runtime.GOMAXPROCS(0); k++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				e := &encoder{}
				for i := range work {
					e.level = level
					t0 := time.Now()
					if err := e.load(ms[i].m); err != nil {
						t.Error(err)
						continue
					}
					e.tune = tu
					if tu.on && tu.plan.palette {
						e.analyze()
						if e.an.ncolors > 256 {
							e.tune = tuning{}
						}
					}
					out, err := e.encode(ms[i].m)
					if err != nil {
						t.Error(err)
						continue
					}
					times[i] = time.Since(t0)
					sizes[i] = len(out) + 20 + len(out)&1
				}
			}()
		}
		for i := range ms {
			work <- i
		}
		close(work)
		wg.Wait()
		total, raw := 0, 0
		var ns time.Duration
		bycat := map[string]int{}
		for i, c := range ms {
			total += sizes[i]
			raw += c.raw
			ns += times[i]
			bycat[c.category] += sizes[i]
			if per != nil {
				fmt.Fprintf(per, "%s %s %d %d\n", spec, c.rel, sizes[i], times[i].Nanoseconds())
			}
		}
		var line strings.Builder
		for _, c := range catNames {
			fmt.Fprintf(&line, " %s=%.4f", strings.TrimPrefix(c, "textures_"), float64(bycat[c])/float64(cats[c]))
		}
		fmt.Printf("%-28s %10d %.4f %7.1fs%s\n", spec, total, float64(total)/float64(raw), ns.Seconds(), line.String())
	}
}

// TestTuneEstimate prints "plan image estimate" lines into
// MOEGO_WEBP_ESTIMATES for every plan in MOEGO_WEBP_PLANS, to compare the
// estimates' ranking with the real sizes of TestTunePlans.
func TestTuneEstimate(t *testing.T) {
	ms := requireTune(t)
	out, err := os.Create(os.Getenv("MOEGO_WEBP_ESTIMATES"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	specs := strings.Fields(os.Getenv("MOEGO_WEBP_PLANS"))
	type res struct {
		est []float64
		ns  []time.Duration
	}
	rs := make([]res, len(ms))
	work := make(chan int)
	var wg sync.WaitGroup
	for k := 0; k < runtime.GOMAXPROCS(0); k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := &encoder{}
			for i := range work {
				if err := e.load(ms[i].m); err != nil {
					t.Error(err)
					continue
				}
				e.analyze()
				if e.an.ncolors <= 256 {
					e.indexImage()
				}
				for _, spec := range specs {
					_, tu, err := parsePlan(spec)
					if err != nil {
						t.Error(err)
						return
					}
					t0 := time.Now()
					b := -1.0
					if !tu.plan.palette || e.an.ncolors <= 256 {
						b = e.estimate(tu.plan, false) / 8
					}
					rs[i].est = append(rs[i].est, b)
					rs[i].ns = append(rs[i].ns, time.Since(t0))
				}
			}
		}()
	}
	for i := range ms {
		work <- i
	}
	close(work)
	wg.Wait()
	for i, c := range ms {
		for k, spec := range specs {
			fmt.Fprintf(out, "%s %s %.0f %d\n", spec, c.rel, rs[i].est[k], rs[i].ns[k].Nanoseconds())
		}
	}
}

// TestCorpusRoundTrip encodes the tuning corpus at every level and checks
// each output with vp8ltest, when the corpus variables are set.
func TestCorpusRoundTrip(t *testing.T) {
	ms := requireTune(t)
	work := make(chan int)
	var wg sync.WaitGroup
	for k := 0; k < runtime.GOMAXPROCS(0); k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				for _, l := range levels {
					var buf bytes.Buffer
					if err := (&Encoder{CompressionLevel: l}).Encode(&buf, ms[i].m); err != nil {
						t.Error(err)
						continue
					}
					if err := checkFile(buf.Bytes(), ms[i].m); err != nil {
						t.Errorf("%s level %d: %v", ms[i].rel, l, err)
					}
				}
			}
		}()
	}
	for i := range ms {
		work <- i
	}
	close(work)
	wg.Wait()
}

// TestTuneColdAlloc encodes every corpus image at the level in
// MOEGO_WEBP_LEVEL (speed, default or best) with a fresh encoder, for heap
// profiles of cold encodes (-memprofile with -memprofilerate=1).
func TestTuneColdAlloc(t *testing.T) {
	ms := requireTune(t)
	level, _, err := parsePlan(os.Getenv("MOEGO_WEBP_LEVEL"))
	if err != nil {
		t.Fatal(err)
	}
	var total, objs uint64
	for _, c := range ms {
		var a, b runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&a)
		e := &encoder{level: level}
		if _, err := e.encode(c.m); err != nil {
			t.Fatal(err)
		}
		runtime.ReadMemStats(&b)
		total += b.TotalAlloc - a.TotalAlloc
		objs += b.Mallocs - a.Mallocs
	}
	fmt.Printf("cold %s: %.1f MB in %d allocations\n", os.Getenv("MOEGO_WEBP_LEVEL"), float64(total)/1e6, objs)
}

// TestTuneChoice prints, for every corpus image and level in
// MOEGO_WEBP_PLANS, each candidate plan's estimate and real size:
// "level image index estimate bytes ns" lines into MOEGO_WEBP_CHOICE.
func TestTuneChoice(t *testing.T) {
	ms := requireTune(t)
	out, err := os.Create(os.Getenv("MOEGO_WEBP_CHOICE"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	specs := strings.Fields(os.Getenv("MOEGO_WEBP_PLANS"))
	e := &encoder{}
	for _, c := range ms {
		for _, spec := range specs {
			level, _, err := parsePlan(spec)
			if err != nil {
				t.Fatal(err)
			}
			e.level = level
			e.tune = tuning{}
			if err := e.load(c.m); err != nil {
				t.Fatal(err)
			}
			e.analyze()
			if e.an.ncolors <= 256 {
				e.indexImage()
			}
			e.ncache, e.subGreenOn = 0, false
			var cands [maxCands]plan
			plans := e.plans(cands[:0])
			for i, p := range plans {
				t0 := time.Now()
				est := e.estimate(p, false)
				ns := time.Since(t0)
				n := len(e.run(p, vp8lLevel(level)))
				e.undo(p)
				fmt.Fprintf(out, "%s %s %d %.0f %d %d\n", spec, c.rel, i, est/8, n, ns.Nanoseconds())
			}
		}
	}
}

// BenchmarkTuneParts times the transform searches and the estimator alone
// on the tuning corpus (or MOEGO_WEBP_ONLY's part of it), one image after
// another, as DefaultCompression's subtract green + predictor + color
// transform plan runs them.
func BenchmarkTuneParts(b *testing.B) {
	if os.Getenv("MOEGO_WEBP_CORPUS") == "" {
		b.Skip("set MOEGO_WEBP_CORPUS and MOEGO_WEBP_LIST")
	}
	ms, err := corpusOnce()
	if err != nil {
		b.Fatal(err)
	}
	type prepared struct {
		e     *encoder
		res   []uint32 // the predictor residuals
		org   []uint32
		modes []uint8
		row   []uint32
	}
	var ps []prepared
	px := 0
	for _, c := range ms {
		e := &encoder{level: DefaultCompression}
		if err := e.load(c.m); err != nil {
			b.Fatal(err)
		}
		e.analyze()
		if e.an.ncolors <= 256 || e.w == 1 || e.h == 1 {
			continue
		}
		e.setSubGreen(true)
		org := append([]uint32(nil), e.argb...)
		modes := e.searchPredictors(e.argb, e.w, e.h, predParams{bits: 2, passes: 1, bias: predBias, prior: predPrior, set: e.scoreSet()})
		res := append([]uint32(nil), e.argb...)
		applyPredictor(res, e.w, e.h, 2, modes, make([]uint32, e.w))
		ps = append(ps, prepared{e, res, org, append([]uint8(nil), modes...), make([]uint32, e.w)})
		px += e.w * e.h
	}
	run := func(name string, f func(p prepared)) {
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				for _, p := range ps {
					f(p)
				}
			}
			b.ReportMetric(float64(px)*float64(b.N)/b.Elapsed().Seconds()/1e6, "MP/s")
		})
	}
	run("pred", func(p prepared) {
		p.e.searchPredictors(p.org, p.e.w, p.e.h, predParams{bits: 2, passes: 1, bias: predBias, prior: predPrior, set: p.e.scoreSet()})
	})
	run("pred2", func(p prepared) {
		p.e.searchPredictors(p.org, p.e.w, p.e.h, predParams{bits: 2, passes: 2, bias: predBias, prior: predPrior, set: p.e.scoreSet()})
	})
	run("ct", func(p prepared) {
		p.e.searchColorTransform(p.res, p.e.w, p.e.h, ctParams{bits: 4, bias: ctBias})
	})
	run("ctwide", func(p prepared) {
		p.e.searchColorTransform(p.res, p.e.w, p.e.h, ctParams{bits: 4, wide: true, bias: ctBias})
	})
	run("est", func(p prepared) {
		p.e.est.bits(p.res, p.e.w, p.e.h)
	})
	run("apply", func(p prepared) {
		copy(p.e.argb, p.org)
		applyPredictor(p.e.argb, p.e.w, p.e.h, 2, p.modes, p.row)
	})
	run("undo", func(p prepared) {
		copy(p.e.argb, p.res)
		undoPredictor(p.e.argb, p.e.w, p.e.h, 2, p.modes)
	})
}

// TestTuneSide times the webp side of a level alone (MOEGO_WEBP_LEVEL,
// default speed): load, analyze, the plan choice and the transforms with
// their data, without the entropy coding of the main image. Each image runs
// MOEGO_WEBP_REPEAT times (default 5) and its fastest run counts.
func TestTuneSide(t *testing.T) {
	ms := requireTune(t)
	rep := 5
	if s := os.Getenv("MOEGO_WEBP_REPEAT"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil {
			t.Fatal(err)
		}
		rep = v
	}
	name := os.Getenv("MOEGO_WEBP_LEVEL")
	if name == "" {
		name = "speed"
	}
	level, _, err := parsePlan(name)
	if err != nil {
		t.Fatal(err)
	}
	e := &encoder{level: level}
	var total time.Duration
	for _, c := range ms {
		best := time.Duration(1<<63 - 1)
		for r := 0; r < rep; r++ {
			t0 := time.Now()
			e.tune = tuning{side: true}
			if _, err := e.encode(c.m); err != nil {
				t.Fatal(err)
			}
			if d := time.Since(t0); d < best {
				best = d
			}
		}
		total += best
	}
	fmt.Printf("side %s: %.1f ms\n", name, total.Seconds()*1e3)
}
