//go:build go1.26

package json

import (
	"bytes"
	stdjson "encoding/json"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

// These tests compare long strings, which the AVX2 kernels escape and
// unescape, with encoding/json.

// escapedPieces make up JSON string contents; %u stands for a backslash
// and a u.
var escapedPieces = func() []string {
	p := []string{
		"plain text ", "0123456789abcdef0123456789abcdef", "é", "東京", "😀", "“", "<a&b>",
		`\n`, `\"`, `\\`, `\/`, `\b`, `\f`, `\r`, `\t`, `\n\n`,
		"%u00e9", "%u0041", "%uD83D%uDE00", "%uD83D", "%uDE00x", "%uD83D%u0041", "%u2028", "%uFFFF", "%u0000",
		"\xff", "\xc3", "\xe6\x9d", "\xed\xa0\x80", "\x80abc", "\xe2\x80\xa8", "\xa9\xa9\xa9\xa9\xa9\xa9\xa9\xa9",
	}
	for i := range p {
		p[i] = strings.ReplaceAll(p[i], "%u", string(rune(92))+"u")
	}
	return p
}()

// rawPieces make up Go strings to encode.
var rawPieces = []string{
	"plain text ", "0123456789abcdef0123456789abcdef", "é", "東京", "😀", "“",
	"\"", "\\", "\n", "\t", "\r", "\b", "\f", "\x00", "\x1f", "\x7f", "<", ">", "&", "/",
	"\xe2\x80\xa8", "\xe2\x80\xa9", "\xff", "\xc3", "\xed\xa0\x80", "\x80abc",
}

// kernelText returns pieces drawn from pieces, mostly enough for the kernels
// and now and then past unquoteBlocksMax; with clean, only pieces of valid
// UTF-8 without line separators.
func kernelText(r *rand.Rand, pieces []string, clean bool) string {
	n := r.Intn(400)
	if r.Intn(40) == 0 {
		n = 4000 + r.Intn(4000)
	}
	var b strings.Builder
	b.WriteString(strings.Repeat("x", r.Intn(70)))
	for range n {
		p := pieces[r.Intn(len(pieces))]
		if clean && (!utf8.ValidString(p) || strings.Contains(p, "\xe2\x80\xa8") || strings.Contains(p, "\xe2\x80\xa9")) {
			// Valid UTF-8 without line separators, which the encoding
			// kernel takes.
			continue
		}
		b.WriteString(p)
	}
	return b.String()
}

func TestLongStringsUnmarshal(t *testing.T) {
	type msg struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
		Text    string `json:"text"`
	}
	r := rand.New(rand.NewSource(7))
	reps := 600
	if raceEnabled {
		reps = 60
	}
	var all []string
	for k := range reps {
		q := `"` + kernelText(r, escapedPieces, k%3 == 0) + `"`
		all = append(all, q)
		checkUnmarshal[string](t, q)
		doc := `{"role":"user","content":` + q + `,"text":` + q + `}`
		checkUnmarshal[msg](t, doc)
		checkUnmarshal[any](t, doc)
		checkUnmarshal[map[string]string](t, doc)
	}
	// Long runs of bytes that are not valid UTF-8.
	for n := 60; n < 140; n++ {
		checkUnmarshal[string](t, `"`+strings.Repeat("\xa9", n)+`"`)
		checkUnmarshal[string](t, `"`+strings.Repeat("\xa9", n)+"\xe1\xe1\xe1\xe1\xe1\xa9\xc3\xc3\xc3\xc3\xc3\xc3"+`"`)
		checkUnmarshal[string](t, `"`+strings.Repeat("\xe4\xb8", n/2)+`"`)
	}
	// More than 64 KiB: the decoder validates first and keeps the strings.
	doc := "[" + strings.Join(all, ",") + "]"
	checkUnmarshal[[]string](t, doc)
	checkUnmarshal[[]any](t, doc)
}

func TestLongStringsMarshal(t *testing.T) {
	r := rand.New(rand.NewSource(8))
	reps := 600
	if raceEnabled {
		reps = 60
	}
	for k := range reps {
		s := kernelText(r, rawPieces, k%3 == 0)
		v := map[string]any{"s": s, "l": []string{s, s[:len(s)/2]}}
		got, gerr := Marshal(v)
		want, werr := stdjson.Marshal(v)
		if !sameError(gerr, werr) || !bytes.Equal(got, want) {
			t.Fatalf("Marshal of %.200q:\n got %.300s %v\nwant %.300s %v", s, got, gerr, want, werr)
		}
		var gb, wb bytes.Buffer
		enc, wenc := NewEncoder(&gb), stdjson.NewEncoder(&wb)
		enc.SetEscapeHTML(false)
		wenc.SetEscapeHTML(false)
		gerr, werr = enc.Encode(v), wenc.Encode(v)
		if !sameError(gerr, werr) || !bytes.Equal(gb.Bytes(), wb.Bytes()) {
			t.Fatalf("Encode without HTML escaping of %.200q:\n got %.300s\nwant %.300s", s, gb.Bytes(), wb.Bytes())
		}
	}
}

// TestEscapedStrings compares strings that the encoder escapes as it writes
// the output, among them long strings that start with plain ASCII, with
// encoding/json.
func TestEscapedStrings(t *testing.T) {
	type pair struct {
		A string
		N int
	}
	pieces := []string{`"`, "\n", "\x01", "<", "&", "é", "\xff", "\u2028", "\x7f", "😀"}
	for _, p := range []int{0, 255, 256, 4096, 20000} {
		for _, piece := range pieces {
			for _, q := range []int{0, 1, 31, 32, 100, 5000} {
				s := strings.Repeat("a", p) + piece + strings.Repeat("b", q)
				for _, v := range []any{s, pair{s, 1}, []string{s, s, "x"}} {
					got, gerr := Marshal(v)
					want, werr := stdjson.Marshal(v)
					if !sameError(gerr, werr) || !bytes.Equal(got, want) {
						t.Fatalf("Marshal(%T) of %d+%q+%d:\n got %.300s\nwant %.300s", v, p, piece, q, got, want)
					}
					got, gerr = MarshalIndent(v, "", " ")
					want, werr = stdjson.MarshalIndent(v, "", " ")
					if !sameError(gerr, werr) || !bytes.Equal(got, want) {
						t.Fatalf("MarshalIndent(%T) of %d+%q+%d:\n got %.300s\nwant %.300s", v, p, piece, q, got, want)
					}
					var gb, wb bytes.Buffer
					enc, wenc := NewEncoder(&gb), stdjson.NewEncoder(&wb)
					enc.SetEscapeHTML(p%2 == 0)
					wenc.SetEscapeHTML(p%2 == 0)
					gerr, werr = enc.Encode(v), wenc.Encode(v)
					if !sameError(gerr, werr) || !bytes.Equal(gb.Bytes(), wb.Bytes()) {
						t.Fatalf("Encode(%T) of %d+%q+%d:\n got %.300s\nwant %.300s", v, p, piece, q, gb.Bytes(), wb.Bytes())
					}
				}
			}
		}
	}
}
