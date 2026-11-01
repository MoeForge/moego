package ojson

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestProbeReplay replays recorded gjson outputs ("get" lines from the
// probe tool) from the directory in OJSON_PROBE.
func TestProbeReplay(t *testing.T) {
	dir := os.Getenv("OJSON_PROBE")
	if dir == "" {
		t.Skip("OJSON_PROBE not set")
	}
	// The custom modifiers the probe tool registers.
	AddModifier("case", func(json, arg string) string {
		switch arg {
		case "upper":
			return strings.ToUpper(json)
		case "lower":
			return strings.ToLower(json)
		}
		return json
	})
	AddModifier("echo", func(json, arg string) string {
		return "[" + fmt.Sprintf("%q", json) + "," + fmt.Sprintf("%q", arg) + "]"
	})
	AddModifier("empty", func(json, arg string) string { return "" })
	files, _ := filepath.Glob(filepath.Join(dir, "*.out"))
	seen := map[string]bool{}
	var pass, fail, skip int
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(f)
		marked := strings.Contains(string(data), "(empty)")
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1<<20), 1<<26)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, `["get"`) {
				if replaySet(t, line) {
					pass++
				}
				continue
			}
			dec := json.NewDecoder(strings.NewReader(line))
			var a []string
			if dec.Decode(&a) != nil || len(a) < 3 {
				continue
			}
			rest := line[dec.InputOffset():]
			if !strings.HasPrefix(rest, " => ") {
				continue
			}
			want := rest[4:]
			if k := strings.Index(want, " Path="); k >= 0 {
				want = want[:k]
			}
			doc, path := a[1], a[2]
			if seen[doc+"\x00"+path] {
				continue
			}
			seen[doc+"\x00"+path] = true
			if strings.HasPrefix(want, "PANIC") || !Valid(doc) && !strings.HasPrefix(path, "..") {
				skip++
				continue
			}
			got := fmtResult(Get(doc, path))
			if !marked {
				got = strings.ReplaceAll(got, "(empty)", "")
			}
			if got == want {
				pass++
				continue
			}
			fail++
			if fail <= 150 {
				if len(doc) > 160 {
					doc = doc[:160] + "..."
				}
				t.Logf("%s\n  doc  %q\n  path %q\n  want %s\n  got  %s", filepath.Base(f), doc, path, want, got)
			}
		}
		fh.Close()
	}
	t.Logf("pass %d fail %d skip %d", pass, fail, skip)
	if fail > 0 {
		t.Fail()
	}
}

func fmtResult(r Result) string {
	s := fmt.Sprintf("T=%d Raw=%q Str=%q Num=%v(%x) Idx=%d Idxs=%v", r.Type, r.Raw, r.Str, r.Num, math.Float64bits(r.Num), r.Index, r.Indexes)
	if r.Indexes != nil && len(r.Indexes) == 0 {
		s += "(empty)"
	}
	return s
}

// replaySet checks a recorded set, setraw, setopt or del line. It reports
// whether the line was such a line and matched. Where sjson turns valid
// input into invalid JSON, ojson must return valid JSON without an error.
func replaySet(t *testing.T, line string) bool {
	dec := json.NewDecoder(strings.NewReader(line))
	var a []string
	if dec.Decode(&a) != nil || len(a) < 3 {
		return false
	}
	want := strings.TrimPrefix(line[dec.InputOffset():], " => ")
	doc, path := a[0+1], a[2]
	if !Valid(doc) || strings.HasPrefix(want, "PANIC") || strings.HasPrefix(want, "BAD") {
		return false
	}
	var out string
	var err error
	switch a[0] {
	case "set", "setopt":
		var v any
		if len(a) < 4 || json.Unmarshal([]byte(a[3]), &v) != nil {
			return false
		}
		if a[0] == "set" {
			out, err = Set(doc, path, v)
		} else {
			mode := ""
			if len(a) > 4 {
				mode = a[4]
			}
			o := &Options{Optimistic: strings.Contains(mode, "o"), ReplaceInPlace: strings.Contains(mode, "i")}
			var b []byte
			b, err = SetBytesOptions([]byte(doc), path, v, o)
			out = string(b)
		}
	case "setraw":
		if len(a) < 4 {
			return false
		}
		out, err = SetRaw(doc, path, a[3])
	case "del":
		out, err = Delete(doc, path)
	default:
		return false
	}
	got := fmt.Sprintf("%q %s", out, probeErr(err))
	if got == want {
		return true
	}
	// A documented deviation: sjson's output is not valid JSON.
	var wout string
	if k := strings.LastIndex(want, "\" "); k > 0 {
		if q, e := strconv.Unquote(want[:k+1]); e == nil {
			wout = q
		}
	}
	if !Valid(wout) && err == nil && (out == doc || Valid(out)) {
		return true
	}
	// The other one: sjson adds a member under a key the object already
	// has, where ojson replaces that member.
	if a[0] != "del" && err == nil && Valid(wout) && Valid(out) && dupCount(wout) > dupCount(doc) && dupCount(out) == dupCount(doc) {
		return true
	}
	// And one more: sjson edits the text of a string value, where ojson
	// replaces the string.
	if a[0] != "del" && err == nil && Valid(wout) && stringReplaced(doc, wout, out) {
		return true
	}
	if a[0] == "del" && err == nil && out == doc && inString(doc, wout) {
		return true
	}
	t.Errorf("%s doc %q path %q\n  want %s\n  got  %s", a[0], doc, path, want, got)
	return false
}

// stringReplaced reports whether out is doc with one string value replaced
// by an object or array, and sjson's output wout is doc, or doc with an edit
// inside the quotes of that string.
func stringReplaced(doc, wout, out string) bool {
	p := 0
	for p < len(doc) && p < len(out) && doc[p] == out[p] {
		p++
	}
	if p == len(doc) || doc[p] != '"' {
		return false
	}
	ve, _ := valueSpan(doc, p)
	if ve <= p || !strings.HasSuffix(out, doc[ve:]) || len(out)-(len(doc)-ve) <= p {
		return false
	}
	if mid := out[p : len(out)-(len(doc)-ve)]; !Valid(mid) || mid[0] != '[' && mid[0] != '{' {
		return false
	}
	if wout == doc {
		return true
	}
	q := 0
	for q < len(doc) && q < len(wout) && doc[q] == wout[q] {
		q++
	}
	n := 0
	for n < len(doc) && n < len(wout) && doc[len(doc)-1-n] == wout[len(wout)-1-n] {
		n++
	}
	return q > p && len(doc)-n < ve-1
}

// dupCount counts the members of the objects in json whose key an earlier
// member of the same object has.
func dupCount(json string) int {
	n := 0
	var walk func(r Result)
	walk = func(r Result) {
		seen := map[string]bool{}
		r.ForEach(func(k, v Result) bool {
			if r.IsObject() {
				if seen[k.Str] {
					n++
				}
				seen[k.Str] = true
			}
			if v.IsObject() || v.IsArray() {
				walk(v)
			}
			return true
		})
	}
	walk(Parse(json))
	return n
}

func probeErr(err error) string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%q", err.Error())
}

// inString reports whether wout is doc with an edit inside the quotes of
// one string value.
func inString(doc, wout string) bool {
	q := 0
	for q < len(doc) && q < len(wout) && doc[q] == wout[q] {
		q++
	}
	n := 0
	for n < len(doc) && n < len(wout) && doc[len(doc)-1-n] == wout[len(wout)-1-n] {
		n++
	}
	for i := 0; i < len(doc); i++ {
		if doc[i] != '"' {
			continue
		}
		e := stringEnd(doc, i+1)
		if e < 0 {
			return false
		}
		if i < q && len(doc)-n < e {
			return true
		}
		i = e - 1
	}
	return false
}
