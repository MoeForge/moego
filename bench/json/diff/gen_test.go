package diff

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math/rand/v2"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

// node is a generated JSON value. The path generator walks it so that the
// paths it writes mostly address values that exist.
type node struct {
	kind byte // '{', '[', 's' string, 'n' number, 'l' true/false/null
	raw  string
	str  string
	keys []string // decoded member names
	kids []*node
}

// gen draws documents and paths from one seeded source, so (seed, i)
// reproduces a case.
type gen struct {
	r     *rand.Rand
	style int // 0 compact, 1 spaced, 2 indented, 3 random whitespace
	dup   bool
}

func newGen(seed, i uint64) *gen { return &gen{r: rand.New(rand.NewPCG(seed, i))} }

func (g *gen) intn(n int) int         { return g.r.IntN(n) }
func (g *gen) chance(p float64) bool  { return g.r.Float64() < p }
func (g *gen) pick(s []string) string { return s[g.r.IntN(len(s))] }

var keyPool = []string{
	"a", "b", "c", "name", "first", "last", "age", "friends", "nets", "children", "x", "id", "tags",
	"a.b", "a*b", "x?y", "@this", "@reverse", "#", "a|b", "", "é", "a\"b", `a\b`, "1", "0", "fav.movie",
	"Last", "k:v", "a b", "[x]", "{y}", "!t", "a=b", "(p)", "x,y", "%",
}

var strPool = []string{
	"", "a", "b", "abc", "Dale", "Murphy", "Craig", "fb", "tw", "ig", "Roger", "x*y", "1", "true", "null",
	"é😀", "a\nb", "<>&", `{"a":1}`, `[1,2]`, `"q"`, "2006-01-02T15:04:05Z", "A", "B", "Jane", "d?le", `\`, " ",
}

var numPool = []string{
	"0", "1", "-1", "2", "3", "10", "37", "44", "47", "68", "1.5", "-0", "1e3", "1E-2", "12345678901234567890",
	"0.1", "100", "-2.5", "9007199254740993", "5e-324", "1.7976931348623157e308", "-12",
}

var recKeys = []string{"first", "last", "age", "nets", "name", "a", "b"}

func (g *gen) str() *node {
	s := g.pick(strPool)
	return &node{kind: 's', str: s, raw: g.quote(s)}
}

func (g *gen) scalar() *node {
	switch g.intn(10) {
	case 0, 1, 2, 3:
		return g.str()
	case 4, 5, 6:
		return &node{kind: 'n', raw: g.pick(numPool)}
	default:
		return &node{kind: 'l', raw: g.pick([]string{"true", "false", "null"})}
	}
}

// value draws a value with at most depth more levels of nesting.
func (g *gen) value(depth int) *node {
	if depth <= 0 || g.chance(0.35) {
		return g.scalar()
	}
	n := g.intn(5)
	if g.chance(0.5) {
		a := &node{kind: '['}
		records := g.chance(0.5)
		for range n {
			if records {
				a.kids = append(a.kids, g.record(depth-1))
			} else {
				a.kids = append(a.kids, g.value(depth-1))
			}
		}
		return a
	}
	o := &node{kind: '{'}
	for range n {
		k := g.pick(keyPool)
		if g.chance(0.4) {
			k = g.pick(recKeys)
		}
		if !g.dup && g.chance(0.97) {
			for _, have := range o.keys {
				if have == k {
					k = ""
					break
				}
			}
			if k == "" {
				continue
			}
		}
		o.keys = append(o.keys, k)
		o.kids = append(o.kids, g.value(depth-1))
	}
	return o
}

// record draws an object with the shared record keys, the shape queries
// are written for.
func (g *gen) record(depth int) *node {
	o := &node{kind: '{'}
	for _, k := range recKeys {
		if g.chance(0.4) {
			continue
		}
		var v *node
		switch {
		case k == "nets" || (depth > 0 && g.chance(0.1)):
			v = &node{kind: '['}
			for range g.intn(4) {
				v.kids = append(v.kids, g.str())
			}
		case k == "age":
			v = &node{kind: 'n', raw: g.pick(numPool)}
		case depth > 0 && g.chance(0.15):
			v = g.value(depth - 1)
		default:
			v = g.scalar()
		}
		o.keys = append(o.keys, k)
		o.kids = append(o.kids, v)
	}
	return o
}

// quote writes s as a JSON string, sometimes with optional escapes.
func (g *gen) quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range s {
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\n':
			b.WriteString(`\n`)
		case c < 0x20:
			b.WriteString(`\u00` + strconv.FormatUint(uint64(c)>>4, 16) + strconv.FormatUint(uint64(c)&15, 16))
		case c == '/' && g.chance(0.3):
			b.WriteString(`\/`)
		case (c >= 0x80 || g.chance(0.05)) && g.chance(0.25):
			if c > 0xffff {
				c -= 0x10000
				b.WriteString(u4(0xd800+(c>>10)) + u4(0xdc00+(c&0x3ff)))
			} else {
				b.WriteString(u4(c))
			}
		default:
			b.WriteRune(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func u4(c rune) string {
	s := strconv.FormatInt(int64(c), 16)
	return `\u` + strings.Repeat("0", 4-len(s)) + s
}

func (g *gen) ws(b *strings.Builder) {
	if g.style == 3 {
		b.WriteString(g.pick([]string{"", "", " ", "\n", "\t", " \r\n "}))
	} else if g.style == 1 {
		b.WriteByte(' ')
	}
}

func (g *gen) nl(b *strings.Builder, depth int) {
	if g.style == 2 {
		b.WriteByte('\n')
		b.WriteString(strings.Repeat("  ", depth))
	} else {
		g.ws(b)
	}
}

func (g *gen) write(b *strings.Builder, n *node, depth int) {
	switch n.kind {
	case '{', '[':
		b.WriteByte(n.kind)
		for i, k := range n.kids {
			if i > 0 {
				b.WriteByte(',')
			}
			g.nl(b, depth+1)
			if n.kind == '{' {
				b.WriteString(g.quote(n.keys[i]))
				g.ws(b)
				b.WriteByte(':')
				g.ws(b)
				if g.style == 2 {
					b.WriteByte(' ')
				}
			}
			g.write(b, k, depth+1)
		}
		if len(n.kids) > 0 {
			g.nl(b, depth)
		} else {
			g.ws(b)
		}
		b.WriteByte(n.kind + 2) // '{'+2 == '}', '['+2 == ']'
	default:
		b.WriteString(n.raw)
	}
}

// doc draws a document. With lines it is JSON Lines: values separated by
// newlines, and root is a virtual array of them.
func (g *gen) doc(lines bool) (string, *node) {
	g.style = g.intn(4)
	g.dup = g.chance(0.03)
	var b strings.Builder
	if g.style == 3 || g.chance(0.1) {
		g.ws(&b)
	}
	if !lines {
		root := g.value(4)
		if g.chance(0.8) {
			root = g.value(4)
			for root.kind != '{' && root.kind != '[' && g.chance(0.8) {
				root = g.value(4)
			}
		}
		g.write(&b, root, 0)
		if g.style == 3 {
			g.ws(&b)
		}
		return b.String(), root
	}
	g.style = 0
	root := &node{kind: '['}
	for i := range 1 + g.intn(4) {
		if i > 0 {
			b.WriteString(g.pick([]string{"\n", "\n", "\r\n", " \n", "\n\n"}))
		}
		v := g.record(1)
		if g.chance(0.3) {
			v = g.value(2)
		}
		root.kids = append(root.kids, v)
		g.write(&b, v, 0)
	}
	if g.chance(0.5) {
		b.WriteByte('\n')
	}
	return b.String(), root
}

var mods = []string{"reverse", "ugly", "pretty", "this", "valid", "flatten", "join", "keys", "values", "tostr", "fromstr", "group", "dig"}

// modifier draws one modifier component with an optional argument.
func (g *gen) modifier() string {
	if g.chance(0.12) {
		return g.pick([]string{"@echo:x", `@echo:{"a":[1]}`, "@case:upper", "@case:lower", "@case", "@len", "@wrap:q", "@echo"})
	}
	m := g.pick(mods)
	s := "@" + m
	if !g.chance(0.4) {
		return s
	}
	switch m {
	case "pretty":
		return s + ":" + g.pick([]string{`{"sortKeys":true}`, `{"indent":"\t"}`, `{"prefix":"> ","width":10}`, `{"width":1}`, `{"indent":"    ","sortKeys":true,"width":200}`, `{}`})
	case "flatten":
		return s + `:{"deep":true}`
	case "join":
		return s + `:{"preserve":true}`
	case "dig":
		return s + ":" + g.pick(recKeys)
	default:
		return s + ":" + g.pick([]string{`x`, `{"a":1}`, `1`, `true`, `[1]`, `"s"`})
	}
}

// comp writes a member name as a path component: escaped, or with
// wildcards standing for some of its characters.
func (g *gen) comp(k string) string {
	if g.chance(0.12) && k != "" {
		rs := []rune(k)
		var b strings.Builder
		for i, c := range rs {
			switch {
			case i == len(rs)-1 && g.chance(0.3):
				b.WriteByte('*')
			case g.chance(0.2):
				b.WriteByte('?')
			default:
				b.WriteString(gjson.Escape(string(c)))
			}
		}
		if g.chance(0.3) {
			return b.String() + "*"
		}
		return b.String()
	}
	return gjson.Escape(k)
}

func (g *gen) sep() string {
	if g.chance(0.2) {
		return "|"
	}
	return "."
}

// walk writes a path relative to n with at most budget components.
func (g *gen) walk(n *node, budget int) string {
	if budget <= 0 || g.chance(0.12) {
		return ""
	}
	if g.chance(0.07) {
		return g.join(g.modifier(), g.walk(n, budget-1))
	}
	switch n.kind {
	case '{':
		if len(n.keys) == 0 || g.chance(0.08) {
			return g.comp(g.pick(keyPool))
		}
		i := g.intn(len(n.keys))
		return g.join(g.comp(n.keys[i]), g.walk(n.kids[i], budget-1))
	case '[':
		var e *node
		ei := 0
		if len(n.kids) > 0 {
			ei = g.intn(len(n.kids))
			e = n.kids[ei]
		} else {
			e = g.scalar()
		}
		switch g.intn(12) {
		case 0, 1, 2:
			if g.chance(0.1) {
				return strconv.Itoa(len(n.kids) + g.intn(3) - 1)
			}
			return g.join(strconv.Itoa(ei), g.walk(e, budget-1))
		case 3:
			return "#"
		case 4, 5, 6:
			return g.join("#", g.walk(e, budget-1))
		case 7, 8:
			return g.join("#("+g.query(n, budget-1)+")", g.walk(e, budget-1))
		case 9, 10:
			return g.join("#("+g.query(n, budget-1)+")#", g.walk(e, budget-1))
		default:
			return g.comp(g.pick(keyPool))
		}
	default:
		if g.chance(0.5) {
			return g.comp(g.pick(keyPool))
		}
		return ""
	}
}

func (g *gen) join(a, b string) string {
	if b == "" {
		return a
	}
	return a + g.sep() + b
}

var ops = []string{"==", "=", "!=", "<", "<=", ">", ">=", "%", "!%"}

// query writes the inside of #(...) for array a.
func (g *gen) query(a *node, budget int) string {
	var e *node
	if len(a.kids) > 0 {
		e = a.kids[g.intn(len(a.kids))]
	} else {
		e = g.scalar()
	}
	lhs := ""
	if e.kind == '{' && len(e.keys) > 0 && g.chance(0.85) {
		i := g.intn(len(e.keys))
		lhs, e = g.comp(e.keys[i]), e.kids[i]
		if e.kind == '[' && g.chance(0.5) {
			if g.chance(0.5) {
				return lhs + ".#(" + g.query(e, budget-1) + ")"
			}
			lhs += ".#"
			e = &node{kind: 'n', raw: strconv.Itoa(len(e.kids))}
		} else if e.kind == '{' && len(e.keys) > 0 {
			j := g.intn(len(e.keys))
			lhs, e = lhs+"."+g.comp(e.keys[j]), e.kids[j]
		}
	} else if e.kind == '[' && g.chance(0.5) {
		lhs, e = "#", &node{kind: 'n', raw: strconv.Itoa(len(e.kids))}
	}
	if lhs != "" && g.chance(0.08) {
		lhs += g.pick([]string{"|@this", ".@this", "|@tostr|@fromstr"})
	}
	if g.chance(0.05) {
		return lhs
	}
	op := g.pick(ops)
	var rhs string
	switch {
	case g.chance(0.08):
		op = g.pick([]string{"==", "!=", "<", ">="})
		rhs = "~" + g.pick([]string{"true", "false", "null", "*"})
	case op == "%" || op == "!%":
		s := e.str
		if e.kind != 's' {
			s = g.pick(strPool)
		}
		rs := []rune(s)
		var b strings.Builder
		for i, c := range rs {
			if i > 0 && g.chance(0.2) {
				b.WriteByte('*')
				break
			}
			if g.chance(0.2) {
				b.WriteByte('?')
			} else {
				b.WriteRune(c)
			}
		}
		if g.chance(0.3) {
			b.WriteByte('*')
		}
		rhs = g.quote(b.String())
	case e.kind == 's' && g.chance(0.7):
		rhs = g.quote(e.str)
	case e.kind == 's':
		rhs = e.str
	case e.kind == 'n' || e.kind == 'l':
		rhs = e.raw
		if g.chance(0.2) {
			rhs = g.pick(numPool)
		}
	default:
		rhs = g.scalar().raw
	}
	if g.chance(0.1) {
		return lhs + " " + op + " " + rhs
	}
	return lhs + op + rhs
}

var literals = []string{`!true`, `!false`, `!null`, `!"x"`, `!"a\"b"`, `!1.5`, `!-1`, `!{"a":1}`, `![1,2]`, `!{"a":[1,{"b":2}]}.a.1`, `![3,1,2]|@reverse`}

// mpath writes a [..] or {..} multipath over root.
func (g *gen) mpath(root *node, obj bool) string {
	var parts []string
	for range 1 + g.intn(3) {
		var p string
		switch {
		case g.chance(0.15):
			p = g.pick(literals)
		case g.chance(0.06):
			p = g.mpath(root, g.chance(0.5))
		default:
			p = g.walk(root, 4)
			if p == "" {
				p = g.comp(g.pick(keyPool))
			}
		}
		if obj && g.chance(0.3) {
			p = g.pick([]string{"k", "name", `"q"`, "a b", `x\:y`}) + ":" + p
		}
		parts = append(parts, p)
	}
	if obj {
		return "{" + strings.Join(parts, ",") + "}"
	}
	return "[" + strings.Join(parts, ",") + "]"
}

const mutAlphabet = `.|#@*?!()[]{},:"\=<>~%0129ab -`

// path draws a path for root.
func (g *gen) path(root *node, lines bool) string {
	var p string
	if lines {
		p = ".." + g.walk(root, 5)
	} else {
		switch r := g.intn(24); {
		case r < 13:
			p = g.walk(root, 6)
		case r < 15:
			p = g.mpath(root, false)
		case r < 17:
			p = g.mpath(root, true)
		case r < 18:
			p = g.pick(literals)
		case r < 20:
			p = g.join(g.modifier(), g.walk(root, 4))
		default:
			p = g.walk(root, 4)
			for range 1 + g.intn(2) {
				q := g.pick([]string{g.modifier(), g.mpath(root, g.chance(0.5)), "0", "#", g.comp(g.pick(recKeys))})
				p += "|" + q
			}
		}
	}
	if g.chance(0.2) {
		p = g.mutate(p)
	}
	return p
}

// mutate applies a few random byte edits from the path syntax alphabet.
func (g *gen) mutate(p string) string {
	b := []byte(p)
	for range 1 + g.intn(3) {
		c := mutAlphabet[g.intn(len(mutAlphabet))]
		i := 0
		if len(b) > 0 {
			i = g.intn(len(b) + 1)
		}
		switch g.intn(3) {
		case 0:
			b = append(b[:i], append([]byte{c}, b[i:]...)...)
		case 1:
			if i < len(b) {
				b = append(b[:i], b[i+1:]...)
			}
		default:
			if i < len(b) {
				b[i] = c
			}
		}
	}
	if !utf8.Valid(b) {
		return p
	}
	return string(b)
}

// rawPool holds the SetRaw values.
var rawPool = []string{`{"r":[1,2]}`, `7`, `"s"`, `[]`, `null`, `{}`, `true`, `"a\"b"`}

// gcase is one generated differential case.
type gcase struct {
	Doc   string `json:"doc"`
	Path  string `json:"path"`
	Sub   string `json:"sub,omitempty"`   // second path for Result.Get and GetMany
	Raw   string `json:"raw,omitempty"`   // SetRaw value
	Val   int    `json:"val,omitempty"`   // index into values for Set
	Lines bool   `json:"lines,omitempty"` // the document is JSON Lines
}

func (g *gen) gcase() gcase {
	lines := g.chance(0.08)
	doc, root := g.doc(lines)
	c := gcase{Doc: doc, Lines: lines, Raw: g.pick(rawPool), Val: g.intn(len(values))}
	c.Path = g.path(root, lines)
	c.Sub = g.walk(root, 3)
	if g.chance(0.3) {
		c.Sub = g.path(root, false)
	}
	return c
}

// caseWire is gcase in the records. encoding/json replaces invalid UTF-8
// with U+FFFD, so a field that is not valid UTF-8 also travels in base64.
type caseWire struct {
	Doc   string            `json:"doc"`
	Path  string            `json:"path"`
	Sub   string            `json:"sub,omitempty"`
	Raw   string            `json:"raw,omitempty"`
	Val   int               `json:"val,omitempty"`
	Lines bool              `json:"lines,omitempty"`
	B64   map[string]string `json:"b64,omitempty"`
}

func (c gcase) MarshalJSON() ([]byte, error) {
	w := caseWire{c.Doc, c.Path, c.Sub, c.Raw, c.Val, c.Lines, nil}
	for k, v := range map[string]string{"doc": c.Doc, "path": c.Path, "sub": c.Sub, "raw": c.Raw} {
		if !utf8.ValidString(v) {
			if w.B64 == nil {
				w.B64 = map[string]string{}
			}
			w.B64[k] = base64.StdEncoding.EncodeToString([]byte(v))
		}
	}
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	err := e.Encode(w)
	return bytes.TrimSpace(b.Bytes()), err
}

func (c *gcase) UnmarshalJSON(data []byte) error {
	var w caseWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*c = gcase{w.Doc, w.Path, w.Sub, w.Raw, w.Val, w.Lines}
	for k, v := range w.B64 {
		d, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return err
		}
		switch k {
		case "doc":
			c.Doc = string(d)
		case "path":
			c.Path = string(d)
		case "sub":
			c.Sub = string(d)
		case "raw":
			c.Raw = string(d)
		}
	}
	return nil
}
