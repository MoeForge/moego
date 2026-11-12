package regexp

import (
	"strings"
	"unsafe"
)

// Set is a list of regular expressions matched together, for tasks such as
// redacting several kinds of secrets or checking text against a block list
// in one pass. Its search results are those of the single expression
// (p0)|(p1)|…|(pn-1) in leftmost-first order: at each position the earliest
// expression in the list that matches wins, and each match reports which
// expression it came from. A Set is safe for concurrent use.
type Set struct {
	exprs []string
	res   []*Regexp
	union *Regexp
	group []int // capture group of each expression in union
}

// CompileSet compiles a list of expressions with the syntax of Compile. If
// one fails to parse, it returns that error.
func CompileSet(exprs []string) (*Set, error) {
	s := &Set{exprs: append([]string(nil), exprs...), res: make([]*Regexp, len(exprs)), group: make([]int, len(exprs))}
	var b strings.Builder
	g := 1
	for i, e := range exprs {
		re, err := Compile(e)
		if err != nil {
			return nil, err
		}
		s.res[i] = re
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteByte('(')
		b.WriteString(e)
		b.WriteByte(')')
		s.group[i] = g
		g += 1 + re.NumSubexp()
	}
	if len(exprs) == 0 {
		b.WriteString(`[^\x00-\x{10FFFF}]`) // matches nothing
	}
	u, err := Compile(b.String())
	if err != nil {
		return nil, err
	}
	s.union = u
	return s, nil
}

// MustCompileSet is like CompileSet but panics if an expression cannot be
// parsed.
func MustCompileSet(exprs []string) *Set {
	s, err := CompileSet(exprs)
	if err != nil {
		panic("regexp: CompileSet: " + err.Error())
	}
	return s
}

// Len returns the number of expressions in the set.
func (s *Set) Len() int { return len(s.exprs) }

// Exprs returns the expressions of the set. The slice should not be
// modified.
func (s *Set) Exprs() []string { return s.exprs }

// MatchString reports whether text contains a match of any expression.
func (s *Set) MatchString(text string) bool { return s.union.MatchString(text) }

// Match reports whether b contains a match of any expression.
func (s *Set) Match(b []byte) bool { return s.union.Match(b) }

// MatchesString returns, in increasing order, the indexes of the expressions
// that match somewhere in text, or nil if none does.
func (s *Set) MatchesString(text string) []int {
	if !s.union.MatchString(text) {
		return nil
	}
	var out []int
	for i, re := range s.res {
		if re.MatchString(text) {
			out = append(out, i)
		}
	}
	return out
}

// Matches is MatchesString for a byte slice.
func (s *Set) Matches(b []byte) []int { return s.MatchesString(bstr(b)) }

// which returns the expression the union match m came from.
func (s *Set) which(m []int) int {
	for i, g := range s.group {
		if m[2*g] >= 0 {
			return i
		}
	}
	return -1
}

// all calls f for successive matches with the index of their expression.
func (s *Set) all(text string, n int, f func(a, b, i int)) {
	if n == 0 {
		return
	}
	var buf [smallSub]int
	m := s.union.subBuf(&buf)
	count := 0
	s.union.matches(text, func(a, b int) bool {
		m[0], m[1] = a, b
		s.union.c.captures(text, m)
		f(a, b, s.which(m))
		count++
		return n < 0 || count < n
	})
}

// FindAllStringIndex returns successive non-overlapping matches in text as
// triples: start, end, and the index of the expression that matched. If
// n >= 0, it returns at most n matches. A return value of nil indicates no
// match.
func (s *Set) FindAllStringIndex(text string, n int) [][]int {
	var flat []int
	s.all(text, n, func(a, b, i int) { flat = append(flat, a, b, i) })
	if len(flat) == 0 {
		return nil
	}
	out := make([][]int, len(flat)/3)
	for k := range out {
		out[k] = flat[3*k : 3*k+3 : 3*k+3]
	}
	return out
}

// FindAllIndex is FindAllStringIndex for a byte slice.
func (s *Set) FindAllIndex(b []byte, n int) [][]int { return s.FindAllStringIndex(bstr(b), n) }

// ReplaceAllStringFunc returns a copy of src in which every match has been
// replaced by the return value of repl, given the index of the expression
// that matched and the matched text.
func (s *Set) ReplaceAllStringFunc(src string, repl func(index int, match string) string) string {
	var out []byte
	last := 0
	s.all(src, -1, func(a, b, i int) {
		if out == nil {
			out = make([]byte, 0, len(src)+16)
		}
		out = append(out, src[last:a]...)
		out = append(out, repl(i, src[a:b])...)
		last = b
	})
	if out == nil {
		return src
	}
	out = append(out, src[last:]...)
	return unsafe.String(unsafe.SliceData(out), len(out))
}

// ReplaceAllFunc is ReplaceAllStringFunc for byte slices.
func (s *Set) ReplaceAllFunc(src []byte, repl func(index int, match []byte) []byte) []byte {
	var out []byte
	last := 0
	found := false
	s.all(bstr(src), -1, func(a, b, i int) {
		found = true
		out = append(out, src[last:a]...)
		out = append(out, repl(i, src[a:b:b])...)
		last = b
	})
	if !found {
		return append([]byte(nil), src...)
	}
	return append(out, src[last:]...)
}

// ReplaceAllLiteralString returns a copy of src with every match replaced
// by repl.
func (s *Set) ReplaceAllLiteralString(src, repl string) string {
	return s.union.ReplaceAllLiteralString(src, repl)
}
