package diff

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/MoeForge/moego/ojson"
)

// catch runs f and returns the panic value, with a short stack when stack
// is set.
func catch(stack bool, f func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint("panic: ", r)
			if stack {
				s := strings.Split(string(debug.Stack()), "\n")
				if len(s) > 24 {
					s = s[:24]
				}
				msg += "\n" + strings.Join(s, "\n")
			}
		}
	}()
	f()
	return ""
}

// errOracle marks a case on which gjson or sjson itself panics; such cases
// are skipped.
var errOracle = mismatch{Op: "oracle", Field: "panic"}

// validCase reports whether c must match the reference exactly: the
// document is valid JSON, or the path is a JSON Lines path and every
// non-blank line is valid.
func validCase(c gcase) bool {
	if malformedLiteral(c.Path) || malformedLiteral(c.Sub) || malformedArg(c.Path) || malformedArg(c.Sub) || customGarbage(c) {
		return false
	}
	if gjson.Valid(c.Doc) {
		return true
	}
	return strings.HasPrefix(c.Path, "..") && linesValid(c.Doc)
}

// malformedLiteral reports a path with a '!' literal that is not valid
// JSON: the literal is malformed input, so such cases are only checked for
// crashes. A scalar literal runs to the next separator.
func malformedLiteral(p string) bool {
	for _, m := range reLit.FindAllStringIndex(p, -1) {
		i := m[1] // after '!'
		if i >= len(p) {
			continue
		}
		switch p[i] {
		case '{', '[', '"':
			if badJSONAt(p, i) {
				return true
			}
		default:
			j := i
			for j < len(p) && !strings.ContainsRune(".|,]}", rune(p[j])) {
				j++
			}
			if !gjson.Valid(p[i:j]) {
				return true
			}
		}
	}
	return false
}

// badJSONAt reports whether the JSON container or string that starts at
// p[i] is cut short or is not valid JSON.
func badJSONAt(p string, i int) bool {
	if p[i] == '"' {
		j := i + 1
		for ; j < len(p) && p[j] != '"'; j++ {
			if p[j] == '\\' {
				j++
			}
		}
		return j >= len(p) || !gjson.Valid(p[i:j+1])
	}
	depth, j := 0, i
	for ; j < len(p); j++ {
		switch p[j] {
		case '"':
			for j++; j < len(p) && p[j] != '"'; j++ {
				if p[j] == '\\' {
					j++
				}
			}
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		}
		if depth == 0 {
			break
		}
	}
	return j >= len(p) || !gjson.Valid(p[i:j+1])
}

// customGarbage reports a path through one of the test's custom modifiers
// whose result is not valid JSON: user modifier output is not validated,
// so it is malformed input from then on.
func customGarbage(c gcase) bool {
	custom := func(p string) bool {
		for _, m := range reMod.FindAllStringSubmatch(p, -1) {
			for _, cm := range customMods {
				if cm.name == m[1] {
					return true
				}
			}
		}
		return false
	}
	bad := false
	catch(false, func() {
		if custom(c.Path) {
			r := gjson.Get(c.Doc, c.Path)
			bad = r.Exists() && !gjson.Valid(r.Raw)
		}
		if custom(c.Sub) {
			for _, r := range []gjson.Result{gjson.Get(c.Doc, c.Sub), gjson.Get(c.Doc, c.Path).Get(c.Sub)} {
				bad = bad || r.Exists() && !gjson.Valid(r.Raw)
			}
		}
	})
	return bad
}

// linesValid reports whether every line of s that is not blank (JSON
// whitespace only) is valid JSON.
func linesValid(s string) bool {
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.Trim(ln, " \t\r"); t != "" && !gjson.Valid(t) {
			return false
		}
	}
	return true
}

// hugeIndex reports a path with a number of four or more digits, also
// when escapes split it (`70\000` is 70000). sjson pads arrays up to such
// indexes without bound, so Set cases skip them.
func hugeIndex(p string) bool {
	p = strings.ReplaceAll(p, "\\", "")
	n := 0
	for i := 0; i < len(p); i++ {
		if p[i] >= '0' && p[i] <= '9' {
			if n++; n >= 4 {
				return true
			}
		} else {
			n = 0
		}
	}
	return false
}

var getOps = []string{"get", "bytes", "many", "rget", "doc", "parse"}
var setOps = []string{"set", "setraw", "del", "docset", "setopt"}

var optCombos = []sjson.Options{{}, {Optimistic: true}, {ReplaceInPlace: true}, {Optimistic: true, ReplaceInPlace: true}}

// runCase runs every operation on c. On a valid case it returns each
// difference from the reference; on a malformed one only ojson panics and
// unbounded output. When Get itself differs, the operations built on it
// are skipped so that one cause reports once.
func runCase(c gcase) []mismatch { return runSome(c, true, true) }

// runSome is runCase restricted to the Get or the Set operations.
func runSome(c gcase, get, set bool) []mismatch {
	if !validCase(c) {
		return crashCheck(c, get, set)
	}
	var ms []mismatch
	if get && malformedParse(c) {
		// gjson parses malformed text on the way (string contents,
		// modifier output): the Get side is malformed input.
		ms = crashCheck(c, true, false)
		get = false
	}
	if set && (!gjson.Valid(c.Doc) || !gjson.Valid(c.Raw)) {
		// Set on JSON Lines, or SetRaw of a malformed value, is outside
		// the contract: no crash only.
		ms = append(ms, crashCheck(c, false, true)...)
		set = gjson.Valid(c.Doc)
	}
	if get {
		gm := runOp("get", c)
		if len(gm) == 1 && gm[0] == errOracle {
			return gm
		}
		ms = append(ms, gm...)
		if len(gm) == 0 {
			// Many and rget also evaluate Sub; when Get(Sub) alone
			// differs, that is a Get bug of its own, found as a Path in
			// other cases.
			sub := c
			sub.Path = c.Sub
			subOK := len(runOp("get", sub)) == 0
			// On a JSON Lines document only the ".." path itself is
			// within the valid-input contract.
			lines := !gjson.Valid(c.Doc)
			for _, op := range getOps[1:] {
				if lines && (op == "many" || op == "rget" || op == "parse") {
					continue
				}
				if subOK || op == "bytes" || op == "doc" || op == "parse" {
					ms = append(ms, runOp(op, c)...)
				}
			}
		}
	}
	if set && !hugeIndex(c.Path) {
		for _, op := range setOps {
			ms = append(ms, runOp(op, c)...)
		}
	}
	for _, m := range ms {
		if m == errOracle {
			return []mismatch{errOracle}
		}
	}
	return ms
}

func manyPaths(c gcase) []string { return []string{c.Path, c.Sub, "a", c.Path, "b.c"} }

// runOp compares one operation; it returns errOracle alone when the
// reference panics.
func runOp(op string, c gcase) []mismatch {
	var want, got []kv
	var gp string
	add := func(k, w, g string) {
		want = append(want, kv{k, w})
		got = append(got, kv{k, g})
	}
	ref := func(f func()) bool { return catch(false, f) == "" }
	var extra []mismatch
	// edit compares one Set, SetRaw or Delete result. When sjson made
	// malformed JSON from a valid document and value, ojson is held to
	// the deviation rule instead (deviationCheck). It reports whether both
	// libraries succeeded with the same output.
	edit := func(name, in, ws string, we error, gs string, ge error, val string, del bool) bool {
		// The rule checks are harness code: a panic in them is a harness
		// bug (area harness), never ojson's.
		var kind, why string
		if p := catch(true, func() {
			if kind = deviationKind(in, c.Path, ws, we, val, del); kind != "" && (ws != gs || errText(we) != errText(ge)) {
				why = deviationCheck(in, c.Path, gs, ge, val, del)
			}
		}); p != "" {
			extra = append(extra, mismatch{Op: op, Field: "harness.panic." + name, Got: p, Want: fmt.Sprintf("input %q, sjson %q, ojson %q", in, ws, gs)})
			return false
		}
		if kind != "" && (ws != gs || errText(we) != errText(ge)) {
			m := mismatch{Op: op, Field: "deviation." + kind + "." + name, Got: fmt.Sprintf("%q", gs), Want: fmt.Sprintf("%q", ws)}
			if strings.HasPrefix(why, "unverified") {
				m.Field, m.Got = "deviation-unverified."+kind+"."+name, why+": "+m.Got
			} else if why != "" {
				m.Field, m.Got = "deviation-failed."+kind+"."+name, fmt.Sprintf("%s: %s (input %q)", why, m.Got, in)
			}
			extra = append(extra, m)
			return false
		}
		add(name+".out", fmt.Sprintf("%q", ws), fmt.Sprintf("%q", gs))
		add(name+".err", errText(we), errText(ge))
		return we == nil && ge == nil && ws == gs
	}
	switch op {
	case "get":
		var w gjson.Result
		var o ojson.Result
		if !ref(func() { w = gjson.Get(c.Doc, c.Path) }) {
			return []mismatch{errOracle}
		}
		if gp = catch(true, func() { o = ojson.Get(c.Doc, c.Path) }); gp != "" {
			break
		}
		if ms := diffKV(op, ocore("", o), gcore("", w)); len(ms) > 0 {
			return ms
		}
		var wm []kv
		if !ref(func() { wm = gmethods(w, c.Doc) }) {
			return nil
		}
		var om []kv
		if gp = catch(true, func() { om = omethods(o, c.Doc) }); gp == "" {
			if w.Exists() && !gjson.Valid(w.Raw) {
				// The methods parse a Raw that is not valid JSON
				// (modifier output, a literal): malformed input, no
				// crash only.
				return nil
			}
			return diffKV(op, om, wm)
		}
	case "bytes":
		w := gjson.GetBytes([]byte(c.Doc), c.Path)
		gp = catch(true, func() {
			buf := []byte(c.Doc)
			o := ojson.GetBytes(buf, c.Path)
			want, got = gcore("b.", w), ocore("b.", o)
			before := odesc(o)
			for i := range buf {
				buf[i] = '#'
			}
			add("b.detach", before, odesc(o))
			o = ojson.GetBytesNoCopy([]byte(c.Doc), c.Path)
			want, got = append(want, gcore("nc.", w)...), append(got, ocore("nc.", o)...)
		})
	case "many":
		ps := manyPaths(c)
		w := gjson.GetMany(c.Doc, ps...)
		w1 := gjson.GetMany(c.Doc, c.Path)
		gp = catch(true, func() {
			sets := [][]ojson.Result{
				ojson.GetMany(c.Doc, ps...), ojson.GetManyBytes([]byte(c.Doc), ps...),
				ojson.GetManyBytesNoCopy(nil, []byte(c.Doc), ps...), ojson.GetMany(c.Doc, c.Path),
			}
			for si, os := range sets {
				ws := w
				if si == 3 {
					ws = w1
				}
				if len(os) != len(ws) {
					add(fmt.Sprintf("many%d.len", si), fmt.Sprint(len(ws)), fmt.Sprint(len(os)))
					continue
				}
				for i := range ws {
					p := fmt.Sprintf("many%d.p%d.", si, i)
					want, got = append(want, gcore(p, ws[i])...), append(got, ocore(p, os[i])...)
				}
			}
		})
	case "rget":
		w := gjson.Parse(c.Doc).Get(c.Path)
		var w2 gjson.Result
		if !ref(func() { w2 = gjson.Get(c.Doc, c.Path).Get(c.Sub) }) {
			return []mismatch{errOracle}
		}
		gp = catch(true, func() {
			want, got = gcore("parse.", w), ocore("parse.", ojson.Parse(c.Doc).Get(c.Path))
			o2 := ojson.Get(c.Doc, c.Path).Get(c.Sub)
			if r := gjson.Get(c.Doc, c.Path); !r.Exists() || gjson.Valid(r.Raw) {
				// Result.Get parses Raw: no crash only when it is
				// malformed.
				want, got = append(want, gcore("chain.", w2)...), append(got, ocore("chain.", o2)...)
			}
		})
	case "doc":
		w := gjson.Get(c.Doc, c.Path)
		edited, err := sjson.SetRaw(c.Doc, "zq", "1")
		gp = catch(true, func() {
			want, got = gcore("doc.", w), ocore("doc.", ojson.NewDocString(c.Doc).Get(c.Path))
			if err == nil {
				d := ojson.NewDoc([]byte(c.Doc))
				if e := d.SetRaw("zq", "1"); e == nil && d.String() == edited {
					want, got = append(want, gcore("doc2.", gjson.Get(edited, c.Path))...), append(got, ocore("doc2.", d.Get(c.Path))...)
				}
			}
		})
	case "parse":
		w := gjson.Parse(c.Doc)
		var wm []kv
		if !ref(func() { wm = gmethods(w, c.Doc) }) {
			return []mismatch{errOracle}
		}
		gp = catch(true, func() {
			o := ojson.Parse(c.Doc)
			want, got = gcore("root.", w), ocore("root.", o)
			want, got = append(want, gcore("rootb.", gjson.ParseBytes([]byte(c.Doc)))...), append(got, ocore("rootb.", ojson.ParseBytes([]byte(c.Doc)))...)
			for _, m := range wm {
				want = append(want, kv{"m.root." + m.k[2:], m.v})
			}
			for _, m := range omethods(o, c.Doc) {
				got = append(got, kv{"m.root." + m.k[2:], m.v})
			}
		})
	case "setopt":
		if !gjson.Valid(c.Raw) {
			return nil
		}
		// SetRawBytesOptions under every option set, with and without
		// spare capacity. Where sjson writes into the input, ojson must
		// too.
		type res struct {
			out   []byte
			err   error
			in    bool
			panic string
		}
		var ws [8]res
		for i := range ws {
			o := optCombos[i%4]
			buf := withRoom(c.Doc, i/4*64)
			if p := catch(false, func() { ws[i].out, ws[i].err = sjson.SetRawBytesOptions(buf, c.Path, []byte(c.Raw), &o) }); p != "" {
				return []mismatch{errOracle}
			}
			ws[i].in = sameMemory(ws[i].out, buf)
		}
		gp = catch(true, func() {
			for i := range ws {
				o := optCombos[i%4]
				oo := ojson.Options{Optimistic: o.Optimistic, ReplaceInPlace: o.ReplaceInPlace}
				buf := withRoom(c.Doc, i/4*64)
				gb, ge := ojson.SetRawBytesOptions(buf, c.Path, []byte(c.Raw), &oo)
				name := fmt.Sprintf("setopt%d", i)
				// Returning the unchanged input is not an in-place write.
				if edit(name, c.Doc, string(ws[i].out), ws[i].err, string(gb), ge, c.Raw, false) && ws[i].in && string(ws[i].out) != c.Doc {
					add(name+".inplace", "true", fmt.Sprint(sameMemory(gb, buf)))
				}
			}
		})
	case "set":
		v := values[c.Val%len(values)]
		var ws string
		var wb []byte
		var we, wbe error
		if !ref(func() { ws, we = sjson.Set(c.Doc, c.Path, v); wb, wbe = sjson.SetBytes([]byte(c.Doc), c.Path, v) }) {
			return []mismatch{errOracle}
		}
		enc := encodeValue(v)
		gp = catch(true, func() {
			gs, ge := ojson.Set(c.Doc, c.Path, v)
			edit("set", c.Doc, ws, we, gs, ge, enc, false)
			gb, gbe := ojson.SetBytes([]byte(c.Doc), c.Path, v)
			if edit("setb", c.Doc, string(wb), wbe, string(gb), gbe, enc, false) || wbe != nil {
				add("setb.nil", fmt.Sprint(wb == nil), fmt.Sprint(gb == nil))
			}
		})
	case "setraw":
		if !gjson.Valid(c.Raw) {
			return nil // a malformed value: crash-checked in runSome
		}
		var ws string
		var we error
		if !ref(func() { ws, we = sjson.SetRaw(c.Doc, c.Path, c.Raw) }) {
			return []mismatch{errOracle}
		}
		gp = catch(true, func() {
			gs, ge := ojson.SetRaw(c.Doc, c.Path, c.Raw)
			edit("setraw", c.Doc, ws, we, gs, ge, c.Raw, false)
		})
	case "del":
		var ws string
		var we error
		if !ref(func() { ws, we = sjson.Delete(c.Doc, c.Path) }) {
			return []mismatch{errOracle}
		}
		gp = catch(true, func() {
			gs, ge := ojson.Delete(c.Doc, c.Path)
			edit("del", c.Doc, ws, we, gs, ge, "", true)
		})
	case "docset":
		if !gjson.Valid(c.Raw) {
			return nil
		}
		// Two overrides of the same path, then a delete, on one Doc. The
		// chain stops at the first edit that does not leave both
		// libraries with the same valid document.
		var w1, w2, w3 string
		var e1, e2, e3 error
		if !ref(func() {
			w1, e1 = sjson.SetRaw(c.Doc, c.Path, c.Raw)
			w2, e2 = w1, e1
			if e1 == nil && gjson.Valid(w1) {
				w2, e2 = sjson.SetRaw(w1, c.Path, "0")
			}
			w3, e3 = w2, e2
			if e2 == nil && gjson.Valid(w2) {
				w3, e3 = sjson.Delete(w2, c.Path)
			}
		}) {
			return []mismatch{errOracle}
		}
		gp = catch(true, func() {
			d := ojson.NewDocString(c.Doc)
			step := func(name, in, w string, we, ge error, val string, del bool) bool {
				if (ge != nil || we != nil) && deviationKind(in, c.Path, w, we, val, del) == "" {
					add(name+".err", errText(we), errText(ge))
					return false
				}
				// A deviation (also one where sjson failed) is checked
				// by edit and ends the chain.
				return edit(name, in, w, we, d.String(), ge, val, del) && we == nil
			}
			if !step("docset1", c.Doc, w1, e1, d.SetRaw(c.Path, c.Raw), c.Raw, false) {
				return
			}
			if !step("docset2", w1, w2, e2, d.SetRaw(c.Path, "0"), "0", false) {
				return
			}
			step("docset3", w2, w3, e3, d.Delete(c.Path), "", true)
		})
	default:
		panic("unknown op " + op)
	}
	if gp != "" {
		return []mismatch{{Op: op, Field: "panic", Got: gp}}
	}
	return append(diffKV(op, got, want), extra...)
}

// outputBound is the largest output accepted from a malformed case.
func outputBound(c gcase) int { return 1<<20 + 64*(len(c.Doc)+len(c.Path)+len(c.Sub)) }

// crashCheck runs the Get or Set ojson operations on a malformed case and
// reports panics and unbounded output only.
func crashCheck(c gcase, get, set bool) []mismatch {
	var ms []mismatch
	big := func(op string, n int) {
		if n > outputBound(c) {
			ms = append(ms, mismatch{Op: op, Field: "unbounded", Got: fmt.Sprint(n, " bytes")})
		}
	}
	run := func(op string, f func()) {
		if p := catch(true, f); p != "" {
			ms = append(ms, mismatch{Op: op, Field: "panic", Got: p})
		}
	}
	if !get {
		goto edits
	}
	run("get", func() {
		r := ojson.Get(c.Doc, c.Path)
		big("get", len(r.Raw)+len(r.Str))
		omethods(r, c.Doc)
		r2 := r.Get(c.Sub)
		big("get", len(r2.Raw))
		omethods(ojson.Parse(c.Doc), c.Doc)
	})
	run("bytes", func() {
		ojson.GetBytes([]byte(c.Doc), c.Path)
		ojson.GetBytesNoCopy([]byte(c.Doc), c.Path)
	})
	run("many", func() {
		ojson.GetMany(c.Doc, manyPaths(c)...)
		ojson.GetManyBytesNoCopy(nil, []byte(c.Doc), manyPaths(c)...)
	})
	run("doc", func() { ojson.NewDocString(c.Doc).Get(c.Path) })
edits:
	if !set || hugeIndex(c.Path) {
		return ms
	}
	run("set", func() {
		s, _ := ojson.Set(c.Doc, c.Path, values[c.Val%len(values)])
		big("set", len(s))
		s, _ = ojson.SetRaw(c.Doc, c.Path, c.Raw)
		big("set", len(s))
		for _, o := range []ojson.Options{{Optimistic: true}, {ReplaceInPlace: true}, {Optimistic: true, ReplaceInPlace: true}} {
			b, _ := ojson.SetRawBytesOptions(withRoom(c.Doc, 64), c.Path, []byte(c.Raw), &o)
			big("set", len(b))
		}
		ojson.Delete(c.Doc, c.Path)
	})
	run("docset", func() {
		d := ojson.NewDocString(c.Doc)
		d.SetRaw(c.Path, c.Raw)
		d.SetRaw(c.Path, "0")
		d.Delete(c.Path)
		big("docset", d.Len())
	})
	return ms
}
