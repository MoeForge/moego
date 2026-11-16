package vp8l

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MoeForge/moego/internal/vp8l/vp8ltest"
)

// TestTune codes the main images of a set of WebP files again and prints
// the total size and time per level. It runs only when MOEGO_VP8L_FILES
// names a directory of .webp files (for example imgbench -save output of
// moego-webp). MOEGO_VP8L_LEVELS lists the levels (default 1,6,9);
// MOEGO_VP8L_EFFORT overrides effort fields as
// level:depth:nice:opt:lazy:relax[:cachesizes[:cacheparse[:cachefirst]]], separated by
// commas (cachesizes is a bit set of cache bits); MOEGO_VP8L_META
// overrides a level's meta plan as level:sizes(.-separated):bins:rounds:groups.
// MOEGO_VP8L_HASHBITS sets hash table bits as level:bits,...
// MOEGO_VP8L_PROCS images are coded at once (default 1, for timing).
func TestTune(t *testing.T) {
	dir := os.Getenv("MOEGO_VP8L_FILES")
	if dir == "" {
		t.Skip("MOEGO_VP8L_FILES not set")
	}
	for _, spec := range strings.Split(os.Getenv("MOEGO_VP8L_EFFORT"), ",") {
		if spec == "" {
			continue
		}
		v := atois(spec)
		ef := &efforts[v[0]]
		ef.depth, ef.nice, ef.opt, ef.lazy, ef.relax = v[1], v[2], v[3], v[4], v[5]
		if len(v) > 6 {
			ef.cache = uint16(v[6])
		}
		if len(v) > 7 {
			ef.cacheParse = v[7] != 0
		}
		if len(v) > 8 {
			ef.cacheFirst = uint16(v[8])
		}
	}
	for _, spec := range strings.Split(os.Getenv("MOEGO_VP8L_META"), ",") {
		if spec == "" {
			continue
		}
		f := strings.Split(spec, ":")
		l, _ := strconv.Atoi(f[0])
		mp := &metaPlan{}
		if f[1] != "" {
			for _, s := range strings.Split(f[1], ".") {
				b, _ := strconv.Atoi(s)
				mp.sizes = append(mp.sizes, b)
			}
		}
		mp.bins, _ = strconv.Atoi(f[2])
		mp.rounds, _ = strconv.Atoi(f[3])
		mp.groups, _ = strconv.Atoi(f[4])
		efforts[l].meta = mp
		if len(mp.sizes) == 0 {
			efforts[l].meta = nil
		}
	}
	if s := os.Getenv("MOEGO_VP8L_RELAX1"); s != "" {
		for _, f := range strings.Split(s, ",") {
			v := atois(f)
			efforts[v[0]].relax1 = v[1]
		}
	}
	if s := os.Getenv("MOEGO_VP8L_HASHBITS"); s != "" {
		for _, f := range strings.Split(s, ",") {
			v := atois(f)
			efforts[v[0]].hashBits = uint(v[1])
		}
	}
	levels := []int{1, 6, 9}
	if s := os.Getenv("MOEGO_VP8L_LEVELS"); s != "" {
		levels = atois(s)
	}
	procs := 1
	if s := os.Getenv("MOEGO_VP8L_PROCS"); s != "" {
		procs, _ = strconv.Atoi(s)
	}
	names, _ := filepath.Glob(filepath.Join(dir, "*.webp"))
	sort.Strings(names)
	type img struct {
		name string
		w, h int
		px   []uint32
		bits int
	}
	var imgs []img
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) < 20 {
			continue
		}
		n := int(binary.LittleEndian.Uint32(data[16:]))
		w, h, px, bits, err := vp8ltest.DecodeMain(data[20 : 20+n])
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		imgs = append(imgs, img{filepath.Base(name), w, h, px, bits})
	}
	orig, pixels := 0, 0
	for _, m := range imgs {
		orig += m.bits
		pixels += m.w * m.h
	}
	t.Logf("%d images, %d pixels, main images as saved: %d bytes", len(imgs), pixels, orig/8)
	per := os.Getenv("MOEGO_VP8L_PERIMAGE") != ""
	// MOEGO_VP8L_COLD=1 codes every image with a new Encoder and reports
	// the bytes allocated.
	cold := os.Getenv("MOEGO_VP8L_COLD") == "1"
	for _, l := range levels {
		listed, toks := 0, 0
		var ms0, ms1 runtime.MemStats
		runtime.ReadMemStats(&ms0)
		var mu sync.Mutex
		total, busy := 0, time.Duration(0)
		perImg := make([]string, len(imgs))
		next := 0
		var wg sync.WaitGroup
		for p := 0; p < procs; p++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				e := new(Encoder)
				for {
					mu.Lock()
					k := next
					next++
					mu.Unlock()
					if k >= len(imgs) {
						return
					}
					m := imgs[k]
					if cold {
						e = new(Encoder)
					}
					runtime.GC()
					start := time.Now()
					e.Reset(l)
					e.Header(m.w, m.h, true)
					e.WriteBits(0, 1)
					b0 := e.Bits()
					e.MainImage(m.px, m.w, m.h)
					bits := e.Bits() - b0
					el := time.Since(start)
					mu.Lock()
					listed += e.main.ml.len()
					toks += len(e.main.toks)
					total += bits
					busy += el
					perImg[k] = fmt.Sprintf("%s %d %d %.1f", m.name, m.bits/8, bits/8, float64(el.Microseconds())/1000)
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		runtime.ReadMemStats(&ms1)
		if cold {
			fmt.Printf("level %d: %.1f MB allocated\n", l, float64(ms1.TotalAlloc-ms0.TotalAlloc)/1e6)
		}
		fmt.Printf("level %d: %.3f matches listed and %.3f tokens per pixel\n", l, float64(listed)/float64(pixels), float64(toks)/float64(pixels))
		fmt.Printf("level %d: %d bytes (saved %d, %+.3f%%) %.2f MP/s (%.1f s)\n", l, total/8, orig/8,
			100*(float64(total)/float64(orig)-1), float64(pixels)/busy.Seconds()/1e6, busy.Seconds())
		if per {
			for _, s := range perImg {
				fmt.Println("  ", s)
			}
		}
	}
}

func atois(s string) []int {
	var v []int
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ':' || r == ',' }) {
		n, _ := strconv.Atoi(f)
		v = append(v, n)
	}
	return v
}
