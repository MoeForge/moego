package diff

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/MoeForge/moego/ojson"
)

var (
	genN    = flag.Int("gen.n", 3000, "generated cases for TestGen")
	genSeed = flag.Uint64("gen.seed", 1, "seed for TestGen; case i is reproduced by (seed, i)")
	genDur  = flag.Duration("gen.dur", 0, "run TestGen for this long instead of gen.n cases")
	genOut  = flag.String("gen.out", "", "append one JSON record per mismatch bucket to this file")
	genMin  = flag.Bool("gen.min", true, "minimize the first case of each bucket")
	fuzzLog = flag.String("fuzz.log", "", "fuzz targets append mismatches here instead of failing; panics still fail")
	minIn   = flag.String("min.in", "", "TestMinLog: fuzz.log file to minimize into gen.out")
	oneCase = flag.String("one", "", `TestOne: a JSON case {"doc":..,"path":..,"sub":..,"raw":..,"val":..}`)
)

// record is one mismatch bucket: a minimized case and how often the bucket
// was hit.
type record struct {
	Bucket string `json:"bucket"`
	Area   string `json:"area"`
	Op     string `json:"op"`
	Field  string `json:"field"`
	Got    string `json:"got"`
	Want   string `json:"want"`
	Case   gcase  `json:"case"`
	Orig   *gcase `json:"orig,omitempty"`
	N      int    `json:"n"`
	Seed   uint64 `json:"seed,omitempty"`
	I      uint64 `json:"i,omitempty"`
	Src    string `json:"src,omitempty"`
}

var (
	reMod  = regexp.MustCompile(`(?:^|[.|\[{,:])@([A-Za-z]+)`)
	reLit  = regexp.MustCompile(`(?:^|[|\[{,:])!`)
	reMult = regexp.MustCompile(`(?:^|[.|])[\[{]`)
	reWild = regexp.MustCompile(`(?:^|[^\\])[*?]`)
)

func isMod(name string) bool {
	for _, m := range mods {
		if m == name {
			return true
		}
	}
	for _, m := range customMods {
		if m.name == name {
			return true
		}
	}
	return false
}

// primary names the most specific path feature, which buckets mismatches.
func primary(p, doc string) string {
	f := "plain"
	switch {
	case strings.HasPrefix(p, ".."):
		f = "lines"
	case reLit.MatchString(p):
		f = "literal"
	case reMult.MatchString(p):
		f = "multipath"
	default:
		for _, m := range reMod.FindAllStringSubmatch(p, -1) {
			if isMod(m[1]) {
				f = "@" + m[1]
				break
			}
		}
		if f != "plain" {
			break
		}
		switch {
		case strings.Contains(p, "#("):
			f = "query"
		case strings.Contains(p, "#.") || strings.Contains(p, "#|"):
			f = "#.map"
		case strings.Contains(p, "#"):
			f = "#count"
		case reWild.MatchString(p):
			f = "wildcard"
		case strings.Contains(p, "|"):
			f = "pipe"
		}
	}
	if dupKeys(doc) {
		f += "+dup"
	}
	return f
}

// dupKeys reports an object with a repeated member name.
func dupKeys(doc string) bool {
	stack := []gjson.Result{gjson.Parse(doc)}
	for n := 0; len(stack) > 0 && n < 10000; n++ {
		r := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		seen := map[string]bool{}
		dup := false
		r.ForEach(func(k, v gjson.Result) bool {
			if r.IsObject() {
				dup = dup || seen[k.Str]
				seen[k.Str] = true
			}
			if v.IsObject() || v.IsArray() {
				stack = append(stack, v)
			}
			return true
		})
		if dup {
			return true
		}
	}
	return false
}

// area files a mismatch under path (path engine, Set, Doc, Result
// metadata), api (modifiers, Result methods, API), harness (a panic in the
// harness's own checks) or deviation (a documented deviation).
func area(m mismatch, c gcase) string {
	pf := primary(c.Path, "")
	if strings.HasPrefix(m.Field, "chain.") {
		pf = primary(c.Sub, "") // Result.Get evaluates Sub
	}
	switch {
	case strings.HasPrefix(m.Field, "harness."):
		return "harness" // a panic in the harness's own checks
	case strings.HasPrefix(m.Field, "deviation.") || strings.HasPrefix(m.Field, "deviation-unverified."):
		return "deviation"
	case strings.HasPrefix(c.Path, "|") || strings.HasPrefix(c.Sub, "|") && strings.HasPrefix(m.Field, "chain."):
		return "path" // a leading separator is path syntax (pipe handling)
	case m.Op == "set" || m.Op == "setraw" || m.Op == "del" || m.Op == "docset" || m.Op == "doc" || m.Op == "setopt":
		return "path"
	case strings.HasPrefix(m.Field, "m.") || m.Op == "api":
		return "api"
	case strings.HasSuffix(m.Field, "Index") || strings.HasSuffix(m.Field, "Indexes"):
		return "path"
	case strings.HasPrefix(pf, "@"):
		p := c.Path
		if strings.HasPrefix(m.Field, "chain.") {
			p = c.Sub
		}
		if m.Op == "get" && modOutputAgrees(c.Doc, p) {
			return "path" // the modifier output matches; the walk on from it differs
		}
		return "api"
	}
	return "path"
}

// modOutputAgrees reports a path whose last top-level modifier is
// followed by more path while the result up to that modifier is the same
// in both libraries.
func modOutputAgrees(doc, p string) bool {
	cs := topComps(p)
	k := -1
	for i, c := range cs {
		if strings.HasPrefix(p[c.s:c.e], "@") {
			k = i
		}
	}
	if k < 0 || k == len(cs)-1 {
		return false
	}
	agree := false
	catch(false, func() {
		pre := p[:cs[k].e]
		agree = len(diffKV("", ocore("", ojson.Get(doc, pre)), gcore("", gjson.Get(doc, pre)))) == 0
	})
	return agree
}

// isDeviation reports a candidate deviation: sjson made malformed JSON from
// valid input. Deviations are recorded but do not fail tests.
func isDeviation(m mismatch) bool {
	return strings.HasPrefix(m.Field, "deviation.") || strings.HasPrefix(m.Field, "deviation-unverified.")
}

// bucketTag prefixes the feature of every bucket while a test runs under
// a global setting such as DisableModifiers.
var bucketTag string

func bucketOf(m mismatch, c gcase) string {
	if m.Field == "panic" && !validCase(c) {
		return "crash " + m.Op + " " + bucketTag + "malformed"
	}
	return area(m, c) + " " + m.Op + "." + m.Field + " " + bucketTag + primary(c.Path, c.Doc)
}

// collector gathers the buckets of one run.
type collector struct {
	mu   sync.Mutex
	recs map[string]*record
	keys []string
}

func newCollector() *collector { return &collector{recs: map[string]*record{}} }

// add counts the mismatches of c, minimizing the first case of each new
// bucket.
func (k *collector) add(c gcase, ms []mismatch, seed, i uint64, src string) {
	seen := map[string]bool{}
	for _, m := range ms {
		b := bucketOf(m, c)
		if seen[b] {
			continue
		}
		seen[b] = true
		k.mu.Lock()
		r := k.recs[b]
		if r != nil {
			r.N++
			k.mu.Unlock()
			continue
		}
		k.mu.Unlock()
		r = &record{Bucket: b, Op: m.Op, Field: m.Field, Got: m.Got, Want: m.Want, Case: c, N: 1, Seed: seed, I: i, Src: src}
		if *genMin {
			mc := minimize(c, m.Op, m.Field)
			if mc != c {
				orig := c
				r.Orig, r.Case = &orig, mc
				if mm := findField(mc, m.Op, m.Field); mm != nil {
					r.Got, r.Want = mm.Got, mm.Want
				}
			}
		}
		r.Area = area(m, r.Case)
		r.Bucket = bucketOf(m, r.Case) // by the minimized case
		k.mu.Lock()
		if old := k.recs[b]; old != nil {
			old.N++
		} else {
			k.recs[b] = r
			k.keys = append(k.keys, b)
		}
		k.mu.Unlock()
	}
}

func findField(c gcase, op, field string) *mismatch {
	var ms []mismatch
	if validCase(c) && !(isGetOp(op) && malformedParse(c)) {
		ms = runOp(op, c)
	} else {
		ms = crashCheck(c, true, true)
	}
	for _, m := range ms {
		if m.Op == op && m.Field == field {
			return &m
		}
	}
	return nil
}

// report logs the buckets, most frequent first, and appends them to
// gen.out.
func (k *collector) report(t *testing.T) { k.write(t, true) }

func (k *collector) write(t *testing.T, fail bool) {
	sort.SliceStable(k.keys, func(a, b int) bool { return k.recs[k.keys[a]].N > k.recs[k.keys[b]].N })
	var out *bufio.Writer
	if *genOut != "" {
		f, err := os.OpenFile(*genOut, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		out = bufio.NewWriter(f)
		defer out.Flush()
	}
	for _, b := range k.keys {
		r := k.recs[b]
		if fail && r.Area != "deviation" {
			t.Errorf("%6d  %s\n        case %s\n        got  %s\n        want %s", r.N, b, caseJSON(r.Case), clip(r.Got), clip(r.Want))
		}
		if out != nil {
			out.WriteString(recJSON(r) + "\n")
		}
	}
}

func clip(s string) string {
	if len(s) > 600 {
		return s[:600] + "..."
	}
	return s
}

func caseJSON(c gcase) string {
	var b strings.Builder
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.Encode(c)
	return strings.TrimSpace(b.String())
}

func recJSON(r *record) string {
	var b strings.Builder
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.Encode(r)
	return strings.TrimSpace(b.String())
}

// TestGen compares ojson with gjson and sjson on generated documents and
// paths. Every bucket of differences is reported once, minimized.
//
//	go test -run TestGen -args -gen.n=100000 -gen.seed=7 -gen.out=/tmp/m.jsonl
func TestGen(t *testing.T) {
	k := newCollector()
	start := time.Now()
	var cases, oracle, malformed int
	for i := uint64(0); ; i++ {
		if *genDur > 0 {
			if i%64 == 0 && time.Since(start) > *genDur {
				break
			}
		} else if i >= uint64(*genN) {
			break
		}
		c := newGen(*genSeed, i).gcase()
		cases++
		if !validCase(c) {
			malformed++
		}
		ms := runCase(c)
		if len(ms) == 1 && ms[0] == errOracle {
			// The reference panics; ojson still must not.
			oracle++
			if oracle <= 3 {
				t.Logf("reference panics on case %d: %s", i, caseJSON(c))
			}
			ms = crashCheck(c, true, true)
		}
		k.add(c, ms, *genSeed, i, "gen")
	}
	t.Logf("%d cases (%d malformed, %d skipped on reference panics), %d buckets, %v", cases, malformed, oracle, len(k.keys), time.Since(start).Round(time.Millisecond))
	k.report(t)
}

// TestOne runs one case and prints every difference.
//
//	go test -run TestOne -args -one='{"doc":"[1,2]","path":"#(==2)"}'
func TestOne(t *testing.T) {
	if *oneCase == "" {
		t.Skip("no -one case")
	}
	var c gcase
	if err := json.Unmarshal([]byte(*oneCase), &c); err != nil {
		t.Fatal(err)
	}
	t.Logf("valid=%v gjson.Get: %s", validCase(c), gdesc(gjson.Get(c.Doc, c.Path)))
	for _, m := range runCase(c) {
		t.Errorf("%s %s.%s\n got  %s\n want %s", area(m, c), m.Op, m.Field, m.Got, m.Want)
	}
}

// regressFile holds minimized cases found by the generator and the
// fuzzers, one record per line. Fixed cases stay as regressions.
const regressFile = "testdata/regress.jsonl"

func readRecords(name string) ([]record, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rs []record
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<24)
	for sc.Scan() {
		if len(strings.TrimSpace(sc.Text())) == 0 {
			continue
		}
		var r record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("%s: %v", name, err)
		}
		rs = append(rs, r)
	}
	return rs, sc.Err()
}

// TestRegress replays the retained cases. Subtests are named
// area/bucket, so -run 'TestRegress/path/' selects the path-engine cases.
func TestRegress(t *testing.T) {
	rs, err := readRecords(regressFile)
	if os.IsNotExist(err) {
		t.Skip("no " + regressFile)
	}
	if err != nil {
		t.Fatal(err)
	}
	k := newCollector()
	for i, r := range rs {
		_, rest, _ := strings.Cut(r.Bucket, " ")
		name := fmt.Sprintf("%s/%s/%d", r.Area, strings.ReplaceAll(rest, " ", "_"), i)
		t.Run(name, func(t *testing.T) {
			ms := runCase(r.Case)
			if len(ms) == 1 && ms[0] == errOracle {
				return
			}
			for _, m := range ms {
				if !isDeviation(m) {
					t.Errorf("%s.%s case %s\n got  %s\n want %s", m.Op, m.Field, caseJSON(r.Case), clip(m.Got), clip(m.Want))
				}
			}
			k.add(r.Case, ms, 0, uint64(i), "regress")
		})
	}
	// With gen.out, the cases that still fail join the mismatch report.
	k.write(t, false)
}

// TestMinLog minimizes the cases a fuzz.log collected and appends the
// buckets to gen.out.
func TestMinLog(t *testing.T) {
	if *minIn == "" {
		t.Skip("no -min.in")
	}
	rs, err := readRecords(*minIn)
	if err != nil {
		t.Fatal(err)
	}
	k := newCollector()
	for _, r := range rs {
		ms := runCase(r.Case)
		if len(ms) == 1 && ms[0] == errOracle {
			continue
		}
		k.add(r.Case, ms, 0, 0, r.Src)
	}
	k.report(t)
}

// fuzz logging: the first few cases of each bucket per process.
var (
	logMu   sync.Mutex
	logSeen = map[string]int{}
)

func fuzzCheck(t *testing.T, c gcase, get, set bool, src string) {
	if len(c.Doc) > 4096 || len(c.Path) > 512 || len(c.Sub) > 512 {
		return
	}
	for _, m := range runSome(c, get, set) {
		if m == errOracle {
			return
		}
		if isDeviation(m) && *fuzzLog == "" {
			continue
		}
		if *fuzzLog == "" || m.Field == "panic" || m.Field == "unbounded" {
			t.Fatalf("%s %s.%s case %s\n got  %s\n want %s", area(m, c), m.Op, m.Field, caseJSON(c), m.Got, m.Want)
		}
		b := bucketOf(m, c)
		logMu.Lock()
		n := logSeen[b]
		logSeen[b] = n + 1
		logMu.Unlock()
		if n >= 3 {
			continue
		}
		r := &record{Bucket: b, Op: m.Op, Field: m.Field, Got: clip(m.Got), Want: clip(m.Want), Case: c, N: 1, Src: src}
		f, err := os.OpenFile(*fuzzLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(recJSON(r) + "\n")
		f.Close()
	}
}

// seedCases are the fuzz seeds: examples, retained regressions and
// generated cases.
func seedCases() []gcase {
	var cs []gcase
	for _, e := range examples {
		cs = append(cs, gcase{Doc: e.doc, Path: e.path, Raw: `7`})
	}
	if rs, err := readRecords(regressFile); err == nil {
		for _, r := range rs {
			cs = append(cs, r.Case)
		}
	}
	for i := range uint64(300) {
		cs = append(cs, newGen(99, i).gcase())
	}
	return cs
}

// FuzzGet2 compares the Get operations on any document and path; on
// malformed input only crashes count.
//
//	go test -run '^$' -fuzz FuzzGet2 -fuzztime 1h -args -fuzz.log=/tmp/fg.jsonl
func FuzzGet2(f *testing.F) {
	for _, c := range seedCases() {
		f.Add(c.Doc, c.Path, c.Sub)
	}
	f.Fuzz(func(t *testing.T, doc, path, sub string) {
		fuzzCheck(t, gcase{Doc: doc, Path: path, Sub: sub, Raw: `7`}, true, false, "FuzzGet2")
	})
}

// FuzzSet2 compares Set, SetRaw, Delete and Doc edits.
func FuzzSet2(f *testing.F) {
	for _, c := range seedCases() {
		f.Add(c.Doc, c.Path, c.Raw, uint8(c.Val))
	}
	f.Fuzz(func(t *testing.T, doc, path, raw string, val uint8) {
		if len(raw) > 256 {
			return
		}
		fuzzCheck(t, gcase{Doc: doc, Path: path, Raw: raw, Val: int(val)}, false, true, "FuzzSet2")
	})
}
