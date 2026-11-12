// Package regexp implements regular expression search with the syntax,
// API and results of the standard library's regexp package.
//
// The syntax is parsed by the standard regexp/syntax package, so accepted
// patterns, error messages and capture numbering are the same. Matching runs
// on byte automata: a lazily built DFA shared by all goroutines, literal
// prefilters, and an exact NFA where the DFA cannot decide. Matching takes
// time linear in the input and bounded memory for any pattern.
package regexp

import (
	"regexp/syntax"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
	"unsafe"
)

// Regexp is the representation of a compiled regular expression. A Regexp
// is safe for concurrent use by multiple goroutines, except for
// configuration methods, such as Longest.
type Regexp struct {
	expr    string
	c       *core
	longest bool
}

// core is the compiled, immutable part of a Regexp.
type core struct {
	expr      string
	numSubexp int
	names     []string

	plan

	// tree is the parsed expression of patterns that need the engines,
	// until they are first built. Later builds parse expr again.
	tree atomic.Pointer[syntax.Regexp]
	eng  atomic.Pointer[engines]
	mu   sync.Mutex

	// matchFn is match specialized for the plan at compile time.
	matchFn func(c *core, s string) bool
}

// engines hold everything a pattern derives from its expression for
// matching: the program, the automata and the NFA scratch pool. They are
// built on first use, so Compile stays cheap, and each automaton is built
// when a search first needs it. Like the pooled machines of the standard
// library, they do not outlive idle periods: a garbage collection that finds
// them unused since the previous one releases them, and the next search
// builds them again.
type engines struct {
	prog    *prog
	fwd     atomic.Pointer[lazyDFA]
	rev     atomic.Pointer[lazyDFA]
	inner   atomic.Pointer[lazyDFA] // planInner: reverse DFA of the part before the literal
	long    atomic.Pointer[lazyDFA] // anchored forward DFA for leftmost-longest ends
	fixed   []int32                 // capInfo.fixed
	caps    atomic.Pointer[capInfo]
	pike    sync.Pool
	capPool sync.Pool   // *capScratch
	used    atomic.Bool // a search ran since the last collection
}

func (e *engines) touch() {
	if !e.used.Load() {
		e.used.Store(true)
	}
}

// gcWatch releases the engines of a pattern once they go unused for a whole
// collection cycle. It is unreachable as soon as it is armed, so its
// finalizer runs after the next collection; while the engines are in use it
// re-arms itself.
type gcWatch struct {
	c *core
	e *engines
}

func watch(c *core, e *engines) {
	runtime.SetFinalizer(&gcWatch{c, e}, (*gcWatch).collected)
}

func (w *gcWatch) collected() {
	if w.c.eng.Load() != w.e {
		return
	}
	if w.e.used.Swap(false) {
		watch(w.c, w.e)
		return
	}
	w.c.eng.CompareAndSwap(w.e, nil)
}

// Compile parses a regular expression and returns, if successful, a Regexp
// object that can be used to match against text. Matching is leftmost-first,
// as in Perl and the standard library.
func Compile(expr string) (*Regexp, error) {
	return compile(expr, syntax.Perl, false)
}

// CompilePOSIX is like Compile but restricts the regular expression to POSIX
// ERE (egrep) syntax and changes the match semantics to leftmost-longest.
func CompilePOSIX(expr string) (*Regexp, error) {
	return compile(expr, syntax.POSIX, true)
}

// MustCompile is like Compile but panics if the expression cannot be parsed.
func MustCompile(str string) *Regexp {
	re, err := Compile(str)
	if err != nil {
		panic(`regexp: Compile(` + quote(str) + `): ` + err.Error())
	}
	return re
}

// MustCompilePOSIX is like CompilePOSIX but panics if the expression cannot
// be parsed.
func MustCompilePOSIX(str string) *Regexp {
	re, err := CompilePOSIX(str)
	if err != nil {
		panic(`regexp: CompilePOSIX(` + quote(str) + `): ` + err.Error())
	}
	return re
}

func quote(s string) string {
	if strconv.CanBackquote(s) {
		return "`" + s + "`"
	}
	return strconv.Quote(s)
}

func compile(expr string, mode syntax.Flags, longest bool) (*Regexp, error) {
	c, admit := cacheLookup(expr, mode)
	if c != nil {
		return &Regexp{expr: expr, c: c, longest: longest}, nil
	}
	if admit {
		expr = cacheExpr(expr)
	}
	re, err := syntax.Parse(expr, mode)
	if err != nil {
		return nil, err
	}
	// The Regexp, its core and the names of a pattern without captures
	// share one allocation; so does the plan of a class run.
	var r *Regexp
	var names []string
	var rp *runPlan
	if isRunCandidate(re) {
		a := &struct {
			re    Regexp
			c     core
			names [1]string
			run   runPlan
		}{}
		r, c, names = &a.re, &a.c, a.names[:]
		rp = &a.run
	} else {
		a := &struct {
			re    Regexp
			c     core
			names [1]string
		}{}
		r, c, names = &a.re, &a.c, a.names[:]
	}
	c.expr, c.mode, c.numSubexp = expr, mode, re.MaxCap()
	if c.numSubexp == 0 {
		c.names = names
	} else {
		c.names = re.CapNames()
	}
	c.analyze(re, rp)
	switch {
	case c.kind == planWild:
		c.matchFn = matchWild
	case c.kind == planDFA && c.pf == nil:
		c.matchFn = matchDFA
	default:
		c.matchFn = (*core).match
	}
	*r = Regexp{expr: expr, c: c, longest: longest}
	if admit {
		remember(c, mode)
	}
	return r, nil
}

// program returns the rune program.
func (c *core) program() *prog { return c.engines().prog }

// parse parses the expression again, for engines rebuilt after a release.
func (c *core) parse() *syntax.Regexp {
	re, err := syntax.Parse(c.expr, c.mode)
	if err != nil {
		panic("regexp: reparse failed: " + err.Error())
	}
	return re
}

// String returns the source text used to compile the regular expression.
func (re *Regexp) String() string { return re.expr }

// Copy returns a new Regexp object copied from re. Calling Longest on one
// copy does not affect another.
//
// Deprecated: In earlier releases, when using a Regexp in multiple
// goroutines, giving each goroutine its own copy helped to avoid lock
// contention. As of Go 1.12, using Copy is no longer necessary to avoid lock
// contention. Copy may still be appropriate if the reason for its use is to
// make two copies with different Longest settings.
func (re *Regexp) Copy() *Regexp {
	re2 := *re
	return &re2
}

// Longest makes future searches prefer leftmost-longest matches.
func (re *Regexp) Longest() { re.longest = true }

// NumSubexp returns the number of parenthesized subexpressions in this
// Regexp.
func (re *Regexp) NumSubexp() int { return re.c.numSubexp }

// SubexpNames returns the names of the parenthesized subexpressions in this
// Regexp. The name for the first sub-expression is names[1], so that if m is
// a match slice, the name for m[i] is SubexpNames()[i]. Since the Regexp as
// a whole cannot be named, names[0] is always the empty string. The slice
// should not be modified.
func (re *Regexp) SubexpNames() []string { return re.c.names }

// SubexpIndex returns the index of the first subexpression with the given
// name, or -1 if there is no subexpression with that name.
func (re *Regexp) SubexpIndex(name string) int {
	if name != "" {
		for i, s := range re.c.names {
			if name == s {
				return i
			}
		}
	}
	return -1
}

// MarshalText implements encoding.TextMarshaler. The output matches that of
// calling the String method.
func (re *Regexp) MarshalText() ([]byte, error) {
	return []byte(re.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler by calling Compile on
// the encoded value.
func (re *Regexp) UnmarshalText(text []byte) error {
	newRE, err := Compile(string(text))
	if err != nil {
		return err
	}
	*re = *newRE
	return nil
}

var specialBytes [16]byte

func special(b byte) bool {
	return b < utf8.RuneSelf && specialBytes[b%16]&(1<<(b/16)) != 0
}

func init() {
	for _, b := range []byte(`\.+*?()|[]{}^$`) {
		specialBytes[b%16] |= 1 << (b / 16)
	}
}

// QuoteMeta returns a string that escapes all regular expression
// metacharacters inside the argument text; the returned string is a regular
// expression matching the literal text.
func QuoteMeta(s string) string {
	var i int
	for i = 0; i < len(s); i++ {
		if special(s[i]) {
			break
		}
	}
	if i >= len(s) {
		return s
	}
	b := make([]byte, 2*len(s)-i)
	copy(b, s[:i])
	j := i
	for ; i < len(s); i++ {
		if special(s[i]) {
			b[j] = '\\'
			j++
		}
		b[j] = s[i]
		j++
	}
	return string(b[:j])
}

// bstr views b as a string without copying; the string must not outlive the
// call.
func bstr(b []byte) string {
	return unsafe.String(unsafe.SliceData(b), len(b))
}

// MatchString reports whether the string s contains any match of the
// regular expression pattern.
func MatchString(pattern string, s string) (matched bool, err error) {
	re, err := Compile(pattern)
	if err != nil {
		return false, err
	}
	return re.MatchString(s), nil
}

// Match reports whether the byte slice b contains any match of the regular
// expression pattern.
func Match(pattern string, b []byte) (matched bool, err error) {
	re, err := Compile(pattern)
	if err != nil {
		return false, err
	}
	return re.Match(b), nil
}

// MatchString reports whether the string s contains any match of the
// regular expression re.
func (re *Regexp) MatchString(s string) bool {
	// Written to stay within the inlining budget, so texts of impossible
	// length are rejected without a call.
	return uint(len(s)-re.c.minLen) <= re.c.lenSpan && re.c.matchFn(re.c, s)
}

// Match reports whether the byte slice b contains any match of the regular
// expression re.
func (re *Regexp) Match(b []byte) bool {
	c := re.c
	if uint(len(b)-c.minLen) > c.lenSpan {
		return false
	}
	return c.matchFn(c, bstr(b))
}

// FindString returns a string holding the text of the leftmost match in s
// of the regular expression. If there is no match, the return value is an
// empty string, but it will also be empty if the regular expression
// successfully matches an empty string. Use FindStringIndex or
// FindStringSubmatch if it is necessary to distinguish these cases.
func (re *Regexp) FindString(s string) string {
	a, b := re.find(s, 0)
	if a < 0 {
		return ""
	}
	return s[a:b]
}

// FindStringIndex returns a two-element slice of integers defining the
// location of the leftmost match in s of the regular expression. The match
// itself is at s[loc[0]:loc[1]]. A return value of nil indicates no match.
func (re *Regexp) FindStringIndex(s string) (loc []int) {
	a, b := re.find(s, 0)
	if a < 0 {
		return nil
	}
	return []int{a, b}
}

// Find returns a slice holding the text of the leftmost match in b of the
// regular expression. A return value of nil indicates no match.
func (re *Regexp) Find(b []byte) []byte {
	x, y := re.find(bstr(b), 0)
	if x < 0 {
		return nil
	}
	return b[x:y:y]
}

// FindIndex returns a two-element slice of integers defining the location
// of the leftmost match in b of the regular expression. The match itself is
// at b[loc[0]:loc[1]]. A return value of nil indicates no match.
func (re *Regexp) FindIndex(b []byte) (loc []int) {
	x, y := re.find(bstr(b), 0)
	if x < 0 {
		return nil
	}
	return []int{x, y}
}

func (re *Regexp) find(s string, pos int) (int, int) {
	if re.longest {
		return re.c.findLongest(s, pos)
	}
	return re.c.find(s, pos)
}

// allState is the position of an All iteration, so it can resume.
type allState struct {
	pos, count, prev int
	done             bool
}

// allMatches appends successive non-overlapping matches to dst until n
// have been found, following the standard library's rule for empty
// matches. With stopFull it returns early, without setting st.done, when
// dst is full.
func (re *Regexp) allMatches(dst []int, s string, n int, st *allState, stopFull bool) []int {
	switch c := re.c; {
	case c.kind == planRun && !c.run().wide && !c.run().lazy:
		return c.run().appendAll(dst, s, n, st, stopFull)
	case c.kind == planLitRun:
		// Every match from a start has one end, so Longest finds the same.
		return c.litRun().appendAll(c.pf, dst, s, n, st, stopFull)
	}
	end := len(s)
	for st.count < n && st.pos <= end {
		if stopFull && len(dst) == cap(dst) {
			return dst
		}
		a, b := re.find(s, st.pos)
		if a < 0 {
			break
		}
		accept := true
		if b == st.pos {
			if a == st.prev {
				accept = false
			}
			if st.pos < end {
				if s[st.pos] < utf8.RuneSelf {
					st.pos++
				} else {
					_, w := utf8.DecodeRuneInString(s[st.pos:])
					st.pos += w
				}
			} else {
				st.pos = end + 1
			}
		} else {
			st.pos = b
		}
		st.prev = b
		if accept {
			dst = append(dst, a, b)
			st.count++
		}
	}
	st.done = true
	return dst
}

// pairsPool holds match-pair scratch for the All routines, which collect
// matches first and then allocate their results at the exact size. Results
// that fit in a small stack buffer skip the pool.
var pairsPool = sync.Pool{New: func() any { return new([]int) }}

const smallPairs = 32

func (re *Regexp) collect(s string, n int, small *[smallPairs]int) (*[]int, []int) {
	if n < 0 {
		n = len(s) + 1
	}
	st := allState{prev: -1}
	m := re.allMatches(small[:0], s, n, &st, true)
	if st.done {
		return nil, m
	}
	// More matches than the buffer holds: continue in pooled scratch. The
	// pool keeps the grown slice, so release needs only p.
	p := pairsPool.Get().(*[]int)
	pm := append((*p)[:0], m...)
	pm = re.allMatches(pm, s, n, &st, false)
	*p = pm
	return p, pm
}

func release(p *[]int) {
	if p != nil && cap(*p) <= 1<<16 {
		*p = (*p)[:0]
		pairsPool.Put(p)
	}
}

// findAllIndex returns the matches of FindAllStringIndex. Past the stack
// buffer the pairs go to bounded chunks that the result keeps. Growing a
// result never copies earlier chunks or keeps an obsolete large buffer.
func (re *Regexp) findAllIndex(s string, n int) [][]int {
	var small [smallPairs]int
	if n < 0 {
		n = len(s) + 1
	}
	st := allState{prev: -1}
	m := re.allMatches(small[:0], s, n, &st, true)
	if st.done {
		return indexResult(m)
	}
	const chunkInts = 2048
	var local [8][]int
	chunks := local[:0]
	flat := append(make([]int, 0, min(chunkInts, pairsCap(len(m), s, n, &st))), m...)
	for {
		flat = re.allMatches(flat, s, n, &st, true)
		if len(flat) != 0 {
			chunks = append(chunks, flat)
		}
		if st.done {
			break
		}
		remaining := pairsCap(2*st.count, s, n, &st) - 2*st.count
		flat = make([]int, 0, min(chunkInts, remaining))
	}
	out := make([][]int, st.count)
	k := 0
	for _, chunk := range chunks {
		for i := 0; i < len(chunk); i += 2 {
			out[k] = chunk[i : i+2 : i+2]
			k++
		}
	}
	return out
}

// pairsCap is the capacity for the pairs of an All search that has found k
// pair ints up to st.pos: the count the match rate so far predicts for all
// of s and a quarter, at least twice k, at most 16 times k and what the
// rest of s can hold. It is even, as pairs are appended two ints at a time.
func pairsCap(k int, s string, n int, st *allState) int {
	c := 2 * k
	if st.pos > 0 {
		c = max(c, int(float64(k)*float64(len(s))/float64(st.pos)*1.25))
	}
	c = min(c, 16*k, k+2*min(n-st.count, len(s)-st.pos+1))
	return max(c&^1, k+2)
}

// indexResult builds [][]int results: one allocation for up to four
// matches, two otherwise.
func indexResult(m []int) [][]int {
	switch len(m) {
	case 0:
		return nil
	case 2:
		r := new(struct {
			out  [1][]int
			flat [2]int
		})
		copy(r.flat[:], m)
		r.out[0] = r.flat[0:2:2]
		return r.out[:]
	case 4, 6, 8:
		r := new(struct {
			out  [4][]int
			flat [8]int
		})
		copy(r.flat[:], m)
		k := len(m) / 2
		for i := 0; i < k; i++ {
			r.out[i] = r.flat[2*i : 2*i+2 : 2*i+2]
		}
		return r.out[:k:k]
	}
	flat := make([]int, len(m))
	copy(flat, m)
	out := make([][]int, len(m)/2)
	for k := range out {
		out[k] = flat[2*k : 2*k+2 : 2*k+2]
	}
	return out
}

// FindAllString is the 'All' version of FindString; it returns a slice of
// all successive matches of the expression, as defined by the 'All'
// description in the package comment. A return value of nil indicates no
// match.
func (re *Regexp) FindAllString(s string, n int) []string {
	var small [smallPairs]int
	p, m := re.collect(s, n, &small)
	var result []string
	if len(m) > 0 {
		result = make([]string, len(m)/2)
		for k := range result {
			result[k] = s[m[2*k]:m[2*k+1]]
		}
	}
	release(p)
	return result
}

// FindAllStringIndex is the 'All' version of FindStringIndex; it returns a
// slice of all successive matches of the expression, as defined by the 'All'
// description in the package comment. A return value of nil indicates no
// match.
func (re *Regexp) FindAllStringIndex(s string, n int) [][]int {
	return re.findAllIndex(s, n)
}

// FindAll is the 'All' version of Find; it returns a slice of all successive
// matches of the expression, as defined by the 'All' description in the
// package comment. A return value of nil indicates no match.
func (re *Regexp) FindAll(b []byte, n int) [][]byte {
	var small [smallPairs]int
	p, m := re.collect(bstr(b), n, &small)
	var result [][]byte
	if len(m) > 0 {
		result = make([][]byte, len(m)/2)
		for k := range result {
			x, y := m[2*k], m[2*k+1]
			result[k] = b[x:y:y]
		}
	}
	release(p)
	return result
}

// FindAllIndex is the 'All' version of FindIndex; it returns a slice of all
// successive matches of the expression, as defined by the 'All' description
// in the package comment. A return value of nil indicates no match.
func (re *Regexp) FindAllIndex(b []byte, n int) [][]int {
	return re.findAllIndex(bstr(b), n)
}

// --- search orchestration ---

func (c *core) engines() *engines {
	if e := c.eng.Load(); e != nil {
		e.touch()
		return e
	}
	return c.buildEngines()
}

func (c *core) buildEngines() *engines {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.eng.Load(); e != nil {
		return e
	}
	re := c.tree.Swap(nil)
	if re == nil {
		re = c.parse()
	}
	e := &engines{prog: compileProg(re, c.numSubexp)}
	if c.numSubexp > 0 {
		// The capture offsets come from the tree in hand, so a first
		// submatch search does not parse the expression again.
		e.fixed = capOffsets(re, c.numSubexp)
	}
	e.used.Store(true)
	c.eng.Store(e)
	watch(c, e)
	return e
}

// forward returns the forward DFA, building it on first use.
func (c *core) forward(e *engines) *lazyDFA {
	if d := e.fwd.Load(); d != nil {
		return d
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := e.fwd.Load(); d != nil {
		return d
	}
	d := newLazyDFA(buildForward(e.prog), false)
	d.usePF = c.pf != nil
	e.fwd.Store(d)
	return d
}

// reverse returns the reverse DFA, building it on first use.
func (c *core) reverse(e *engines) *lazyDFA {
	if d := e.rev.Load(); d != nil {
		return d
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := e.rev.Load(); d != nil {
		return d
	}
	d := newLazyDFA(buildReverse(e.prog), true)
	e.rev.Store(d)
	return d
}

// dfaBudget bounds the DFA cache of one direction. It grows with the size of
// the automaton and of a state, so the working set of large alternations
// fits.
func dfaBudget(nodes, stride int) int {
	b := 16<<10 + (nodes/4+32)*(4*stride+64)
	if b > 1<<20 {
		b = 1 << 20
	}
	return b
}

func (c *core) getPike(e *engines) *pikeMachine {
	if m, ok := e.pike.Get().(*pikeMachine); ok {
		return m
	}
	return newPikeMachine(e.prog)
}

func (c *core) pikeFind(s string, pos int) (int, int) {
	e := c.engines()
	m := c.getPike(e)
	m.reset(2)
	a, b := -1, -1
	if m.exec(s, pos, c.anchorStart, pikeFirst, 0) {
		a, b = m.match[0], m.match[1]
	}
	e.pike.Put(m)
	return a, b
}

// reject reports whether s cannot contain a match, from length and the
// literals an anchored pattern must start or end with.
func (c *core) reject(s string, pos int) bool {
	if len(s)-pos < c.minLen {
		return true
	}
	if c.anchorStart && (pos > 0 || c.reqPrefix != "" && !strings.HasPrefix(s, c.reqPrefix)) {
		return true
	}
	return c.reqSuffix != "" && !strings.HasSuffix(s, c.reqSuffix)
}

func (c *core) match(s string) bool {
	if c.kind != planDFA && c.kind < planEnd {
		return c.matchPlan(s)
	}
	if c.kind == planOnePassLit {
		var m [2]int
		return c.matchOnePassLit(s, m[:])
	}
	if c.reject(s, 0) {
		return false
	}
	e := c.engines()
	if c.kind == planEnd {
		if start, res := c.endStart(e, s, 0); res == resOK {
			return start >= 0
		}
		if ok, res := c.forward(e).isMatch(s, false); res == resOK {
			return ok
		}
	} else if c.pf == nil {
		ok, res := c.forward(e).isMatch(s, c.anchorStart)
		if res == resOK {
			return ok
		}
	} else {
		end, _, res := c.forward(e).find(s, 0, c.anchorStart, true, c.pf)
		if res == resOK {
			return end >= 0
		}
	}
	a, _ := c.pikeFind(s, 0)
	return a >= 0
}

// matchDFA is the match function of DFA patterns without a prefilter.
func matchDFA(c *core, s string) bool {
	if len(s) < c.minLen || len(s) >= 8 && (!c.rejPre.ok(load64(s, 0)) || !c.rejSuf.ok(load64(s, len(s)-8))) {
		return false
	}
	if e := c.eng.Load(); e != nil {
		// rejPre and rejSuf hold the first and last 8 bytes of reqPrefix and
		// reqSuffix, so the full tests matter only for longer literals.
		if len(c.reqPrefix) > 8 && !strings.HasPrefix(s, c.reqPrefix) ||
			len(c.reqSuffix) > 8 && !strings.HasSuffix(s, c.reqSuffix) {
			return false
		}
		e.touch()
		if d := e.fwd.Load(); d != nil {
			if ok, res := d.isMatch(s, c.anchorStart); res == resOK {
				return ok
			}
		}
	}
	return c.match(s)
}

// matchPlan is match for plans that decide without the DFA.
func (c *core) matchPlan(s string) bool {
	switch c.kind {
	case planWild:
		return c.wild().match(s)
	case planLiteral, planFold:
		return c.pf.find(s, 0) >= 0
	case planEmpty:
		return true
	case planRun:
		a, _ := c.run().find(s, 0)
		return a >= 0
	case planTailRun:
		a, _ := c.tailRun().find(s, 0)
		return a >= 0
	case planLitRun:
		a, _ := c.litRun().find(c.pf, s, 0)
		return a >= 0
	}
	a, _ := c.delim().find(s, 0)
	return a >= 0
}

func (c *core) find(s string, pos int) (int, int) {
	switch c.kind {
	case planLiteral, planFold:
		j := c.pf.find(s, pos)
		if j < 0 {
			return -1, -1
		}
		return j, j + c.fixedLen
	case planEmpty:
		return pos, pos
	case planRun:
		return c.run().find(s, pos)
	case planWild:
		if pos > 0 || !c.wild().match(s) {
			return -1, -1
		}
		return 0, len(s)
	case planDelim:
		return c.delim().find(s, pos)
	case planTailRun:
		return c.tailRun().find(s, pos)
	case planLitRun:
		return c.litRun().find(c.pf, s, pos)
	}
	if c.reject(s, pos) {
		return -1, -1
	}
	e := c.engines()
	switch c.kind {
	case planEnd:
		start, res := c.endStart(e, s, pos)
		if res != resOK {
			return c.findDFA(e, s, pos)
		}
		if start < 0 {
			return -1, -1
		}
		return start, len(s)
	case planInner:
		return c.findInner(e, s, pos)
	case planOnePassLit:
		var m [2]int
		if !c.matchOnePassLit(s, m[:]) {
			return -1, -1
		}
		return m[0], m[1]
	}
	return c.findDFA(e, s, pos)
}

// endStart finds the start of the match of an end-anchored pattern with the
// reverse DFA. For a pattern that can match U+FFFD it reports resQuit when
// the bytes scanned are not valid UTF-8.
func (c *core) endStart(e *engines, s string, pos int) (int, int) {
	start, stop, res := c.reverse(e).rfindStop(s, pos, len(s))
	if res == resOK && c.fffd && !utf8.ValidString(s[max(stop, pos):]) {
		res = resQuit
	}
	return start, res
}

// findDFA finds the end with the forward DFA, then the start.
func (c *core) findDFA(e *engines, s string, pos int) (int, int) {
	fwd := c.forward(e)
	if c.pf != nil {
		// Every match starts at a prefilter candidate, so the first
		// candidate with an anchored match is the leftmost start; no
		// reverse scan.
		// Failed anchored scans may overlap; past a work budget the
		// search continues unanchored, which keeps it linear.
		budget := 2*(len(s)-pos) + 256
		for p := pos; ; {
			cand := c.pf.find(s, p)
			if cand < 0 {
				return -1, -1
			}
			end, _, stop, res := fwd.search(s, cand, true, false, nil)
			if res != resOK {
				return c.pikeFind(s, pos)
			}
			if end >= 0 {
				return cand, end
			}
			p = cand + 1
			if budget -= stop - cand; budget < 0 {
				pos = p
				break
			}
		}
	}
	end, from, res := fwd.find(s, pos, c.anchorStart, false, c.pf)
	if res != resOK {
		return c.pikeFind(s, pos)
	}
	if end < 0 {
		return -1, -1
	}
	if c.fixedLen >= 0 {
		return end - c.fixedLen, end
	}
	if c.anchorStart {
		return pos, end
	}
	// No match starts before from: the prefilter skipped those positions.
	start, res := c.reverse(e).rfind(s, from, end)
	if res != resOK || start < 0 {
		return c.pikeFind(s, pos)
	}
	return start, end
}

// findLongest implements leftmost-longest search (POSIX and Longest). Both
// semantics start the match at the leftmost position where any match
// starts, so the leftmost-first plans find the start; the longest end from
// there comes from an anchored DFA that keeps going after matches.
func (c *core) findLongest(s string, pos int) (int, int) {
	a, b := c.find(s, pos)
	if a < 0 || c.fixedLen >= 0 || c.anchorEnd || c.kind == planWild || c.kind == planOnePassLit || c.kind == planLitRun || c.kind == planRun && !c.run().lazy {
		return a, b // every match from a has the same end
	}
	e := c.engines()
	if end, _, _, res := c.longest(e).search(s, a, true, false, nil); res == resOK && end >= b {
		return a, end
	}
	m := c.getPike(e)
	m.reset(2)
	if m.exec(s, a, true, pikeLongest, 0) {
		b = m.match[1]
	}
	e.pike.Put(m)
	return a, b
}

// longest returns the anchored leftmost-longest DFA, building it on first
// use.
func (c *core) longest(e *engines) *lazyDFA {
	if d := e.long.Load(); d != nil {
		return d
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := e.long.Load(); d != nil {
		return d
	}
	d := newLazyDFA(buildForward(e.prog), true)
	e.long.Store(d)
	return d
}
