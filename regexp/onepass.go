package regexp

import (
	"regexp/syntax"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"
)

// A onePassProg keeps the branch choices proved disjoint by onePass.
// The rune instructions and capture numbering belong to the original prog.
type onePassProg struct {
	prog
	alts        []onePassAlt
	first, last bset // possible first and last bytes of nonempty input
	prefix      string
	entry       uint32        // after the leading \A and prefix
	hits        atomic.Uint32 // successful interpreted searches, until table construction
	large       sync.Pool     // *[]int, for captures that do not fit on the stack
	table       atomic.Pointer[onePassTable]
}

type onePassAlt struct {
	left, right []rune
	empty       uint32 // branch that can match without reading, or opFail at 0
}

func (a *onePassAlt) next(in *inst, r rune) uint32 {
	if classHas(a.left, r) {
		return in.out
	}
	if classHas(a.right, r) {
		return in.out1
	}
	return a.empty
}

// onePassCaptures builds the anchored capture search independently of the
// engines that recover captures inside an already known match.
func (e *engines) onePassCaptures() *onePassProg {
	e.oneOnce.Do(func() {
		p := new(onePassProg)
		if e.prog.onePass(p) {
			e.one = p
		}
	})
	return e.one
}

// onePass reports whether the program is one-pass: it starts with \A, and
// at each alternation the next character
// selects at most one branch, where at most one branch may reach the match
// without reading. LiteralPrefix reports a result that depends on this
// verdict, so it follows the standard library's rules for it, including the
// loop rewrites below and the size limit. If dst is supplied, it also
// records the branches and prefix for execution.
func (p *prog) onePass(dst *onePassProg) bool {
	insts := p.insts
	n := len(insts)
	if p.start == 0 || n >= 1000 {
		return false
	}
	if in := &insts[p.start]; in.op != opEmpty || syntax.EmptyOp(in.arg)&syntax.EmptyBeginText == 0 {
		return false
	}
	// No alternation and no assertion other than \z may lead straight to
	// the match; in a program with alternations, or with onePassEndOnly,
	// nothing else may either.
	toMatch := func(pc uint32) bool { return insts[pc].op == opMatch }
	nalt := 0
	endOnly := true
	for i := range insts {
		if insts[i].op == opAlt {
			nalt++
		}
	}
	for i := range insts {
		in := &insts[i]
		switch in.op {
		case opAlt:
			if toMatch(in.out) || toMatch(in.out1) {
				return false
			}
		case opEmpty:
			if toMatch(in.out) && syntax.EmptyOp(in.arg)&syntax.EmptyEndText == 0 {
				return false
			}
		case opMatch, opFail:
		default:
			if toMatch(in.out) {
				if nalt != 0 || onePassEndOnly {
					return false
				}
				endOnly = false
			}
		}
	}

	// Branch targets of each alternation, after two rewrites of an
	// alternation A whose one branch is an alternation B and whose other
	// branch O is not: if B loops straight back to A, that loop leads to O
	// instead; then, if B's branch that was checked for the loop leads to
	// O, A skips B and goes to B's other branch.
	if nalt != 0 {
		insts = slices.Clone(insts)
	}
	if dst != nil {
		dst.prog = *p
		dst.insts = insts
		dst.alts = make([]onePassAlt, nalt)
	}
	isAlt := func(pc uint32) bool { return insts[pc].op == opAlt }
	alt := uint32(0)
	for pc := range insts {
		if !isAlt(uint32(pc)) {
			continue
		}
		in := &insts[pc]
		in.arg = alt
		alt++
		toB, other := &in.out1, &in.out
		if !isAlt(*toB) {
			toB, other = other, toB
			if !isAlt(*toB) {
				continue
			}
		}
		if isAlt(*other) {
			continue
		}
		b := *toB
		loop, rest := &insts[b].out, &insts[b].out1
		if *rest == uint32(pc) && *loop != uint32(pc) {
			loop, rest = rest, loop
		}
		if *loop == uint32(pc) {
			*loop = *other
		}
		if *other == *loop {
			*toB = *rest
		}
	}

	// The characters each instruction can read first, and whether it
	// reaches the match without reading, computed through empty
	// instructions. Each reading instruction queues its successor, whose
	// closure is examined in a later round; a closure revisiting an
	// instruction uses what is known about it so far.
	first := make([][]rune, n)
	null := make([]bool, n)
	read := make([]bool, n)
	seen := make([]uint32, n)
	round := uint32(0)
	queued := make([]bool, n)
	queue := []uint32{p.start}
	queued[p.start] = true
	var check func(pc uint32) bool
	check = func(pc uint32) bool {
		if seen[pc] == round {
			return true
		}
		seen[pc] = round
		in := &insts[pc]
		switch in.op {
		case opMatch, opFail:
			null[pc] = in.op == opMatch
		case opAlt:
			x, y := in.out, in.out1
			if !check(x) || !check(y) || null[x] && null[y] {
				return false
			}
			null[pc] = null[x] || null[y]
			m, ok := mergeDisjoint(first[x], first[y])
			if !ok {
				return false
			}
			first[pc] = m
			if dst != nil {
				a := onePassAlt{left: first[x], right: first[y]}
				if null[x] {
					a.empty = x
				} else if null[y] {
					a.empty = y
				}
				dst.alts[in.arg] = a
			}
		case opNop, opSave, opEmpty:
			if !check(in.out) {
				return false
			}
			null[pc] = null[in.out]
			first[pc] = first[in.out]
		default:
			null[pc] = false
			if read[pc] {
				break
			}
			read[pc] = true
			if !queued[in.out] {
				queued[in.out] = true
				queue = append(queue, in.out)
			}
			first[pc] = p.instRunes(in)
		}
		return true
	}
	for k := 0; k < len(queue); k++ {
		round++
		if !check(queue[k]) {
			return false
		}
	}
	if dst != nil {
		dst.prefix, dst.entry = p.onePassPrefix()
		if !endOnly || 2*(p.numCap+1) > maxStepSlots || n > maxStepInsts {
			dst.hits.Store(onePassTableHits)
		}
		if null[p.start] && !endOnly {
			dst.first.addRange(0, 255) // an empty prefix can match on any input
		} else {
			dst.first.addRuneBytes(first[p.start])
		}
		if !endOnly {
			dst.last.addRange(0, 255)
		} else {
			// The last rune must leave only empty instructions before match.
			for pc, in := range insts {
				if read[pc] && null[in.out] {
					dst.last.addRuneBytes(first[pc])
				}
			}
		}
	}
	return true
}

// addRuneBytes adds the ASCII bytes in rs. A non-ASCII member admits any
// high byte, including malformed input when the class contains U+FFFD.
func (b *bset) addRuneBytes(rs []rune) {
	for i := 0; i < len(rs); i += 2 {
		lo, hi := rs[i], rs[i+1]
		if hi >= utf8.RuneSelf {
			b.addRange(byte(min(lo, utf8.RuneSelf)), 255)
			break
		}
		b.addRange(byte(lo), byte(hi))
	}
}

// onePassPrefix returns the literal after the initial begin-text assertion.
// A capture ends the literal so that execution visits its save instruction.
func (p *prog) onePassPrefix() (string, uint32) {
	pc := p.insts[p.start].out
	for p.insts[pc].op == opNop {
		pc = p.insts[pc].out
	}
	return p.literalRun(pc, false)
}

// submatch writes into buf when the indexes fit. Otherwise it searches in
// scratch and allocates the caller's result only after a match.
func (p *onePassProg) submatch(s string, buf []int) []int {
	if len(s) > 0 && (!p.first.has(s[0]) || !p.last.has(s[len(s)-1])) || !strings.HasPrefix(s, p.prefix) {
		return nil
	}
	n := 2 * (p.numCap + 1)
	var small [smallSubmatch]int
	m := buf
	switch {
	case n <= len(buf):
	case n <= len(small):
		m = small[:]
	default:
		v, _ := p.large.Get().(*[]int)
		if v == nil {
			slots := make([]int, n)
			v = &slots
		}
		defer p.large.Put(v)
		m = *v
	}
	if !p.match(s, m[:n]) {
		return nil
	}
	if n <= len(buf) {
		return buf[:n]
	}
	// Older versions of slices.Clone append to m[:0:0], which makes
	// the local scratch escape even when the search fails.
	return append([]int(nil), m[:n]...)
}

// match follows a single instruction path. At an alternation the current
// rune selects the branch before any of its captures are written.
func (p *onePassProg) match(s string, m []int) bool {
	for i := range m {
		m[i] = -1
	}
	pos, pc := len(p.prefix), p.entry
	if table := p.table.Load(); table != nil {
		return table.match(s, pos, m)
	}
	r, width := decodeAt(s, pos)
	for {
		in := &p.insts[pc]
		switch in.op {
		case opFail:
			return false
		case opMatch:
			m[0], m[1] = 0, pos
			if p.hits.Load() < onePassTableHits && p.hits.Add(1) == onePassTableHits {
				p.table.Store(p.buildTable())
			}
			return true
		case opAlt:
			pc = p.alts[in.arg].next(in, r)
			continue
		case opSave:
			m[in.arg] = pos
		case opEmpty:
			if syntax.EmptyOp(in.arg)&^syntax.EmptyOpContext(runeBefore(s, pos), r) != 0 {
				return false
			}
		case opNop:
		default:
			if width == 0 || !p.matchRune(in, r) {
				return false
			}
			pos += width
			r, width = decodeAt(s, pos)
		}
		pc = in.out
	}
}

// onePassLit is the plan of one-pass patterns that start with \A and a
// literal containing a rune that has no UTF-8 encoding (a surrogate). The
// standard library starts such a search by comparing the literal byte for
// byte with the text, the surrogate written as the encoding of U+FFFD, and
// continues with the rest of the pattern after it. So the pattern matches
// text that starts with that encoding, although the rune itself matches
// nothing; reader input, which it reads rune by rune, never matches.
type onePassLit struct {
	lit string // the literal as bytes
	end uint32 // the instruction after it
}

// onePassLitPlan returns the onePassLit plan of p, or nil if it has none.
func (p *prog) onePassLitPlan() *onePassLit {
	in := &p.insts[p.start]
	if in.op != opEmpty || syntax.EmptyOp(in.arg)&syntax.EmptyBeginText == 0 {
		return nil
	}
	// literalRun stops at U+FFFD itself, so an encoded U+FFFD in the
	// literal stands for a surrogate.
	lit, end := p.onePassPrefix()
	if !strings.Contains(lit, "\uFFFD") || !p.onePass(nil) {
		return nil
	}
	return &onePassLit{lit: lit, end: end}
}

// matchOnePassLit matches a onePassLit pattern at the start of s. It fills
// dst (at least the [start, end] pair) and reports whether there is a match.
func (c *core) matchOnePassLit(s string, dst []int) bool {
	ol := c.onePassLit()
	if !strings.HasPrefix(s, ol.lit) {
		return false
	}
	e := c.engines()
	m := c.getPike(e)
	m.reset(len(dst))
	m.entry = ol.end
	// The rest of a one-pass pattern has a single way to match, so the
	// first match found is also the longest.
	ok := m.exec(s, len(ol.lit), true, pikeFirst, 0)
	m.entry = 0
	if ok {
		copy(dst, m.match)
		dst[0] = 0
	}
	e.pike.Put(m)
	return ok
}

// instRunes returns the characters a reading instruction accepts as sorted
// ranges.
func (p *prog) instRunes(in *inst) []rune {
	switch in.op {
	case opRune1:
		return []rune{rune(in.arg), rune(in.arg)}
	case opFold1:
		return asciiFold[in.arg]
	case opClass:
		return p.classes[in.arg]
	case opAnyNotNL:
		return anyNotNLClass
	}
	return []rune{0, unicode.MaxRune}
}

// mergeDisjoint merges two sorted range lists, failing if they share a
// character.
func mergeDisjoint(a, b []rune) ([]rune, bool) {
	if len(a) == 0 {
		return b, true
	}
	if len(b) == 0 {
		return a, true
	}
	out := make([]rune, 0, len(a)+len(b))
	for len(a) > 0 || len(b) > 0 {
		var lo, hi rune
		if len(b) == 0 || len(a) > 0 && a[0] <= b[0] {
			lo, hi, a = a[0], a[1], a[2:]
		} else {
			lo, hi, b = b[0], b[1], b[2:]
		}
		if len(out) > 0 && lo <= out[len(out)-1] {
			return nil, false
		}
		out = append(out, lo, hi)
	}
	return out, true
}
