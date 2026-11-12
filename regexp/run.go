package regexp

import (
	"math/bits"
	"regexp/syntax"
	"sync"
	"unicode/utf8"
)

// runPlan matches patterns that are a run of one character class, such as
// \s+, \d{11} or \p{Han}+: find the first character in the class, then
// extend. Characters are decoded as the standard library does, so invalid
// UTF-8 is U+FFFD of width 1.
type runPlan struct {
	class    []rune
	in       *[256]bool // ASCII members, indexed by any byte; shared when interned
	nib      *runNibbles
	vec      bool // use the vector scan: runs are required to be long
	wide     bool // the class has non-ASCII members (including U+FFFD)
	min, max int  // max < 0: unbounded
	lazy     bool
}

// isRunCandidate reports whether re has the shape of a class run, so
// compile can reserve space for its plan.
func isRunCandidate(re *syntax.Regexp) bool {
	re = unwrapCaptures(re)
	return re.Op == syntax.OpPlus || re.Op == syntax.OpRepeat && re.Min >= 1
}

// init fills rp for re and reports whether re is a class run.
func (rp *runPlan) init(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpPlus:
		rp.min, rp.max = 1, -1
	case syntax.OpRepeat:
		if re.Min < 1 || re.Max >= 0 && re.Max < re.Min {
			return false
		}
		rp.min, rp.max = re.Min, re.Max
	default:
		return false
	}
	rp.lazy = re.Flags&syntax.NonGreedy != 0
	sub := unwrapCaptures(re.Sub[0])
	switch sub.Op {
	case syntax.OpCharClass:
		rp.class = sub.Rune
	case syntax.OpAnyCharNotNL:
		rp.class = anyNotNLClass
	case syntax.OpAnyChar:
		rp.class = anyClass
	case syntax.OpLiteral:
		if len(sub.Rune) != 1 {
			return false
		}
		r := sub.Rune[0]
		rp.class = []rune{r, r}
		if sub.Flags&syntax.FoldCase != 0 {
			if o := foldOrbit(r); o != nil {
				rp.class = o
			}
		}
	default:
		return false
	}
	if len(rp.class) == 0 {
		return false
	}
	t, wide := runTable(rp.class)
	rp.in, rp.nib, rp.wide = &t.in, &t.nib, wide
	if !wide {
		rp.class = nil // the table holds every member
	}
	rp.vec = hasSpanKernel && rp.min >= 16
	return true
}

// unit reports whether the character at s[i] is in the class, and its width.
func (rp *runPlan) unit(s string, i int) (bool, int) {
	b := s[i]
	if b < utf8.RuneSelf {
		return rp.in[b], 1
	}
	r, w := utf8.DecodeRuneInString(s[i:])
	return rp.wide && classHas(rp.class, r), w
}

func (rp *runPlan) find(s string, pos int) (int, int) {
	if !rp.wide && !rp.lazy {
		return rp.findASCII(s, pos)
	}
	i := pos
	n := len(s)
	for i < n {
		// First character in the class.
		p := -1
		if !rp.wide {
			for ; i < n; i++ {
				if rp.in[s[i]] {
					p = i
					break
				}
			}
		} else {
			for i < n {
				ok, w := rp.unit(s, i)
				if ok {
					p = i
					break
				}
				i += w
			}
		}
		if p < 0 {
			return -1, -1
		}
		// Extend.
		j, k := p, 0
		for j < n && (rp.max < 0 || k < rp.max) {
			if rp.lazy && k == rp.min {
				break
			}
			ok, w := rp.unit(s, j)
			if !ok {
				break
			}
			j += w
			k++
		}
		if k >= rp.min {
			return p, j
		}
		i = j
	}
	return -1, -1
}

// findASCII is find for greedy runs of an ASCII-only class: one byte is
// one character, and non-ASCII bytes are never members.
func (rp *runPlan) findASCII(s string, i int) (int, int) {
	if rp.vec {
		return rp.findVec(s, i)
	}
	in := rp.in
	n := len(s)
	for {
		for i < n && !in[s[i]] {
			i++
		}
		if i >= n {
			return -1, -1
		}
		p := i
		lim := n
		if rp.max >= 0 && n-p > rp.max {
			lim = p + rp.max
		}
		for i < lim && in[s[i]] {
			i++
		}
		if i-p >= rp.min {
			return p, i
		}
	}
}

// findVec is findASCII with the vector scan, for runs that are long or
// required to be long (min >= 16): gaps and runs are tested a few bytes at
// a time before handing over.
func (rp *runPlan) findVec(s string, i int) (int, int) {
	in := rp.in
	n := len(s)
	for {
		for e := min(n, i+48); i < e && !in[s[i]]; {
			i++
		}
		if i < n && !in[s[i]] {
			i += span(s[i:], &rp.nib.lo, &rp.nib.hi, in, false)
		}
		if i >= n {
			return -1, -1
		}
		p := i
		lim := n
		if rp.max >= 0 && n-p > rp.max {
			lim = p + rp.max
		}
		for e := min(lim, i+32); i < e && in[s[i]]; {
			i++
		}
		if i < lim && in[s[i]] {
			i += span(s[i:lim], &rp.nib.lo, &rp.nib.hi, in, true)
		}
		if i-p >= rp.min {
			return p, i
		}
	}
}

// appendAll is allMatches for greedy ASCII runs. Their matches are never
// empty, so they follow each other directly.
func (rp *runPlan) appendAll(dst []int, s string, n int, st *allState, stopFull bool) []int {
	if !rp.vec && rp.max < 0 {
		return rp.appendRuns(dst, s, n, st, stopFull)
	}
	for st.count < n {
		if stopFull && len(dst) == cap(dst) {
			return dst
		}
		a, b := rp.findASCII(s, st.pos)
		if a < 0 {
			break
		}
		dst = append(dst, a, b)
		st.pos = b
		st.count++
	}
	st.done = true
	return dst
}

// appendRuns is appendAll for unbounded runs without the vector scan, with
// the scan inlined so the loop state stays in registers across matches.
func (rp *runPlan) appendRuns(dst []int, s string, n int, st *allState, stopFull bool) []int {
	if hasSpanKernel {
		return rp.appendRunMasks(dst, s, n, st, stopFull)
	}
	in, m := rp.in, rp.min
	i, count := st.pos, st.count
	for count < n {
		if stopFull && len(dst) == cap(dst) {
			st.pos, st.count = i, count
			return dst
		}
		for i < len(s) && !in[s[i]] {
			i++
		}
		if i >= len(s) {
			break
		}
		p := i
		for i < len(s) && in[s[i]] {
			i++
		}
		if i-p >= m {
			dst = append(dst, p, i)
			count++
		}
	}
	st.pos, st.count, st.done = i, count, true
	return dst
}

// appendRunMasks is appendRuns over the membership masks of 64-byte
// blocks. A run boundary costs a few bit operations instead of a loop exit
// the branch predictor misses, which matters for short runs such as the
// spaces between words.
func (rp *runPlan) appendRunMasks(dst []int, s string, n int, st *allState, stopFull bool) []int {
	var masks [16]uint64
	m, count := rp.min, st.count
	if count >= n {
		st.done = true
		return dst
	}
	p, inRun := 0, false
	for o := st.pos; o < len(s); {
		w := min(len(s)-o, 64*len(masks))
		nb := w / 64
		classMasks(s[o:o+64*nb], &rp.nib.lo, &rp.nib.hi, rp.in, masks[:nb])
		if w > 64*nb {
			masks[nb] = blockMask(s[o+64*nb:o+w], rp.in) // bits past the end stay clear
			nb++
		}
		for k, bm := range masks[:nb] {
			ob := o + 64*k
			for j := 0; j < 64; {
				if !inRun {
					x := bm >> j
					if x == 0 {
						break
					}
					j += bits.TrailingZeros64(x)
					p, inRun = ob+j, true
					continue
				}
				x := ^bm >> j
				if x == 0 {
					break
				}
				j += bits.TrailingZeros64(x)
				inRun = false
				if e := ob + j; e-p >= m {
					if stopFull && len(dst) == cap(dst) {
						st.pos, st.count = p, count
						return dst
					}
					dst = append(dst, p, e)
					if count++; count >= n {
						st.pos, st.count, st.done = e, count, true
						return dst
					}
				}
			}
		}
		o += w
	}
	if inRun && len(s)-p >= m {
		if stopFull && len(dst) == cap(dst) {
			st.pos, st.count = p, count
			return dst
		}
		dst = append(dst, p, len(s))
		count++
	}
	st.pos, st.count, st.done = len(s), count, true
	return dst
}

// runTables interns the membership tables of classes by their ASCII part:
// patterns over the same common classes (\s, \d, \w, ...) share one.
var runTables struct {
	sync.Mutex
	m map[[2]uint64]*runTab
}

type runNibbles struct{ lo, hi [16]byte }

type runTab struct {
	in  [256]bool
	nib runNibbles
}

// runTable returns the ASCII membership tables of a sorted class and
// whether the class has non-ASCII members.
func runTable(class []rune) (*runTab, bool) {
	var key [2]uint64
	wide := false
	for i := 0; i+1 < len(class); i += 2 {
		lo, hi := class[i], class[i+1]
		if hi >= utf8.RuneSelf {
			wide = true
		}
		if lo >= utf8.RuneSelf {
			break
		}
		for r := lo; r <= hi && r < utf8.RuneSelf; r++ {
			key[r>>6] |= 1 << (r & 63)
		}
	}
	runTables.Lock()
	defer runTables.Unlock()
	if t := runTables.m[key]; t != nil {
		return t, wide
	}
	t := new(runTab)
	for b := 0; b < 128; b++ {
		t.in[b] = key[b>>6]&(1<<(b&63)) != 0
	}
	t.nib.lo, t.nib.hi = nibbleTables(&t.in)
	if runTables.m == nil {
		runTables.m = make(map[[2]uint64]*runTab)
	}
	if len(runTables.m) < 4096 {
		runTables.m[key] = t
	}
	return t, wide
}

// tailRunPlan matches C{n,} D{0,k}: a greedy run of an ASCII class C
// followed by up to k characters of an ASCII class D disjoint from C, such
// as base64 runs with their padding. The run ends at the first byte not
// in C, so where D may start is fixed and taking it greedily is what
// leftmost-first matching does.
type tailRunPlan struct {
	run  runPlan
	tail *[256]bool
	tmax int
}

func newTailRunPlan(re *syntax.Regexp) *tailRunPlan {
	if re.Op != syntax.OpConcat || len(re.Sub) != 2 {
		return nil
	}
	head, t := unwrapCaptures(re.Sub[0]), unwrapCaptures(re.Sub[1])
	if !isRunCandidate(head) || t.Flags&syntax.NonGreedy != 0 {
		return nil
	}
	var tmax int
	switch t.Op {
	case syntax.OpQuest:
		tmax = 1
	case syntax.OpStar:
		tmax = -1
	case syntax.OpRepeat:
		if t.Min != 0 || t.Max < 0 {
			return nil
		}
		tmax = t.Max
	default:
		return nil
	}
	var run, tr runPlan
	if !run.init(head) || run.wide || run.lazy || run.max >= 0 {
		return nil
	}
	sub := syntax.Regexp{Op: syntax.OpPlus, Sub: t.Sub, Flags: t.Flags}
	if !tr.init(&sub) || tr.wide {
		return nil
	}
	for b := 0; b < 128; b++ {
		if tr.in[b] && run.in[b] {
			return nil
		}
	}
	return &tailRunPlan{run: run, tail: tr.in, tmax: tmax}
}

func (tp *tailRunPlan) find(s string, pos int) (int, int) {
	a, b := tp.run.findASCII(s, pos)
	if a < 0 {
		return -1, -1
	}
	for k := 0; b < len(s) && (tp.tmax < 0 || k < tp.tmax) && tp.tail[s[b]]; k++ {
		b++
	}
	return a, b
}

// litRunPlan matches L C{n,m}: a case-sensitive literal followed by a
// greedy run of an ASCII class, such as key prefixes with their bodies
// (sk-[A-Za-z0-9]{20,}) or \.(-\d+). A match starts at the first
// occurrence of L followed by at least n members of C and takes the whole
// run, up to m. Every match from a given start has that one end, so it is
// also the leftmost-longest match.
type litRunPlan struct {
	lit  string
	run  runPlan
	caps []int32 // fixed capture offsets (see capInfo.fixed), or nil
}

func newLitRunPlan(re *syntax.Regexp) *litRunPlan {
	// The parts are those of a concatenation, each possibly inside a
	// capture: literals, then the run. The last part may itself be such a
	// concatenation, as in \.(-\d+).
	var buf [8]*syntax.Regexp
	parts := buf[:0]
	for depth := 0; depth < 2; depth++ {
		re = unwrapCaptures(re)
		if re.Op != syntax.OpConcat || len(parts)+len(re.Sub) > len(buf) {
			break
		}
		for _, p := range re.Sub {
			parts = append(parts, unwrapCaptures(p))
		}
		re = parts[len(parts)-1]
		if unwrapCaptures(re).Op != syntax.OpConcat {
			break
		}
		parts = parts[:len(parts)-1]
	}
	if len(parts) < 2 {
		return nil
	}
	n := 0
	for _, p := range parts[:len(parts)-1] {
		if p.Op != syntax.OpLiteral || p.Flags&syntax.FoldCase != 0 || hasSpecialRune(p.Rune) {
			return nil
		}
		n += len(p.Rune)
	}
	last := parts[len(parts)-1]
	if n == 0 || last.Op == syntax.OpConcat || !isRunCandidate(last) || last.Flags&syntax.NonGreedy != 0 {
		return nil
	}
	var rp runPlan
	if !rp.init(last) || rp.wide {
		return nil
	}
	var rb [16]rune
	lit := rb[:0]
	for _, p := range parts[:len(parts)-1] {
		lit = append(lit, p.Rune...)
	}
	return &litRunPlan{lit: string(lit), run: rp}
}

// appendAll is allMatches for litRun plans. Their matches are never empty,
// so they follow each other directly.
func (lr *litRunPlan) appendAll(pf *prefilter, dst []int, s string, n int, st *allState, stopFull bool) []int {
	for st.count < n {
		if stopFull && len(dst) == cap(dst) {
			return dst
		}
		a, b := lr.find(pf, s, st.pos)
		if a < 0 {
			break
		}
		dst = append(dst, a, b)
		st.pos, st.prev = b, b
		st.count++
	}
	st.done = true
	return dst
}

// find returns the leftmost match at or after pos, finding candidates for
// L with pf.
func (lr *litRunPlan) find(pf *prefilter, s string, pos int) (int, int) {
	in, n := lr.run.in, len(lr.lit)
	for {
		j := pf.find(s, pos)
		if j < 0 {
			return -1, -1
		}
		i := j + n
		lim := len(s)
		if lr.run.max >= 0 && lim-i > lr.run.max {
			lim = i + lr.run.max
		}
		e := i
		for e < lim && e-i < 32 && in[s[e]] {
			e++
		}
		if e-i == 32 && e < lim && in[s[e]] {
			e += span(s[e:lim], &lr.run.nib.lo, &lr.run.nib.hi, in, true)
		}
		if e-i >= lr.run.min {
			return j, e
		}
		// The run stopped at e short of the minimum (e < lim, as max >=
		// min). A later candidate starting by e-n would begin its run
		// inside this one and stop at e as well, with a shorter run.
		pos = max(j+1, e-n+1)
	}
}
