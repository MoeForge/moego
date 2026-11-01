package diff

import (
	"sort"
	"strings"
)

// item is one member or element of a container in a valid document:
// text s..e, and the end of the previous sibling and start of the next,
// or -1.
type item struct{ s, e, prev, next int }

type jscan struct {
	d     string
	i     int
	items []item
	vals  [][2]int
}

func (p *jscan) skip() {
	for p.i < len(p.d) && strings.IndexByte(" \t\r\n", p.d[p.i]) >= 0 {
		p.i++
	}
}

func (p *jscan) str() {
	for p.i++; p.d[p.i] != '"'; p.i++ {
		if p.d[p.i] == '\\' {
			p.i++
		}
	}
	p.i++
}

func link(sib []item) []item {
	for k := range sib {
		sib[k].prev, sib[k].next = -1, -1
		if k > 0 {
			sib[k].prev = sib[k-1].e
		}
		if k+1 < len(sib) {
			sib[k].next = sib[k+1].s
		}
	}
	return sib
}

// value scans one value; it recurses, so it is only used on the small
// documents of failing cases.
func (p *jscan) value() int {
	p.skip()
	s := p.i
	switch c := p.d[p.i]; c {
	case '{', '[':
		var sib []item
		for p.i++; ; {
			p.skip()
			if p.d[p.i] == c+2 {
				p.i++
				break
			}
			is := p.i
			if c == '{' {
				p.str()
				p.skip()
				p.i++
			}
			sib = append(sib, item{s: is, e: p.value()})
			p.skip()
			if p.d[p.i] == ',' {
				p.i++
			}
		}
		p.items = append(p.items, link(sib)...)
	case '"':
		p.str()
	default:
		for p.i < len(p.d) && strings.IndexByte(" \t\r\n,]}", p.d[p.i]) < 0 {
			p.i++
		}
	}
	p.vals = append(p.vals, [2]int{s, p.i})
	return p.i
}

// edits lists smaller variants of a valid document (or JSON Lines
// stream): items removed, largest first, then values replaced by short
// ones.
func edits(d string) (out []string) {
	p := &jscan{d: d}
	if catch(false, func() {
		var top []item
		for p.skip(); p.i < len(p.d); p.skip() {
			s := p.i
			top = append(top, item{s: s, e: p.value()})
		}
		p.items = append(p.items, link(top)...)
	}) != "" {
		return nil
	}
	sort.SliceStable(p.items, func(a, b int) bool { return p.items[a].e-p.items[a].s > p.items[b].e-p.items[b].s })
	for _, it := range p.items {
		switch {
		case it.next >= 0:
			out = append(out, d[:it.s]+d[it.next:])
		case it.prev >= 0:
			out = append(out, d[:it.prev]+d[it.e:])
		default:
			out = append(out, d[:it.s]+d[it.e:])
		}
	}
	sort.SliceStable(p.vals, func(a, b int) bool { return p.vals[a][1]-p.vals[a][0] > p.vals[b][1]-p.vals[b][0] })
	for _, v := range p.vals {
		t := d[v[0]:v[1]]
		for _, r := range []string{"{}", "[]", "0", `""`, "1"} {
			if len(r) < len(t) || (len(r) == len(t) && r < t) {
				if (t[0] == '{' || t[0] == '[') && r[0] != t[0] && r[0] != '0' {
					continue
				}
				out = append(out, d[:v[0]]+r+d[v[1]:])
			}
		}
	}
	if c := strings.Join(strings.Fields(d), ""); c != d && !strings.Contains(d, `"`) {
		out = append(out, c)
	}
	return out
}

// shrinkText removes chunks of s while keep holds, halving the chunk size
// down to one byte.
func shrinkText(s string, keep func(string) bool, budget *int) string {
	for n := len(s) / 2; n >= 1; n /= 2 {
		for i := 0; i+n <= len(s) && *budget > 0; {
			t := s[:i] + s[i+n:]
			*budget--
			if keep(t) {
				s = t
			} else {
				i += n
			}
		}
	}
	return s
}

// hasField reports whether running op on c still differs in field.
func hasField(c gcase, op, field string) bool {
	if !validCase(c) || isGetOp(op) && malformedParse(c) {
		return false
	}
	var ms []mismatch
	if op != "get" && strings.Contains(strings.Join(getOps, " "), op) {
		if catch(false, func() { ms = runOp("get", c) }) != "" || len(ms) > 0 {
			return false
		}
	}
	if catch(false, func() { ms = runOp(op, c) }) != "" {
		return false
	}
	for _, m := range ms {
		if m.Field == field {
			return true
		}
	}
	return false
}

// crashes reports whether c still panics in op.
func crashes(c gcase, op string) bool {
	for _, m := range crashCheck(c, true, true) {
		if m.Op == op && m.Field == "panic" {
			return true
		}
	}
	return false
}

// minimize shrinks the document and paths of c while op keeps differing in
// field (or keeps panicking, for malformed cases).
func minimize(c gcase, op, field string) gcase {
	keep := func(d gcase) bool { return hasField(d, op, field) }
	if !validCase(c) {
		keep = func(d gcase) bool { return !validCase(d) && crashes(d, op) }
	}
	if !keep(c) {
		return c
	}
	budget := 6000
	for changed := true; changed && budget > 0; {
		changed = false
		if validCase(c) {
			for _, d := range edits(c.Doc) {
				if budget--; budget < 0 {
					break
				}
				t := c
				t.Doc = d
				if keep(t) {
					c, changed = t, true
					break
				}
			}
			if changed {
				continue
			}
		} else {
			c.Doc = shrinkText(c.Doc, func(s string) bool { t := c; t.Doc = s; return keep(t) }, &budget)
		}
		for _, get := range []func(*gcase) *string{
			func(t *gcase) *string { return &t.Path },
			func(t *gcase) *string { return &t.Sub },
			func(t *gcase) *string { return &t.Raw },
		} {
			old := *get(&c)
			s := shrinkText(old, func(s string) bool { t := c; *get(&t) = s; return keep(t) }, &budget)
			if s != old {
				*get(&c), changed = s, true
			}
		}
		if c.Val != 0 {
			t := c
			t.Val = 0
			if keep(t) {
				c, changed = t, true
			}
		}
	}
	return c
}

func isGetOp(op string) bool {
	for _, o := range getOps {
		if o == op {
			return true
		}
	}
	return false
}
