// Package diff checks ojson against gjson and sjson on generated inputs.
package diff

import (
	"errors"
	"fmt"
	"math"
	"testing"
	"unsafe"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/MoeForge/moego/ojson"
)

var docs = []string{
	`{"a":1,"b":{"c":"x","d":[1,2,{"e":true}]},"f":null}`,
	` { "a" : 1 , "b" : { "c" : "x" } } `,
	"{\n  \"model\": \"gpt-4o\",\n  \"messages\": [\n    {\"role\": \"user\", \"content\": \"hi\"}\n  ]\n}",
	`{}`, `{ }`, `[]`, `[ ]`, `[1,2,3]`, ` [ 1 , 2 ] `, `[[1,2],[3]]`,
	`{"a":{"a":{"a":1}},"a":2}`, `{"a":1,"a":2}`, `{"a":[{"b":1},{"b":2}]}`,
	`{"k\"q":1,"k\\b":2,"a":3,"a.b":4,"a*b":5,"a?b":6}`,
	`{"n":-1.5e3,"z":0,"big":12345678901234567890,"s":"a\nbé😀"}`,
	`"str"`, `5`, `true`, `null`, ``, `  `, `x`, `{"a":`, `[1,`, `{"a":1`, `{"a" 1}`,
}

var paths = []string{
	"a", "b", "b.c", "b.d", "b.d.0", "b.d.2.e", "b.d.-1", "b.d.5", "f", "x", "x.y",
	"0", "1", "2", "-1", "5", "0.1", "1.0", "model", "messages.0.content", "messages.-1.role",
	`k\"q`, `k\\b`, "a", `a\.b`, `a\*b`, `a\?b`, "n", "z", "big", "s", "a.a.a", "a.a",
	"a.b.c.d", ":0", "b.:1", "b.d.:9", "", "a.", ".a", "a..b", "3", "b.d.",
}

func res(r gjson.Result) string {
	return fmt.Sprintf("T=%d Raw=%q Str=%q Num=%x Idx=%d", r.Type, r.Raw, r.Str, math.Float64bits(r.Num), r.Index)
}

func ores(r ojson.Result) string {
	return fmt.Sprintf("T=%d Raw=%q Str=%q Num=%x Idx=%d", r.Type, r.Raw, r.Str, math.Float64bits(r.Num), r.Index)
}

func plainPath(p string) bool {
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '*', '?', '#', '@', '|', '!', '=', '<', '>', '%', '[', '{', '(':
			return false
		case '\\':
			i++
		}
	}
	return true
}

// Malformed documents are only checked for crashes: the contract asks
// for exact results on valid JSON alone.
func TestGet(t *testing.T) {
	for _, js := range docs {
		valid := gjson.Valid(js)
		for _, p := range paths {
			if !plainPath(p) {
				continue
			}
			want := res(gjson.Get(js, p))
			got := ores(ojson.Get(js, p))
			if valid && got != want {
				t.Errorf("Get(%q, %q)\n got %s\nwant %s", js, p, got, want)
			}
			wb := res(gjson.GetBytes([]byte(js), p))
			gb := ores(ojson.GetBytes([]byte(js), p))
			if valid && wb != gb {
				t.Errorf("GetBytes(%q, %q)\n got %s\nwant %s", js, p, gb, wb)
			}
		}
		var ps []string
		for _, p := range paths {
			if plainPath(p) {
				ps = append(ps, p)
			}
		}
		w := gjson.GetMany(js, ps...)
		g := ojson.GetMany(js, ps...)
		for i := range ps {
			if valid && res(w[i]) != ores(g[i]) {
				t.Errorf("GetMany(%q)[%q]\n got %s\nwant %s", js, ps[i], ores(g[i]), res(w[i]))
			}
		}
	}
}

func TestValid(t *testing.T) {
	extra := []string{`{"a":1}x`, `[1,]`, `{"a":1,}`, `01`, `-`, `1.`, `1e`, `"\x"`, "\"a\tb\"", `"\u12"`, `[`, `]`, `{"a":tru}`, `nul`, ` 1 `, "\x00"}
	for _, js := range append(docs, extra...) {
		if w, g := gjson.Valid(js), ojson.Valid(js); w != g {
			t.Errorf("Valid(%q) got %v want %v", js, g, w)
		}
	}
}

var values = []any{1, "x", true, nil, 1.5, -7, "a\"b\n", map[string]int{"k": 1}, []int{1, 2}, float32(0.1), uint8(200), " <>&"}

func TestSet(t *testing.T) {
	for _, js := range docs {
		valid := gjson.Valid(js)
		for _, p := range paths {
			if !plainPath(p) {
				continue
			}
			for _, v := range values {
				ws, we := sjson.Set(js, p, v)
				gs, ge := ojson.Set(js, p, v)
				if valid && (ws != gs || errText(we) != errText(ge)) {
					t.Errorf("Set(%q, %q, %#v)\n got %q %v\nwant %q %v", js, p, v, gs, ge, ws, we)
				}
				wb, we2 := sjson.SetBytes([]byte(js), p, v)
				gb, ge2 := ojson.SetBytes([]byte(js), p, v)
				if valid && (string(wb) != string(gb) || errText(we2) != errText(ge2) || (wb == nil) != (gb == nil)) {
					t.Errorf("SetBytes(%q, %q, %#v)\n got %q %v\nwant %q %v", js, p, v, gb, ge2, wb, we2)
				}
			}
			raw := `{"r":[1,2]}`
			ws, we := sjson.SetRaw(js, p, raw)
			gs, ge := ojson.SetRaw(js, p, raw)
			if valid && (ws != gs || errText(we) != errText(ge)) {
				t.Errorf("SetRaw(%q, %q)\n got %q %v\nwant %q %v", js, p, gs, ge, ws, we)
			}
		}
	}
}

func TestDelete(t *testing.T) {
	for _, js := range docs {
		valid := gjson.Valid(js)
		for _, p := range paths {
			if !plainPath(p) {
				continue
			}
			ws, we := sjson.Delete(js, p)
			gs, ge := ojson.Delete(js, p)
			if valid && (ws != gs || errText(we) != errText(ge)) {
				t.Errorf("Delete(%q, %q)\n got %q %v\nwant %q %v", js, p, gs, ge, ws, we)
			}
		}
	}
}

// withRoom returns s as a byte slice with room spare bytes after it.
func withRoom(s string, room int) []byte {
	b := make([]byte, len(s), len(s)+room)
	copy(b, s)
	return b
}

// sameMemory reports whether b starts where in does.
func sameMemory(b, in []byte) bool {
	return cap(b) > 0 && cap(in) > 0 && unsafe.SliceData(b) == unsafe.SliceData(in)
}

// TestSetOptions compares SetBytesOptions and SetRawBytesOptions with sjson
// under each combination of options, with and without spare capacity, on
// valid documents. Results, errors and nil results must match, and where
// sjson writes the result into the input, ojson must too.
func TestSetOptions(t *testing.T) {
	combos := []sjson.Options{{}, {Optimistic: true}, {ReplaceInPlace: true}, {Optimistic: true, ReplaceInPlace: true}}
	var cases, both, ours, deviations int
	check := func(what, js, p string, v any, o sjson.Options, room int, win, wb, gin, gb []byte, we, ge error) {
		cases++
		if s, _ := v.(string); o.Optimistic && o.ReplaceInPlace && string(wb) == js && s != "" {
			// sjson leaves the document unchanged when it replaces a value
			// with a string that needs escaping; ojson sets it, as SetBytes
			// does. This is a documented deviation.
			if plain, _ := sjson.SetBytes([]byte(js), p, v); string(plain) != js {
				deviations++
				wb = plain
			}
		}
		if string(wb) != string(gb) || errText(we) != errText(ge) || (wb == nil) != (gb == nil) {
			t.Errorf("MISMATCH %s %+v room=%d value=%#v sjsonUnchanged=%v\n doc %q path %q\n got %q %v\nwant %q %v", what, o, room, v, string(wb) == js, js, p, gb, ge, wb, we)
		}
		ws, gs := sameMemory(wb, win), sameMemory(gb, gin)
		if ws && !gs {
			t.Errorf("%s(%q, %q, %+v, room %d): sjson wrote into the input, ojson did not", what, js, p, o, room)
		}
		if ws && gs {
			both++
		} else if gs {
			ours++
		}
	}
	for _, js := range docs {
		if !gjson.Valid(js) {
			continue
		}
		for _, p := range paths {
			if !plainPath(p) {
				continue
			}
			for _, o := range combos {
				oo := ojson.Options{Optimistic: o.Optimistic, ReplaceInPlace: o.ReplaceInPlace}
				for _, room := range []int{0, 8, 64} {
					for _, v := range append(values, 0.2, "override", 4096) {
						win, gin := withRoom(js, room), withRoom(js, room)
						wb, we := sjson.SetBytesOptions(win, p, v, &o)
						gb, ge := ojson.SetBytesOptions(gin, p, v, &oo)
						check("SetBytesOptions", js, p, v, o, room, win, wb, gin, gb, we, ge)
					}
					for _, raw := range []string{`{"r":[1,2]}`, `7`, `"s"`, `[]`} {
						win, gin := withRoom(js, room), withRoom(js, room)
						wb, we := sjson.SetRawBytesOptions(win, p, []byte(raw), &o)
						gb, ge := ojson.SetRawBytesOptions(gin, p, []byte(raw), &oo)
						check("SetRawBytesOptions", js, p, raw, o, room, win, wb, gin, gb, we, ge)
					}
				}
			}
		}
	}
	t.Logf("%d cases; in place in both: %d; in place in ojson only: %d; sjson escaped-string no-ops set by ojson: %d", cases, both, ours, deviations)
}

var _ = errors.New
