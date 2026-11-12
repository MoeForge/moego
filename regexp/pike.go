package regexp

import (
	"io"
	"regexp/syntax"
	"unicode/utf8"
)

// pike is the exact rune-level NFA simulation. It decodes input exactly as
// the standard library does (invalid UTF-8 is U+FFFD of width 1) and serves
// as the reference engine for submatches and as the linear-time fallback
// when the DFA gives up or meets invalid UTF-8.

type pikeQueue struct {
	sparse []uint32
	dense  []uint32 // pcs in priority order
	capIdx []int32  // per dense entry: offset into caps arena, or -1
	n      int
}

func (q *pikeQueue) init(n int) {
	if cap(q.sparse) < n {
		q.sparse = make([]uint32, n)
		q.dense = make([]uint32, n)
		q.capIdx = make([]int32, n)
	}
	q.sparse = q.sparse[:n]
	q.dense = q.dense[:n]
	q.capIdx = q.capIdx[:n]
	q.n = 0
}

func (q *pikeQueue) has(pc uint32) bool {
	i := q.sparse[pc]
	return int(i) < q.n && q.dense[i] == pc
}

func (q *pikeQueue) add(pc uint32) int {
	i := q.n
	q.sparse[pc] = uint32(i)
	q.dense[i] = pc
	q.capIdx[i] = -1
	q.n++
	return i
}

type pikeMachine struct {
	p       *prog
	q0, q1  pikeQueue
	ncap    int
	caps    [2][]int // capture arenas for q0 and q1
	scratch []int
	match   []int // captures of the best match so far
	stack   []pikeFrame
	entry   uint32 // if nonzero, exec starts here instead of at the program start
}

type pikeFrame struct {
	pc   uint32
	slot int32 // >= 0: restore scratch[slot] = val
	val  int
}

func newPikeMachine(p *prog) *pikeMachine { return &pikeMachine{p: p} }

func (m *pikeMachine) reset(ncap int) {
	n := len(m.p.insts)
	m.q0.init(n)
	m.q1.init(n)
	if ncap < 2 {
		ncap = 2
	}
	m.ncap = ncap
	if cap(m.scratch) < ncap {
		m.scratch = make([]int, ncap)
		m.match = make([]int, ncap)
	}
	m.scratch = m.scratch[:ncap]
	m.match = m.match[:ncap]
}

// add follows empty transitions from pc0 in priority order and queues every
// consuming instruction and match it reaches, each with a copy of the
// captures in m.scratch at that point.
func (m *pikeMachine) add(q *pikeQueue, arena *[]int, pc0 uint32, pos int, flag syntax.EmptyOp) {
	insts := m.p.insts
	stk := append(m.stack[:0], pikeFrame{pc: pc0, slot: -1})
	for len(stk) > 0 {
		f := stk[len(stk)-1]
		stk = stk[:len(stk)-1]
		if f.slot >= 0 {
			m.scratch[f.slot] = f.val
			continue
		}
		pc := f.pc
		for pc != 0 && !q.has(pc) {
			i := q.add(pc)
			in := &insts[pc]
			switch in.op {
			case opAlt:
				stk = append(stk, pikeFrame{pc: in.out1, slot: -1})
				pc = in.out
				continue
			case opNop:
				pc = in.out
				continue
			case opEmpty:
				if syntax.EmptyOp(in.arg)&^flag == 0 {
					pc = in.out
					continue
				}
			case opSave:
				if int(in.arg) < m.ncap {
					stk = append(stk, pikeFrame{slot: int32(in.arg), val: m.scratch[in.arg]})
					m.scratch[in.arg] = pos
				}
				pc = in.out
				continue
			case opMatch, opRune1, opClass, opFold1, opAny, opAnyNotNL:
				q.capIdx[i] = int32(len(*arena))
				*arena = append(*arena, m.scratch...)
			}
			break
		}
	}
	m.stack = stk[:0]
}

func (p *prog) matchRune(in *inst, r rune) bool {
	switch in.op {
	case opRune1:
		return rune(in.arg) == r
	case opClass:
		return classHas(p.classes[in.arg], r)
	case opFold1:
		return r|0x20 == rune(in.arg)
	case opAnyNotNL:
		return r != '\n'
	case opAny:
		return true
	}
	return false
}

func classHas(c []rune, r rune) bool {
	if len(c) <= 16 {
		for i := 0; i < len(c); i += 2 {
			if r < c[i] {
				return false
			}
			if r <= c[i+1] {
				return true
			}
		}
		return false
	}
	lo, hi := 0, len(c)/2
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		if c[2*m+1] < r {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo < len(c)/2 && c[2*lo] <= r
}

// decodeAt returns the rune at s[i:] as the standard library decodes it, or
// -1 with width 0 at the end.
func decodeAt(s string, i int) (rune, int) {
	if i >= len(s) {
		return -1, 0
	}
	if c := s[i]; c < utf8.RuneSelf {
		return rune(c), 1
	}
	return utf8.DecodeRuneInString(s[i:])
}

// runeBefore returns a rune with the same empty-width context as the rune
// ending at i (only newline and ASCII word characters matter), or -1 at 0.
func runeBefore(s string, i int) rune {
	if i <= 0 {
		return -1
	}
	if c := s[i-1]; c < utf8.RuneSelf {
		return rune(c)
	}
	return utf8.RuneError
}

// Pike modes.
const (
	pikeFirst   = iota // leftmost-first
	pikeLongest        // leftmost-longest
	// pikeEnd: anchored at pos, the highest-priority match that ends
	// exactly at end. With [pos, end] known from the automata, this gives
	// the submatches for either semantics.
	pikeEnd
)

// exec runs the program over s from pos. On success the captures (at least
// the [start, end] pair) are in m.match.
func (m *pikeMachine) exec(s string, pos int, anchored bool, mode, end int) bool {
	p := m.p
	insts := p.insts
	runq, nextq := &m.q0, &m.q1
	ra, na := &m.caps[0], &m.caps[1]
	runq.n, nextq.n = 0, 0
	*ra, *na = (*ra)[:0], (*na)[:0]
	matched := false
	start := pos
	if mode == pikeEnd {
		anchored = true
	}
	pc0 := p.start
	if m.entry != 0 {
		pc0 = m.entry
	}
	r, w := decodeAt(s, pos)
	prev := runeBefore(s, pos)
	for {
		if !matched && (!anchored || pos == start) {
			for i := range m.scratch {
				m.scratch[i] = -1
			}
			m.scratch[0] = pos
			m.add(runq, ra, pc0, pos, syntax.EmptyOpContext(prev, r))
		}
		if runq.n == 0 {
			break
		}
		r2, w2 := rune(-1), 0
		if w > 0 {
			r2, w2 = decodeAt(s, pos+w)
		}
		nflag := syntax.EmptyOpContext(r, r2)
		nextq.n = 0
		*na = (*na)[:0]
	step:
		for j := 0; j < runq.n; j++ {
			ci := runq.capIdx[j]
			if ci < 0 {
				continue // empty-width bookkeeping entry
			}
			in := &insts[runq.dense[j]]
			c := (*ra)[ci : int(ci)+m.ncap]
			if mode == pikeLongest && matched && c[0] > m.match[0] {
				continue // starts after the match found: cannot win
			}
			if in.op == opMatch {
				switch mode {
				case pikeFirst:
					copy(m.match, c)
					m.match[1] = pos
					matched = true
					break step // lower-priority threads lose
				case pikeLongest:
					if !matched || pos > m.match[1] {
						copy(m.match, c)
						m.match[1] = pos
						matched = true
					}
				default:
					if pos == end {
						copy(m.match, c)
						m.match[1] = pos
						return true
					}
				}
				continue
			}
			if w > 0 && p.matchRune(in, r) {
				copy(m.scratch, c)
				m.add(nextq, na, in.out, pos+w, nflag)
			}
		}
		if w == 0 || mode == pikeEnd && pos >= end {
			break
		}
		pos += w
		prev = r
		r, w = r2, w2
		runq, nextq = nextq, runq
		ra, na = na, ra
	}
	return matched
}

// execReader is exec for runes read from r, with one rune of lookahead.
// With any, it stops at the first match found and m.match is not set.
func (m *pikeMachine) execReader(rd io.RuneReader, anchored bool, mode int, any bool) bool {
	read := func() (rune, int) {
		c, n, err := rd.ReadRune()
		if err != nil || n <= 0 {
			return -1, 0
		}
		return c, n
	}
	p := m.p
	insts := p.insts
	runq, nextq := &m.q0, &m.q1
	ra, na := &m.caps[0], &m.caps[1]
	runq.n, nextq.n = 0, 0
	*ra, *na = (*ra)[:0], (*na)[:0]
	matched := false
	pos := 0
	r, w := read()
	r2, w2 := rune(-1), 0
	if w > 0 {
		r2, w2 = read()
	}
	prev := rune(-1)
	for {
		if !matched && (!anchored || pos == 0) {
			for i := range m.scratch {
				m.scratch[i] = -1
			}
			m.scratch[0] = pos
			m.add(runq, ra, p.start, pos, syntax.EmptyOpContext(prev, r))
		}
		if runq.n == 0 {
			break
		}
		nflag := syntax.EmptyOpContext(r, r2)
		nextq.n = 0
		*na = (*na)[:0]
	step:
		for j := 0; j < runq.n; j++ {
			ci := runq.capIdx[j]
			if ci < 0 {
				continue
			}
			in := &insts[runq.dense[j]]
			c := (*ra)[ci : int(ci)+m.ncap]
			if mode == pikeLongest && matched && c[0] > m.match[0] {
				continue
			}
			if in.op == opMatch {
				if mode == pikeFirst || !matched || pos > m.match[1] {
					copy(m.match, c)
					m.match[1] = pos
					matched = true
				}
				if mode == pikeFirst {
					break step
				}
				continue
			}
			if w > 0 && p.matchRune(in, r) {
				copy(m.scratch, c)
				m.add(nextq, na, in.out, pos+w, nflag)
			}
		}
		if w == 0 || any && matched {
			break
		}
		pos += w
		prev = r
		r, w = r2, w2
		if w > 0 {
			r2, w2 = read()
		} else {
			r2, w2 = -1, 0
		}
		runq, nextq = nextq, runq
		ra, na = na, ra
	}
	return matched
}
