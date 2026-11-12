package ojson

import (
	"bytes"
	"errors"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
)

// countingReaderAt counts the bytes read.
type countingReaderAt struct {
	r *bytes.Reader
	n int64
}

func (c *countingReaderAt) ReadAt(b []byte, off int64) (int, error) {
	n, err := c.r.ReadAt(b, off)
	c.n += int64(n)
	return n, err
}

func smallWindow(t testing.TB, window int) {
	w := readerWindow
	readerWindow = window
	t.Cleanup(func() { readerWindow = w })
}

func randomJSON(r *rand.Rand, b *strings.Builder, depth int) {
	sp := func() {
		for r.Intn(5) == 0 {
			b.WriteString([]string{" ", "\n", "\t", "\r"}[r.Intn(4)])
		}
	}
	sp()
	kind := r.Intn(9)
	if depth <= 0 && kind >= 6 {
		kind = r.Intn(6)
	}
	switch kind {
	case 0:
		b.WriteString([]string{"null", "true", "false"}[r.Intn(3)])
	case 1:
		b.WriteString([]string{"0", "-1", "12", "1.5", "-0.25", "1e3", "2.5E-3", "123456789012", "1e999"}[r.Intn(9)])
	case 2, 3:
		b.WriteByte('"')
		seed := []string{"a", "x y", "é", `\n`, `\"`, `\u00e9`, `\\`, "{", "]", ","}[r.Intn(10)]
		for n := []int{0, 1, 3, 10, 40, 40, 40, 5000}[r.Intn(8)]; n > 0; n-- {
			b.WriteString(seed)
		}
		b.WriteByte('"')
	case 6:
		b.WriteByte('[')
		for n := r.Intn(6); n > 0; n-- {
			randomJSON(r, b, depth-1)
			sp()
			if n > 1 {
				b.WriteByte(',')
			}
		}
		b.WriteByte(']')
	default:
		names := []string{"a", "b", "c", "model", "group", "0", "1", "12", "x-y", "m_1", `mod\u0065l`, `a\"b`, "", strings.Repeat("k", 50)}
		b.WriteByte('{')
		for n := r.Intn(6); n > 0; n-- {
			sp()
			b.WriteString(`"` + names[r.Intn(len(names))] + `"`)
			sp()
			b.WriteByte(':')
			randomJSON(r, b, depth-1)
			sp()
			if n > 1 {
				b.WriteByte(',')
			}
		}
		b.WriteByte('}')
	}
}

func randomPaths(r *rand.Rand) []string {
	comps := []string{"a", "b", "c", "model", "group", "0", "1", "12", "2", "x-y", "m_1", "zz"}
	paths := make([]string, 1+r.Intn(5))
	for i := range paths {
		var p []string
		for n := 1 + r.Intn(4); n > 0; n-- {
			p = append(p, comps[r.Intn(len(comps))])
		}
		paths[i] = strings.Join(p, ".")
	}
	return paths
}

func checkReaderAt(t *testing.T, doc string, paths ...string) {
	t.Helper()
	want := GetMany(doc, paths...)
	got, err := GetManyReaderAt(bytes.NewReader([]byte(doc)), int64(len(doc)), paths...)
	if err != nil {
		t.Fatalf("GetManyReaderAt(%q, %q): %v", doc, paths, err)
	}
	if !reflect.DeepEqual(got, want) {
		for k := range got {
			if !reflect.DeepEqual(got[k], want[k]) {
				t.Fatalf("GetManyReaderAt(%q) path %q:\n got %+v\nwant %+v", doc, paths[k], got[k], want[k])
			}
		}
		t.Fatalf("GetManyReaderAt(%q, %q): lengths differ", doc, paths)
	}
}

func TestGetManyReaderAt(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for _, size := range []int{64 << 10, 16, 7, 1} {
		smallWindow(t, size)
		for i := 0; i < randomIterations(5000); i++ {
			var b strings.Builder
			randomJSON(r, &b, 4)
			checkReaderAt(t, b.String(), randomPaths(r)...)
		}
	}
	// Other paths and malformed input read the whole document.
	for _, p := range []string{"a.#", "a.*", "a.#(b=1)", "@this", "{a,b}", "a|b", `a\.b`, "a.00", "a..b", ".a"} {
		checkReaderAt(t, `{"a":[{"b":1},{"b":2}],"a.b":3,"b":"x"}`, p, "b")
	}
	for _, doc := range []string{`["a":{},"b":1]`, `[1 2]`, `{"a":1 "b":2}`, `{,"a":1}`, `[,1]`, `[1,]`, `{"a":,"b":1}`,
		`{"a":1,}`, `[:]`, `{"a"::1}`, `[1,,2]`, `{"b":[0 1],"a":2}`} {
		for _, p := range []string{"a", "b", "0", "1", "b.1"} {
			checkReaderAt(t, doc, p)
		}
	}
	for _, doc := range []string{"", " ", `{"a"`, `{"a":`, `{"a":"x`, `{"a":1,`, `[1,`, `{a:1}`, `{"a":1}}`, `]`, `}`, `{"a":[}`} {
		if _, err := GetManyReaderAt(bytes.NewReader([]byte(doc)), int64(len(doc)), "a", "a.b", "0"); err != nil {
			t.Errorf("%q: %v", doc, err)
		}
	}
}

func FuzzGetManyReaderAt(f *testing.F) {
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 64; i++ {
		var b strings.Builder
		randomJSON(r, &b, 4)
		f.Add(b.String(), strings.Join(randomPaths(r), " "), uint8(i))
	}
	f.Fuzz(func(t *testing.T, doc, paths string, sz uint8) {
		smallWindow(t, 1+int(sz))
		ps := strings.Fields(paths)
		if len(ps) == 0 || len(ps) > 8 {
			return
		}
		if Valid(doc) {
			checkReaderAt(t, doc, ps...)
			return
		}
		GetManyReaderAt(bytes.NewReader([]byte(doc)), int64(len(doc)), ps...)
	})
}

// The paths near the start need one window of a long document, and the
// results hold no more of it than they name.
func TestGetManyReaderAtBounded(t *testing.T) {
	image := strings.Repeat("A", 32<<20)
	doc := `{"model":"gpt-4o","group":"vip","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"` + image + `"}}]}],"after":{"x":1}}`
	c := &countingReaderAt{r: bytes.NewReader([]byte(doc))}
	res, err := GetManyReaderAt(c, int64(len(doc)), "model", "group")
	if err != nil || res[0].Str != "gpt-4o" || res[1].Str != "vip" {
		t.Fatalf("%v, %v", res, err)
	}
	if c.n > int64(2*readerWindow) {
		t.Errorf("read %d bytes for paths at the start", c.n)
	}
	checkReaderAt(t, doc, "model", "after.x", "messages.0.role", "missing")
	checkReaderAt(t, doc, "messages.0.content.0.image_url.url")
	checkReaderAt(t, doc, "messages")
}

func TestGetManyReaderAtErrors(t *testing.T) {
	if _, err := GetManyReaderAt(bytes.NewReader(nil), -1, "a"); err == nil {
		t.Error("no error for a negative size")
	}
	// A size past the end of the reader.
	if _, err := GetManyReaderAt(bytes.NewReader([]byte(`{"a":1}`)), 100, "a"); err != nil {
		t.Logf("past the end: %v", err)
	}
	if _, err := GetManyReaderAt(bytes.NewReader([]byte(`{"a":1}`)), 100, "a.*"); err == nil {
		t.Error("no error for a size past the end, whole read")
	}
	boom := errors.New("boom")
	if _, err := GetManyReaderAt(failingReaderAt{boom}, 100, "a"); !errors.Is(err, boom) {
		t.Errorf("error %v", err)
	}
}

type failingReaderAt struct{ err error }

func (f failingReaderAt) ReadAt(b []byte, off int64) (int, error) { return 0, f.err }

func TestValidReader(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	check := func(doc string) {
		t.Helper()
		want := Valid(doc)
		for _, rd := range []func() (bool, error){
			func() (bool, error) { return ValidReader(strings.NewReader(doc)) },
			func() (bool, error) { return ValidReader(iotest.OneByteReader(strings.NewReader(doc))) },
			func() (bool, error) { return ValidReader(iotest.HalfReader(strings.NewReader(doc))) },
			func() (bool, error) { return ValidReader(iotest.DataErrReader(strings.NewReader(doc))) },
		} {
			if got, err := rd(); got != want || err != nil {
				t.Fatalf("ValidReader(%q) = %v, %v, want %v", doc, got, err, want)
			}
		}
	}
	for _, sz := range []int{64 << 10, 8, 3} {
		smallWindow(t, sz)
		for i := 0; i < randomIterations(1500); i++ {
			var b strings.Builder
			randomJSON(r, &b, 4)
			doc := b.String()
			check(doc)
			if len(doc) > 0 {
				// A damaged copy.
				d := []byte(doc)
				d[r.Intn(len(d))] = "{}[],:\"\\ 0a\x01"[r.Intn(12)]
				check(string(d))
			}
		}
	}
	for _, doc := range []string{"", " ", "1", "1 ", " 1", "01", "-", "1.", "1e", "nul", "null", "true ", `"a`, `"a"`, `"\u12"`, `"\u123g"`, `"\q"`, "[1,]", "{\"a\":}", "[1]x", strconv.Quote(strings.Repeat("x", 100000)), strings.Repeat("[", 100000) + strings.Repeat("]", 100000), strings.Repeat("1", 300000)} {
		check(doc)
	}
	boom := errors.New("boom")
	if _, err := ValidReader(iotest.ErrReader(boom)); !errors.Is(err, boom) {
		t.Errorf("error %v", err)
	}
}

func FuzzValidReader(f *testing.F) {
	r := rand.New(rand.NewSource(4))
	for i := 0; i < 64; i++ {
		var b strings.Builder
		randomJSON(r, &b, 4)
		f.Add(b.String())
	}
	f.Fuzz(func(t *testing.T, doc string) {
		smallWindow(t, 5)
		want := Valid(doc)
		got, err := ValidReader(iotest.OneByteReader(strings.NewReader(doc)))
		if got != want || err != nil {
			t.Fatalf("ValidReader(%q) = %v, %v, want %v", doc, got, err, want)
		}
	})
}
