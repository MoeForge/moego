package regexp

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// collectUntil runs collections until cond holds or a deadline passes.
func collectUntil(cond func() bool) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(time.Millisecond) // let finalizers run
		if cond() {
			return true
		}
	}
	return false
}

func TestEnginesReleasedWhenIdle(t *testing.T) {
	re := MustCompile(`[a-z]+@[a-z]+\.com`)
	s := "write to bob@example.com today"
	want := []int{9, 24}
	if got := re.FindStringIndex(s); !equalInts(got, want) {
		t.Fatalf("FindStringIndex = %v, want %v", got, want)
	}
	if re.c.eng.Load() == nil {
		t.Fatal("engines not built")
	}
	if !collectUntil(func() bool { return re.c.eng.Load() == nil }) {
		t.Fatal("engines still held after idle collections")
	}
	if got := re.FindStringIndex(s); !equalInts(got, want) {
		t.Fatalf("after release: FindStringIndex = %v, want %v", got, want)
	}
	if re.c.eng.Load() == nil {
		t.Fatal("engines not rebuilt")
	}
}

func TestEnginesKeptWhileUsed(t *testing.T) {
	re := MustCompile(`\w+@\w+`)
	re.MatchString("a@b")
	e := re.c.eng.Load()
	for i := 0; i < 5; i++ {
		re.MatchString("x@y")
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
	if re.c.eng.Load() != e {
		t.Fatal("engines released while in use")
	}
}

// TestEnginesConcurrentGC searches from several goroutines while
// collections release and rebuild the engines.
func TestEnginesConcurrentGC(t *testing.T) {
	pats := []string{`[a-z]+@[a-z]+\.com`, `(?i)error|warn`, `\b\d{3}-\d{4}\b`, `x[^y]*y$`}
	in := "Error: call 555-1234 or mail bob@example.com; warn x..y"
	var res []string
	for _, p := range pats {
		res = append(res, sprint(MustCompile(p).FindAllStringIndex(in, -1)))
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	var errs sync.Map
	var work sync.WaitGroup
	for g := 0; g < 4; g++ {
		work.Add(1)
		go func() {
			defer work.Done()
			res2 := make([]*Regexp, len(pats))
			for i, p := range pats {
				res2[i] = MustCompile(p)
			}
			for k := 0; k < 2000; k++ {
				i := k % len(pats)
				if got := sprint(res2[i].FindAllStringIndex(in, -1)); got != res[i] {
					errs.Store(pats[i], [2]string{got, res[i]})
				}
			}
		}()
	}
	work.Wait()
	close(stop)
	wg.Wait()
	errs.Range(func(k, v any) bool {
		t.Errorf("%s: got/want %v", k, v)
		return true
	})
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sprint(m [][]int) string {
	b := []byte{}
	for _, x := range m {
		for _, v := range x {
			b = append(b, byte('0'+v/10), byte('0'+v%10), ',')
		}
		b = append(b, ';')
	}
	return string(b)
}
