// Command imgbench measures lossless image encoders on a corpus: output
// size, encode time and decode time with each format's reference decoders,
// after checking that every output decodes back to the source pixels.
//
// Pin the process to one CPU from outside (taskset); GOMAXPROCS defaults
// to 1 so that encoders with internal parallelism run single-threaded.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MoeForge/moego/bench/image/codecs"
)

var (
	corpusDir = flag.String("corpus", "", "directory the corpus list's paths are relative to")
	listFile  = flag.String("list", "corpus.txt", "corpus list: lines of `sha256  relpath`")
	only      = flag.String("images", "", "comma-separated substrings; keep only images whose path contains one")
	every     = flag.Int("every", 1, "keep only every n-th image of the list (index mod n == 0)")
	shard     = flag.String("shard", "", "k/n: keep the k-th of n shards; shards balance pixel counts (greedy, largest first)")
	encSpec   = flag.String("encoders", "all", "comma-separated name/setting patterns (path.Match); see -listenc")
	outDir    = flag.String("out", "", "output directory")
	tag       = flag.String("tag", "", "suffix for output file names (default: the -encoders value, sanitized)")
	minTime   = flag.Duration("mintime", 100*time.Millisecond, "keep timing a cell until this much time has passed")
	minRuns   = flag.Int("minruns", 5, "and at least this many runs")
	maxRuns   = flag.Int("maxruns", 100, "at most this many runs")
	slow      = flag.Duration("slow", 2*time.Second, "time a cell only once (the checked run) when one run takes longer")
	slowMin   = flag.Int("slowruns", 3, "runs for cells between -mintime and -slow per run")
	procs     = flag.Int("procs", 1, "GOMAXPROCS")
	checkOnly = flag.Bool("check", false, "only check correctness and sizes, no timing")
	saveDir   = flag.String("save", "", "if set, write every encoded file here")
	checked   = flag.String("checked", "", "directory of a -check run; time only the cells that passed there")
	resume    = flag.Bool("resume", false, "skip encoder/image cells that already have a row in -out's results-*.csv (use a new -tag)")
	listEnc   = flag.Bool("listenc", false, "list encoders and decoders and exit")
)

type corpusEntry struct {
	sum, rel, category string
	index              int // position in the full list
}

func main() {
	flag.Parse()
	runtime.GOMAXPROCS(*procs)
	encs, err := codecs.Select(*encSpec)
	if err != nil {
		log.Fatal(err)
	}
	if *listEnc {
		for _, e := range codecs.Encoders() {
			fmt.Printf("%-28s %-5s pure=%-5v %s %s\n", e.ID(), e.Format, e.Pure, e.Lib, e.Note)
		}
		for _, f := range []string{"png", "webp"} {
			for _, d := range codecs.Decoders(f) {
				fmt.Printf("decoder %-4s %-3s %s\n", f, d.Name, d.Lib)
			}
		}
		return
	}
	if *corpusDir == "" || *outDir == "" {
		log.Fatal("need -corpus and -out")
	}
	entries, err := readList(*listFile)
	if err != nil {
		log.Fatal(err)
	}
	if *only != "" {
		var keep []corpusEntry
		for _, e := range entries {
			for _, s := range strings.Split(*only, ",") {
				if strings.Contains(e.rel, s) {
					keep = append(keep, e)
					break
				}
			}
		}
		entries = keep
	}
	if *every > 1 {
		var keep []corpusEntry
		for _, e := range entries {
			if e.index%*every == 0 {
				keep = append(keep, e)
			}
		}
		entries = keep
	}
	done := map[string]bool{}
	if *resume {
		done = readCells(*outDir, func(string) bool { return true })
	}
	if *shard != "" {
		var k, n int
		if _, err := fmt.Sscanf(*shard, "%d/%d", &k, &n); err != nil || n <= 0 || k < 0 || k >= n {
			log.Fatalf("bad -shard %q", *shard)
		}
		entries = shardOf(entries, k, n, func(e corpusEntry) bool {
			for _, enc := range encs {
				if !done[enc.ID()+"\x00"+e.rel] {
					return true
				}
			}
			return false
		})
	}
	if *tag == "" {
		*tag = strings.Map(func(r rune) rune {
			if r == '/' || r == ',' || r == '*' {
				return '_'
			}
			return r
		}, *encSpec)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatal(err)
	}
	writeMeta(encs, entries)

	passed := map[string]bool{}
	if *checked != "" {
		passed = readCells(*checked, func(st string) bool { return st == "ok" || st == "alpha0" })
		if len(passed) == 0 {
			log.Fatalf("-checked %s: no passed cells", *checked)
		}
	}

	imgW := newCSV("images", "image", "index", "category", "sha256", "file_bytes", "src_type", "width", "height", "pixels", "has_alpha", "alpha0_pixels", "colors")
	resW := newCSV("results", "image", "category", "width", "height", "has_alpha", "encoder", "setting", "format", "pure",
		"status", "detail", "bytes", "enc_runs", "enc_median_ns", "enc_min_ns", "enc_alloc_bytes", "enc_allocs",
		"dec_go_runs", "dec_go_median_ns", "dec_c_runs", "dec_c_median_ns")
	runW := newCSV("runs", "image", "encoder", "setting", "op", "ns")
	defer imgW.close()
	defer resW.close()
	defer runW.close()

	start := time.Now()
	for i, ce := range entries {
		path := filepath.Join(*corpusDir, ce.rel)
		raw, err := os.ReadFile(path)
		if err != nil {
			log.Fatal(err)
		}
		if s := sha256.Sum256(raw); hex.EncodeToString(s[:]) != ce.sum {
			log.Fatalf("%s: sha256 mismatch", ce.rel)
		}
		dec, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			log.Fatalf("%s: %v", ce.rel, err)
		}
		src := codecs.ToNRGBA(dec)
		w, h := src.Rect.Dx(), src.Rect.Dy()
		hasAlpha := !codecs.Opaque(src)
		colors, alpha0 := stats(src)
		imgW.row(ce.rel, ce.index, ce.category, ce.sum, len(raw), srcType(raw), w, h, w*h, hasAlpha, alpha0, colors)
		log.Printf("[%d/%d] %s %dx%d alpha=%v colors=%d (%.0fs)", i+1, len(entries), ce.rel, w, h, hasAlpha, colors, time.Since(start).Seconds())
		for _, e := range encs {
			if *checked != "" && !passed[e.ID()+"\x00"+ce.rel] || done[e.ID()+"\x00"+ce.rel] {
				continue
			}
			measure(e, ce, src, hasAlpha, resW, runW)
		}
		imgW.flush()
		resW.flush()
		runW.flush()
	}
	log.Printf("done in %.0fs", time.Since(start).Seconds())
}

func measure(e *codecs.Encoder, ce corpusEntry, src *image.NRGBA, hasAlpha bool, resW, runW *csvOut) {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	row := func(status, detail string, size int, enc, decGo, decC series, alloc, allocs uint64) {
		resW.row(ce.rel, ce.category, w, h, hasAlpha, e.Name, e.Setting, e.Format, e.Pure,
			status, detail, size, enc.n(), enc.median(), enc.min(), alloc, allocs,
			decGo.n(), decGo.median(), decC.n(), decC.median())
		for _, r := range []struct {
			op string
			s  series
		}{{"encode", enc}, {"decode-go", decGo}, {"decode-c", decC}} {
			if len(r.s) > 0 {
				runW.row(ce.rel, e.Name, e.Setting, r.op, r.s.join())
			}
		}
		// A row costs seconds to minutes; keep it if the run is stopped.
		resW.flush()
		runW.flush()
	}

	runtime.GC()
	t0 := time.Now()
	out, err := safeEncode(e, src)
	first := time.Since(t0)
	if err != nil {
		log.Printf("  %-28s encode error: %v", e.ID(), err)
		row("encode-error", err.Error(), 0, nil, nil, nil, 0, 0)
		return
	}
	if *saveDir != "" {
		name := strings.NewReplacer("/", "_").Replace(ce.rel) + "." + strings.ReplaceAll(e.ID(), "/", "_") + "." + e.Format
		if err := os.WriteFile(filepath.Join(*saveDir, name), out, 0o644); err != nil {
			log.Fatal(err)
		}
	}

	status, details := "ok", []string{}
	decs := codecs.Decoders(e.Format)
	for _, d := range decs {
		got, err := safeDecode(d, out)
		switch {
		case err != nil:
			details = append(details, d.Name+": decode error: "+err.Error())
			status = "decode-error"
		default:
			switch diff := codecs.Compare(src, codecs.ToNRGBA(got)); diff {
			case "":
			case "alpha0":
				details = append(details, d.Name+": RGB under alpha 0 changed")
				if status == "ok" {
					status = "alpha0"
				}
			default:
				details = append(details, d.Name+": "+diff)
				status = "mismatch"
			}
		}
	}
	detail := strings.Join(details, "; ")
	if status != "ok" && status != "alpha0" {
		log.Printf("  %-28s %s: %s", e.ID(), status, detail)
		row(status, detail, len(out), series{first}, nil, nil, 0, 0)
		return
	}
	if *checkOnly {
		log.Printf("  %-28s %9d B  first encode %.2fs %s", e.ID(), len(out), first.Seconds(), status)
		row(status, detail, len(out), series{first}, nil, nil, 0, 0)
		return
	}

	// The checked run above is a warm-up; it is the only timed run when it
	// takes longer than -slow. The first timed run also counts Go heap
	// allocations and keeps its output, which is compared with the checked
	// one after timing.
	var enc, decGo, decC series
	var alloc, allocs uint64
	var again []byte
	if first >= *slow {
		enc = series{first}
	} else {
		runs := 0
		enc = timeIt(first, func() error {
			var a, b runtime.MemStats
			if runs == 0 {
				runtime.ReadMemStats(&a)
			}
			o, err := e.Encode(src)
			if runs == 0 {
				runtime.ReadMemStats(&b)
				alloc, allocs = b.TotalAlloc-a.TotalAlloc, b.Mallocs-a.Mallocs
				again = o
			}
			runs++
			return err
		})
		if enc == nil {
			log.Printf("  %-28s timed encode failed", e.ID())
			row("timing-error", "a timed encode failed", len(out), nil, nil, nil, 0, 0)
			return
		}
	}
	if again != nil && !bytes.Equal(again, out) {
		// Some encoders are not deterministic; the second output must
		// round-trip as well, and the row notes both sizes.
		for _, d := range decs {
			got, err := safeDecode(d, again)
			if err == nil {
				if diff := codecs.Compare(src, codecs.ToNRGBA(got)); diff != "" && diff != "alpha0" {
					err = fmt.Errorf("%s", diff)
				}
			}
			if err != nil {
				log.Printf("  %-28s second output fails: %s: %v", e.ID(), d.Name, err)
				row("nondeterministic-mismatch", fmt.Sprintf("second encode (%d bytes) fails %s: %v", len(again), d.Name, err), len(out), enc, nil, nil, alloc, allocs)
				return
			}
		}
		if detail != "" {
			detail += "; "
		}
		detail += fmt.Sprintf("nondeterministic output: %d then %d bytes, both round-trip", len(out), len(again))
	}
	for _, d := range decs {
		s := timeIt(0, func() error { _, err := d.Decode(out); return err })
		if d.Name == "go" {
			decGo = s
		} else {
			decC = s
		}
	}
	log.Printf("  %-28s %9d B  enc %8.2f MP/s  dec go %7.1f c %7.1f MP/s %s", e.ID(), len(out),
		mps(w*h, enc.median()), mps(w*h, decGo.median()), mps(w*h, decC.median()), status)
	row(status, detail, len(out), enc, decGo, decC, alloc, allocs)
}

func mps(px int, ns int64) float64 {
	if ns <= 0 {
		return 0
	}
	return float64(px) / float64(ns) * 1e3
}

type series []time.Duration

func (s series) n() int { return len(s) }

func (s series) median() int64 {
	if len(s) == 0 {
		return 0
	}
	c := slices.Clone(s)
	slices.Sort(c)
	if len(c)%2 == 1 {
		return int64(c[len(c)/2])
	}
	return int64(c[len(c)/2-1]+c[len(c)/2]) / 2
}

func (s series) min() int64 {
	if len(s) == 0 {
		return 0
	}
	return int64(slices.Min(s))
}

func (s series) join() string {
	parts := make([]string, len(s))
	for i, d := range s {
		parts[i] = strconv.FormatInt(int64(d), 10)
	}
	return strings.Join(parts, ";")
}

// timeIt runs f until -minruns runs and -mintime have passed (capped by
// -maxruns). A run slower than -mintime drops the target to -slowruns runs.
// first, when nonzero, is the duration of an untimed warm-up run already
// made; it only picks the run count.
func timeIt(first time.Duration, f func() error) series {
	runtime.GC()
	want := *minRuns
	if first > *minTime {
		want = *slowMin
	}
	var s series
	var total time.Duration
	for len(s) < *maxRuns && (len(s) < want || total < *minTime) {
		t0 := time.Now()
		if err := f(); err != nil {
			log.Printf("timed run failed: %v", err)
			return nil
		}
		d := time.Since(t0)
		s = append(s, d)
		total += d
		if len(s) == 1 && d > *minTime && want > *slowMin {
			want = *slowMin
		}
	}
	return s
}

func safeEncode(e *codecs.Encoder, m *image.NRGBA) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	out, err = e.Encode(m)
	if err == nil && len(out) == 0 {
		err = fmt.Errorf("empty output")
	}
	return
}

func safeDecode(d *codecs.Decoder, data []byte) (m image.Image, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return d.Decode(data)
}

// stats counts distinct NRGBA values and fully transparent pixels.
func stats(m *image.NRGBA) (colors, alpha0 int) {
	px := make([]uint32, 0, len(m.Pix)/4)
	for i := 0; i < len(m.Pix); i += 4 {
		px = append(px, uint32(m.Pix[i])<<24|uint32(m.Pix[i+1])<<16|uint32(m.Pix[i+2])<<8|uint32(m.Pix[i+3]))
		if m.Pix[i+3] == 0 {
			alpha0++
		}
	}
	slices.Sort(px)
	for i := range px {
		if i == 0 || px[i] != px[i-1] {
			colors++
		}
	}
	return colors, alpha0
}

// srcType names the source PNG's IHDR color type and bit depth.
func srcType(raw []byte) string {
	if len(raw) < 26 {
		return "?"
	}
	names := map[byte]string{0: "gray", 2: "rgb", 3: "palette", 4: "gray+alpha", 6: "rgba"}
	n, ok := names[raw[25]]
	if !ok {
		n = "type" + strconv.Itoa(int(raw[25]))
	}
	return n + strconv.Itoa(int(raw[24]))
}

// readCells returns the encoder-ID/image pairs in dir's results-*.csv
// whose status satisfies keep.
func readCells(dir string, keep func(status string) bool) map[string]bool {
	files, err := filepath.Glob(filepath.Join(dir, "results-*.csv"))
	if err != nil {
		log.Fatal(err)
	}
	out := map[string]bool{}
	for _, name := range files {
		f, err := os.Open(name)
		if err != nil {
			log.Fatal(err)
		}
		recs, err := csv.NewReader(f).ReadAll()
		f.Close()
		if err != nil {
			log.Fatalf("%s: %v", name, err)
		}
		if len(recs) == 0 {
			continue
		}
		col := map[string]int{}
		for i, h := range recs[0] {
			col[h] = i
		}
		for _, r := range recs[1:] {
			if keep(r[col["status"]]) {
				out[r[col["encoder"]]+"/"+r[col["setting"]]+"\x00"+r[col["image"]]] = true
			}
		}
	}
	return out
}

// shardOf assigns images to n shards, largest first, each to the shard
// with the fewest pixels so far (ties to the lowest shard), and returns
// shard k's images in list order. Images without work are dropped.
func shardOf(entries []corpusEntry, k, n int, work func(corpusEntry) bool) []corpusEntry {
	px := make([]int, len(entries))
	order := make([]int, len(entries))
	for i, e := range entries {
		if work(e) {
			px[i] = ihdrPixels(filepath.Join(*corpusDir, e.rel))
		} else {
			px[i] = -1
		}
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return px[order[a]] > px[order[b]] })
	load := make([]int, n)
	mine := make([]bool, len(entries))
	for _, i := range order {
		best := 0
		for s := 1; s < n; s++ {
			if load[s] < load[best] {
				best = s
			}
		}
		if px[i] < 0 {
			continue
		}
		load[best] += px[i]
		mine[i] = best == k
	}
	var out []corpusEntry
	for i, e := range entries {
		if mine[i] {
			out = append(out, e)
		}
	}
	return out
}

// ihdrPixels reads width*height from a PNG's IHDR.
func ihdrPixels(path string) int {
	f, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	var b [24]byte
	if _, err := io.ReadFull(f, b[:]); err != nil {
		log.Fatalf("%s: %v", path, err)
	}
	return int(binary.BigEndian.Uint32(b[16:20])) * int(binary.BigEndian.Uint32(b[20:24]))
}

func readList(path string) ([]corpusEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []corpusEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sum, rel, ok := strings.Cut(line, "  ")
		if !ok {
			return nil, fmt.Errorf("bad list line %q", line)
		}
		cat, _, _ := strings.Cut(rel, "/")
		out = append(out, corpusEntry{sum, rel, cat, len(out)})
	}
	return out, sc.Err()
}

func writeMeta(encs []*codecs.Encoder, entries []corpusEntry) {
	type encMeta struct {
		ID, Format, Lib, Note string
		Pure                  bool
		Index                 int // position in codecs.Encoders()
	}
	index := map[string]int{}
	for i, e := range codecs.Encoders() {
		index[e.ID()] = i
	}
	m := map[string]any{
		"start":      time.Now().Format(time.RFC3339),
		"go":         runtime.Version(),
		"goos":       runtime.GOOS,
		"goarch":     runtime.GOARCH,
		"gomaxprocs": runtime.GOMAXPROCS(0),
		"cpu":        cpuModel(),
		"args":       os.Args[1:],
		"images":     len(entries),
	}
	if h, err := os.Hostname(); err == nil {
		m["host"] = h
	}
	var em []encMeta
	for _, e := range encs {
		em = append(em, encMeta{e.ID(), e.Format, e.Lib, e.Note, e.Pure, index[e.ID()]})
	}
	m["encoders"] = em
	dm := map[string]string{}
	for _, f := range []string{"png", "webp"} {
		for _, d := range codecs.Decoders(f) {
			dm[f+"/"+d.Name] = d.Lib
		}
	}
	m["decoders"] = dm
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(*outDir, "meta-"+*tag+".json"), append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
}

func cpuModel() string {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(l, ":"); ok && strings.TrimSpace(k) == "model name" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

type csvOut struct {
	f *os.File
	w *csv.Writer
}

func newCSV(name string, header ...string) *csvOut {
	f, err := os.Create(filepath.Join(*outDir, name+"-"+*tag+".csv"))
	if err != nil {
		log.Fatal(err)
	}
	c := &csvOut{f, csv.NewWriter(f)}
	c.w.Write(header)
	return c
}

func (c *csvOut) row(vals ...any) {
	rec := make([]string, len(vals))
	for i, v := range vals {
		rec[i] = fmt.Sprint(v)
	}
	if err := c.w.Write(rec); err != nil {
		log.Fatal(err)
	}
}

func (c *csvOut) flush() {
	c.w.Flush()
	if err := c.w.Error(); err != nil {
		log.Fatal(err)
	}
}

func (c *csvOut) close() {
	c.flush()
	c.f.Close()
}
