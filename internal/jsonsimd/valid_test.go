package jsonsimd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// genValue appends a random JSON value of at most depth levels.
func genValue(r *rand.Rand, b []byte, depth int) []byte {
	ws := func(b []byte) []byte {
		for r.Intn(4) == 0 {
			b = append(b, " \t\r\n"[r.Intn(4)])
		}
		return b
	}
	b = ws(b)
	k := r.Intn(10)
	if depth <= 0 && k < 2 {
		k = 2 + r.Intn(8)
	}
	switch k {
	case 0:
		b = append(b, '{')
		n := r.Intn(5)
		for i := 0; i < n; i++ {
			if i > 0 {
				b = append(b, ',')
			}
			b = ws(b)
			b = genString(r, b)
			b = ws(b)
			b = append(b, ':')
			b = genValue(r, b, depth-1)
		}
		b = ws(b)
		b = append(b, '}')
	case 1:
		b = append(b, '[')
		n := r.Intn(5)
		for i := 0; i < n; i++ {
			if i > 0 {
				b = append(b, ',')
			}
			b = genValue(r, b, depth-1)
		}
		b = ws(b)
		b = append(b, ']')
	case 2, 3:
		b = genString(r, b)
	case 4, 5:
		b = append(b, []string{"0", "-0", "12", "-3.25", "1e9", "2E-3", "0.5e+7", "123456789012345678901234567890"}[r.Intn(8)]...)
	case 6:
		b = append(b, "true"...)
	case 7:
		b = append(b, "false"...)
	default:
		b = append(b, "null"...)
	}
	return ws(b)
}

func genString(r *rand.Rand, b []byte) []byte {
	b = append(b, '"')
	n := r.Intn(12)
	if r.Intn(8) == 0 {
		n = 200 + r.Intn(400)
	}
	for i := 0; i < n; i++ {
		switch r.Intn(20) {
		case 0:
			b = append(b, `\"`...)
		case 1:
			b = append(b, `\\`...)
		case 2:
			b = append(b, `\u00e9`...)
		case 3:
			b = append(b, "é"...)
		case 4:
			b = append(b, `\n`...)
		case 5:
			b = append(b, "\xe2\x80\xa8"...)
		case 6:
			b = append(b, "<&>"[r.Intn(3)])
		default:
			b = append(b, byte('a'+r.Intn(26)))
		}
	}
	return append(b, '"')
}

// mutate changes a few bytes of b.
func mutate(r *rand.Rand, b []byte) []byte {
	b = append([]byte(nil), b...)
	for n := 1 + r.Intn(3); n > 0 && len(b) > 0; n-- {
		i := r.Intn(len(b))
		switch r.Intn(4) {
		case 0:
			const set = `{}[]:,"\ 0a-e.tfn`
			b[i] = set[r.Intn(len(set))]
		case 1:
			b = append(b[:i], b[i+1:]...)
		case 2:
			b = append(b[:i], append([]byte{byte(r.Intn(256))}, b[i:]...)...)
		default:
			b = b[:i]
		}
	}
	return b
}

func tablesOf(s string, blocks bool) (bool, Tables) {
	var t Tables
	var ok bool
	if blocks {
		ok = validateBlocks(s, 10000, resetTables(&t, len(s)), nil, nil)
	} else {
		ok = validateScalar(s, 10000, resetTables(&t, len(s)), nil, nil)
	}
	return ok, t
}

// randomIterations limits repeated random cases under the race detector.
// Table cases, boundary checks and concurrent tests keep their full coverage.
func randomIterations(n int) int {
	if raceEnabled {
		return max(1, n/10)
	}
	return n
}

// checkStream compares StreamCheck, fed in pieces of several sizes, with the
// result for the whole document, and checks the positions it lists.
func checkStream(t *testing.T, doc []byte, want bool) {
	t.Helper()
	for _, piece := range []int{1, 2, 3, 7, 16, 64, 1 << 30} {
		c := StreamCheck{Long: 0, MaxDepth: 10000}
		if piece == 3 || piece == 16 {
			c.Long = 8
		}
		end, tooLong, ok := feedPieces(&c, doc, piece)
		if tooLong {
			continue
		}
		rest := bytes.TrimLeft(doc[min(end, len(doc)):], " \t\r\n")
		if got := ok && len(rest) == 0; got != want {
			t.Fatalf("StreamCheck(%q) with pieces of %d = %v, end %d, want %v", doc, piece, got, end, want)
		}
		if !want || c.Long == 0 {
			continue
		}
		for _, sp := range c.Strings {
			if doc[sp.Start] != '"' || doc[sp.End] != '"' || sp.End-sp.Start+1 < int64(c.Long) {
				t.Fatalf("StreamCheck(%q): string %+v", doc, sp)
			}
		}
		for i, ct := range c.Containers {
			// Each check decodes the whole container, which is quadratic
			// in deep documents; under the race detector, a document of
			// many containers checks every 64th.
			if raceEnabled && len(c.Containers) > 64 && i%64 != 0 {
				continue
			}
			o, e := doc[ct.Off], doc[ct.End]
			if o != '{' && o != '[' || e != o+2 || ct.End-ct.Off+1 < int64(c.Long) || !json.Valid(doc[ct.Off:ct.End+1]) {
				t.Fatalf("StreamCheck(%q): container %+v", doc, ct)
			}
			var v any
			if err := json.Unmarshal(doc[ct.Off:ct.End+1], &v); err != nil {
				continue
			}
			n := 0
			switch v := v.(type) {
			case []any:
				n = len(v)
			case map[string]any:
				n = ct.N
				if ct.N < len(v) {
					t.Fatalf("StreamCheck(%q): container %+v, %d distinct members", doc, ct, len(v))
				}
			}
			if ct.N != n {
				t.Fatalf("StreamCheck(%q): container %+v, %d elements", doc, ct, n)
			}
		}
	}
}

// feedPieces feeds doc to c in pieces of at most n bytes, and returns the
// end of the value, whether it stopped for a long token, and whether the
// value is valid.
func feedPieces(c *StreamCheck, doc []byte, n int) (int, bool, bool) {
	var buf []byte
	for len(doc) > 0 || len(buf) > 0 {
		k := min(n, len(doc))
		buf = append(buf, doc[:k]...)
		doc = doc[k:]
		used, st := c.Feed(buf, len(doc) == 0)
		switch st {
		case StreamEnd:
			return int(c.End), false, true
		case StreamInvalid:
			return int(c.End), false, false
		case StreamTooLong:
			return 0, true, false
		}
		buf = append(buf[:0], buf[used:]...)
		if len(doc) == 0 {
			return 0, false, false
		}
	}
	_, st := c.Feed(nil, true)
	return int(c.End), false, st == StreamEnd
}

func checkDoc(t *testing.T, doc []byte) {
	t.Helper()
	s := string(doc)
	want := json.Valid(doc)
	if got := Validate(s, 10000, nil); got != want {
		t.Fatalf("Validate(%q) = %v, want %v", s, got, want)
	}
	okS, tabS := tablesOf(s, false)
	if okS != want {
		t.Fatalf("validateScalar(%q) = %v, want %v", s, okS, want)
	}
	if HaveAVX2 && len(s) >= 64 {
		okB, tabB := tablesOf(s, true)
		if okB != want {
			t.Fatalf("validateBlocks(%q) = %v, want %v", s, okB, want)
		}
		if want && !reflect.DeepEqual(norm(tabB, len(s)), norm(tabS, len(s))) {
			t.Fatalf("tables differ for %q:\nblocks %+v\nscalar %+v", s, tabB, tabS)
		}
	}
	if HaveAVX2 && len(s) >= 64 {
		if r := validatePlain(s); r != leanDeep && (r == leanValid) != want {
			t.Fatalf("validatePlain(%q) = %d, want %v", s, r, want)
		}
		for _, d := range []int{0, 2, 63, 64, 65, 4095, 4096, 4097, 10000} {
			if got, w := validateLean(s, d, nil, nil), validateScalar(s, d, nil, nil, nil); got != w {
				t.Fatalf("validateLean(%q, %d) = %v, want %v", s, d, got, w)
			}
		}
	}
	checkStream(t, doc, want)
	checkRest(t, s)
	checkValue(t, s)
	checkCount(t, s, want)
	if want && HaveAVX2 {
		var c bytes.Buffer
		json.Compact(&c, doc)
		if got := AppendCompact(nil, s); string(got) != c.String() {
			t.Fatalf("AppendCompact(%q) = %q, want %q", s, got, c.String())
		}
	}
	hasHTML := htmlIn(s, 0, len(s))
	for _, html := range []bool{false, true} {
		ok, spaced, h := ValidateSpaced(s, 10000, html)
		if ok != want {
			t.Fatalf("ValidateSpaced(%q) = %v, want %v", s, ok, want)
		}
		if want {
			var c bytes.Buffer
			if err := json.Compact(&c, doc); err == nil && spaced != (c.Len() != len(s)) {
				t.Fatalf("ValidateSpaced(%q) spaced = %v", s, spaced)
			}
		}
		if h != (ok && html && hasHTML) {
			t.Fatalf("ValidateSpaced(%q, %v) html = %v", s, html, h)
		}
	}
	if got := HasHTML(s); got != hasHTML {
		t.Fatalf("HasHTML(%q) = %v, want %v", s, got, hasHTML)
	}
	// As a stream: the value followed by more input. A decoder of
	// encoding/json reading the same input is the oracle: an end is where
	// it ends the value, and PrefixIncomplete is where it wants more input.
	// PrefixInvalid is always allowed; the caller then scans byte by byte.
	for _, tail := range []string{"", " ", "{", "]", `"\x01`, "  [1,2]"} {
		in := s + tail
		end := ValidatePrefix(in, 10000, nil)
		lead := strings.TrimLeft(in, " \t\r\n")
		isContainer := len(lead) > 0 && (lead[0] == '{' || lead[0] == '[')
		if !isContainer {
			if end != PrefixInvalid {
				t.Fatalf("ValidatePrefix(%q) = %d for a non-container", in, end)
			}
			continue
		}
		dec := json.NewDecoder(strings.NewReader(in))
		var raw json.RawMessage
		derr := dec.Decode(&raw)
		switch {
		case end >= 0:
			if derr != nil || dec.InputOffset() != int64(end) {
				t.Fatalf("ValidatePrefix(%q) = %d, decoder ends at %d with %v", in, end, dec.InputOffset(), derr)
			}
			var pt Tables
			if ValidatePrefix(in, 10000, &pt) != end {
				t.Fatalf("ValidatePrefix with tables differs for %q", in)
			}
			_, full := tablesOf(in[:end], false)
			if !reflect.DeepEqual(norm(pt, end), norm(full, end)) {
				t.Fatalf("prefix tables differ for %q:\n got %+v\nwant %+v", in, pt, full)
			}
		case end == PrefixIncomplete:
			if derr != io.ErrUnexpectedEOF {
				t.Fatalf("ValidatePrefix(%q) = incomplete, decoder: %v", in, derr)
			}
		case end == PrefixInvalid:
			if want && tail == "" {
				t.Fatalf("ValidatePrefix(%q) = invalid for a valid value", in)
			}
		default:
			t.Fatalf("ValidatePrefix(%q) = %d", in, end)
		}
		// Every cut of a valid value is incomplete or invalid.
		if want && tail == "" && len(in) > 1 {
			cut := in[:min(len(in)/2+len(in)%7, len(in))]
			if len(cut) < len(strings.TrimRight(in, " \t\r\n")) {
				if e := ValidatePrefix(cut, 10000, nil); e >= 0 {
					t.Fatalf("ValidatePrefix(%q) = %d for a cut value", cut, e)
				}
			}
		}
	}
}

// norm drops the spans that start at or past end, which a prefix
// validation may record after the value, and makes empty tables nil.
func norm(t Tables, end int) Tables {
	var n Tables
	for _, sp := range t.Spans {
		if int(sp.Start) < end {
			n.Spans = append(n.Spans, sp)
		}
	}
	if len(t.Containers) > 0 {
		n.Containers = t.Containers
	}
	return n
}

func TestValidateRandom(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < randomIterations(20000); i++ {
		doc := genValue(r, nil, 4)
		checkDoc(t, doc)
		checkDoc(t, mutate(r, doc))
	}
}

// TestCountLong checks CountElements on documents of many blocks, whose
// containers span blocks.
func TestCountLong(t *testing.T) {
	r := rand.New(rand.NewSource(6))
	for i := 0; i < 300; i++ {
		b := []byte{'['}
		for k := r.Intn(40); k >= 0; k-- {
			if r.Intn(3) == 0 {
				b = append(b, `{"a":[{"b":"[{,}]"},[1,2,{"c":[[],{}]}]],"d":"\\\"]"}`...)
			} else {
				b = genValue(r, b, 2+r.Intn(5))
			}
			b = append(b, ',')
		}
		b = append(b, `"]"]`...)
		checkCount(t, string(b), true)
	}
}

func TestValidateEdges(t *testing.T) {
	for _, doc := range []string{
		``, ` `, `{}`, `[]`, `[[[]]]`, `{"a":{}}`, `"` + strings.Repeat("x", 300) + `"`,
		`[` + strings.Repeat(`"`+strings.Repeat("é", 200)+`",`, 5) + `1]`,
		`{"a":"\u0000"}`, `{"a":"\ud800"}`, `[1,2,3]` + strings.Repeat(" ", 100),
		strings.Repeat("[", 10000) + strings.Repeat("]", 10000),
		strings.Repeat("[", 10001) + strings.Repeat("]", 10001),
		`[1e]`, `[-]`, `[01]`, `[1.]`, `[.5]`, `[tru]`, `[nul]`, `{"a" "b"}`, `{"a":1,}`, `[1,]`,
		"[\"\x1f\"]", "[\"\x7f\"]",
		strings.Repeat("[", 63) + strings.Repeat("]", 63), strings.Repeat("[", 64) + strings.Repeat("]", 64),
		strings.Repeat(`{"a":[`, 40) + strings.Repeat("]}", 40), strings.Repeat(`[{"k":`, 33) + "1" + strings.Repeat("}]", 33),
		strings.Repeat("[", 64) + strings.Repeat("]", 63), strings.Repeat("[", 65) + "}" + strings.Repeat("]", 64),
		`{"a":0,0:""}`, `{"a":"b",2:1}`, `{"a":[],8 0}`, `{"a":{},:"x"}`, `[{"":0,0:[1]}]`,
	} {
		checkDoc(t, []byte(doc))
		checkDoc(t, []byte(strings.Repeat(" ", 70)+doc))
	}
}

// TestValidateLongStrings checks documents whose strings cross blocks,
// many long enough to be skipped from the block where they start, with
// escapes, other bytes and errors near the ends of blocks.
func TestValidateLongStrings(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	str := func(b []byte) []byte {
		b = append(b, '"')
		for k := r.Intn(400); k > 0; k-- {
			switch x := r.Intn(200); x {
			case 0:
				b = append(b, `\"`...)
			case 1:
				b = append(b, `\\`...)
			case 2:
				b = append(b, `\u00e9`...)
			case 3:
				b = append(b, "é"...)
			default:
				b = append(b, byte('a'+x%26))
			}
		}
		return append(b, '"')
	}
	for i := 0; i < randomIterations(3000); i++ {
		b := []byte(strings.Repeat(" ", r.Intn(64)) + "{")
		for k := r.Intn(8); k >= 0; k-- {
			b = append(str(b), ':')
			switch r.Intn(3) {
			case 0:
				b = str(b)
			case 1:
				b = append(str(append(str(append(b, '[')), ',')), ']')
			default:
				b = append(b, `{"n":12,"t":true}`...)
			}
			b = append(b, ',')
		}
		b[len(b)-1] = '}'
		checkDoc(t, b)
		checkDoc(t, mutate(r, b))
		c := append([]byte(nil), b...)
		c[r.Intn(len(c))] = "\x1f\\\"\n"[r.Intn(4)]
		checkDoc(t, c)
	}
}

// TestValidateMemberAfterComma checks every pair of bytes after a comma in
// an object, where the lean grammar takes the opening quote of a member
// name and its colon in one step.
func TestValidateMemberAfterComma(t *testing.T) {
	pad := strings.Repeat(" ", 64)
	for _, head := range []string{`{"k":0,`, `{"k":"s",`, `{"k":{},`} {
		for a := 0; a < 256; a++ {
			for b := 0; b < 256; b++ {
				s := pad + head + string([]byte{byte(a), byte(b)}) + `"x"}`
				want := json.Valid([]byte(s))
				if got := Validate(s, 10000, nil); got != want {
					t.Fatalf("Validate(%q) = %v, want %v", s, got, want)
				}
				if HaveAVX2 {
					if got := validateLean(s, 10000, nil, nil); got != want {
						t.Fatalf("validateLean(%q) = %v, want %v", s, got, want)
					}
				}
			}
		}
	}
}

func FuzzValidate(f *testing.F) {
	for _, d := range benchDocs {
		f.Add([]byte(d.doc))
	}
	f.Add([]byte(denseDoc(2000)))
	f.Fuzz(func(t *testing.T, doc []byte) {
		checkDoc(t, doc)
	})
}

func TestStringEnd(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 5000; i++ {
		doc := genString(r, nil)
		if r.Intn(4) == 0 {
			doc = append(doc[:len(doc)-1], append([]byte(strings.Repeat("y", r.Intn(300))), '"')...)
		}
		s := string(doc) + `,"z"`
		if got := StringEnd(s, 1); got != len(doc)-1 {
			t.Fatalf("StringEnd(%q) = %d, want %d", s, got, len(doc)-1)
		}
		if got := StringEnd(string(doc[:len(doc)-1]), 1); got != -1 {
			t.Fatalf("StringEnd of unterminated %q = %d", doc, got)
		}
		end, flags := ScanString(s, 1)
		if want := stringFlags(string(doc[1 : len(doc)-1])); end != len(doc)-1 || flags != want {
			t.Fatalf("ScanString(%q) = %d, %d, want %d, %d", s, end, flags, len(doc)-1, want)
		}
		if end, _ := ScanString(string(doc[:len(doc)-1]), 1); end != -1 {
			t.Fatalf("ScanString of unterminated %q = %d", doc, end)
		}
		// With a byte changed, ScanString ends where StringEnd does.
		b := []byte(s)
		b[1+r.Intn(len(doc)-1)] = "\\x\x00\x1fu\""[r.Intn(6)]
		bad := string(b)
		if end, _ := ScanString(bad, 1); end != StringEnd(bad, 1) {
			t.Fatalf("ScanString(%q) = %d, StringEnd %d", bad, end, StringEnd(bad, 1))
		}
	}
}

// TestStrEnd checks strEnd against a byte loop, with stops and bytes of
// 0x80 and above anywhere, before and after the end.
func TestStrEnd(t *testing.T) {
	if !HaveAVX2 {
		t.Skip("no AVX2")
	}
	r := rand.New(rand.NewSource(11))
	for n := 0; n < randomIterations(200000); n++ {
		b := make([]byte, r.Intn(300))
		for k := range b {
			b[k] = 'a' + byte(r.Intn(26))
		}
		for m := r.Intn(3); m > 0 && len(b) > 0; m-- {
			b[r.Intn(len(b))] = "\"\\\x00\x1f\x7f"[r.Intn(5)]
		}
		for m := r.Intn(3); m > 0 && len(b) > 0; m-- {
			b[r.Intn(len(b))] = 0x80 + byte(r.Intn(128))
		}
		i := r.Intn(len(b) + 1)
		checkStrEnd(t, string(b), i)
	}
}

// TestStrEndLong checks strEnd on runs long enough for its 128-byte
// steps, with few stops and bytes of 0x80 and above, in any order.
func TestStrEndLong(t *testing.T) {
	if !HaveAVX2 {
		t.Skip("no AVX2")
	}
	r := rand.New(rand.NewSource(12))
	for n := 0; n < randomIterations(50000); n++ {
		b := make([]byte, r.Intn(1500))
		for k := range b {
			b[k] = 0x20 + byte(r.Intn(0x5f))
			if b[k] == '"' || b[k] == '\\' {
				b[k] = 'q'
			}
		}
		for m := r.Intn(3); m > 0 && len(b) > 0; m-- {
			b[r.Intn(len(b))] = "\"\\\x00\x1f\x21\x23\x7f"[r.Intn(7)]
		}
		for m := r.Intn(4); m > 0 && len(b) > 0; m-- {
			b[r.Intn(len(b))] = 0x80 + byte(r.Intn(128))
		}
		checkStrEnd(t, string(b), r.Intn(len(b)+1))
	}
}

// TestStrSkip checks strSkip against a loop over the 64-byte blocks, on
// runs with few stops and bytes of 0x80 and above, in any order.
func TestStrSkip(t *testing.T) {
	if !HaveAVX2 {
		t.Skip("no AVX2")
	}
	r := rand.New(rand.NewSource(13))
	for n := 0; n < randomIterations(50000); n++ {
		b := make([]byte, r.Intn(1500))
		for k := range b {
			b[k] = 0x20 + byte(r.Intn(0x5f))
			if b[k] == '"' || b[k] == '\\' {
				b[k] = 'q'
			}
		}
		for m := r.Intn(3); m > 0 && len(b) > 0; m-- {
			b[r.Intn(len(b))] = "\"\\\x00\x1f\x21\x23\x7f"[r.Intn(7)]
		}
		for m := r.Intn(4); m > 0 && len(b) > 0; m-- {
			b[r.Intn(len(b))] = 0x80 + byte(r.Intn(128))
		}
		s, i := string(b), r.Intn(len(b)+1)
		want, high := i, false
	blocks:
		for ; want+64 <= len(s); want += 64 {
			for _, c := range []byte(s[want : want+64]) {
				if c == '"' || c == '\\' || c < 0x20 {
					break blocks
				}
			}
			for _, c := range []byte(s[want : want+64]) {
				high = high || c >= 0x80
			}
		}
		if j, h := strSkip(s, i); j != want || h != high {
			t.Fatalf("strSkip(%q, %d) = %d, %v, want %d, %v", s, i, j, h, want, high)
		}
	}
}

// TestStrSkipHTML checks strSkipHTML against a loop over the 64-byte
// blocks, on runs with few stops, bytes HasHTML looks for and other bytes
// of 0x80 and above, in any order.
func TestStrSkipHTML(t *testing.T) {
	if !HaveAVX2 {
		t.Skip("no AVX2")
	}
	r := rand.New(rand.NewSource(14))
	for n := 0; n < randomIterations(50000); n++ {
		b := make([]byte, r.Intn(1500))
		for k := range b {
			b[k] = 0x20 + byte(r.Intn(0x5f))
			if strings.IndexByte("\"\\<>&", b[k]) >= 0 {
				b[k] = 'q'
			}
		}
		for m := r.Intn(3); m > 0 && len(b) > 0; m-- {
			b[r.Intn(len(b))] = "\"\\\x00\x1f\x21\x23\x7f"[r.Intn(7)]
		}
		for m := r.Intn(3); m > 0 && len(b) > 0; m-- {
			b[r.Intn(len(b))] = "<>&\xe2\x3c\x3e\x3d\x24\x3f"[r.Intn(9)]
		}
		for m := r.Intn(4); m > 0 && len(b) > 0; m-- {
			b[r.Intn(len(b))] = 0x80 + byte(r.Intn(128))
		}
		s, i := string(b), r.Intn(len(b)+1)
		want, html := i, false
	blocks:
		for ; want+64 <= len(s); want += 64 {
			for _, c := range []byte(s[want : want+64]) {
				if c == '"' || c == '\\' || c < 0x20 {
					break blocks
				}
			}
			for _, c := range []byte(s[want : want+64]) {
				html = html || c == '<' || c == '>' || c == '&' || c == 0xe2
			}
		}
		if j, h := strSkipHTML(s, i); j != want || h != html {
			t.Fatalf("strSkipHTML(%q, %d) = %d, %v, want %d, %v", s, i, j, h, want, html)
		}
	}
}

// checkStrEnd checks strEnd(s, i): the first stop byte when it lies in
// the 64- and 32-byte reads that fit in s, else the end of those reads.
func checkStrEnd(t *testing.T, s string, i int) {
	t.Helper()
	j, h := strEnd(s, i)
	e := i + (len(s)-i)&^63
	if len(s)-e >= 32 {
		e += 32
	}
	want := i
	for want < e && s[want] != '"' && s[want] != '\\' && s[want] >= 0x20 {
		want++
	}
	high := false
	for k := i; k < want; k++ {
		high = high || s[k] >= 0x80
	}
	if j != want || h != high {
		t.Fatalf("strEnd(%q, %d) = %d, %v, want %d, %v", s, i, j, h, want, high)
	}
}

func TestHasHTML(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for i := 0; i < 5000; i++ {
		n := r.Intn(300)
		b := make([]byte, n)
		for j := range b {
			b[j] = "abcdefgh\xe2\x80\xa8\xa9\x82<>&"[r.Intn(15)]
			if r.Intn(4) != 0 {
				b[j] = 'a'
			}
		}
		s := string(b)
		if got, want := HasHTML(s), htmlIn(s, 0, len(s)); got != want {
			t.Fatalf("HasHTML(%q) = %v", s, got)
		}
	}
}

// TestValidateSpacedHTML checks the HTML report of ValidateSpaced on
// documents with long strings, which it reads once for both.
func TestValidateSpacedHTML(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	pieces := []string{"<", ">", "&", "\xe2\x80\xa8", "\xe2\x80\xa9", "\xe2\x82\xac", "\xe2", `\"`, `\\`, `\n`, "\x01", "\xff"}
	for i := 0; i < 3000; i++ {
		var b strings.Builder
		b.WriteString("[")
		for k := r.Intn(5); k >= 0; k-- {
			b.WriteString(`"`)
			n := r.Intn(4000)
			for j := 0; j < n; j++ {
				if r.Intn(1500) == 0 {
					b.WriteString(pieces[r.Intn(len(pieces))])
				} else {
					b.WriteByte('a' + byte(j%26))
				}
			}
			b.WriteString(`"`)
			if k > 0 {
				b.WriteString([]string{",", ", ", ",\n"}[r.Intn(3)])
			}
		}
		b.WriteString("]")
		s := b.String()
		want := json.Valid([]byte(s))
		ok, _, h := ValidateSpaced(s, 10000, true)
		if ok != want || h != (want && htmlIn(s, 0, len(s))) {
			t.Fatalf("ValidateSpaced(%q) = %v, html %v", s, ok, h)
		}
	}
}

// TestCopyPlainBlocks checks the blocks CopyPlainBlocks copies against a
// byte by byte check.
func TestCopyPlainBlocks(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	bad := "\x00\x1f\"\\<>&\x7f\x80\xe2\xff "
	for i := 0; i < 20000; i++ {
		b := make([]byte, r.Intn(700))
		for j := range b {
			b[j] = 'a' + byte(j%26)
			if r.Intn(400) == 0 {
				b[j] = bad[r.Intn(len(bad))]
			}
		}
		s := string(b)
		for _, html := range []bool{false, true} {
			want := 0
			if HaveAVX2 {
				for want+64 <= len(s) && plainBlock(s[want:want+64], html) {
					want += 64
				}
			}
			dst := make([]byte, len(s))
			if n := CopyPlainBlocks(dst, s, html); n != want || string(dst[:n]) != s[:n] {
				t.Fatalf("CopyPlainBlocks(%q, %v) = %d, want %d", s, html, n, want)
			}
		}
	}
}

func plainBlock(s string, html bool) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c >= 0x80 || c == '"' || c == '\\' || html && (c == '<' || c == '>' || c == '&') {
			return false
		}
	}
	return true
}

var benchDocs = []struct{ name, doc string }{
	{"chunk", `{"id":"chatcmpl-B9MHDbslfkBeAs8l4bebGdFOJ6PeG","object":"chat.completion.chunk","created":1741569952,"model":"gpt-4o-2024-08-06","system_fingerprint":"fp_f9f4fb6dbf","choices":[{"index":0,"delta":{"content":" the capital of France is Paris, a city"},"logprobs":null,"finish_reason":null}],"usage":null}`},
	{"tail", `{"":[0,{"":{"name": "lookup", "parameters": {"type": "object", "properties": {"q": {"type": "string"}}}}}]
}`},
	{"usage", `{"prompt_tokens":1117,"completion_tokens":46,"total_tokens":1163,"prompt_tokens_details":{"cached_tokens":1024,"text_tokens":0,"audio_tokens":0,"image_tokens":0},"completion_tokens_details":{"text_tokens":0,"audio_tokens":0,"image_tokens":0,"reasoning_tokens":0},"input_tokens":0,"output_tokens":0,"input_tokens_details":null}`},
}

// denseDoc returns a request of about n bytes made of many small members,
// with a structural byte every few bytes.
func denseDoc(n int) string {
	var b strings.Builder
	b.WriteString(`{"model":"gpt-4o","stream":true,"messages":[`)
	for i := 0; b.Len() < n*3/4; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"role":"user","content":"Message %d: tell me about the weather in city number %d.","name":"u%d"}`, i, i*7, i)
	}
	b.WriteString(`],"tools":[`)
	for i := 0; b.Len() < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"type":"function","function":{"name":"tool_%d","description":"Looks up item %d.","parameters":{"type":"object","properties":{"q":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":%d}},"required":["q"]}}}`, i, i, 100+i)
	}
	b.WriteString(`]}`)
	return b.String()
}

func BenchmarkValidate(b *testing.B) {
	docs := append(benchDocs[:len(benchDocs):len(benchDocs)], struct{ name, doc string }{"dense64k", denseDoc(64 << 10)})
	for _, d := range docs {
		var t Tables
		b.Run(d.name+"/blocks", func(b *testing.B) {
			b.SetBytes(int64(len(d.doc)))
			for i := 0; i < b.N; i++ {
				validateBlocks(d.doc, 10000, resetTables(&t, len(d.doc)), nil, nil)
			}
		})
		b.Run(d.name+"/scalar", func(b *testing.B) {
			b.SetBytes(int64(len(d.doc)))
			for i := 0; i < b.N; i++ {
				validateScalar(d.doc, 10000, resetTables(&t, len(d.doc)), nil, nil)
			}
		})
		b.Run(d.name+"/blocks-notab", func(b *testing.B) {
			b.SetBytes(int64(len(d.doc)))
			for i := 0; i < b.N; i++ {
				validateBlocks(d.doc, 10000, nil, nil, nil)
			}
		})
		b.Run(d.name+"/lean", func(b *testing.B) {
			b.SetBytes(int64(len(d.doc)))
			for i := 0; i < b.N; i++ {
				validateLean(d.doc, 10000, nil, nil)
			}
		})
	}
}

// checkRest compares ValidateRest on continuations of s with Validate on
// the same continuations after a prefix that opens the containers.
func checkRest(t *testing.T, s string) {
	t.Helper()
	r := rand.New(rand.NewSource(int64(len(s))))
	for _, depth := range []int{1, 2, 63, 64, 65, 200} {
		kinds := make([]uint64, (depth+63)/64)
		for k := range kinds {
			kinds[k] = r.Uint64()
		}
		obj := func(k int) bool { return kinds[k>>6]>>(k&63)&1 != 0 }
		var pre, post strings.Builder
		for k := 0; k < depth; k++ {
			if obj(k) {
				pre.WriteString(`{"":`)
			} else {
				pre.WriteString("[0,")
			}
		}
		for k := depth - 1; k >= 0; k-- {
			if obj(k) {
				post.WriteByte('}')
			} else {
				post.WriteByte(']')
			}
		}
		more := ",1"
		if obj(depth - 1) {
			more = `,"k":[1]`
		}
		closers := post.String()
		for _, rest := range []string{s, s + closers, s + more + closers, s + " " + closers[1:], s + closers + "]", s + more} {
			want := Validate(pre.String()+rest, 10000, nil)
			if got := ValidateRest(rest, kinds, depth, 10000, &scratchTables); got != want {
				t.Fatalf("ValidateRest(%q) at depth %d = %v, want %v", rest, depth, got, want)
			}
			ok, ends, all := ValidateRestEnds(rest, kinds, depth, 10000, &scratchTables)
			if ok != want {
				t.Fatalf("ValidateRestEnds(%q) at depth %d = %v, want %v", rest, depth, ok, want)
			}
			if ok && ends != nil {
				ref := restEndsRef(rest, depth)
				n := min(len(ref), restEnds)
				if !slices.Equal(ends, ref[:n]) || all != (len(ref) < restEnds) {
					t.Fatalf("ValidateRestEnds(%q) at depth %d: ends %v all %v, want %v", rest, depth, ends, all, ref)
				}
			}
		}
	}
}

// restEndsRef returns the ends of the arrays and objects that close at
// depth in valid s, which continues inside depth open containers.
func restEndsRef(s string, depth int) []int32 {
	var ends []int32
	d := depth
	for j := 0; j < len(s); j++ {
		switch s[j] {
		case '"':
			for j++; s[j] != '"'; j++ {
				if s[j] == '\\' {
					j++
				}
			}
		case '{', '[':
			d++
		case '}', ']':
			if d--; d == depth {
				ends = append(ends, int32(j+1))
			}
		}
	}
	return ends
}

// scratchTables lends its scratch space to the validations of the tests,
// one after another, as a decoder's tables do.
var scratchTables Tables

// checkValue compares ValidateValue with ValidatePrefix on s and on s with
// bytes after it.
func checkValue(t *testing.T, s string) {
	t.Helper()
	if len(s) == 0 || s[0] != '{' && s[0] != '[' {
		return
	}
	for _, x := range []string{s, s + " x", s + strings.Repeat(" ", 70) + "\"\x01\""} {
		want := max(ValidatePrefix(x, 10000, nil), PrefixInvalid)
		// A value may be rejected for a bad string after it in the same
		// block, which makes the document invalid.
		got := ValidateValue(x, 10000, &scratchTables)
		if got != want && (got != PrefixInvalid || json.Valid([]byte(s))) {
			t.Fatalf("ValidateValue(%q) = %d, want %d", x, got, want)
		}
		end, spaced, long := ValidateValueInfo(x, 10000, nil)
		if end != got || long < 0 || long > max(end, 0) {
			t.Fatalf("ValidateValueInfo(%q) = %d, %d, want %d", x, end, long, got)
		}
		if end > 0 && !spaced {
			var c bytes.Buffer
			if json.Compact(&c, []byte(x[:end])) != nil || c.Len() != end {
				t.Fatalf("ValidateValueInfo(%q) not spaced", x)
			}
		}
	}
	if !HaveAVX2 || len(s) < 64 || len(s) > 1000 || !json.Valid([]byte(s)) {
		return
	}
	// A compact value is not spaced.
	var c bytes.Buffer
	json.Compact(&c, []byte(s))
	x := c.String() + " , "
	if end, spaced, _ := ValidateValueInfo(x, 10000, &scratchTables); end != c.Len() || spaced && len(x) >= 64 {
		t.Fatalf("ValidateValueInfo(%q) = %d, %v", x, end, spaced)
	}
}

// checkCount compares CountElements with a scan byte by byte, from the
// starts of elements and members of s and the ends of its opening
// brackets, outside strings.
func checkCount(t *testing.T, s string, valid bool) {
	t.Helper()
	if !HaveAVX2 {
		return
	}
	inStr, esc, tested := false, false, 0
	for j := 1; j < len(s) && tested < 300; j++ {
		c := s[j-1]
		switch {
		case esc:
			esc = false
		case inStr:
			inStr, esc = c != '"', c == '\\'
		case c == '"':
			inStr = true
		case c == '[' || c == '{' || c == ',':
			tested++
			for depth := range 3 {
				n, end := CountElements(s[j:], depth, 0)
				if valid {
					wn, wend := countRef(s[j:], depth)
					if n != wn || end != wend {
						t.Fatalf("CountElements(%q, %d) = %d, %d, want %d, %d", s[j:], depth, n, end, wn, wend)
					}
					if n, _ := CountElements(s[j:], depth, 100); n != wn && n != -1 {
						t.Fatalf("CountElements(%q, %d, 100) = %d, want %d", s[j:], depth, n, wn)
					}
				} else if n != -1 && s[j+end] != ']' && s[j+end] != '}' {
					t.Fatalf("CountElements(%q, %d) ends at %d, not a bracket", s[j:], depth, end)
				}
			}
		}
	}
}

func countRef(s string, depth int) (int, int) {
	n := 1
	for j := 0; j < len(s); j++ {
		switch s[j] {
		case '"':
			for j++; j < len(s) && s[j] != '"'; j++ {
				if s[j] == '\\' {
					j++
				}
			}
		case '[', '{':
			depth++
		case ']', '}':
			if depth == 0 {
				return n, j
			}
			depth--
		case ',':
			if depth == 0 {
				n++
			}
		}
	}
	return -1, len(s)
}
