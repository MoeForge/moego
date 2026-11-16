package deflate

import (
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestTune compresses the filtered rows cached by bench/image's
// deflatebench (-cache DIR) and prints the total size and speed. It runs
// only when MOEGO_DEFLATE_ROWS names that directory. MOEGO_DEFLATE_LEVELS
// lists the levels (default 1,6,9); MOEGO_DEFLATE_TUNE overrides chain
// parameters as level:depth:nice:good:lazy, separated by commas.
func TestTune(t *testing.T) {
	dir := os.Getenv("MOEGO_DEFLATE_ROWS")
	if dir == "" {
		t.Skip("MOEGO_DEFLATE_ROWS not set")
	}
	for _, spec := range strings.Split(os.Getenv("MOEGO_DEFLATE_TUNE"), ",") {
		if spec == "" {
			continue
		}
		var v [5]int
		for i, f := range strings.Split(spec, ":") {
			v[i], _ = strconv.Atoi(f)
		}
		chainLevels[v[0]] = chainLevel{depth: v[1], nice: v[2], good: v[3], lazy: v[4] != 0}
	}
	verifyMatches = false
	exactSplit = os.Getenv("MOEGO_DEFLATE_NOEXACT") == ""
	noSplit = os.Getenv("MOEGO_DEFLATE_NOSPLIT") != ""
	for name, p := range map[string]*float64{"HBASE": &headerBase, "HSYM": &headerPerSym} {
		if v, err := strconv.ParseFloat(os.Getenv("MOEGO_DEFLATE_"+name), 64); err == nil {
			*p = v
		}
	}
	for name, p := range map[string]*int{"SCOREW": &scoreW, "MINSCORE": &minScoreAdj, "LAZYBONUS": &lazyBonus, "SEG": &segTokens, "XSEG": &exactSegTokens, "RELAX": &relaxCap, "WALK": &walkDist,
		"FASTTAIL": &fastTail, "BUFTOKENS": &bufTokens} {
		if v, err := strconv.Atoi(os.Getenv("MOEGO_DEFLATE_" + name)); err == nil {
			*p = v
		}
	}
	// The near-optimal parse knobs apply to levels 8 and 9.
	for name, f := range map[string]func(*optLevel) *int{
		"OPTDEPTH": func(p *optLevel) *int { return &p.depth },
		"OPTNICE":  func(p *optLevel) *int { return &p.nice },
		"OPTITERS": func(p *optLevel) *int { return &p.iters },
		"OPTFIRST": func(p *optLevel) *int { return &p.firstIters },
		"REPARSE":  func(p *optLevel) *int { return &p.reparse },
		"OPTSEG":   func(p *optLevel) *int { return &p.seg },
	} {
		if v, err := strconv.Atoi(os.Getenv("MOEGO_DEFLATE_" + name)); err == nil {
			for l := minOptLevel; l <= BestCompression; l++ {
				*f(&optLevels[l]) = v
			}
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.rows"))
	slices.Sort(files)
	// MOEGO_DEFLATE_LIST, the corpus list, groups the images by category.
	category := map[string]string{}
	if list, err := os.ReadFile(os.Getenv("MOEGO_DEFLATE_LIST")); err == nil {
		for _, line := range strings.Split(string(list), "\n") {
			if sum, rel, ok := strings.Cut(line, "  "); ok && !strings.HasPrefix(line, "#") {
				category[sum], _, _ = strings.Cut(rel, "/")
			}
		}
	}
	var inputs []tuneInput
	total := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		sum := strings.TrimSuffix(filepath.Base(f), ".rows")
		if only := os.Getenv("MOEGO_DEFLATE_ONLY"); only != "" && !strings.Contains(category[sum], only) {
			continue
		}
		inputs = append(inputs, tuneInput{b[8:], int(binary.LittleEndian.Uint32(b)), int(binary.LittleEndian.Uint32(b[4:])), category[sum]})
		total += len(b) - 8
	}
	if len(inputs) == 0 {
		t.Fatal("no inputs")
	}
	levels := os.Getenv("MOEGO_DEFLATE_LEVELS")
	if levels == "" {
		levels = "1,6,9"
	}
	if os.Getenv("MOEGO_DEFLATE_COLD") != "" {
		for _, ls := range strings.Split(levels, ",") {
			level, _ := strconv.Atoi(ls)
			coldAllocs(level, inputs)
		}
		return
	}
	runs, _ := strconv.Atoi(os.Getenv("MOEGO_DEFLATE_RUNS"))
	runs = max(runs, 1)
	w := NewWriter(io.Discard, 1)
	for _, ls := range strings.Split(levels, ",") {
		level, _ := strconv.Atoi(ls)
		var cw countWriter
		var best time.Duration
		type cat struct {
			in, out int
			time    time.Duration
		}
		cats := map[string]*cat{}
		for r := 0; r < runs; r++ {
			cw = 0
			t0 := time.Now()
			for _, in := range inputs {
				c0, t1 := cw, time.Now()
				w.Reset(&cw, level)
				w.SetHint(in.bpp, in.stride)
				w.Write(in.rows)
				w.Close()
				c := cats[in.category]
				if c == nil {
					c = &cat{}
					cats[in.category] = c
				}
				if r == 0 {
					c.in += len(in.rows)
					c.out += int(cw - c0)
				}
				c.time += time.Since(t1)
			}
			if d := time.Since(t0); r == 0 || d < best {
				best = d
			}
		}
		fmt.Printf("level %d: %d images, %d -> %d bytes, %.1f MB/s\n", level, len(inputs), total, int(cw), float64(total)/1e6/best.Seconds())
		if o := w.opt; o != nil && level >= minOptLevel {
			fmt.Printf("level %d: %.2f matches listed per position\n", level, float64(o.listed)/float64(max(o.positions, 1)))
		}
		if os.Getenv("MOEGO_DEFLATE_LIST") != "" {
			var names []string
			for name := range cats {
				names = append(names, name)
			}
			slices.Sort(names)
			for _, name := range names {
				c := cats[name]
				fmt.Printf("level %d %-16s %10d %6.1f MB/s\n", level, name, c.out, float64(c.in)*float64(runs)/1e6/c.time.Seconds())
			}
		}
	}
}

type tuneInput struct {
	rows        []byte
	bpp, stride int
	category    string
}

// coldAllocs prints the bytes allocated by a new Writer encoding each
// input row by row, as a PNG encoder feeds it, next to compress/zlib's.
func coldAllocs(level int, inputs []tuneInput) {
	measure := func(rows []byte, stride int, enc func() io.WriteCloser) uint64 {
		var m0, m1 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m0)
		w := enc()
		for p := rows; len(p) > 0; {
			n := min(stride, len(p))
			w.Write(p[:n])
			p = p[n:]
		}
		w.Close()
		runtime.ReadMemStats(&m1)
		return m1.TotalAlloc - m0.TotalAlloc
	}
	var ours, theirs, oursMax, theirsMax uint64
	for _, in := range inputs {
		a := measure(in.rows, in.stride, func() io.WriteCloser {
			w := NewWriter(io.Discard, level)
			w.SetHint(in.bpp, in.stride)
			return w
		})
		b := measure(in.rows, in.stride, func() io.WriteCloser {
			w, _ := zlib.NewWriterLevel(io.Discard, level)
			return w
		})
		ours, theirs = ours+a, theirs+b
		oursMax, theirsMax = max(oursMax, a), max(theirsMax, b)
	}
	fmt.Printf("cold level %d: %d images, moego %.1f MB (largest %d KB), compress/zlib %.1f MB (largest %d KB)\n",
		level, len(inputs), float64(ours)/1e6, oursMax>>10, float64(theirs)/1e6, theirsMax>>10)
}

type countWriter int

func (c *countWriter) Write(p []byte) (int, error) {
	*c += countWriter(len(p))
	return len(p), nil
}
