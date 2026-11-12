package codec

import (
	"os"
	"runtime"
	"runtime/metrics"
	"syscall"
	"testing"
)

// run measures op. It first calls op once and collects the garbage, so
// that no engine's numbers hold the work of its first call (compiling
// code or plans for the types) or that call's garbage. For numbers that
// compare engines, run each row of each engine in a process of its own:
// the caches of the engines measured before, and the heap their rows
// left, make every collection of the next one slower.
func run(b *testing.B, op func() error) {
	b.ReportAllocs()
	if err := op(); err != nil {
		b.Fatal(err)
	}
	runtime.GC()
	defer gcStats(b)()
	for b.Loop() {
		if err := op(); err != nil {
			b.Fatal(err)
		}
	}
}

// gcStats returns a function that, when MOEGO_BENCH_GC is set, reports
// the garbage collections, the CPU time the collector took and the page
// faults per operation of b since gcStats was called, and the heap goal
// and the scannable heap at the end. Call it before the loop and the
// result after it.
func gcStats(b *testing.B) func() {
	if os.Getenv("MOEGO_BENCH_GC") == "" {
		return func() {}
	}
	s := []metrics.Sample{
		{Name: "/gc/cycles/total:gc-cycles"},
		{Name: "/cpu/classes/gc/total:cpu-seconds"},
		{Name: "/gc/heap/goal:bytes"},
		{Name: "/gc/scan/heap:bytes"},
	}
	var ru syscall.Rusage
	metrics.Read(s)
	syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	cycles, cpu, faults := s[0].Value.Uint64(), s[1].Value.Float64(), ru.Minflt
	return func() {
		metrics.Read(s)
		syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
		n := float64(b.N)
		b.ReportMetric(float64(s[0].Value.Uint64()-cycles)/n, "gc/op")
		b.ReportMetric((s[1].Value.Float64()-cpu)*1e9/n, "gc-ns/op")
		b.ReportMetric(float64(ru.Minflt-faults)/n, "faults/op")
		b.ReportMetric(float64(s[2].Value.Uint64())/(1<<20), "goal-MiB")
		b.ReportMetric(float64(s[3].Value.Uint64())/(1<<20), "scan-MiB")
	}
}
