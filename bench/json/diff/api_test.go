package diff

import (
	"strconv"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/MoeForge/moego/ojson"
)

// customMods are registered in both libraries, so generated paths exercise
// AddModifier and user modifiers with arguments.
var customMods = []struct {
	name string
	fn   func(json, arg string) string
}{
	{"echo", func(j, a string) string { return a }},
	{"case", func(j, a string) string {
		switch a {
		case "upper":
			return strings.ToUpper(j)
		case "lower":
			return strings.ToLower(j)
		}
		return j
	}},
	{"len", func(j, a string) string { return strconv.Itoa(len(j)) }},
	{"wrap", func(j, a string) string { return `{"j":` + j + `,"a":` + strconv.Quote(a) + `}` }},
}

func init() {
	for _, m := range customMods {
		gjson.AddModifier(m.name, m.fn)
		ojson.AddModifier(m.name, m.fn)
	}
}

// apiStrings are inputs for Escape and AppendJSONString.
func apiStrings() []string {
	ss := append([]string{}, keyPool...)
	ss = append(ss, strPool...)
	ss = append(ss, "\x00\x01\x1f\x7f", "\xff", "a\xc3", "  ", "�", "😀", "<script>&amp;</script>",
		"\t\r\n\b\f", `A`, "a.b*c?d|e#f@g!h=i<j>k%l[m]n{o}p(q)r,s:t\"u\\v~w x", "é.é")
	for i := range uint64(200) {
		g := newGen(5, i)
		var b []byte
		for range g.intn(12) {
			b = append(b, byte(g.intn(256)))
		}
		ss = append(ss, string(b), g.mutate(g.pick(keyPool)))
	}
	return ss
}

func apiCheck(t *testing.T, s string) {
	t.Helper()
	if w, g := gjson.Escape(s), ojson.Escape(s); w != g {
		t.Errorf("Escape(%q)\n got  %q\n want %q", s, g, w)
	}
	for _, noesc := range []bool{false, true} {
		gjson.DisableEscapeHTML, ojson.DisableEscapeHTML = noesc, noesc
		w := string(gjson.AppendJSONString([]byte("p"), s))
		var g string
		if p := catch(true, func() { g = string(ojson.AppendJSONString([]byte("p"), s)) }); p != "" {
			g = p
		}
		if w != g {
			t.Errorf("AppendJSONString(%q) DisableEscapeHTML=%v\n got  %q\n want %q", s, noesc, g, w)
		}
	}
	gjson.DisableEscapeHTML, ojson.DisableEscapeHTML = false, false
}

func linesOut(doc string) (w, g string) {
	var b strings.Builder
	gjson.ForEachLine(doc, func(r gjson.Result) bool {
		b.WriteString(gdesc(r) + "; ")
		return !strings.Contains(r.Raw, `"stop"`)
	})
	w = b.String()
	b.Reset()
	if p := catch(true, func() {
		ojson.ForEachLine(doc, func(r ojson.Result) bool {
			b.WriteString(odesc(r) + "; ")
			return !strings.Contains(r.Raw, `"stop"`)
		})
	}); p != "" {
		return w, p
	}
	return w, b.String()
}

func TestAPI(t *testing.T) {
	for _, s := range apiStrings() {
		apiCheck(t, s)
	}
	names := append([]string{"", "x", "@reverse", "Reverse"}, mods...)
	for _, m := range customMods {
		names = append(names, m.name)
	}
	for _, n := range names {
		if w, g := gjson.ModifierExists(n, nil), ojson.ModifierExists(n, nil); w != g {
			t.Errorf("ModifierExists(%q) got %v want %v", n, g, w)
		}
	}
	docs := []string{"", "\n", lines, `1 2 3`, "{\"a\":1}\n\n[2]\r\n\"stop\"\n4", "[1,\n2]\n3", ` {"a" : 1} `}
	for i := range uint64(300) {
		d, _ := newGen(6, i).doc(true)
		docs = append(docs, d)
	}
	for _, d := range docs {
		if w, g := linesOut(d); w != g {
			t.Errorf("ForEachLine(%q)\n got  %s\n want %s", d, g, w)
		}
	}
}

// TestDisableModifiers compares generated modifier paths with modifiers
// disabled in both libraries.
func TestDisableModifiers(t *testing.T) {
	gjson.DisableModifiers, ojson.DisableModifiers = true, true
	bucketTag = "nomod:"
	defer func() { gjson.DisableModifiers, ojson.DisableModifiers, bucketTag = false, false, "" }()
	k := newCollector()
	for i := range uint64(2000) {
		c := newGen(8, i).gcase()
		if !strings.Contains(c.Path, "@") || !validCase(c) {
			continue
		}
		ms := runSome(c, true, false)
		if len(ms) == 1 && ms[0] == errOracle {
			continue
		}
		k.add(c, ms, 8, i, "nomod")
	}
	k.report(t)
}

// FuzzAPI compares Escape, AppendJSONString and ForEachLine.
func FuzzAPI(f *testing.F) {
	for _, s := range apiStrings() {
		f.Add(s)
	}
	f.Add(lines)
	f.Fuzz(func(t *testing.T, s string) {
		apiCheck(t, s)
		exact := linesValid(s)
		w, g := linesOut(s)
		if strings.HasPrefix(g, "panic: ") || (exact && w != g) {
			t.Errorf("ForEachLine(%q)\n got  %s\n want %s", s, g, w)
		}
	})
}
