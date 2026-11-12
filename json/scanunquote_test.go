//go:build go1.26

package json

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// scanPieces are the parts the strings of TestScanUnquote are made of.
var scanPieces = []string{
	"a", "plain text ", "0123456789abcdef0123456789abcdef", "é", "東京", "\U0001F600",
	`\n`, `\"`, `\\`, `\/`, `\b`, `\f`, `\r`, `\t`, `é`, `A`, `😀`,
	`\uD83D`, `\uDE00`, `\uD83Dx`, `\uD83DA`, `\uD83D\uD83D`, `￿`, `\u0000`,
	"\xff", "\xc3", "\xe6\x9d", "\xed\xa0\x80", "\x80abc",
}

// scanBad are the pieces that make a string invalid.
var scanBad = []string{"\x01", "\x1f", `\x`, `\u12`, `\u12g4`, `\`, `\U0041`, "\n"}

func TestScanUnquote(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	check := func(s string) {
		t.Helper()
		want, wflags := jsonsimd.ScanString(s, 1)
		i, hi := jsonsimd.PlainRun(s, 1, 0)
		out, end, flags := scanUnquote([]byte("pre"), s, 1, i, hi)
		if end != want || flags&^spanUnquoted != wflags {
			t.Fatalf("scanUnquote(%.80q) = %d, %#x, want %d, %#x", s, end, flags, want, wflags)
		}
		if end < 0 {
			return
		}
		switch {
		case flags&spanUnquoted != 0:
			if v := unquote([]byte("pre"), s[1:end]); !bytes.Equal(out, v) {
				t.Fatalf("scanUnquote(%.80q) value %q, want %q", s, out, v)
			}
		case flags&jsonsimd.SpanEscaped != 0 && end-1 < unquoteMax && (utf8.ValidString(s[1:end]) || !jsonsimd.HaveAVX2):
			// With the kernel, a string that is not valid UTF-8 is left
			// to unquote.
			t.Fatalf("scanUnquote(%.80q) did not decode a short string", s)
		}
	}
	for range 40000 {
		var b strings.Builder
		b.WriteByte('"')
		n := r.Intn(12)
		if r.Intn(8) == 0 {
			n = 80 + r.Intn(300)
		}
		for range n {
			b.WriteString(scanPieces[r.Intn(len(scanPieces))])
		}
		if r.Intn(6) == 0 {
			b.WriteString(scanBad[r.Intn(len(scanBad))])
		}
		if r.Intn(10) != 0 {
			b.WriteByte('"')
		}
		if r.Intn(2) == 0 {
			b.WriteString(`,"x":1}`)
		}
		check(b.String())
	}
	// Around unquoteMax and the 8- and 32-byte steps of the scan.
	for _, n := range []int{0, 1, 7, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, 65, unquoteMax - 3, unquoteMax - 2, unquoteMax - 1, unquoteMax, unquoteMax + 1, 3 * unquoteMax} {
		for _, esc := range []string{`\n`, `é`, `😀`, "é"} {
			body := strings.Repeat("x", n)
			check(`"` + esc + body + `"`)
			check(`"` + body + esc + `"`)
			check(`"` + body + esc + body + `"`)
			check(`"` + body + esc)
		}
	}
}

func TestScanUnquoteDecode(t *testing.T) {
	type msg struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
		Name    *string
		Tags    []string
	}
	r := rand.New(rand.NewSource(6))
	for range 3000 {
		var b strings.Builder
		b.WriteString(`[`)
		for k := range 1 + r.Intn(4) {
			if k > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`{"role":"`)
			for range r.Intn(4) {
				b.WriteString(scanPieces[r.Intn(len(scanPieces))])
			}
			b.WriteString(`","content":"`)
			for range r.Intn(40) {
				b.WriteString(scanPieces[r.Intn(len(scanPieces))])
			}
			if r.Intn(20) == 0 {
				b.WriteString(scanBad[r.Intn(len(scanBad))])
			}
			b.WriteString(`","Name":"a\tb","Tags":["été","x\"y"]}`)
		}
		b.WriteString(`]`)
		doc := b.String()
		checkUnmarshal[[]msg](t, doc)
		checkUnmarshal[[]map[string]any](t, doc)
		checkUnmarshal[any](t, doc)
	}
}
