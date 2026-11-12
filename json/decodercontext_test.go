//go:build go1.26

package json

import (
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

// TestDecoderValueContexts compares Token and Decode, after tokens that
// leave the Decoder at each kind of place, on values that are complete,
// cut short or wrong, with encoding/json.
func TestDecoderValueContexts(t *testing.T) {
	values := []string{
		`[1,]`, `[x`, `[1 2]`, `{x`, `{"a" 1}`, `{"a":}`, `[}`, `{]`, `[1,,`, `{"a":1,}`, `[1:2]`, `{"a":1]`,
		`[[1,]]`, `{"a":[}`, `[tru]`, `[1.]`, `[1 `, `{"a":1 `, `[]`, `{}`, `[`, `{`, `[[`, `{"a"`, `[1]`,
		`{"a":1}`, `[1,`, `{"a":1,`, `[]]`, `{}}`, `[}]`, `{"a":]}`, `tru`, `true`, `1.`, `"a`,
	}
	places := []struct {
		prefix string
		tokens int
	}{{`{`, 1}, {`{"k":1,`, 3}, {`{"k":`, 2}, {`[`, 1}, {`[1,`, 2}, {``, 0}, {`{"k":1`, 3}}
	for _, v := range values {
		for _, p := range places {
			for _, token := range []bool{false, true} {
				in := p.prefix + v
				gd, wd := NewDecoder(strings.NewReader(in)), stdjson.NewDecoder(strings.NewReader(in))
				for range p.tokens {
					gd.Token()
					wd.Token()
				}
				var gerr, werr error
				if token {
					_, gerr = gd.Token()
					_, werr = wd.Token()
				} else {
					var g, w any
					gerr, werr = gd.Decode(&g), wd.Decode(&w)
				}
				if !sameError(gerr, werr) || gd.InputOffset() != wd.InputOffset() {
					t.Errorf("%q after %d tokens, Token %v: got %v at %d, want %v at %d", in, p.tokens, token, describe(gerr), gd.InputOffset(), describe(werr), wd.InputOffset())
				}
			}
		}
	}
}

// TestDecoderSeparatorReads compares how far Token and Decode read, given
// one byte at a time, before they report a comma or colon out of place,
// with encoding/json.
func TestDecoderSeparatorReads(t *testing.T) {
	for _, in := range []string{
		`{ ,00`, `{ , 1 2`, `{ ,]`, `{ , `, `{ ,`, `[ :0`, ` ,0`, ` , `, `[ ,,00 `, `{"a" ,1 `, `{"a" , `,
		`[1 ,,00 `, `{"a": ,00 `, `{"a":1,,00 `, `{"a":1 ::00 `, `[1 :1 `, `[1 :]`, `{"a":1 :}`,
	} {
		for _, token := range []bool{false, true} {
			gd := NewDecoder(iotest.OneByteReader(strings.NewReader(in)))
			wd := stdjson.NewDecoder(iotest.OneByteReader(strings.NewReader(in)))
			var gerr, werr error
			for k := 0; gerr == nil && werr == nil && k < 6; k++ {
				if token || k == 0 {
					_, gerr = gd.Token()
					_, werr = wd.Token()
				} else {
					var g, w any
					gerr, werr = gd.Decode(&g), wd.Decode(&w)
				}
			}
			gb, _ := io.ReadAll(gd.Buffered())
			wb, _ := io.ReadAll(wd.Buffered())
			if !sameError(gerr, werr) || gd.InputOffset() != wd.InputOffset() || string(gb) != string(wb) {
				t.Errorf("%q, Token %v: got %v at %d with %q buffered, want %v at %d with %q", in, token, describe(gerr), gd.InputOffset(), gb, describe(werr), wd.InputOffset(), wb)
			}
		}
	}
}

// TestDecoderKeptErrors compares the errors of sequences of Token (T),
// Decode (D) and More (M) calls, which may keep an error for the calls
// after, with encoding/json, and what is buffered after them.
func TestDecoderKeptErrors(t *testing.T) {
	for _, c := range []struct{ in, ops string }{
		{`{"a":}`, "TTTTDT"}, {`{"a":}`, "TTTDTD"}, {`{"a":}`, "TTDTT"}, {`{"a" 1}`, "TTTDT"}, {`[1 2]`, "TTTDT"},
		{`[}`, "TTDT"}, {`{]`, "TTDT"}, {`{"a":1]`, "TTTTDT"}, {`[1,]`, "TTTDT"}, {`{"a":1,}`, "TTTTDT"},
		{`{1:2}`, "TTDT"}, {`[x]`, "TDTT"}, {`{{`, "TTDTD"}, {`{[`, "TTDTD"}, {`{"a":1,{`, "TTTTDTD"}, {`{{}`, "TTDT"},
		{`["" ,`, "TTDMMD"}, {`[""  ,  `, "TTDMT"}, {`["" ,`, "TTTMMT"}, {`{"a" : `, "TTTMT"}, {`{"a":1 ,`, "TTTDMT"}, {`["" ,`, "TTDTM"},
	} {
		gd, wd := NewDecoder(strings.NewReader(c.in)), stdjson.NewDecoder(strings.NewReader(c.in))
		for k, op := range c.ops {
			var gerr, werr error
			switch op {
			case 'T':
				_, gerr = gd.Token()
				_, werr = wd.Token()
			case 'D':
				var g, w any
				gerr, werr = gd.Decode(&g), wd.Decode(&w)
			default:
				if g, w := gd.More(), wd.More(); g != w {
					gerr = fmt.Errorf("More %v, want %v", g, w)
				}
			}
			gb, _ := io.ReadAll(gd.Buffered())
			wb, _ := io.ReadAll(wd.Buffered())
			if !sameError(gerr, werr) || gd.InputOffset() != wd.InputOffset() || string(gb) != string(wb) {
				t.Errorf("%q, %s, call %d: got %v at %d with %q buffered, want %v at %d with %q", c.in, c.ops, k, describe(gerr), gd.InputOffset(), gb, describe(werr), wd.InputOffset(), wb)
				break
			}
		}
	}
}

// scriptReader reads its parts in turn; a part "!" is a read that fails
// with errScript and gives no data, and a part "$" one that gives io.EOF
// before the parts after it.
type scriptReader struct{ parts []string }

var errScript = errors.New("script")

func (s *scriptReader) Read(b []byte) (int, error) {
	if len(s.parts) == 0 {
		return 0, io.EOF
	}
	p := s.parts[0]
	if p == "!" {
		s.parts = s.parts[1:]
		return 0, errScript
	}
	if p == "$" {
		s.parts = s.parts[1:]
		return 0, io.EOF
	}
	n := copy(b, p)
	if n == len(p) {
		s.parts = s.parts[1:]
	} else {
		s.parts[0] = p[n:]
	}
	return n, nil
}

// TestDecoderPeekReads compares what Token (T), Decode (D), Decode into a
// string (S) or an int (I) and More (M) read past a comma or colon when a
// read there fails, and what the calls after report, with encoding/json.
func TestDecoderPeekReads(t *testing.T) {
	testScriptReads(t, []struct{ parts, ops string }{
		{" ,|!| 0", "MT"}, {" ,|!| 0", "TT"}, {" ,|!| 0", "MM"}, {" ,|!| 0", "MD"}, {" ,|!| 0", "TD"}, {" ,|!|!| 0", "MTT"},
		{"[1 :|!| 2]", "TTMT"}, {"[1 :|!| 2]", "TTT"}, {"[1 :|!| 2]", "TTTT"}, {"[1 ,|!| 2]", "TTMT"}, {"[1 ,|!| 2]", "TTTT"},
		{`{"a" :|!| 1}`, "TTMT"}, {`{"a" ,|!| 1}`, "TTMT"}, {`{"a" ,|!| 1}`, "TTTT"}, {" |!| 0", "MT"}, {"[1|!|]", "TTMT"},
		{" ,|!| 0", "DT"}, {" ,|!| 0", "DD"}, {" ,|!| 0", "MDT"}, {" ,|!| 0", "DTD"}, {" ,|!|!| 0", "TDT"}, {" ,|!|!| 0", "TDD"},
		{" ,|!|!| 0", "TTD"}, {" ,|!|!| 0", "DMT"}, {"[1 :|!| 2]", "TTDT"}, {`{"a" ,|!| 1}`, "TTDT"}, {`{"a" ,|!|!| 1}`, "TTTDT"},
		{"[0,   |!|}", "TSTTD"}, {"[0, |!| ]", "TSTT"}, {"[0 ,|!|]", "TTTDT"}, {"[0,|!|]", "TTMTD"}, {"[0, |!|!| ]", "TSTTT"},
		{"[0,|!|}", "TSDT"}, {"[0,|!|]", "TSMDT"}, {`{"a":1,|!| }`, "TTTTT"}, {`{"a":1 ,|!|]`, "TTTDT"}, {`{"a":|!| }`, "TTTT"},
		{`{"a":|!|]`, "TTTD"}, {"[0,|!| ,1]", "TSTT"}, {"[[0,|!|]]", "TTSTTT"}, {"[0,|!| 1]", "TSTT"}, {"[0, |!| ]", "TSTMT"},
		{"[0, |!| ]", "TSTMD"}, {"[0, |!| ]", "TTTMMT"},
		{"[0:|!|]", "TTTDT"}, {"[0:|!|]", "TTTTI"}, {"[0: |!|}", "TTMMT"}, {"[0: |!|}", "TTMMI"}, {"[0 :|!|]", "TTMT"},
		{"[0:|!| 1]", "TTTD"}, {`{"a":1:|!| }`, "TTTTDT"}, {`{"a":1 :|!|}`, "TTTMMT"}, {"[[0] :|!|}", "TTTTMMI"},
		{"[0," + strings.Repeat(" ", 44) + "100000000000000: |!|}", "TSTTIT"},
	})
}

// TestDecoderReadsAfterEOF compares the calls with encoding/json when the
// reader gives io.EOF and then more: data, an error or io.EOF again. A
// number that the end of the input ends in an array or object is read on.
func TestDecoderReadsAfterEOF(t *testing.T) {
	testScriptReads(t, []struct{ parts, ops string }{
		{"[0|$|!", "S"}, {"[1|$|!", "D"}, {"[0|$|!", "IT"}, {"[0|$|1", "D"}, {"[0|$|1", "TT"}, {"[0|$|:", "D"},
		{"[0|$|,:", "D"}, {"[0|$|,,", "I"}, {"[1|$|e400,2]", "I"}, {"[0|$| ", "S"}, {"[0|$|,", "D"},
		{"[0|$|,1]", "D"}, {"[0|$|]", "DD"}, {"[1.5|$|]", "D"}, {"[1.|$|5]", "D"}, {"[-|$|1]", "D"},
		{"[1e|$|5]", "D"}, {"[1e5|$|0]", "D"}, {"[[0|$|!|]", "S"}, {"[[0|$|]]", "D"}, {`{"a":1|$|:`, "D"},
		{`{"a":1|$|}`, "D"}, {`{"x":1e400|$|!`, "D"}, {`{"a":[0|$|!`, "D"}, {"[0|$|$|]", "DD"},
		{"{1|$|!", "TTT"}, {"{1|$|!|!", "TTTT"}, {"{1|$|2|$|!", "TTTT"}, {"{1|$|x", "TTT"}, {"{1|$|!", "TTD"},
		{"{1|$|!", "TMT"}, {"{-|$|1", "TTT"}, {"{1|$|!", "TD"},
		{"$|:", "SMMM"}, {"$|,", "SMD"}, {"$|]", "SMM"}, {"$|x", "SMT"}, {"$| 1", "SMD"}, {"$|\x00", "SM"},
		{"$|:", "MM"}, {"1|$|:", "DMM"}, {"1|$|2", "DDM"},
		{"[|!|$|,", "TTTMD"}, {"{|$|:", "TTTSD"}, {"[|$|,", "TTTM"}, {"[|$| ,", "TTMT"}, {"[|$|  :", "TTT"},
		{"[1,|$|]", "TTTT"}, {"[1,|$|2]", "TTTM"}, {"[1,|$|2]", "TTTT"}, {"[1 ,|$| ]", "TTTMT"},
		{`{"a"|$|:1}`, "TTTT"}, {`{"a":|$| 1}`, "TTTMT"}, {"[1|$|$|]", "TTTT"}, {"[1|$|!|]", "TTTT"},
	})
}

func testScriptReads(t *testing.T, cases []struct{ parts, ops string }) {
	for _, c := range cases {
		gd := NewDecoder(&scriptReader{strings.Split(c.parts, "|")})
		wd := stdjson.NewDecoder(&scriptReader{strings.Split(c.parts, "|")})
		for k, op := range c.ops {
			var gerr, werr error
			switch op {
			case 'T':
				_, gerr = gd.Token()
				_, werr = wd.Token()
			case 'D':
				var g, w any
				gerr, werr = gd.Decode(&g), wd.Decode(&w)
			case 'S':
				var g, w string
				gerr, werr = gd.Decode(&g), wd.Decode(&w)
			case 'I':
				var g, w int
				gerr, werr = gd.Decode(&g), wd.Decode(&w)
			default:
				if g, w := gd.More(), wd.More(); g != w {
					gerr = fmt.Errorf("More %v, want %v", g, w)
				}
			}
			gb, _ := io.ReadAll(gd.Buffered())
			wb, _ := io.ReadAll(wd.Buffered())
			if !sameError(gerr, werr) || gd.InputOffset() != wd.InputOffset() || string(gb) != string(wb) {
				t.Errorf("%q, %s, call %d: got %v at %d with %q buffered, want %v at %d with %q", c.parts, c.ops, k, describe(gerr), gd.InputOffset(), gb, describe(werr), wd.InputOffset(), wb)
				break
			}
		}
	}
}
