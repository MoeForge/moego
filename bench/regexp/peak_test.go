package bench

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestPeak measures one implementation and workload class in a fresh
// process. RXPEAKRATE and RXPEAKN specify a fixed rate and iteration count;
// RXPEAKCALIBRATE=1 measures the work needed to select those values.
// RXPEAKLEGACY=1 enables the unpaced, unequal-load reference method below.
// Its RXPEAKTIME and RXPEAKGCS limits let implementations do different
// amounts of work, so those results are not equal-load comparisons.
func TestPeak(t *testing.T) {
	spec := os.Getenv("RXPEAK")
	if spec == "" {
		t.Skip("set RXPEAK=<impl>/<class>")
	}
	if os.Getenv("RXPEAKLEGACY") != "1" {
		testPeakEqual(t)
		return
	}
	name, class, _ := strings.Cut(spec, "/")
	var im *impl
	for i := range impls {
		if impls[i].name == name {
			im = &impls[i]
		}
	}
	if im == nil {
		t.Fatalf("unknown implementation %q", name)
	}
	var cs []*benchCase
	for i := range cases() {
		if c := &cases()[i]; c.class == class {
			cs = append(cs, c)
		}
	}
	if len(cs) == 0 {
		t.Fatalf("unknown class %q", class)
	}
	statm, err := os.Open("/proc/self/statm")
	if err != nil {
		t.Skip("no /proc/self/statm")
	}
	defer statm.Close()
	var buf [128]byte
	rss := func() uint64 {
		n, _ := statm.ReadAt(buf[:], 0)
		f := buf[:n]
		for len(f) > 0 && f[0] != ' ' {
			f = f[1:]
		}
		var pages uint64
		for f = f[1:]; len(f) > 0 && f[0] >= '0' && f[0] <= '9'; f = f[1:] {
			pages = pages*10 + uint64(f[0]-'0')
		}
		return pages * uint64(os.Getpagesize())
	}

	var minTime time.Duration
	if v := os.Getenv("RXPEAKTIME"); v != "" {
		if minTime, err = time.ParseDuration(v); err != nil {
			t.Fatalf("RXPEAKTIME: %v", err)
		}
	}
	var minGC uint64
	if v := os.Getenv("RXPEAKGCS"); v != "" {
		if minGC, err = strconv.ParseUint(v, 10, 32); err != nil {
			t.Fatalf("RXPEAKGCS: %v", err)
		}
	}

	runtime.GC()
	debug.FreeOSMemory()
	var ms0, ms1 runtime.MemStats
	runtime.ReadMemStats(&ms0)
	heap0, rss0 := heapInUse(), rss()
	var peakHeap, peakRSS atomic.Uint64
	sample := func() {
		if h := heapInUse(); h > peakHeap.Load() {
			peakHeap.Store(h)
		}
		if r := rss(); r > peakRSS.Load() {
			peakRSS.Store(r)
		}
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(250 * time.Microsecond)
		defer tick.Stop()
		for {
			sample()
			select {
			case <-stop:
				return
			case <-tick.C:
			}
		}
	}()
	es := make([]engine, len(cs))
	for i, c := range cs {
		es[i], err = im.compile(c.pat)
		if err != nil {
			t.Fatal(err)
		}
	}
	cycles := []metrics.Sample{{Name: "/gc/cycles/automatic:gc-cycles"}}
	metrics.Read(cycles)
	gc0 := cycles[0].Value.Uint64()
	t0 := time.Now()
	rounds := 0
	for {
		rounds++
		for i, c := range cs {
			if es[i] == nil {
				continue
			}
			for _, s := range c.in {
				run(es[i], c, s)
			}
		}
		if rounds >= peakRounds && time.Since(t0) >= minTime {
			metrics.Read(cycles)
			if cycles[0].Value.Uint64()-gc0 >= minGC {
				break
			}
		}
	}
	elapsed := time.Since(t0)
	close(stop)
	<-done
	sample()
	runtime.ReadMemStats(&ms1)
	runtime.KeepAlive(es)
	fmt.Printf("PEAK %s %s heap=%d rss=%d time=%v alloc=%d gcs=%d rounds=%d heap0=%d rss0=%d gomaxprocs=%d\n", name, class,
		int64(peakHeap.Load())-int64(heap0), int64(peakRSS.Load())-int64(rss0), elapsed.Round(time.Millisecond),
		ms1.TotalAlloc-ms0.TotalAlloc, ms1.NumGC-ms0.NumGC, rounds, heap0, rss0, runtime.GOMAXPROCS(0))
}

// peakRounds is the number of times TestPeak runs each case.
const peakRounds = 20

var heapSample = []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}, {Name: "/memory/classes/heap/unused:bytes"}}

// heapInUse returns the bytes of heap spans in use: live and dead objects
// not yet swept, and the unused space in those spans.
func heapInUse() uint64 {
	metrics.Read(heapSample)
	return heapSample[0].Value.Uint64() + heapSample[1].Value.Uint64()
}
