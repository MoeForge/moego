package bench

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Equal-load samples use the same RXPEAKN class iterations and RXPEAKRATE
// iterations/second for every implementation. Set RXPEAKCALIBRATE=1 instead
// to determine an unpaced iteration count and execution time. Calibration
// results are not memory comparisons. Neither mode forces a collection.
func testPeakEqual(t *testing.T) {
	name, class, ok := strings.Cut(os.Getenv("RXPEAK"), "/")
	if !ok {
		t.Fatal("set RXPEAK=<impl>/<class>")
	}
	calibrate := os.Getenv("RXPEAKCALIBRATE") == "1"
	var rate float64
	var count int
	var err error
	if !calibrate {
		rate, err = strconv.ParseFloat(os.Getenv("RXPEAKRATE"), 64)
		if err != nil || rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
			t.Fatal("RXPEAKRATE must be finite and positive")
		}
		count, err = strconv.Atoi(os.Getenv("RXPEAKN"))
		if err != nil || count <= 0 || float64(count)/rate > 24*60*60 {
			t.Fatal("RXPEAKN must be positive and the duration at most 24 hours")
		}
	}
	cs := casesForClass(class)
	if len(cs) == 0 || len(class) != 1 {
		t.Fatalf("unknown class %q", class)
	}
	found := false
	for _, im := range impls {
		found = found || im.name == name
	}
	if !found {
		t.Fatalf("unknown implementation %q", name)
	}
	statm, err := os.Open("/proc/self/statm")
	if err != nil {
		t.Skip("RSS measurement requires /proc/self/statm")
	}
	defer statm.Close()
	var peakHeap, peakRSS atomic.Uint64
	var readError atomic.Bool
	maximum := func(v *atomic.Uint64, n uint64) {
		for old := v.Load(); n > old; old = v.Load() {
			if v.CompareAndSwap(old, n) {
				return
			}
		}
	}
	observer := func() func() (uint64, uint64) {
		samples := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}, {Name: "/memory/classes/heap/unused:bytes"}}
		buf := make([]byte, 128)
		return func() (uint64, uint64) {
			metrics.Read(samples)
			heap := samples[0].Value.Uint64() + samples[1].Value.Uint64()
			n, err := statm.ReadAt(buf, 0)
			if (err != nil && err != io.EOF) || n == 0 {
				readError.Store(true)
				return heap, 0
			}
			i := 0
			for i < n && buf[i] != ' ' {
				i++
			}
			var pages uint64
			for i++; i < n && buf[i] >= '0' && buf[i] <= '9'; i++ {
				pages = pages*10 + uint64(buf[i]-'0')
			}
			rss := pages * uint64(os.Getpagesize())
			maximum(&peakHeap, heap)
			maximum(&peakRSS, rss)
			return heap, rss
		}
	}
	observe, sample := observer(), observer()
	cycles := []metrics.Sample{{Name: "/gc/cycles/automatic:gc-cycles"}, {Name: "/gc/cycles/forced:gc-cycles"}}
	bodies := make([]func(*testing.B), len(cs))
	b := &testing.B{N: 1}
	stop, done := make(chan struct{}), make(chan struct{})
	ticker := time.NewTicker(250 * time.Microsecond)
	var once sync.Once
	stopSampler := func() { once.Do(func() { close(stop); <-done; ticker.Stop() }) }
	var before, compiled, after runtime.MemStats
	runtime.ReadMemStats(&before)
	heap0, rss0 := observe()
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				sample()
			}
		}
	}()
	defer stopSampler()
	operations := 0
	for i := range cs {
		bodies[i], err = direct(name, &cs[i])
		if err != nil {
			t.Fatal(err)
		}
		operations += len(cs[i].in)
		observe()
	}
	runtime.ReadMemStats(&compiled)
	metrics.Read(cycles)
	gc0 := cycles[0].Value.Uint64()
	start := time.Now()
	iterations, late := 0, 0
	var maxLag, workTime, maxWork time.Duration
	for calibrate || iterations < count {
		if !calibrate {
			due := start.Add(time.Duration(float64(iterations) / rate * float64(time.Second)))
			if delay := time.Until(due); delay > 0 {
				time.Sleep(delay)
			}
			lag := time.Since(due)
			maxLag = max(maxLag, lag)
			if lag.Seconds() > 1/rate {
				late++
			}
		}
		workStart := time.Now()
		for _, body := range bodies {
			body(b)
			observe()
		}
		work := time.Since(workStart)
		workTime += work
		maxWork = max(maxWork, work)
		iterations++
		if calibrate && iterations >= 20 && time.Since(start) >= time.Second {
			metrics.Read(cycles)
			if cycles[0].Value.Uint64()-gc0 >= 3 {
				break
			}
		}
	}
	if !calibrate {
		if delay := time.Until(start.Add(time.Duration(float64(count) / rate * float64(time.Second)))); delay > 0 {
			time.Sleep(delay)
		}
	}
	elapsed := time.Since(start)
	stopSampler()
	observe()
	runtime.ReadMemStats(&after)
	metrics.Read(cycles)
	gcs, forced := cycles[0].Value.Uint64()-gc0, after.NumForcedGC-before.NumForcedGC
	mode := "equal-load"
	if calibrate {
		mode = "calibration"
	}
	result := map[string]any{
		"mode": mode, "implementation": name, "class": class,
		"rate": rate, "iterations": iterations, "operations_per_iteration": operations,
		"seconds": elapsed.Seconds(), "work_seconds": workTime.Seconds(), "max_work_seconds": maxWork.Seconds(),
		"heap": peakHeap.Load(), "rss": peakRSS.Load(), "heap_base": heap0, "rss_base": rss0,
		"heap_delta": int64(peakHeap.Load()) - int64(heap0), "rss_delta": int64(peakRSS.Load()) - int64(rss0),
		"alloc": after.TotalAlloc - compiled.TotalAlloc, "alloc_with_compile": after.TotalAlloc - before.TotalAlloc,
		"bytes_per_iteration": float64(after.TotalAlloc-compiled.TotalAlloc) / float64(iterations),
		"bytes_per_op":        float64(after.TotalAlloc-compiled.TotalAlloc) / float64(iterations*operations),
		"gcs":                 gcs, "forced_gcs": forced, "gomaxprocs": runtime.GOMAXPROCS(0),
		"late_iterations": late, "max_lag_seconds": maxLag.Seconds(),
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("PEAK_EQUAL %s\n", data)
	runtime.KeepAlive(bodies)
	if readError.Load() {
		t.Error("could not sample RSS")
	}
	if forced != 0 || gcs < 3 {
		t.Errorf("need at least 3 automatic collections and no forced collections: automatic=%d forced=%d", gcs, forced)
	}
	if !calibrate && elapsed.Seconds() > float64(count)/rate*1.01+0.01 {
		t.Errorf("requested rate not sustained: %.3f iterations/s, want %.3f", float64(count)/elapsed.Seconds(), rate)
	}
}

func TestClassInputs(t *testing.T) {
	for _, class := range []string{"A", "B", "C", "D", "E", "F", "G", "H"} {
		var want []benchCase
		for _, c := range cases() {
			if c.class == class {
				want = append(want, c)
			}
		}
		if got := casesForClass(class); !reflect.DeepEqual(got, want) {
			t.Errorf("class %s inputs differ from the full suite", class)
		}
	}
}
