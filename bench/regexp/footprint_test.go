package bench

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	moego "github.com/MoeForge/moego/regexp"
)

// retained returns the live heap after collections, with time for
// finalizers between them, so pools and caches that the implementations
// release at GC are gone. Every implementation is measured the same way.
func retained() uint64 {
	var m runtime.MemStats
	for i := 0; i < 4; i++ {
		runtime.GC()
		time.Sleep(2 * time.Millisecond)
	}
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// TestFootprint reports, per case, the memory each compiled and warmed
// pattern retains after GC (RXFOOT=1).
func TestFootprint(t *testing.T) {
	if os.Getenv("RXFOOT") == "" {
		t.Skip("set RXFOOT=1")
	}
	for i := range cases() {
		c := &cases()[i]
		line := fmt.Sprintf("%-28s", c.class+"/"+c.name)
		for _, im := range impls {
			keep := make([]engine, 64)
			h0 := retained()
			for k := range keep {
				e, err := im.compile(c.pat)
				if err != nil {
					continue
				}
				for _, s := range c.in {
					run(e, c, s)
				}
				keep[k] = e
			}
			h1 := retained()
			line += fmt.Sprintf(" %s=%d", im.name, (int64(h1)-int64(h0))/int64(len(keep)))
			runtime.KeepAlive(keep)
		}
		fmt.Println(line)
	}
}

// rssKiB returns the resident set of the process.
func rssKiB() int64 {
	b, _ := os.ReadFile("/proc/self/status")
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) > 1 && f[0] == "VmRSS:" {
			n, _ := strconv.ParseInt(f[1], 10, 64)
			return n
		}
	}
	return -1
}

// TestCompileOnce reports the first compile of the sensitive-filter pattern
// in a fresh process by the implementation that RXCOMPILEONCE names, with
// moego's compile cache off: its time, the bytes it allocated, the heap
// the compiled pattern keeps after GC, and the growth of the resident set.
func TestCompileOnce(t *testing.T) {
	name := os.Getenv("RXCOMPILEONCE")
	if name == "" {
		t.Skip("set RXCOMPILEONCE to an implementation")
	}
	defer moego.SetCacheSize(moego.SetCacheSize(0))
	pat := SensitivePattern(SensitiveWords(1000, 10))
	for _, im := range impls {
		if im.name != name {
			continue
		}
		var m0, m1 runtime.MemStats
		h0, rss0 := retained(), rssKiB()
		runtime.ReadMemStats(&m0)
		start := time.Now()
		e, err := im.compile(pat)
		elapsed := time.Since(start)
		runtime.ReadMemStats(&m1)
		if err != nil {
			t.Fatal(err)
		}
		h1, rss1 := retained(), rssKiB()
		fmt.Printf("COMPILE_ONCE %s ns=%d alloc=%d retained=%d rss_kib=%d\n", name, elapsed.Nanoseconds(),
			m1.TotalAlloc-m0.TotalAlloc, int64(h1)-int64(h0), rss1-rss0)
		runtime.KeepAlive(e)
		return
	}
	t.Fatalf("unknown implementation %q", name)
}
