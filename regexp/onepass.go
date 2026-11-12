package regexp

import (
	"regexp/syntax"
	"strings"
	"unicode"
)

// onePass reports whether the program is one-pass: it starts with \A, and
// at each alternation the next character
// selects at most one branch, where at most one branch may reach the match
// without reading. LiteralPrefix reports a result that depends on this
// verdict, so it follows the standard library's rules for it, including the
// loop rewrites below and the size limit.
func (p *prog) onePass() bool {
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
	hasAlt := false
	for i := range insts {
		if insts[i].op == opAlt {
			hasAlt = true
			break
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
			if (hasAlt || onePassEndOnly) && toMatch(in.out) {
				return false
			}
		}
	}

	// Branch targets of each alternation, after two rewrites of an
	// alternation A whose one branch is an alternation B and whose other
	// branch O is not: if B loops straight back to A, that loop leads to O
	// instead; then, if B's branch that was checked for the loop leads to
	// O, A skips B and goes to B's other branch.
	left := make([]uint32, 2*n)
	right := left[n:]
	left = left[:n]
	for i := range insts {
		left[i], right[i] = insts[i].out, insts[i].out1
	}
	isAlt := func(pc uint32) bool { return insts[pc].op == opAlt }
	for pc := range insts {
		if !isAlt(uint32(pc)) {
			continue
		}
		toB, other := &right[pc], &left[pc]
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
		loop, rest := &left[b], &right[b]
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
			x, y := left[pc], right[pc]
			if !check(x) || !check(y) || null[x] && null[y] {
				return false
			}
			null[pc] = null[x] || null[y]
			m, ok := mergeDisjoint(first[x], first[y])
			if !ok {
				return false
			}
			first[pc] = m
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
	return true
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
	pc := in.out
	for p.insts[pc].op == opNop {
		pc = p.insts[pc].out
	}
	// literalRun stops at U+FFFD itself, so an encoded U+FFFD in the
	// literal stands for a surrogate.
	lit, end := p.literalRun(pc, false)
	if !strings.Contains(lit, "\uFFFD") || !p.onePass() {
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
