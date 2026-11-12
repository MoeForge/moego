package regexp

import (
	"io"
	"iter"
	"regexp/syntax"
	"strings"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

// smallSub is the number of submatch indexes kept on the stack.
const smallSub = 20

// subBuf returns a slice for the submatch indexes of re, in buf if it fits.
func (re *Regexp) subBuf(buf *[smallSub]int) []int {
	n := 2 * (re.c.numSubexp + 1)
	if n <= smallSub {
		return buf[:n]
	}
	return make([]int, n)
}

// submatchAt finds the leftmost match at or after pos and fills dst with
// its submatch indexes.
func (re *Regexp) submatchAt(s string, pos int, dst []int) bool {
	a, b := re.find(s, pos)
	if a < 0 {
		return false
	}
	dst[0], dst[1] = a, b
	if len(dst) > 2 {
		re.c.captures(s, dst)
	}
	return true
}

// FindSubmatchIndex returns a slice holding the index pairs identifying the
// leftmost match of the regular expression in b and the matches, if any, of
// its subexpressions, as defined by the 'Submatch' and 'Index' descriptions
// in the package comment. A return value of nil indicates no match.
func (re *Regexp) FindSubmatchIndex(b []byte) []int {
	return re.FindStringSubmatchIndex(bstr(b))
}

// FindStringSubmatchIndex returns a slice holding the index pairs
// identifying the leftmost match of the regular expression in s and the
// matches, if any, of its subexpressions, as defined by the 'Submatch' and
// 'Index' descriptions in the package comment. A return value of nil
// indicates no match.
func (re *Regexp) FindStringSubmatchIndex(s string) []int {
	var buf [smallSub]int
	m := re.subBuf(&buf)
	if !re.submatchAt(s, 0, m) {
		return nil
	}
	return append([]int(nil), m...)
}

// FindSubmatch returns a slice of slices holding the text of the leftmost
// match of the regular expression in b and the matches, if any, of its
// subexpressions, as defined by the 'Submatch' descriptions in the package
// comment. A return value of nil indicates no match.
func (re *Regexp) FindSubmatch(b []byte) [][]byte {
	var buf [smallSub]int
	m := re.subBuf(&buf)
	if !re.submatchAt(bstr(b), 0, m) {
		return nil
	}
	out := make([][]byte, len(m)/2)
	for i := range out {
		if x, y := m[2*i], m[2*i+1]; x >= 0 {
			out[i] = b[x:y:y]
		}
	}
	return out
}

// FindStringSubmatch returns a slice of strings holding the text of the
// leftmost match of the regular expression in s and the matches, if any, of
// its subexpressions, as defined by the 'Submatch' description in the
// package comment. A return value of nil indicates no match.
func (re *Regexp) FindStringSubmatch(s string) []string {
	var buf [smallSub]int
	m := re.subBuf(&buf)
	if !re.submatchAt(s, 0, m) {
		return nil
	}
	out := make([]string, len(m)/2)
	for i := range out {
		if x, y := m[2*i], m[2*i+1]; x >= 0 {
			out[i] = s[x:y]
		}
	}
	return out
}

// allSubmatches returns the submatch indexes of successive matches, as
// FindAllStringIndex finds them, flattened with a stride of
// 2*(NumSubexp()+1). The caller releases p.
func (re *Regexp) allSubmatches(s string, n int, small *[smallPairs]int) (p *[]int, flat []int, k int) {
	pp, m := re.collect(s, n, small)
	k = len(m) / 2
	if k == 0 {
		release(pp)
		return nil, nil, 0
	}
	stride := 2 * (re.c.numSubexp + 1)
	flat = make([]int, k*stride)
	for i := 0; i < k; i++ {
		d := flat[i*stride : (i+1)*stride]
		d[0], d[1] = m[2*i], m[2*i+1]
		if stride > 2 {
			re.c.captures(s, d)
		}
	}
	release(pp)
	return nil, flat, k
}

// FindAllSubmatchIndex is the 'All' version of FindSubmatchIndex; it
// returns a slice of all successive matches of the expression, as defined by
// the 'All' description in the package comment. A return value of nil
// indicates no match.
func (re *Regexp) FindAllSubmatchIndex(b []byte, n int) [][]int {
	return re.FindAllStringSubmatchIndex(bstr(b), n)
}

// FindAllStringSubmatchIndex is the 'All' version of
// FindStringSubmatchIndex; it returns a slice of all successive matches of
// the expression, as defined by the 'All' description in the package
// comment. A return value of nil indicates no match.
func (re *Regexp) FindAllStringSubmatchIndex(s string, n int) [][]int {
	var small [smallPairs]int
	pp, m := re.collect(s, n, &small)
	k := len(m) / 2
	if k == 0 {
		release(pp)
		return nil
	}
	stride := 2 * (re.c.numSubexp + 1)
	out, flat := subIndexResult(k, stride)
	for i := 0; i < k; i++ {
		d := flat[i*stride : (i+1)*stride : (i+1)*stride]
		d[0], d[1] = m[2*i], m[2*i+1]
		if stride > 2 {
			re.c.captures(s, d)
		}
		out[i] = d
	}
	release(pp)
	return out
}

// subIndexResult returns the result slice and the flat index storage of k
// submatch results of stride ints, in one allocation when they are small.
func subIndexResult(k, stride int) ([][]int, []int) {
	if k == 1 && stride <= 4 {
		r := new(struct {
			out  [1][]int
			flat [4]int
		})
		return r.out[:], r.flat[:stride]
	}
	if k*stride <= 16 && k <= 4 {
		r := new(struct {
			out  [4][]int
			flat [16]int
		})
		return r.out[:k:k], r.flat[:k*stride]
	}
	return make([][]int, k), make([]int, k*stride)
}

// FindAllSubmatch is the 'All' version of FindSubmatch; it returns a slice
// of all successive matches of the expression, as defined by the 'All'
// description in the package comment. A return value of nil indicates no
// match.
func (re *Regexp) FindAllSubmatch(b []byte, n int) [][][]byte {
	var small [smallPairs]int
	_, flat, k := re.allSubmatches(bstr(b), n, &small)
	if k == 0 {
		return nil
	}
	stride := len(flat) / k
	texts := make([][]byte, len(flat)/2)
	for i := range texts {
		if x, y := flat[2*i], flat[2*i+1]; x >= 0 {
			texts[i] = b[x:y:y]
		}
	}
	out := make([][][]byte, k)
	for i := range out {
		out[i] = texts[i*stride/2 : (i+1)*stride/2 : (i+1)*stride/2]
	}
	return out
}

// FindAllStringSubmatch is the 'All' version of FindStringSubmatch; it
// returns a slice of all successive matches of the expression, as defined by
// the 'All' description in the package comment. A return value of nil
// indicates no match.
func (re *Regexp) FindAllStringSubmatch(s string, n int) [][]string {
	var small [smallPairs]int
	_, flat, k := re.allSubmatches(s, n, &small)
	if k == 0 {
		return nil
	}
	stride := len(flat) / k
	texts := make([]string, len(flat)/2)
	for i := range texts {
		if x, y := flat[2*i], flat[2*i+1]; x >= 0 {
			texts[i] = s[x:y]
		}
	}
	out := make([][]string, k)
	for i := range out {
		out[i] = texts[i*stride/2 : (i+1)*stride/2 : (i+1)*stride/2]
	}
	return out
}

// --- Expand ---

// Expand appends template to dst and returns the result; during the append,
// Expand replaces variables in the template with corresponding matches
// drawn from src. The match slice should have been returned by
// FindSubmatchIndex.
//
// In the template, a variable is denoted by a substring of the form $name
// or ${name}, where name is a non-empty sequence of letters, digits, and
// underscores. A purely numeric name like $1 refers to the submatch with the
// corresponding index; other names refer to capturing parentheses named
// with the (?P<name>...) syntax. A reference to an out of range or unmatched
// index or a name that is not present in the regular expression is replaced
// with an empty slice.
//
// In the $name form, name is taken to be as long as possible: $1x is
// equivalent to ${1x}, not ${1}x, and, $10 is equivalent to ${10}, not
// ${1}0.
//
// To insert a literal $ in the output, use $$ in the template.
func (re *Regexp) Expand(dst []byte, template []byte, src []byte, match []int) []byte {
	return re.expand(dst, bstr(template), bstr(src), match)
}

// ExpandString is like Expand but the template and source are strings. It
// appends to and returns a byte slice in order to give the calling code
// control over allocation.
func (re *Regexp) ExpandString(dst []byte, template string, src string, match []int) []byte {
	return re.expand(dst, template, src, match)
}

func (re *Regexp) expand(dst []byte, t string, src string, match []int) []byte {
	for {
		i := strings.IndexByte(t, '$')
		if i < 0 {
			return append(dst, t...)
		}
		dst = append(dst, t[:i]...)
		t = t[i+1:]
		if t != "" && t[0] == '$' {
			dst = append(dst, '$')
			t = t[1:]
			continue
		}
		name, rest, ok := templateVar(t)
		if !ok {
			dst = append(dst, '$') // not a variable: the $ stays as text
			continue
		}
		t = rest
		if g := groupNumber(name); g >= 0 {
			if 2*g+1 < len(match) && match[2*g] >= 0 {
				dst = append(dst, src[match[2*g]:match[2*g+1]]...)
			}
			continue
		}
		for g, s := range re.c.names {
			if g > 0 && s == name && 2*g+1 < len(match) && match[2*g] >= 0 {
				dst = append(dst, src[match[2*g]:match[2*g+1]]...)
				break
			}
		}
	}
}

// templateVar parses the name after a $ in a template: {name} or the
// longest run of letters, digits and underscores.
func templateVar(t string) (name, rest string, ok bool) {
	brace := t != "" && t[0] == '{'
	if brace {
		t = t[1:]
	}
	n := 0
	for n < len(t) {
		c := t[n]
		if c < utf8.RuneSelf {
			if c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' {
				n++
				continue
			}
			break
		}
		r, w := utf8.DecodeRuneInString(t[n:])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			break
		}
		n += w
	}
	if n == 0 {
		return "", "", false
	}
	name, rest = t[:n], t[n:]
	if brace {
		if rest == "" || rest[0] != '}' {
			return "", "", false
		}
		rest = rest[1:]
	}
	return name, rest, true
}

// groupNumber returns the submatch a numeric template variable refers to,
// or -1 if name is not a number: ASCII digits without a leading zero, read
// while the value stays below 10^8 before each digit.
func groupNumber(name string) int {
	if len(name) > 1 && name[0] == '0' {
		return -1
	}
	n := 0
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c < '0' || c > '9' || n >= 1e8 {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// --- Replace ---

// replaceAll returns src with every match replaced by what add appends for
// it, or ok == false if there is no match. m holds the indexes add needs:
// the match, or with ncap > 2 all its submatches.
func (re *Regexp) replaceAll(src string, ncap int, grow int, add func(dst []byte, m []int) []byte) ([]byte, bool) {
	var small [smallPairs]int
	p, pairs := re.collect(src, -1, &small)
	if len(pairs) == 0 {
		release(p)
		return nil, false
	}
	var buf [smallSub]int
	m := buf[:2]
	if ncap > 2 {
		m = re.subBuf(&buf)
	}
	var out []byte
	if grow >= 0 {
		size := len(src) + grow*len(pairs)/2
		for i := 0; i < len(pairs); i += 2 {
			size -= pairs[i+1] - pairs[i]
		}
		out = make([]byte, 0, size)
	} else {
		out = make([]byte, 0, len(src)+len(src)/8+16)
	}
	last := 0
	for i := 0; i < len(pairs); i += 2 {
		a, b := pairs[i], pairs[i+1]
		out = append(out, src[last:a]...)
		m[0], m[1] = a, b
		if len(m) > 2 {
			re.c.captures(src, m)
		}
		out = add(out, m)
		last = b
	}
	release(p)
	return append(out, src[last:]...), true
}

// replaceLiteral is replaceAll for a replacement without template
// variables. A match equal to repl stays in place, so text such as the
// single spaces of a whitespace collapse is copied in long stretches.
func (re *Regexp) replaceLiteral(src, repl string) ([]byte, bool) {
	var small [smallPairs]int
	p, pairs := re.collect(src, -1, &small)
	if len(pairs) == 0 {
		release(p)
		return nil, false
	}
	size := len(src) + len(repl)*len(pairs)/2
	for i := 0; i < len(pairs); i += 2 {
		size -= pairs[i+1] - pairs[i]
	}
	out := make([]byte, 0, size)
	last := 0
	for i := 0; i < len(pairs); i += 2 {
		a, b := pairs[i], pairs[i+1]
		if b-a == len(repl) && src[a:b] == repl {
			continue
		}
		out = append(out, src[last:a]...)
		out = append(out, repl...)
		last = b
	}
	release(p)
	return append(out, src[last:]...), true
}

// ReplaceAllString returns a copy of src, replacing matches of the Regexp
// with the replacement string repl. Inside repl, $ signs are interpreted as
// in Expand.
func (re *Regexp) ReplaceAllString(src, repl string) string {
	out, ok := re.replaceTemplate(src, repl)
	if !ok {
		return src
	}
	return unsafe.String(unsafe.SliceData(out), len(out))
}

// ReplaceAll returns a copy of src, replacing matches of the Regexp with the
// replacement text repl. Inside repl, $ signs are interpreted as in Expand.
func (re *Regexp) ReplaceAll(src, repl []byte) []byte {
	out, ok := re.replaceTemplate(bstr(src), string(repl))
	if !ok {
		out = append([]byte(nil), src...)
	}
	return nilIfEmpty(out)
}

func (re *Regexp) replaceTemplate(src, repl string) ([]byte, bool) {
	if strings.IndexByte(repl, '$') < 0 {
		return re.replaceLiteral(src, repl)
	}
	return re.replaceAll(src, 2*(re.c.numSubexp+1), -1, func(dst []byte, m []int) []byte {
		return re.expand(dst, repl, src, m)
	})
}

// ReplaceAllLiteralString returns a copy of src, replacing matches of the
// Regexp with the replacement string repl. The replacement repl is
// substituted directly, without using Expand.
func (re *Regexp) ReplaceAllLiteralString(src, repl string) string {
	out, ok := re.replaceLiteral(src, repl)
	if !ok {
		return src
	}
	return unsafe.String(unsafe.SliceData(out), len(out))
}

// ReplaceAllLiteral returns a copy of src, replacing matches of the Regexp
// with the replacement bytes repl. The replacement repl is substituted
// directly, without using Expand.
func (re *Regexp) ReplaceAllLiteral(src, repl []byte) []byte {
	out, ok := re.replaceLiteral(bstr(src), bstr(repl))
	if !ok {
		out = append([]byte(nil), src...)
	}
	return nilIfEmpty(out)
}

// ReplaceAllStringFunc returns a copy of src in which all matches of the
// Regexp have been replaced by the return value of function repl applied to
// the matched substring. The replacement returned by repl is substituted
// directly, without using Expand.
func (re *Regexp) ReplaceAllStringFunc(src string, repl func(string) string) string {
	out, ok := re.replaceAll(src, 2, -1, func(dst []byte, m []int) []byte {
		return append(dst, repl(src[m[0]:m[1]])...)
	})
	if !ok {
		return src
	}
	return unsafe.String(unsafe.SliceData(out), len(out))
}

// ReplaceAllFunc returns a copy of src in which all matches of the Regexp
// have been replaced by the return value of function repl applied to the
// matched byte slice. The replacement returned by repl is substituted
// directly, without using Expand.
func (re *Regexp) ReplaceAllFunc(src []byte, repl func([]byte) []byte) []byte {
	out, ok := re.replaceAll(bstr(src), 2, -1, func(dst []byte, m []int) []byte {
		return append(dst, repl(src[m[0]:m[1]])...)
	})
	if !ok {
		out = append([]byte(nil), src...)
	}
	return nilIfEmpty(out)
}

// nilIfEmpty returns nil for an empty result, as appending nothing to a nil
// slice does.
func nilIfEmpty(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

// --- Split ---

// Split slices s into substrings separated by the expression and returns a
// slice of the substrings between those expression matches.
//
// The slice returned by this method consists of all the substrings of s not
// contained in the slice returned by FindAllString. When called on an
// expression that contains no metacharacters, it is equivalent to
// strings.SplitN.
//
// Example:
//
//	s := regexp.MustCompile("a*").Split("abaabaccadaaae", 5)
//	// s: ["", "b", "b", "c", "cadaaae"]
//
// The count determines the number of substrings to return:
//   - n > 0: at most n substrings; the last substring will be the unsplit remainder;
//   - n == 0: the result is nil (zero substrings);
//   - n < 0: all substrings.
func (re *Regexp) Split(s string, n int) []string {
	if n == 0 {
		return nil
	}
	if len(re.expr) > 0 && len(s) == 0 {
		return []string{""}
	}
	var small [smallPairs]int
	p, m := re.collect(s, n, &small)
	out := make([]string, 0, len(m)/2+1)
	beg, end := 0, 0
	for i := 0; i < len(m); i += 2 {
		if n > 0 && len(out) == n-1 {
			break
		}
		end = m[i]
		if m[i+1] != 0 {
			out = append(out, s[beg:end])
		}
		beg = m[i+1]
	}
	release(p)
	if end != len(s) {
		out = append(out, s[beg:])
	}
	return out
}

// SplitSeq returns an iterator over substrings of s separated by the
// expression. It yields the substrings Split(s, -1) returns.
func (re *Regexp) SplitSeq(s string) iter.Seq[string] {
	return func(yield func(string) bool) {
		if len(re.expr) > 0 && len(s) == 0 {
			yield("")
			return
		}
		beg, end, more := 0, 0, true
		re.matches(s, func(a, b int) bool {
			end = a
			if b != 0 && !yield(s[beg:end]) {
				more = false
				return false
			}
			beg = b
			return true
		})
		if more && end != len(s) {
			yield(s[beg:])
		}
	}
}

// --- iterators ---

// matches yields the index pairs of successive matches in s.
func (re *Regexp) matches(s string, yield func(a, b int) bool) {
	st := allState{prev: -1}
	var buf [2]int
	for {
		m := re.allMatches(buf[:0], s, st.count+1, &st, false)
		if len(m) == 0 || !yield(m[0], m[1]) {
			return
		}
	}
}

// intChunk hands out small independent int slices from shared blocks, so
// iterators allocate once per several matches.
type intChunk struct{ free []int }

func (c *intChunk) take(n int) []int {
	if len(c.free) < n {
		c.free = make([]int, max(n, 16))
	}
	s := c.free[:n:n]
	c.free = c.free[n:]
	return s
}

// All returns an iterator over all the matches for re in b.
func (re *Regexp) All(b []byte) iter.Seq[[]byte] {
	return func(yield func([]byte) bool) {
		re.matches(bstr(b), func(x, y int) bool { return yield(b[x:y:y]) })
	}
}

// AllString returns an iterator over all the matches for re in s.
func (re *Regexp) AllString(s string) iter.Seq[string] {
	return func(yield func(string) bool) {
		re.matches(s, func(x, y int) bool { return yield(s[x:y]) })
	}
}

// AllIndex returns an iterator over the locations of all matches for re in
// b.
func (re *Regexp) AllIndex(b []byte) iter.Seq[[]int] {
	return re.allIndex(bstr(b))
}

// AllStringIndex returns an iterator over the locations of all matches for
// re in s.
func (re *Regexp) AllStringIndex(s string) iter.Seq[[]int] {
	return re.allIndex(s)
}

func (re *Regexp) allIndex(s string) iter.Seq[[]int] {
	return func(yield func([]int) bool) {
		var c intChunk
		re.matches(s, func(x, y int) bool {
			m := c.take(2)
			m[0], m[1] = x, y
			return yield(m)
		})
	}
}

// submatches yields the submatch indexes of successive matches in s. The
// slice is reused between calls.
func (re *Regexp) submatches(s string, yield func(m []int) bool) {
	var buf [smallSub]int
	d := re.subBuf(&buf)
	re.matches(s, func(x, y int) bool {
		d[0], d[1] = x, y
		if len(d) > 2 {
			re.c.captures(s, d)
		}
		return yield(d)
	})
}

// AllSubmatch returns an iterator over all the matches for re in b,
// including submatches. In each match m, the overall match is m[0], the
// first submatch is m[1], and so on.
func (re *Regexp) AllSubmatch(b []byte) iter.Seq[[][]byte] {
	return func(yield func([][]byte) bool) {
		re.submatches(bstr(b), func(d []int) bool {
			out := make([][]byte, len(d)/2)
			for i := range out {
				if x, y := d[2*i], d[2*i+1]; x >= 0 {
					out[i] = b[x:y:y]
				}
			}
			return yield(out)
		})
	}
}

// AllStringSubmatch returns an iterator over all the matches for re in s,
// including submatches. In each match m, m[0] is the overall match, m[1] is
// the first submatch, and so on.
func (re *Regexp) AllStringSubmatch(s string) iter.Seq[[]string] {
	return func(yield func([]string) bool) {
		re.submatches(s, func(d []int) bool {
			out := make([]string, len(d)/2)
			for i := range out {
				if x, y := d[2*i], d[2*i+1]; x >= 0 {
					out[i] = s[x:y]
				}
			}
			return yield(out)
		})
	}
}

// AllSubmatchIndex returns an iterator over the locations of all matches
// for re in b, including submatches. In each match m, the overall match is
// b[m[0]:m[1]], the first submatch is b[m[2]:m[3]], and so on.
func (re *Regexp) AllSubmatchIndex(b []byte) iter.Seq[[]int] {
	return re.allSubmatchIndex(bstr(b))
}

// AllStringSubmatchIndex returns an iterator over the locations of all
// matches for re in s, including submatches. In each match m, the overall
// match is s[m[0]:m[1]], the first submatch is s[m[2]:m[3]], and so on.
func (re *Regexp) AllStringSubmatchIndex(s string) iter.Seq[[]int] {
	return re.allSubmatchIndex(s)
}

func (re *Regexp) allSubmatchIndex(s string) iter.Seq[[]int] {
	return func(yield func([]int) bool) {
		var c intChunk
		re.submatches(s, func(d []int) bool {
			m := c.take(len(d))
			copy(m, d)
			return yield(m)
		})
	}
}

// --- io.RuneReader ---

// The reader methods run the Pike machine over the runes as they are read,
// so they read only as far as the result requires and work on unbounded
// readers. Positions are byte offsets in the reader's input, counted with
// the sizes the reader reports.

func (re *Regexp) execReader(r io.RuneReader, ncap int) []int {
	c := re.c
	e := c.engines()
	m := c.getPike(e)
	m.reset(ncap)
	mode := pikeFirst
	if re.longest {
		mode = pikeLongest
	}
	var out []int
	if m.execReader(r, c.anchorStart, mode, ncap == 0) {
		out = append([]int(nil), m.match...)
	}
	e.pike.Put(m)
	return out
}

// MatchReader reports whether the text returned by the RuneReader contains
// any match of the regular expression re.
func (re *Regexp) MatchReader(r io.RuneReader) bool {
	return re.execReader(r, 0) != nil
}

// MatchReader reports whether the text returned by the RuneReader contains
// any match of the regular expression pattern. More complicated queries
// need to use Compile and the full Regexp interface.
func MatchReader(pattern string, r io.RuneReader) (matched bool, err error) {
	re, err := Compile(pattern)
	if err != nil {
		return false, err
	}
	return re.MatchReader(r), nil
}

// FindReaderIndex returns a two-element slice of integers defining the
// location of the leftmost match of the regular expression in text read
// from the RuneReader. The match text was found in the input stream at byte
// offset loc[0] through loc[1]-1. A return value of nil indicates no match.
func (re *Regexp) FindReaderIndex(r io.RuneReader) (loc []int) {
	return re.execReader(r, 2)
}

// FindReaderSubmatchIndex returns a slice holding the index pairs
// identifying the leftmost match of the regular expression of text read by
// the RuneReader, and the matches, if any, of its subexpressions, as defined
// by the 'Submatch' and 'Index' descriptions in the package comment. A
// return value of nil indicates no match.
func (re *Regexp) FindReaderSubmatchIndex(r io.RuneReader) []int {
	return re.execReader(r, 2*(re.c.numSubexp+1))
}

// --- text and literal prefix ---

// AppendText implements encoding.TextAppender. The output matches that of
// calling the String method.
func (re *Regexp) AppendText(b []byte) ([]byte, error) {
	return append(b, re.String()...), nil
}

// LiteralPrefix returns a literal string that must begin any match of the
// regular expression re. It returns the boolean true if the literal string
// comprises the entire regular expression.
func (re *Regexp) LiteralPrefix() (prefix string, complete bool) {
	p := compileProg(re.c.parse(), re.c.numSubexp)
	insts := p.insts
	pc := p.start
	if p.onePass() {
		// A one-pass program starts with \A: the literal follows it, and
		// it is complete when only \z follows the literal.
		pc = insts[pc].out
		for insts[pc].op == opNop {
			pc = insts[pc].out
		}
		lit, pc := p.literalRun(pc, false)
		in := &insts[pc]
		if lit == "" {
			return "", in.op == opMatch
		}
		complete = in.op == opEmpty && syntax.EmptyOp(in.arg)&syntax.EmptyEndText != 0 && insts[in.out].op == opMatch
		return lit, complete
	}
	pc = p.skipEmpty(pc)
	lit, pc := p.literalRun(pc, true)
	return lit, insts[pc].op == opMatch
}

// skipEmpty follows no-ops and capture marks from pc.
func (p *prog) skipEmpty(pc uint32) uint32 {
	for op := p.insts[pc].op; op == opNop || op == opSave; op = p.insts[pc].op {
		pc = p.insts[pc].out
	}
	return pc
}

// literalRun collects the case-sensitive runes from pc on, other than
// U+FFFD, and returns where it stopped. With skip, no-ops and capture
// marks between the runes are passed over.
func (p *prog) literalRun(pc uint32, skip bool) (string, uint32) {
	var b []byte
	for {
		in := &p.insts[pc]
		if in.op != opRune1 || rune(in.arg) == utf8.RuneError {
			return string(b), pc
		}
		b = utf8.AppendRune(b, rune(in.arg))
		pc = in.out
		if skip {
			pc = p.skipEmpty(pc)
		}
	}
}
