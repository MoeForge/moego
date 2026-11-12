//go:build go1.26

package json

import (
	"bytes"
	stdjson "encoding/json"
	"math/rand"
	"strings"
	"testing"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// TestIndentChecked compares the one-pass indent with validation followed
// by appendIndentValid, and Indent with encoding/json's, on valid and
// malformed documents.
func TestIndentChecked(t *testing.T) {
	r := rand.New(rand.NewSource(31))
	pres := []string{"", ">", "\t", "  -- "}
	inds := []string{"", "  ", "\t", strings.Repeat("x", 20)}
	for n := 0; n < randomIterations(20000); n++ {
		var sb strings.Builder
		sb.WriteString([]string{"", " ", "\n\t"}[r.Intn(3)])
		randomDoc(r, &sb, r.Intn(6))
		sb.WriteString([]string{"", " ", "\n", " \r\n\t"}[r.Intn(4)])
		b := []byte(sb.String())
		switch r.Intn(4) {
		case 0:
			b = smallMutate(r, b)
		case 1:
			k := 1 + r.Intn(300)
			b = []byte(strings.Repeat("[", k) + sb.String() + strings.Repeat("]", k))
		}
		pre, ind := pres[r.Intn(len(pres))], inds[r.Intn(len(inds))]
		checkIndent(t, b, pre, ind)
	}
}

func checkIndent(t *testing.T, b []byte, pre, ind string) {
	t.Helper()
	valid := jsonsimd.Validate(string(b), maxNestingDepth, nil)
	head := []byte("head")
	got, ok := appendIndentChecked(head, b, pre, ind)
	if ok && !valid {
		t.Fatalf("appendIndentChecked(%q) accepts input that is not valid", b)
	}
	if ok {
		if want := appendIndentValid([]byte("head"), b, pre, ind); !bytes.Equal(got, want) {
			t.Fatalf("appendIndentChecked(%q, %q, %q) = %q, want %q", b, pre, ind, got, want)
		}
	} else if string(got) != "head" {
		t.Fatalf("appendIndentChecked(%q) = %q after failing", b, got)
	}
	var w, sw bytes.Buffer
	err, serr := Indent(&w, b, pre, ind), stdjson.Indent(&sw, b, pre, ind)
	if (err == nil) != (serr == nil) || err != nil && err.Error() != serr.Error() || !bytes.Equal(w.Bytes(), sw.Bytes()) {
		t.Fatalf("Indent(%q, %q, %q) = %q, %v, want %q, %v", b, pre, ind, w.Bytes(), err, sw.Bytes(), serr)
	}
}

// smallMutate changes a few bytes of b.
func smallMutate(r *rand.Rand, b []byte) []byte {
	b = append([]byte(nil), b...)
	for n := 1 + r.Intn(3); n > 0 && len(b) > 0; n-- {
		k := r.Intn(len(b))
		switch r.Intn(4) {
		case 0:
			const set = "{}[]:,\"\\ 0a-e.tfnu\x01"
			b[k] = set[r.Intn(len(set))]
		case 1:
			b = append(b[:k], b[k+1:]...)
		case 2:
			b = append(b[:k], append([]byte{byte(r.Intn(256))}, b[k:]...)...)
		default:
			b = b[:k]
		}
	}
	return b
}
