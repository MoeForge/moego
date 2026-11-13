//go:build amd64 && !purego

package json

import (
	"bytes"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// cpuLimits name the CPUs, by the features they lack, whose paths the
// tests below compare with those of this CPU: without AVX2 the block
// kernels do not run, and without AES or AVX stamps hash with
// hash/maphash.
var cpuLimits = []string{"avx2", "avx", "aes", "avx,aes"}

type cpuItem struct {
	Name string            `json:"name"`
	Tags []string          `json:"tags"`
	Note string            `json:"note"`
	Raw  RawMessage        `json:"raw,omitempty"`
	N    float64           `json:"n"`
	M    map[string]string `json:"m,omitempty"`
}

type cpuItemAlias cpuItem

// cpuMarshaler marshals an alias of its item, as methods that marshal an
// alias of their type do, which Marshal stamps once it sees such output
// come back, and then changes the output as Edit says.
type cpuMarshaler struct {
	Item cpuItem
	Edit int
}

func (m cpuMarshaler) MarshalJSON() ([]byte, error) {
	b, err := Marshal((*cpuItemAlias)(&m.Item))
	if err != nil {
		return b, err
	}
	switch m.Edit {
	case 1:
		b[len(b)/2] = '<'
	case 2:
		b = append(b, ' ', '\n')
	case 3:
		b = b[:len(b)-1]
	case 4:
		b = bytes.Clone(b)
	case 5:
		b[len(b)/3] = 'Q'
	}
	return b, nil
}

// cpuUnmarshaler unmarshals into an alias of its item, as methods that
// unmarshal into an alias of their type do: the decoder stamps such input.
// cpuStampedInputs counts the inputs it got stamped.
type cpuUnmarshaler struct{ Item cpuItem }

var cpuStampedInputs int

func (u *cpuUnmarshaler) UnmarshalJSON(b []byte) error {
	if len(b) >= stampMin && (&decodeState{}).inputStamped(b) {
		cpuStampedInputs++
	}
	var a cpuItemAlias
	err := Unmarshal(b, &a)
	u.Item = cpuItem(a)
	return err
}

type cpuDoc struct {
	Items []cpuUnmarshaler `json:"items"`
	One   *cpuUnmarshaler  `json:"one"`
	Raw   RawMessage       `json:"raw"`
	Any   any              `json:"any"`
	Text  string           `json:"text"`
	Nums  []float64        `json:"nums"`
	Strs  []string         `json:"strs"`
	Map   map[string]any   `json:"map"`
}

// cpuLog records what the functions give, one entry for each result.
type cpuLog struct{ lines []string }

func (l *cpuLog) add(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

// out records output bytes and an error: short output as it is, long
// output by its length, its start and a hash.
func (l *cpuLog) out(what string, b []byte, err error) {
	if len(b) <= 256 {
		l.add("%s %q %s", what, b, cpuErr(err))
		return
	}
	h := fnv.New64a()
	h.Write(b)
	l.add("%s %q... (%d bytes, hash %x) %s", what, b[:128], len(b), h.Sum64(), cpuErr(err))
}

// diff returns the first entry where l and m differ.
func (l *cpuLog) diff(m *cpuLog) (k int, a, b string, ok bool) {
	for k = range max(len(l.lines), len(m.lines)) {
		a, b = "<none>", "<none>"
		if k < len(l.lines) {
			a = l.lines[k]
		}
		if k < len(m.lines) {
			b = m.lines[k]
		}
		if a != b {
			return k, a, b, false
		}
	}
	return 0, "", "", true
}

// cpuErr describes err with the offsets it carries.
func cpuErr(err error) string {
	if err == nil {
		return "<nil>"
	}
	s := fmt.Sprintf("%T %v", err, err)
	var se *SyntaxError
	if errors.As(err, &se) {
		s += fmt.Sprintf(" offset %d", se.Offset)
	}
	var ue *UnmarshalTypeError
	if errors.As(err, &ue) {
		s += fmt.Sprintf(" offset %d field %q value %q type %v", ue.Offset, ue.Field, ue.Value, ue.Type)
	}
	return s
}

// cpuShow returns v as encoding/json writes it, which shows the values
// behind pointers.
func cpuShow(v any) string {
	b, err := stdjson.Marshal(v)
	if err != nil {
		return "marshal: " + err.Error()
	}
	return string(b)
}

// cpuChunks returns at most n bytes from each Read.
type cpuChunks struct {
	r io.Reader
	n int
}

func (c *cpuChunks) Read(p []byte) (int, error) { return c.r.Read(p[:min(len(p), c.n)]) }

// cpuPlain are the bytes of the plain runs of cpuString.
const cpuPlain = "abcdefghijklmnopqrstuvwxyz0123456789 .,:;{}[]"

// cpuString returns JSON string contents: runs of plain ASCII, escapes,
// HTML characters, characters of two to four bytes, U+2028 and invalid
// UTF-8, n pieces long.
func cpuString(r *rand.Rand, n int) string {
	pieces := []string{`\"`, `\\`, `\/`, `\n`, `\t`, `\u00e9`, `\ud83d\ude00`, `\u2028`, "é", "中文", "😀",
		"\u2028", "\u2029", "<", ">", "&", "\xff", "\xe2\x80"}
	var b strings.Builder
	for range n {
		if r.Intn(8) == 0 {
			b.WriteString(pieces[r.Intn(len(pieces))])
			continue
		}
		for k := r.Intn(40); k >= 0; k-- {
			b.WriteByte(cpuPlain[r.Intn(len(cpuPlain))])
		}
	}
	return b.String()
}

// cpuValue appends a random JSON value to b.
func cpuValue(r *rand.Rand, b []byte, depth int) []byte {
	ws := func(b []byte) []byte {
		if r.Intn(5) == 0 {
			b = append(b, strings.Repeat(" \t\n\r"[r.Intn(4):][:1], r.Intn(80))...)
		}
		return b
	}
	b = ws(b)
	k := r.Intn(10)
	if depth <= 0 && k < 3 || len(b) > 1<<16 {
		k = 3 + r.Intn(7)
	}
	switch k {
	case 0, 1:
		b = append(b, '{')
		for i := range r.Intn(7) {
			if i > 0 {
				b = append(b, ',')
			}
			b = ws(b)
			b = append(b, `"`+[]string{"name", "tags", "note", "raw", "n", "m", "items", "one", "any", "text", "nums", "strs", "map", "k\\u0065y", cpuString(r, 2)}[r.Intn(15)]+`"`...)
			b = append(ws(b), ':')
			b = cpuValue(r, b, depth-1)
		}
		b = append(ws(b), '}')
	case 2:
		b = append(b, '[')
		n := r.Intn(6)
		if r.Intn(5) == 0 {
			n = 50 + r.Intn(100)
		}
		for i := range n {
			if i > 0 {
				b = append(b, ',')
			}
			b = cpuValue(r, b, depth-1)
		}
		b = append(ws(b), ']')
	case 3, 4, 5:
		n := r.Intn(4)
		if r.Intn(4) == 0 {
			n = 10 + r.Intn(300)
		}
		b = append(b, `"`+cpuString(r, n)+`"`...)
	case 6, 7:
		b = append(b, []string{"0", "-0", "12", "-3.25", "1e9", "2E-3", "0.5e+7", "123456789012345678901234567890", "1e400"}[r.Intn(9)]...)
	default:
		b = append(b, []string{"true", "false", "null"}[r.Intn(3)]...)
	}
	return ws(b)
}

// cpuItems returns items whose notes are short, as long as the stamp hash
// reads, and long.
func cpuItems(r *rand.Rand) []cpuItem {
	var items []cpuItem
	for i := range 1 + r.Intn(4) {
		note := cpuString(r, []int{1, 8, 40, 300}[r.Intn(4)])
		items = append(items, cpuItem{
			Name: "item" + strconv.Itoa(i), Tags: []string{"a", "<b>", cpuString(r, 2)}, Note: note,
			Raw: RawMessage(`{"k": [1, 2, {"x": "` + strings.Repeat("y", r.Intn(100)) + `"}]}`),
			N:   float64(r.Intn(1000)) / 8, M: map[string]string{"q": "\u2028", "p": cpuString(r, 3)},
		})
	}
	return items
}

// cpuCorpus returns inputs for Unmarshal, Valid, Compact, Indent and the
// Decoder: random values, documents of cpuDoc, inputs of the fuzz tests,
// long strings and deep nesting, and changed and cut copies of them.
func cpuCorpus(t *testing.T, r *rand.Rand) [][]byte {
	n := 40
	if raceEnabled {
		n = 5
	}
	var docs [][]byte
	for range n {
		docs = append(docs, cpuValue(r, nil, 5))
		var raw []stdjson.RawMessage
		for _, it := range cpuItems(r) {
			b, _ := stdjson.Marshal(it)
			raw = append(raw, b)
		}
		doc := map[string]any{"items": raw, "one": raw[0], "raw": stdjson.RawMessage(cpuValue(r, nil, 3)),
			"any": stdjson.RawMessage(cpuValue(r, nil, 3)), "text": cpuString(r, r.Intn(100)),
			"nums": []float64{1, 2.5, -3e9}, "strs": []string{cpuString(r, 5), cpuString(r, 50)}, "map": map[string]any{"a": 1}}
		if b, err := stdjson.Marshal(doc); err == nil {
			docs = append(docs, b)
		}
		if b, err := stdjson.MarshalIndent(doc, "", "  "); err == nil {
			docs = append(docs, b)
		}
	}
	files, _ := filepath.Glob("testdata/fuzz/*/*")
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			for _, pre := range []string{"string(", "[]byte("} {
				if v, ok := strings.CutPrefix(line, pre); ok {
					if s, err := strconv.Unquote(strings.TrimSuffix(v, ")")); err == nil {
						docs = append(docs, []byte(s))
					}
				}
			}
		}
	}
	long := strings.Repeat("abcdefghijklmnopqrstuvwxyz012345", 2<<10)
	for _, s := range []string{
		`"` + long + `"`, `"` + long + `\n` + long + `"`, `["` + long[:40000] + `\u00e9` + long[:1000] + `", 1]`,
		`{"text":"` + strings.Repeat(long, 9) + `"}`, `{"text":"` + strings.Repeat("é", 1<<16) + `"}`,
		`[` + strings.Repeat(`1, `, 30000) + `2]`, `{"nums":[` + strings.Repeat(`1.5,`, 20000) + `2]}`,
		strings.Repeat("[", 64) + strings.Repeat("]", 64), strings.Repeat(`{"a":[`, 100) + strings.Repeat("]}", 100),
		strings.Repeat("[", 10000) + strings.Repeat("]", 10000), strings.Repeat("[", 10001) + strings.Repeat("]", 10001),
		`{"raw":` + strings.Repeat(" ", 200) + `[1,  2]` + strings.Repeat(" ", 200) + `}`,
	} {
		docs = append(docs, []byte(s))
	}
	for i := range len(docs) {
		b := bytes.Clone(docs[i])
		if len(b) > 0 {
			c := "\"\\{}[],: 1a\x01\xff"
			b[r.Intn(len(b))] = c[r.Intn(len(c))]
			docs = append(docs, b, docs[i][:r.Intn(len(docs[i]))])
		}
	}
	return docs
}

// cpuDecode runs the checks, rewriters and decoders of the package on doc,
// and the encoders on what they decode.
func cpuDecode(l *cpuLog, doc []byte) {
	l.add("valid %v", Valid(doc))
	var buf bytes.Buffer
	err := Compact(&buf, doc)
	l.out("compact", buf.Bytes(), err)
	// Indenting deep nesting writes a line per level, as long as its depth.
	deep := bytes.Count(doc, []byte("[")) > 1000
	if !deep {
		buf.Reset()
		err = Indent(&buf, doc, ">", "\t")
		l.out("indent", buf.Bytes(), err)
	}
	buf.Reset()
	HTMLEscape(&buf, doc)
	l.out("html", buf.Bytes(), nil)
	var v any
	err = Unmarshal(doc, &v)
	l.out("any "+cpuShow(v), nil, err)
	if err == nil {
		out, err := Marshal(v)
		l.out("marshal", out, err)
		if !deep {
			out, err = MarshalIndent(v, "", "  ")
			l.out("marshal indent", out, err)
		}
		buf.Reset()
		enc := NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		err = enc.Encode(v)
		l.out("encode", buf.Bytes(), err)
	}
	var d, pre cpuDoc
	pre.Text, pre.Nums = "x", []float64{9, 9, 9, 9}
	for _, p := range []*cpuDoc{&d, &pre} {
		err = Unmarshal(doc, p)
		l.out("doc", []byte(cpuShow(p)), err)
		out, err := Marshal(p)
		l.out("doc marshal", out, err)
	}
	var raws []RawMessage
	err = UnmarshalNoCopy(doc, &raws)
	l.out("raws", []byte(fmt.Sprintf("%q", raws)), err)
	var m map[string]RawMessage
	err = Unmarshal(doc, &m)
	l.out("raw map", []byte(cpuShow(m)), err)
	if err == nil {
		out, err := Marshal(m)
		l.out("raw map marshal", out, err)
	}
	stream := string(doc) + "\n" + string(doc) + " 1 "
	for _, n := range []int{1, 61, 1 << 20} {
		if n == 1 && len(doc) > 1000 {
			continue
		}
		dec := NewDecoder(&cpuChunks{strings.NewReader(stream), n})
		dec.UseNumber()
		for k := 0; k < 4; k++ {
			var v any
			err := dec.Decode(&v)
			l.out(fmt.Sprintf("decode %d at %d more %v", n, dec.InputOffset(), dec.More()), []byte(cpuShow(v)), err)
			if err != nil {
				break
			}
		}
	}
	if len(doc) < 8<<10 {
		dec := NewDecoder(&cpuChunks{strings.NewReader(stream), 61})
		for k := 0; k < 3000; k++ {
			tok, err := dec.Token()
			l.add("token %T %v %s %d", tok, tok, cpuErr(err), dec.InputOffset())
			if err != nil {
				break
			}
		}
	}
}

// cpuEncode runs the encoders on values with strings that reach the
// block kernels and the lazy copy, and on method output that Marshal
// stamps.
func cpuEncode(l *cpuLog, r *rand.Rand) {
	plain := strings.Repeat("abcdefghijklmnopqrstuvwxyz012345", 640)
	var strs []string
	for _, n := range []int{0, 31, 32, 63, 64, 255, 256, 4095, 4096, 16384, 20000} {
		strs = append(strs, plain[:n])
		for _, c := range []string{`"`, `\`, "\n", "\x01", "<", "&", "é", "\u2028", "\xff", "😀"} {
			if n > 0 {
				at := r.Intn(n)
				strs = append(strs, plain[:at]+c+plain[at:n])
			}
		}
	}
	for _, at := range []int{100, 4095, 4096, 5000, 15000} {
		strs = append(strs, plain[:at]+`"`+plain[at:], plain[:at]+"é"+plain[at:])
	}
	values := []any{strs, map[string]any{"s": strs[len(strs)-1], "raw": RawMessage(`{"a" : [1, "<b>"] }`)}}
	for i := range 40 {
		var ms []cpuMarshaler
		for _, it := range cpuItems(r) {
			ms = append(ms, cpuMarshaler{it, i % 6})
		}
		values = append(values, ms, map[string]any{"m": ms[0], "list": ms})
	}
	for _, v := range values {
		out, err := Marshal(v)
		l.out("marshal", out, err)
		for _, html := range []bool{false, true} {
			var buf bytes.Buffer
			enc := NewEncoder(&buf)
			enc.SetEscapeHTML(html)
			if html {
				enc.SetIndent("", " ")
			}
			err := enc.Encode(v)
			l.out(fmt.Sprintf("encode html %v", html), buf.Bytes(), err)
		}
	}
}

// cpuCompare runs f on this CPU, as on each CPU of cpuLimits, and on this
// CPU again, and fails at the first result that differs.
func cpuCompare(t *testing.T, what string, f func(l *cpuLog)) {
	t.Helper()
	var want cpuLog
	f(&want)
	for _, drop := range append(cpuLimits, "") {
		var got cpuLog
		func() {
			defer jsonsimd.LimitCPU(drop)()
			f(&got)
		}()
		if k, g, w, ok := got.diff(&want); !ok {
			t.Fatalf("%s without %q, result %d:\n got %.600s\nwant %.600s", what, drop, k, g, w)
		}
	}
}

// TestDecodeWithoutAVX2 compares what Unmarshal, the Decoder, Valid,
// Compact, Indent and HTMLEscape give, errors and their offsets included,
// on this CPU and as on CPUs without AVX2, AVX or AES.
func TestDecodeWithoutAVX2(t *testing.T) {
	if !jsonsimd.HaveAVX2 {
		t.Skip("no AVX2")
	}
	r := rand.New(rand.NewSource(20261113))
	docs := cpuCorpus(t, r)
	// Methods that unmarshal into an alias of their type get their input
	// stamped after some calls.
	for range 64 {
		var d cpuDoc
		Unmarshal(docs[1], &d)
	}
	cpuStampedInputs = 0
	for _, doc := range docs {
		cpuCompare(t, fmt.Sprintf("input %.200q", doc), func(l *cpuLog) { cpuDecode(l, doc) })
	}
	if cpuStampedInputs == 0 {
		t.Fatal("no input of an UnmarshalJSON method was stamped")
	}
}

// TestEncodeWithoutAVX2 compares what Marshal and the Encoder give on this
// CPU and as on CPUs without AVX2, AVX or AES, once Marshal stamps the
// output of methods.
func TestEncodeWithoutAVX2(t *testing.T) {
	if !jsonsimd.HaveAVX2 {
		t.Skip("no AVX2")
	}
	item := cpuItems(rand.New(rand.NewSource(1)))[0]
	item.Note = strings.Repeat("note ", 40)
	for i := 0; ; i++ {
		out, err := Marshal((*cpuItemAlias)(&item))
		if err != nil {
			t.Fatal(err)
		}
		if ok, _ := stamped(out); ok {
			break
		}
		if i == 100000 {
			t.Fatal("output not stamped")
		}
		if _, err := Marshal(cpuMarshaler{item, 0}); err != nil {
			t.Fatal(err)
		}
	}
	cpuCompare(t, "Marshal", func(l *cpuLog) { cpuEncode(l, rand.New(rand.NewSource(7))) })
}

// TestLazyCopyWithoutAVX2 checks that a long plain string becomes a lazy
// reference only with AVX2: LazyCopy holds on amd64 whatever the CPU, and
// without AVX2 QuoteCountBlocks counts no block, so the string is
// written as it is.
func TestLazyCopyWithoutAVX2(t *testing.T) {
	s := strings.Repeat("abcdefghijklmnopqrstuvwxyz012345", lazyMin/32+1)
	for _, drop := range append(cpuLimits, "") {
		func() {
			defer jsonsimd.LimitCPU(drop)()
			e := getEncodeState(false)
			b := e.str(e.buf[:0], s)
			lazy := 0
			for _, r := range e.refs {
				if r.lazy {
					lazy++
				}
			}
			putEncodeState(e, b)
			if want := map[bool]int{false: 0, true: 1}[jsonsimd.HaveAVX2]; lazy != want {
				t.Fatalf("without %q: %d lazy references, want %d", drop, lazy, want)
			}
			for _, v := range []any{[]string{s, s}, []string{s + `"`, "<" + s}} {
				got, err := Marshal(v)
				want, werr := stdjson.Marshal(v)
				if err != nil || werr != nil || !bytes.Equal(got, want) {
					t.Fatalf("without %q: Marshal = %.40q..., %v", drop, got, err)
				}
			}
		}()
	}
}
