// Command deflatebench measures zlib compressors on the bytes a PNG encoder
// feeds to zlib: for each corpus image, image/png's filtered rows (its
// DefaultCompression output, inflated). Every output is inflated and
// compared with the input before it is timed.
//
// Pin the process to one CPU from outside (taskset). Results go to
// -out/results-TAG.csv, one row per engine and image; -sum merges result
// files into a table.
package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/binary"
	"encoding/csv"
	"encoding/hex"
	"flag"
	"fmt"
	"image/png"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MoeForge/moego/internal/deflate"
)

var (
	corpusDir = flag.String("corpus", "", "directory the corpus list's paths are relative to")
	listFile  = flag.String("list", "corpus.txt", "corpus list: lines of `sha256  relpath`")
	only      = flag.String("images", "", "comma-separated substrings; keep only images whose path contains one")
	every     = flag.Int("every", 1, "keep only every n-th image of the list")
	shard     = flag.String("shard", "", "k/n: keep the k-th of n shards, balanced by input size")
	engSpec   = flag.String("engines", "all", "comma-separated name/setting patterns (path.Match), -pattern removes")
	outDir    = flag.String("out", "", "output directory")
	tag       = flag.String("tag", "", "suffix for the results file name (default: the -engines value, sanitized)")
	cacheDir  = flag.String("cache", "", "directory caching the filtered rows of each image")
	minTime   = flag.Duration("mintime", 200*time.Millisecond, "keep timing a cell until this much time has passed")
	minRuns   = flag.Int("minruns", 5, "and at least this many runs")
	maxRuns   = flag.Int("maxruns", 200, "at most this many runs")
	procs     = flag.Int("procs", 1, "GOMAXPROCS")
	checkOnly = flag.Bool("check", false, "only check correctness and sizes, no timing")
	sumDirs   = flag.String("sum", "", "comma-separated result directories to summarize instead of running")
	listEng   = flag.Bool("listeng", false, "list engines and exit")
	cpuProf   = flag.String("cpuprofile", "", "write a CPU profile of the timed runs to this file")
	analyzeIt = flag.Bool("analyze", false, "print the block and symbol statistics of each engine's output instead of timing")
)

// An engine compresses src into a zlib stream appended to dst[:0].
type engine struct {
	name     string // compressor/setting
	lib      string
	compress func(dst, src []byte, bpp, stride int) []byte
}

var engines []*engine

func addEngine(e *engine) { engines = append(engines, e) }

type sliceWriter struct{ b []byte }

func (s *sliceWriter) Write(p []byte) (int, error) {
	s.b = append(s.b, p...)
	return len(p), nil
}

func init() {
	for _, level := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9} {
		w := deflate.NewWriter(nil, level)
		var sw sliceWriter
		addEngine(&engine{name: fmt.Sprintf("moego/l%d", level), lib: "github.com/MoeForge/moego/internal/deflate with SetHint",
			compress: func(dst, src []byte, bpp, stride int) []byte {
				sw.b = dst[:0]
				w.Reset(&sw, level)
				w.SetHint(bpp, stride)
				w.Write(src)
				if err := w.Close(); err != nil {
					log.Fatal(err)
				}
				return sw.b
			}})
	}
	for _, level := range []int{1, 6, 9} {
		w, _ := zlib.NewWriterLevel(nil, level)
		var sw sliceWriter
		addEngine(&engine{name: fmt.Sprintf("zlib-go/l%d", level), lib: "compress/zlib " + runtime.Version(),
			compress: func(dst, src []byte, bpp, stride int) []byte {
				sw.b = dst[:0]
				w.Reset(&sw)
				w.Write(src)
				if err := w.Close(); err != nil {
					log.Fatal(err)
				}
				return sw.b
			}})
	}
}

// selectEngines returns the engines matching spec.
func selectEngines(spec string) ([]*engine, error) {
	keep := map[*engine]bool{}
	for _, p := range strings.Split(spec, ",") {
		p = strings.TrimSpace(p)
		drop := strings.HasPrefix(p, "-")
		p = strings.TrimPrefix(p, "-")
		if p == "all" {
			p = "*"
		}
		matched := false
		for _, e := range engines {
			ok, err := path.Match(p, e.name)
			if err != nil {
				return nil, err
			}
			if ok || p == "*" {
				matched = true
				if drop {
					delete(keep, e)
				} else {
					keep[e] = true
				}
			}
		}
		if !matched {
			return nil, fmt.Errorf("no engine matches %q", p)
		}
	}
	var out []*engine
	for _, e := range engines {
		if keep[e] {
			out = append(out, e)
		}
	}
	return out, nil
}

type entry struct {
	sum, rel string
	index    int
	size     int
}

func readList(path string) ([]entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []entry
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
		out = append(out, entry{sum: sum, rel: rel, index: len(out)})
	}
	return out, sc.Err()
}

// rowsOf returns image/png's filtered rows for the PNG file data, with the
// bytes per pixel and per row (filter byte included).
func rowsOf(data []byte) (rows []byte, bpp, stride int, err error) {
	m, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		return nil, 0, 0, err
	}
	b := buf.Bytes()[8:]
	var idat []byte
	var width, height, depth, ctype int
	for len(b) >= 12 {
		n := int(binary.BigEndian.Uint32(b))
		body := b[8 : 8+n]
		switch string(b[4:8]) {
		case "IHDR":
			width = int(binary.BigEndian.Uint32(body))
			height = int(binary.BigEndian.Uint32(body[4:]))
			depth, ctype = int(body[8]), int(body[9])
		case "IDAT":
			idat = append(idat, body...)
		}
		b = b[12+n:]
	}
	r, err := zlib.NewReader(bytes.NewReader(idat))
	if err != nil {
		return nil, 0, 0, err
	}
	rows, err = io.ReadAll(r)
	if err != nil {
		return nil, 0, 0, err
	}
	channels := map[int]int{0: 1, 2: 3, 3: 1, 4: 2, 6: 4}[ctype]
	pixelBits := channels * depth
	bpp = max(1, pixelBits/8)
	stride = 1 + (width*pixelBits+7)/8
	if len(rows) != stride*height {
		return nil, 0, 0, fmt.Errorf("rows: %d bytes, want %d×%d", len(rows), stride, height)
	}
	return rows, bpp, stride, nil
}

type input struct {
	entry
	rows        []byte
	bpp, stride int
}

func load(e entry) (*input, error) {
	cache := ""
	if *cacheDir != "" {
		cache = filepath.Join(*cacheDir, e.sum+".rows")
		if b, err := os.ReadFile(cache); err == nil && len(b) >= 8 {
			return &input{entry: e, rows: b[8:], bpp: int(binary.LittleEndian.Uint32(b)), stride: int(binary.LittleEndian.Uint32(b[4:]))}, nil
		}
	}
	data, err := os.ReadFile(filepath.Join(*corpusDir, e.rel))
	if err != nil {
		return nil, err
	}
	if s := sha256.Sum256(data); hex.EncodeToString(s[:]) != e.sum {
		return nil, fmt.Errorf("%s: sha256 mismatch", e.rel)
	}
	rows, bpp, stride, err := rowsOf(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", e.rel, err)
	}
	if cache != "" {
		b := make([]byte, 8, 8+len(rows))
		binary.LittleEndian.PutUint32(b, uint32(bpp))
		binary.LittleEndian.PutUint32(b[4:], uint32(stride))
		if err := os.WriteFile(cache, append(b, rows...), 0o644); err != nil {
			return nil, err
		}
	}
	return &input{entry: e, rows: rows, bpp: bpp, stride: stride}, nil
}

func inflate(z []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(z))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func main() {
	flag.Parse()
	runtime.GOMAXPROCS(*procs)
	if *listEng {
		for _, e := range engines {
			fmt.Printf("%-16s %s\n", e.name, e.lib)
		}
		return
	}
	if *sumDirs != "" {
		summarize(strings.Split(*sumDirs, ","))
		return
	}
	engs, err := selectEngines(*engSpec)
	if err != nil {
		log.Fatal(err)
	}
	if *corpusDir == "" || *outDir == "" {
		log.Fatal("need -corpus and -out")
	}
	entries, err := readList(*listFile)
	if err != nil {
		log.Fatal(err)
	}
	var keep []entry
	for _, e := range entries {
		if e.index%*every != 0 {
			continue
		}
		if *only != "" && !slices.ContainsFunc(strings.Split(*only, ","), func(s string) bool { return strings.Contains(e.rel, s) }) {
			continue
		}
		keep = append(keep, e)
	}
	entries = keep
	if *shard != "" {
		var k, n int
		if _, err := fmt.Sscanf(*shard, "%d/%d", &k, &n); err != nil || n <= 0 || k < 0 || k >= n {
			log.Fatalf("bad -shard %q", *shard)
		}
		for i := range entries {
			if st, err := os.Stat(filepath.Join(*corpusDir, entries[i].rel)); err == nil {
				entries[i].size = int(st.Size())
			}
		}
		order := slices.Clone(entries)
		sort.SliceStable(order, func(i, j int) bool { return order[i].size > order[j].size })
		load := make([]int, n)
		mine := map[int]bool{}
		for _, e := range order {
			s := slices.Index(load, slices.Min(load))
			load[s] += e.size
			if s == k {
				mine[e.index] = true
			}
		}
		keep = keep[:0]
		for _, e := range entries {
			if mine[e.index] {
				keep = append(keep, e)
			}
		}
		entries = keep
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatal(err)
	}
	if *cacheDir != "" {
		if err := os.MkdirAll(*cacheDir, 0o755); err != nil {
			log.Fatal(err)
		}
	}
	t := *tag
	if t == "" {
		t = strings.Map(func(r rune) rune {
			if r == '/' || r == '*' || r == ',' {
				return '_'
			}
			return r
		}, *engSpec)
	}
	f, err := os.Create(filepath.Join(*outDir, "results-"+t+".csv"))
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	cw := csv.NewWriter(f)
	cw.Write([]string{"engine", "image", "input", "output", "bpp", "stride", "runs", "median_ns", "min_ns", "allocs_per_op", "bytes_per_op"})
	cw.Flush()

	if *cpuProf != "" {
		pf, err := os.Create(*cpuProf)
		if err != nil {
			log.Fatal(err)
		}
		if err := pprof.StartCPUProfile(pf); err != nil {
			log.Fatal(err)
		}
		defer pprof.StopCPUProfile()
	}
	var dst []byte
	for _, e := range entries {
		in, err := load(e)
		if err != nil {
			log.Fatal(err)
		}
		for _, eng := range engs {
			dst = eng.compress(dst[:0], in.rows, in.bpp, in.stride)
			got, err := inflate(dst)
			if err != nil || !bytes.Equal(got, in.rows) {
				log.Fatalf("%s on %s: output does not inflate to the input (%v)", eng.name, e.rel, err)
			}
			size := len(dst)
			if *analyzeIt {
				st, err := analyze(dst)
				if err != nil {
					log.Fatalf("%s on %s: %v", eng.name, e.rel, err)
				}
				st.print(eng.name + " " + e.rel)
				continue
			}
			var times []time.Duration
			var allocs, allocBytes float64
			if !*checkOnly {
				var before, after runtime.MemStats
				runtime.GC()
				runtime.ReadMemStats(&before)
				var total time.Duration
				for len(times) < *minRuns || (total < *minTime && len(times) < *maxRuns) {
					t0 := time.Now()
					dst = eng.compress(dst[:0], in.rows, in.bpp, in.stride)
					d := time.Since(t0)
					times = append(times, d)
					total += d
				}
				runtime.ReadMemStats(&after)
				allocs = float64(after.Mallocs-before.Mallocs) / float64(len(times))
				allocBytes = float64(after.TotalAlloc-before.TotalAlloc) / float64(len(times))
				if len(dst) != size {
					log.Fatalf("%s on %s: size changed between runs", eng.name, e.rel)
				}
			}
			slices.Sort(times)
			var med, lo time.Duration
			if len(times) > 0 {
				med, lo = times[len(times)/2], times[0]
			}
			cw.Write([]string{eng.name, e.rel, strconv.Itoa(len(in.rows)), strconv.Itoa(size), strconv.Itoa(in.bpp), strconv.Itoa(in.stride),
				strconv.Itoa(len(times)), strconv.FormatInt(int64(med), 10), strconv.FormatInt(int64(lo), 10),
				strconv.FormatFloat(allocs, 'f', 2, 64), strconv.FormatFloat(allocBytes, 'f', 0, 64)})
			cw.Flush()
		}
		fmt.Fprintf(os.Stderr, "%s done\n", e.rel)
	}
}

type row struct {
	engine, image  string
	input, output  int64
	medianNs       int64
	allocs, abytes float64
}

// summarize prints per engine the totals over the images every engine has.
func summarize(dirs []string) {
	byEngine := map[string]map[string]row{}
	for _, d := range dirs {
		files, _ := filepath.Glob(filepath.Join(d, "results-*.csv"))
		for _, file := range files {
			f, err := os.Open(file)
			if err != nil {
				log.Fatal(err)
			}
			recs, err := csv.NewReader(f).ReadAll()
			f.Close()
			if err != nil {
				log.Fatal(err)
			}
			for _, r := range recs[1:] {
				in, _ := strconv.ParseInt(r[2], 10, 64)
				out, _ := strconv.ParseInt(r[3], 10, 64)
				med, _ := strconv.ParseInt(r[7], 10, 64)
				al, _ := strconv.ParseFloat(r[9], 64)
				ab, _ := strconv.ParseFloat(r[10], 64)
				if byEngine[r[0]] == nil {
					byEngine[r[0]] = map[string]row{}
				}
				byEngine[r[0]][r[1]] = row{r[0], r[1], in, out, med, al, ab}
			}
		}
	}
	var names []string
	common := map[string]int{}
	for name, imgs := range byEngine {
		names = append(names, name)
		for img := range imgs {
			common[img]++
		}
	}
	var images []string
	for img, n := range common {
		if n == len(names) {
			images = append(images, img)
		}
	}
	sort.Strings(names)
	sort.Strings(images)
	type total struct {
		in, out, ns    int64
		allocs, abytes float64
	}
	totals := map[string]total{}
	for _, name := range names {
		var t total
		for _, img := range images {
			r := byEngine[name][img]
			t.in += r.input
			t.out += r.output
			t.ns += r.medianNs
			t.allocs += r.allocs
			t.abytes += r.abytes
		}
		totals[name] = t
	}
	ref := totals["libdeflate/l6"]
	fmt.Printf("%d images, %.1f MB of filtered rows\n\n", len(images), float64(totals[names[0]].in)/1e6)
	fmt.Println("| engine | output bytes | vs libdeflate l6 | MB/s | allocs/op | bytes/op |")
	fmt.Println("|---|---:|---:|---:|---:|---:|")
	for _, name := range names {
		t := totals[name]
		vs := "-"
		if ref.out > 0 {
			vs = fmt.Sprintf("%.4f", float64(t.out)/float64(ref.out))
		}
		speed := "-"
		if t.ns > 0 {
			speed = fmt.Sprintf("%.1f", float64(t.in)/1e6/(float64(t.ns)/1e9))
		}
		n := float64(len(images))
		fmt.Printf("| %s | %d | %s | %s | %.1f | %.0f |\n", name, t.out, vs, speed, t.allocs/n, t.abytes/n)
	}
}
