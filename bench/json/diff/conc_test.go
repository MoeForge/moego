package diff

import (
	"fmt"
	"sync"
	"testing"

	"github.com/MoeForge/moego/ojson"
)

// TestConcurrent runs generated cases from several goroutines at once, so
// the compiled-path cache and the modifier registry are used concurrently,
// while one goroutine registers and looks up modifiers. Run it with -race.
// Every result must equal the one computed before the goroutines started.
func TestConcurrent(t *testing.T) {
	var cases []gcase
	var want []string
	for i := range uint64(600) {
		c := newGen(21, i).gcase()
		if !validCase(c) || hugeIndex(c.Path) {
			continue
		}
		cases = append(cases, c)
		want = append(want, concDesc(c))
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := range 3 {
				if g == 0 {
					name := fmt.Sprintf("conc%d", round)
					ojson.AddModifier(name, func(j, a string) string { return j })
					if !ojson.ModifierExists(name, nil) {
						t.Errorf("ModifierExists(%q) after AddModifier is false", name)
					}
				}
				for i := range cases {
					k := (i + g*97) % len(cases)
					if got := concDesc(cases[k]); got != want[k] {
						t.Errorf("goroutine %d: case %s\n got  %s\n want %s", g, caseJSON(cases[k]), got, want[k])
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}

// concDesc describes Get, GetMany and a Doc edit of c.
func concDesc(c gcase) string {
	s := odesc(ojson.Get(c.Doc, c.Path))
	for _, r := range ojson.GetMany(c.Doc, manyPaths(c)...) {
		s += "|" + odesc(r)
	}
	d := ojson.NewDocString(c.Doc)
	err := d.SetRaw(c.Path, c.Raw)
	return s + "|" + errText(err) + "|" + d.String() + "|" + odesc(d.Get(c.Sub))
}
