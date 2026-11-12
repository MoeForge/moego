//go:build go1.26

package json

import (
	"bytes"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// These tests read values past the window of the Decoder from readers that
// can seek, and compare the results with encoding/json.

type largeItem struct {
	Name string   `json:"name"`
	Text string   `json:"text,omitempty"`
	N    int      `json:"n"`
	F    float64  `json:"f"`
	Tags []string `json:"tags,omitempty"`
}

type largeDoc struct {
	Model string                        `json:"model"`
	Items []largeItem                   `json:"items"`
	Raw   stdjson.RawMessage            `json:"raw"`
	Any   any                           `json:"any"`
	M     map[string]any                `json:"m"`
	MS    map[string]string             `json:"ms"`
	MR    map[string]stdjson.RawMessage `json:"mr"`
	MI    map[int]string                `json:"mi"`
	MT    map[textKey]int               `json:"mt"`
	Up    upper                         `json:"up"`
	B     []byte                        `json:"b"`
	Num   stdjson.Number                `json:"num"`
	Q     int                           `json:"q,string"`
	Ints  []int                         `json:"ints"`
	Arr   [2]string                     `json:"arr"`
	Deep  [][]largeItem                 `json:"deep"`
	P     *largeItem                    `json:"p"`
	S     string                        `json:"s"`
	After string                        `json:"after"`
}

func longText(n int, seed string) string {
	return strings.Repeat(seed, n/len(seed)+1)[:n]
}

// largeDocs returns values of hundreds of KiB with long strings in each
// place one can be, and containers past the window.
func largeDocs() map[string]string {
	long := longText(300<<10, "abcdefghij")
	esc := strings.Repeat(`line\n\"quoted\"\té \u00e9\ud83d\ude00\ud800 `, 6000)
	var items strings.Builder
	items.WriteByte('[')
	for i := 0; i < 8000; i++ {
		if i > 0 {
			items.WriteByte(',')
		}
		fmt.Fprintf(&items, `{"name":"item-%d","n":%d,"f":%d.25,"tags":["a","b"],"unknown":{"x":[1,2,3]}}`, i, i, i)
	}
	items.WriteByte(']')
	var ints strings.Builder
	ints.WriteByte('[')
	for i := 0; i < 100000; i++ {
		if i > 0 {
			ints.WriteByte(',')
		}
		ints.WriteString(strconv.Itoa(i))
	}
	ints.WriteByte(']')
	strs := `["` + long + `","` + long + `","x"]`
	return map[string]string{
		"string":    `{"model":"` + long + `","after":"z"}`,
		"strings":   `{"model":"m","items":[{"name":"` + long + `","text":"` + esc + `"},{"name":"b"}],"after":"z"}`,
		"escaped":   `{"s":"` + esc + `","after":"z"}`,
		"utf8":      `{"s":"` + longText(100<<10, "héllo wörld ") + `","after":"z"}`,
		"badutf8":   "{\"s\":\"" + longText(100<<10, "abc\xff") + `","after":"z"}`,
		"items":     `{"items":` + items.String() + `,"after":"z"}`,
		"ints":      `{"ints":` + ints.String() + `,"after":"z"}`,
		"raw":       `{"raw":{"a":"` + long + `","b":[1,2,3]},"after":"z"}`,
		"rawstring": `{"raw":"` + long + `","after":"z"}`,
		"rawitems":  `{"raw":` + items.String() + `,"after":"z"}`,
		"any":       `{"any":{"a":"` + long + `","b":` + items.String() + `},"after":"z"}`,
		"anystring": `{"any":"` + long + `","after":"z"}`,
		"map":       `{"m":{"` + long + `":1,"b":"` + long + `"},"ms":{"x":"` + long + `"},"after":"z"}`,
		"mapraw":    `{"mr":{"x":"` + long + `","y":` + items.String() + `,"z":1},"after":"z"}`,
		"mapint":    `{"mi":{"1":"` + long + `","2":"b"},"after":"z"}`,
		"mapbad":    `{"mi":{"` + longText(40<<10, "9") + `":"a","x":"` + long + `"},"after":"z"}`,
		"mapkeys":   `{"m":` + strings.Replace(items.String(), "[", `{"k":[`, 1) + `},"after":"z"}`,
		"maptext":   `{"mt":{"` + longText(50<<10, "x") + `-b":1,"c-d":2},"after":"z"}`,
		"method":    `{"up":"` + long + `","after":"z"}`,
		"bytes":     `{"b":"` + longText(200<<10, "QUJD") + `","after":"z"}`,
		"numstring": `{"num":"` + longText(40<<10, "1") + `","after":"z"}`,
		"numbad":    `{"num":"` + longText(40<<10, "x") + `","after":"z"}`,
		"quoted":    `{"q":"` + longText(40<<10, "1") + `","after":"z"}`,
		"ignored":   `{"nope":"` + long + `","other":` + items.String() + `,"after":"z"}`,
		"arrayover": `{"arr":["a","b","` + long + `",` + items.String() + `],"after":"z"}`,
		"deep":      `{"deep":[` + items.String() + `,` + items.String() + `],"after":"z"}`,
		"ptr":       `{"p":{"name":"` + long + `"},"after":"z"}`,
		"typeerr":   `{"model":` + items.String() + `,"ints":"` + long + `","s":12,"after":"z"}`,
		"typeerr2":  `{"items":{"a":"` + long + `"},"model":"` + long + `","q":1,"n":1,"s":["x"],"after":12}`,
		"array":     strs,
		"top":       items.String(),
		"spaced":    strings.ReplaceAll(items.String(), ",", " ,\n  "),
		"longws":    `{"model":"m"` + strings.Repeat(" ", 40<<10) + `,"after":"z"}` + strings.Repeat(" ", 200<<10),
	}
}

// largeFile is a reader that can seek and shows when it was used so.
type largeFile struct {
	*bytes.Reader
	seeks int
}

func (f *largeFile) Seek(off int64, whence int) (int64, error) {
	f.seeks++
	return f.Reader.Seek(off, whence)
}

func checkLargeType[T any](t *testing.T, name, doc string) {
	t.Helper()
	input := "  " + doc + "\n  [1,2] {\"after\":\"second\"} 7 \"x\""
	f := &largeFile{Reader: bytes.NewReader([]byte(input))}
	gd := NewDecoder(f)
	wd := stdjson.NewDecoder(strings.NewReader(input))
	for k := 0; k < 5; k++ {
		var g, w T
		gerr, werr := gd.Decode(&g), wd.Decode(&w)
		if !sameError(gerr, werr) {
			t.Fatalf("%s value %d: errors differ:\n got %v\nwant %v", name, k, clipErr(gerr), clipErr(werr))
		}
		if !reflect.DeepEqual(g, w) {
			t.Fatalf("%s value %d: values differ", name, k)
		}
		if a, b := gd.InputOffset(), wd.InputOffset(); a != b {
			t.Fatalf("%s value %d: InputOffset %d, want %d", name, k, a, b)
		}
	}
	var g, w any
	gerr, werr := gd.Decode(&g), wd.Decode(&w)
	if gerr != io.EOF || werr != io.EOF {
		t.Fatalf("%s: at the end: %v, %v", name, gerr, werr)
	}
	if len(doc) > 1<<19 && f.seeks == 0 {
		t.Errorf("%s: no seek for a value of %d bytes", name, len(doc))
	}
}

func clipErr(err error) string {
	if err == nil {
		return "<nil>"
	}
	s := err.Error()
	if len(s) > 400 {
		s = s[:400] + "..."
	}
	return s
}

func TestDecoderLarge(t *testing.T) {
	for name, doc := range largeDocs() {
		t.Run(name, func(t *testing.T) {
			checkLargeType[largeDoc](t, name, doc)
			checkLargeType[any](t, name, doc)
			checkLargeType[stdjson.RawMessage](t, name, doc)
			if raceEnabled {
				// The other targets read the same values again.
				return
			}
			checkLargeType[map[string]any](t, name, doc)
			checkLargeType[map[string]stdjson.RawMessage](t, name, doc)
			checkLargeType[[]largeItem](t, name, doc)
			checkLargeType[struct{ After string }](t, name, doc)
		})
	}
}

// smallWindow makes every value but the shortest large for the test.
func smallWindow(t testing.TB) {
	w, tr, l, m := streamWindow, streamTrigger, streamLong, streamMargin
	streamWindow, streamTrigger, streamLong, streamMargin = 256, 8, 16, 5*16+64
	t.Cleanup(func() { streamWindow, streamTrigger, streamLong, streamMargin = w, tr, l, m })
}

// randomDoc writes a random value with the member names of largeDoc.
func randomDoc(r *rand.Rand, b *strings.Builder, depth int) {
	sp := func() {
		for r.Intn(6) == 0 {
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
		b.WriteString([]string{"0", "-1", "12", "1.5", "-0.25", "1e3", "2.5E-3", "123456789012", "1e999", "0.000001"}[r.Intn(10)])
	case 2, 3, 4, 5:
		b.WriteByte('"')
		seed := []string{"a", "x y", "<é>", "\\n", "\\u00e9", "\\ud83d\\ude00", "\\ud800", "\xff", "12", "QUJD"}[r.Intn(10)]
		for n := []int{0, 1, 3, 10, 20, 60}[r.Intn(6)]; n > 0; n-- {
			b.WriteString(seed)
		}
		b.WriteByte('"')
	case 6:
		b.WriteByte('[')
		for n := r.Intn(8); n > 0; n-- {
			randomDoc(r, b, depth-1)
			sp()
			if n > 1 {
				b.WriteByte(',')
			}
		}
		b.WriteByte(']')
	default:
		names := []string{"model", "items", "raw", "any", "m", "ms", "mr", "mi", "mt", "up", "b", "num", "q", "ints", "arr", "deep", "p", "s",
			"after", "name", "text", "n", "f", "tags", "1", "2", "a-b", "other", "long member name of more than sixteen bytes"}
		b.WriteByte('{')
		for n := r.Intn(8); n > 0; n-- {
			sp()
			b.WriteString(strconv.Quote(names[r.Intn(len(names))]))
			sp()
			b.WriteByte(':')
			randomDoc(r, b, depth-1)
			sp()
			if n > 1 {
				b.WriteByte(',')
			}
		}
		b.WriteByte('}')
	}
}

func checkSmallWindow(t *testing.T, doc string) {
	t.Helper()
	input := doc + " " + doc
	targets := []func() any{
		func() any { return new(largeDoc) }, func() any { return new(any) }, func() any { return new(map[string]any) },
		func() any { return new(map[string]largeDoc) }, func() any { return new([]largeDoc) }, func() any { return new(stdjson.RawMessage) },
		func() any { return new(map[int]string) }, func() any { return new(map[textKey]any) }, func() any { return new([]string) },
		func() any { return new([][]int) }, func() any { return new([3]any) }, func() any { return new(upper) },
	}
	for _, mk := range targets {
		for _, useNumber := range []bool{false, true} {
			f := &chunkFile{Reader: bytes.NewReader([]byte(input))}
			gd, wd := NewDecoder(f), stdjson.NewDecoder(chunkReader{strings.NewReader(input)})
			if useNumber {
				gd.UseNumber()
				wd.UseNumber()
			}
			// Each value takes at least one byte, and the errors that end
			// the stream repeat.
			for range len(input) + 2 {
				g, w := mk(), mk()
				gerr, werr := gd.Decode(g), wd.Decode(w)
				if !sameError(gerr, werr) && !sameCutError(gerr, werr) {
					t.Fatalf("Decode(%T) of %q:\n got %v\nwant %v", g, doc, gerr, werr)
				}
				if werr != nil && (errors.Is(werr, io.EOF) || errors.Is(werr, io.ErrUnexpectedEOF) || errors.As(werr, new(*stdjson.SyntaxError))) {
					break
				}
				if !reflect.DeepEqual(g, w) {
					t.Fatalf("Decode(%T) of %q:\n got %#v\nwant %#v", g, doc, g, w)
				}
				if a, b := gd.InputOffset(), wd.InputOffset(); a != b {
					t.Fatalf("Decode(%T) of %q: InputOffset %d, want %d", g, doc, a, b)
				}
			}
			smallWindowSeeks += f.seeks
		}
	}
}

var smallWindowSeeks int

// sameCutError reports whether a and b are the same syntax error but for
// how much of what is wrong each quotes. After a value read in two passes
// the reads fall elsewhere than those of encoding/json, which quotes a
// character or an escape from what it has read: the one may quote a
// character whole where the other has its first byte only, or quote more
// of an escape. They start at the same offset.
func sameCutError(a, b error) bool {
	var sa, sb *SyntaxError
	if !errors.As(a, &sa) || !errors.As(b, &sb) {
		return false
	}
	qa, ra, oka := cutQuote(sa.Error())
	qb, rb, okb := cutQuote(sb.Error())
	if !oka || !okb || ra != rb || qa == qb {
		return false
	}
	if len(qb) < len(qa) {
		qa, qb, sa, sb = qb, qa, sb, sa
	}
	if !strings.HasPrefix(qb, qa) {
		return false
	}
	if !go127 {
		return sa.Offset == sb.Offset
	}
	// From Go 1.27 the offset is after what is quoted.
	return sa.Offset-int64(len(qa)) == sb.Offset-int64(len(qb))
}

// cutQuote returns the bytes a syntax error quotes from the input, for a
// character or an escape, and the rest of its message.
func cutQuote(msg string) (q, rest string, ok bool) {
	if m, ok := strings.CutPrefix(msg, "invalid character '"); ok {
		i := strings.Index(m, "' ")
		if i < 0 {
			return "", "", false
		}
		c, rest := m[:i], m[i:]
		if b, ok := strings.CutPrefix(c, `\x`); ok && len(b) == 2 {
			n, err := strconv.ParseUint(b, 16, 8)
			return string([]byte{byte(n)}), rest, err == nil
		}
		r, err := strconv.Unquote("'" + c + "'")
		return string(r), rest, err == nil && len(string(r)) > 1
	}
	if m, ok := strings.CutPrefix(msg, "invalid escape sequence "); ok {
		if m, ok := strings.CutSuffix(m, " in string"); ok {
			seq, err := strconv.Unquote(m)
			return seq, "", err == nil
		}
	}
	return "", "", false
}

// chunkFile returns a few bytes from each Read, so that values are seen to
// run past what is buffered.
type chunkFile struct {
	*bytes.Reader
	seeks int
}

func (f *chunkFile) Read(b []byte) (int, error) { return f.Reader.Read(b[:min(len(b), 9)]) }

// chunkReader reads as chunkFile does, for encoding/json to have as much
// of the input at hand as the Decoder: on encoding/json/v2, an invalid
// escape quotes the bytes at hand.
type chunkReader struct{ io.Reader }

func (r chunkReader) Read(b []byte) (int, error) { return r.Reader.Read(b[:min(len(b), 9)]) }

func (f *chunkFile) Seek(off int64, whence int) (int64, error) {
	f.seeks++
	return f.Reader.Seek(off, whence)
}

func TestDecoderSmallWindow(t *testing.T) {
	smallWindow(t)
	// A syntax error after a value read in two passes.
	checkSmallWindow(t, `"00000A`)
	// An input that ends inside a value.
	checkSmallWindow(t, `[[`)
	r := rand.New(rand.NewSource(1))
	n := randomIterations(3000)
	for i := 0; i < n; i++ {
		var b strings.Builder
		randomDoc(r, &b, 4)
		checkSmallWindow(t, b.String())
	}
	if smallWindowSeeks < n/3 {
		t.Errorf("only %d seeks: values were not read in two passes", smallWindowSeeks)
	}
}

func FuzzDecoderSmallWindow(f *testing.F) {
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 100; i++ {
		var b strings.Builder
		randomDoc(r, &b, 4)
		f.Add(b.String())
	}
	f.Fuzz(func(t *testing.T, doc string) {
		smallWindow(t)
		checkSmallWindow(t, doc)
	})
}

func TestDecoderLargeInvalid(t *testing.T) {
	long := longText(300<<10, "abcdefghij")
	for _, doc := range []string{
		`{"model":"` + long + `","after":}`,
		`{"model":"` + long + `"`,
		`{"model":"` + long,
		`{"model":"` + long + "\x01" + `"}`,
		`{"model":"` + long + `\q"}`,
		`[` + strings.Repeat(`1,`, 200<<10) + `]`,
		strings.Repeat(`[`, 10001) + long,
		`[` + strings.Repeat(` `, 200<<10) + `x]`,
		`[01,` + long + `]`,
	} {
		gd := NewDecoder(bytes.NewReader([]byte(doc)))
		wd := stdjson.NewDecoder(strings.NewReader(doc))
		var g, w any
		gerr, werr := gd.Decode(&g), wd.Decode(&w)
		if !sameError(gerr, werr) || gerr == nil {
			t.Errorf("%.40q: error %v, want %v", doc, clipErr(gerr), clipErr(werr))
		}
		if g != nil {
			t.Errorf("%.40q: value set", doc)
		}
		if gerr, werr = gd.Decode(&g), wd.Decode(&w); !sameError(gerr, werr) {
			t.Errorf("%.40q: next error %v, want %v", doc, clipErr(gerr), clipErr(werr))
		}
	}
}

// changingFile turns into other bytes after the first pass.
type changingFile struct {
	*bytes.Reader
	other []byte
	seeks int
}

func (f *changingFile) Seek(off int64, whence int) (int64, error) {
	if off != 0 || whence != io.SeekCurrent {
		f.seeks++
		if f.seeks == 1 {
			pos, _ := f.Reader.Seek(0, io.SeekCurrent)
			f.Reader = bytes.NewReader(f.other)
			f.Reader.Seek(pos, io.SeekStart)
		}
	}
	return f.Reader.Seek(off, whence)
}

func TestDecoderLargeChanged(t *testing.T) {
	long := longText(300<<10, "abcdefghij")
	var items strings.Builder
	items.WriteByte('[')
	for i := 0; i < 30000; i++ {
		if i > 0 {
			items.WriteByte(',')
		}
		fmt.Fprintf(&items, `{"name":"item-%d","n":%d}`, i, i)
	}
	items.WriteByte(']')
	doc := `{"model":"` + long + `","items":` + items.String() + `,"raw":` + items.String() + `,"nope":` + items.String() + `}`
	changes := [][]byte{
		[]byte(doc[:len(doc)/2]),
		bytes.ReplaceAll([]byte(doc), []byte(`"name"`), []byte(`{name"`)),
		bytes.ReplaceAll([]byte(doc), []byte(`,`), []byte(`]`)),
		bytes.ReplaceAll([]byte(doc), []byte(`"`), []byte(`x`)),
		bytes.ReplaceAll([]byte(doc), []byte(`abcdefghij`), []byte(`abc"efghij`)),
		bytes.ReplaceAll([]byte(doc), []byte(`abcdefghij`), []byte("abc\x01efghij")),
		bytes.ReplaceAll([]byte(doc), []byte(`abcdefghij`), []byte(`abc\qefghij`)),
		bytes.ReplaceAll([]byte(doc), []byte(`{`), []byte(`[`)),
		bytes.ReplaceAll([]byte(doc), []byte(`}`), []byte(`]`)),
		bytes.ReplaceAll([]byte(doc), []byte(`:`), []byte(`,`)),
		make([]byte, len(doc)),
	}
	for i, other := range changes {
		for _, target := range []any{new(largeDoc), new(any), new(stdjson.RawMessage), new(map[string]any), new(struct{}), new([]int)} {
			f := &changingFile{Reader: bytes.NewReader([]byte(doc)), other: other}
			err := NewDecoder(f).Decode(target)
			// A struct without fields leaves the changed bytes unread.
			if _, none := target.(*struct{}); err == nil && !none {
				t.Errorf("change %d into %T: no error", i, target)
			}
			if err != nil && !errors.Is(err, errInputChanged) {
				var te *UnmarshalTypeError
				if !errors.As(err, &te) {
					t.Errorf("change %d into %T: %v", i, target, clipErr(err))
				}
			}
		}
	}
}
