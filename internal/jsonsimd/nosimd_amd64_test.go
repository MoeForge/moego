//go:build amd64 && !purego

package jsonsimd

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// kernelDocs returns documents whose strings, containers and whitespace
// span many 64-byte blocks, valid, changed and cut.
func kernelDocs(r *rand.Rand) []string {
	var docs []string
	for i := range randomIterations(1500) {
		doc := genValue(r, nil, 5)
		if i%4 != 0 {
			// An array of values, most of them past 64 bytes.
			doc = genValue(r, []byte{'['}, 5)
			for range 4 + r.Intn(30) {
				doc = genValue(r, append(doc, ','), 5)
			}
			doc = append(doc, ']')
		}
		docs = append(docs, string(doc), string(mutate(r, doc)), string(doc[:r.Intn(len(doc)+1)]))
	}
	long := strings.Repeat("abcdefghijklmnopqrstuvwxyz012345", 64)
	for _, s := range []string{
		`"` + long + `"`, `["` + long + `\u00e9` + long + `", "` + long + "\xff" + `"]`,
		`{"a":"` + long + `<&>` + long + "\u2028" + `"}`, `{"a":"` + long + "\x01" + `"}`,
		strings.Repeat(" ", 200) + `[` + strings.Repeat(`1, `, 3000) + `2]` + strings.Repeat("\n", 100),
		strings.Repeat("[", 64) + strings.Repeat("]", 64), strings.Repeat(`{"a":[`, 100) + strings.Repeat("]}", 100),
		strings.Repeat("[", 5000) + strings.Repeat("]", 5000), strings.Repeat("[", 5000) + strings.Repeat("]", 4999),
		`{"k":` + strings.Repeat(`"\\\"",`, 100) + `0}`, `["` + strings.Repeat(`\\`, 65) + `"]`,
	} {
		docs = append(docs, s, s[:len(s)/2])
	}
	return docs
}

// kernelResults returns what the checks and scans give on s, one line per
// result: those whose results are the same with the block kernels and
// without them, for any input, or for valid input.
func kernelResults(s string) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	for _, d := range []int{0, 3, 64, 10000} {
		add("Validate %d %v", d, Validate(s, d, nil))
	}
	var tab Tables
	ok := Validate(s, 10000, &tab)
	add("Validate tables %v", ok)
	if ok {
		add("tables %+v", norm(tab, len(s)))
	}
	for _, html := range []bool{false, true} {
		ok, spaced, h := ValidateSpaced(s, 10000, html)
		add("ValidateSpaced %v %v", html, ok)
		if ok {
			add("spaced %v html %v", spaced, h)
		}
	}
	for _, depth := range []int{1, 3} {
		kinds := []uint64{0b101}
		add("ValidateRest %d %v", depth, ValidateRest(s, kinds, depth, 10000, &scratchTables))
		ok, _, _ := ValidateRestEnds(s, kinds, depth, 10000, &scratchTables)
		add("ValidateRestEnds %d %v", depth, ok)
	}
	add("HasHTML %v CheckUTF8 %d", HasHTML(s), CheckUTF8(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '"' {
			continue
		}
		end, flags := ScanString(s, i+1)
		run, hi := PlainRun(s, i+1, 0)
		add("at %d: PlainEnd %d StringEnd %d ScanString %d %d PlainRun %d %v", i, PlainEnd(s, i+1), StringEnd(s, i+1), end, flags, run, hi&0x8080808080808080 != 0)
	}
	dst := make([]byte, len(s))
	CopyLong(dst, s)
	add("CopyLong %v", string(dst) == s)
	return out
}

// prefixResult is ValidatePrefix, ValidateValue or ValidateValueInfo on s.
// Each may give PrefixInvalid for a value that a malformed string, number,
// literal or escape after it makes it give up on, which the block kernels
// and the scalar loop may read differently. ValidateValue and
// ValidateValueInfo take arrays and objects only, which is all their
// callers pass them; for other s they are left out.
type prefixResult struct {
	prefix, value, info int
	tables              Tables
}

func prefixResults(s string) prefixResult {
	var r prefixResult
	r.prefix = ValidatePrefix(s, 10000, &r.tables)
	if r.prefix >= 0 {
		r.tables = norm(r.tables, r.prefix)
	}
	r.value, r.info = PrefixInvalid, PrefixInvalid
	if s != "" && (s[0] == '{' || s[0] == '[') {
		r.value = ValidateValue(s, 10000, &scratchTables)
		r.info, _, _ = ValidateValueInfo(s, 10000, nil)
	}
	return r
}

// samePrefix reports whether a and b, results of the same function on s,
// agree: they are equal, or one is PrefixInvalid and the other an end
// with more than whitespace after it, or PrefixIncomplete.
func samePrefix(s string, a, b int) bool {
	if a == b {
		return true
	}
	if a != PrefixInvalid {
		a, b = b, a
	}
	return a == PrefixInvalid && (b == PrefixIncomplete || b >= 0 && strings.TrimLeft(s[b:], " \t\r\n") != "")
}

// TestKernelsOff compares what the checks and scans of the package give
// with the AVX2 kernels and without them, as on CPUs without AVX2 or AVX.
// It leaves out what the kernels alone give, such as EscapedLen, and
// CountElements and AppendCompact, which need AVX2: their callers check
// for it.
func TestKernelsOff(t *testing.T) {
	if !HaveAVX2 {
		t.Skip("no AVX2")
	}
	docs, long := kernelDocs(rand.New(rand.NewSource(20261113))), 0
	for _, s := range docs {
		if len(s) >= 64 {
			long++
		}
	}
	if long < len(docs)/2 {
		t.Fatalf("%d of %d documents reach the block kernels", long, len(docs))
	}
	for _, s := range docs {
		want, wantPrefix := kernelResults(s), prefixResults(s)
		for _, drop := range []string{"avx2", "avx"} {
			undo := LimitCPU(drop)
			got, gotPrefix := kernelResults(s), prefixResults(s)
			undo()
			for k := range max(len(got), len(want)) {
				if k >= len(got) || k >= len(want) || got[k] != want[k] {
					t.Fatalf("%.200q without %s:\n got %.300v\nwant %.300v", s, drop, got[min(k, len(got)-1)], want[min(k, len(want)-1)])
				}
			}
			g, w := gotPrefix, wantPrefix
			if !samePrefix(s, g.prefix, w.prefix) || !samePrefix(s, g.value, w.value) || !samePrefix(s, g.info, w.info) {
				t.Fatalf("%.200q without %s: prefix, value, info %d %d %d, want %d %d %d", s, drop, g.prefix, g.value, g.info, w.prefix, w.value, w.info)
			}
			if g.prefix >= 0 && g.prefix == w.prefix && fmt.Sprint(g.tables) != fmt.Sprint(w.tables) {
				t.Fatalf("%.200q without %s: prefix tables\n got %+v\nwant %+v", s, drop, g.tables, w.tables)
			}
		}
	}
}

// TestLimitCPU checks what LimitCPU turns off, from what is in use, and
// that the function it returns turns it back on. Without AVX, the AES
// instructions still hash in their SSE encodings.
func TestLimitCPU(t *testing.T) {
	avx2, fast := HaveAVX2, FastSum()
	for _, c := range []struct {
		drop       string
		avx2, fast bool
	}{
		{"", avx2, fast}, {"avx2", false, fast}, {"avx", false, fast}, {"aes", avx2, false},
		{"avx,aes", false, false}, {" aes , avx2 ", false, false},
	} {
		undo := LimitCPU(c.drop)
		if HaveAVX2 != c.avx2 || FastSum() != c.fast {
			t.Errorf("LimitCPU(%q): AVX2 kernels %v, AES hash %v; want %v, %v", c.drop, HaveAVX2, FastSum(), c.avx2, c.fast)
		}
		undo()
		if HaveAVX2 != avx2 || FastSum() != fast {
			t.Fatalf("undo of LimitCPU(%q) did not restore the kernels", c.drop)
		}
	}
	// Calls nest: an inner call keeps what an outer one dropped.
	outer := LimitCPU("aes")
	inner := LimitCPU("avx2")
	if HaveAVX2 || FastSum() {
		t.Errorf("LimitCPU(\"avx2\") inside LimitCPU(\"aes\"): AVX2 kernels %v, AES hash %v; want false, false", HaveAVX2, FastSum())
	}
	inner()
	if HaveAVX2 != avx2 || FastSum() {
		t.Errorf("after the inner undo: AVX2 kernels %v, AES hash %v; want %v, false", HaveAVX2, FastSum(), avx2)
	}
	outer()
	if HaveAVX2 != avx2 || FastSum() != fast {
		t.Fatal("the outer undo did not restore the kernels")
	}
	defer func() {
		if recover() == nil {
			t.Error("LimitCPU of an unknown feature did not panic")
		}
		if HaveAVX2 != avx2 || FastSum() != fast {
			t.Error("LimitCPU of an unknown feature changed the kernels")
		}
	}()
	LimitCPU("avx2,sse5")
}
