package regexp

import (
	"fmt"
	"sync"
	"testing"
)

func TestCacheShares(t *testing.T) {
	defer SetCacheSize(SetCacheSize(DefaultCacheSize))
	p := `cache-shares-[a-z]+[0-9]`
	a := MustCompile(p)
	b := MustCompile(p) // admitted
	c := MustCompile(p) // from the cache
	if b.c != c.c {
		t.Fatal("third compile did not reuse the cached expression")
	}
	if a == c || b == c {
		t.Fatal("Compile returned the same *Regexp twice")
	}
	c.Longest()
	if b.longest {
		t.Fatal("Longest on one Regexp changed another")
	}
	if x := MustCompilePOSIX(p); x.c == c.c {
		t.Fatal("POSIX compile reused a Perl-syntax expression")
	}
	if got := c.FindString("x cache-shares-ab1"); got != "cache-shares-ab1" {
		t.Fatalf("FindString = %q", got)
	}
}

func TestCacheOff(t *testing.T) {
	defer SetCacheSize(SetCacheSize(0))
	p := `cache-off-\w+`
	a, b, c := MustCompile(p), MustCompile(p), MustCompile(p)
	if a.c == b.c || b.c == c.c {
		t.Fatal("expressions shared with the cache off")
	}
}

func TestCacheBound(t *testing.T) {
	defer SetCacheSize(SetCacheSize(8))
	for i := 0; i < 200; i++ {
		p := fmt.Sprintf(`bound-%d-\d+`, i)
		MustCompile(p)
		MustCompile(p)
	}
	if n := cache.n.Load(); n > 8 {
		t.Fatalf("cache holds %d entries, limit 8", n)
	}
	n := 0
	for i := range cache.shards {
		n += len(cache.shards[i].ents)
	}
	if int64(n) != cache.n.Load() {
		t.Fatalf("shards hold %d entries, count says %d", n, cache.n.Load())
	}
}

func TestCacheErrors(t *testing.T) {
	for i := 0; i < 3; i++ {
		if _, err := Compile(`a(b`); err == nil || err.Error() != "error parsing regexp: missing closing ): `a(b`" {
			t.Fatalf("Compile(a(b) err = %v", err)
		}
	}
}

func TestCacheConcurrent(t *testing.T) {
	defer SetCacheSize(SetCacheSize(16))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				k := (i*7 + g) % 40
				re := MustCompile(fmt.Sprintf(`conc-%d-(\d+)`, k))
				in := fmt.Sprintf("x conc-%d-%d y", k, i)
				if m := re.FindStringSubmatch(in); m == nil || m[1] != fmt.Sprint(i) {
					t.Errorf("%q on %q = %q", re, in, m)
					return
				}
				if i%500 == 0 && g == 0 {
					SetCacheSize(16)
				}
			}
		}(g)
	}
	wg.Wait()
}
