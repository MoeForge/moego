package regexp

import (
	"math/bits"
	"regexp/syntax"
	"sync/atomic"
	"unicode/utf8"
	"unsafe"
)

// Submatches. A search finds the match [a, b] first. The captures are then
// those of the highest-priority path from a that ends exactly at b, which is
// the path that both leftmost-first and leftmost-longest matching report.
// They come from, in order of preference:
//   - fixed offsets, when everything before or after a group boundary has a
//     fixed byte length, as in <think>(.*?)</think>;
//   - a single-thread walk, while only one instruction can consume each
//     character (one-pass windows such as "((?:[^"\\]|\\.)*)");
//   - the backtracker, when the window is small;
//   - the Pike machine.

// capInfo is computed on the first submatch search of a pattern.
type capInfo struct {
	// fixed gives every capture boundary from slot 2 on as an offset from
	// the match start (>= 0) or end (^offset), or is nil.
	fixed []int32
	// noWalk is set once the single-thread walk meets a character two
	// instructions can consume.
	noWalk atomic.Bool
	// steps memoizes the walk's steps on ASCII bytes, per instruction the
	// walk resumes from (see walker.run), or is nil. A row has an entry per
	// class of cls: 0 when not known yet, else one of the step* values in
	// the low half and, for stepOne, the capture slots the step sets in the
	// high half.
	steps []atomic.Pointer[atomic.Uint32]
	cls   [utf8.RuneSelf]uint8 // see stepClasses
	ncls  int
}

const (
	stepNone  = 1 + iota // no instruction consumes the character
	stepTwo              // two instructions can consume it
	stepEmpty            // the step tests an empty-width assertion: not memoized
	stepMatch            // the path reaches a match at the window end
	stepOne              // one instruction consumes it; stepOne+pc resumes at pc
)

// maxStepSlots is the number of capture slots memoized steps can set, and
// maxStepInsts the size of the programs whose steps are memoized.
const (
	maxStepSlots = 16
	maxStepInsts = 256
)

// capScratch is the pooled scratch of the capture engines.
type capScratch struct {
	bt backtracker
	wk walker
}

func (c *core) capInfo(e *engines) *capInfo {
	if ci := e.caps.Load(); ci != nil {
		return ci
	}
	ci := &capInfo{fixed: e.fixed}
	if p := e.prog; ci.fixed == nil && 2*(p.numCap+1) <= maxStepSlots && len(p.insts) <= maxStepInsts {
		ci.steps = make([]atomic.Pointer[atomic.Uint32], len(p.insts))
		ci.ncls = stepClasses(p, &ci.cls)
	}
	if !e.caps.CompareAndSwap(nil, ci) {
		return e.caps.Load()
	}
	return ci
}

// captures fills dst[2:] with the captures of the match [a, b] = dst[0:2].
func (c *core) captures(s string, dst []int) {
	if c.kind == planOnePassLit {
		c.matchOnePassLit(s, dst)
		return
	}
	if c.kind == planLitRun && c.litRun().caps != nil {
		fixedCaptures(c.litRun().caps, dst)
		return
	}
	if c.kind == planDelim && c.delim().caps != nil {
		fixedCaptures(c.delim().caps, dst)
		return
	}
	a, b := dst[0], dst[1]
	e := c.engines()
	ci := c.capInfo(e)
	if f := ci.fixed; f != nil {
		fixedCaptures(f, dst)
		return
	}
	p := e.prog
	sc, _ := e.capPool.Get().(*capScratch)
	if sc == nil {
		sc = new(capScratch)
	}
	defer e.capPool.Put(sc)
	if !ci.noWalk.Load() {
		found, ok := sc.wk.run(p, ci, s, a, b, dst)
		if ok && found {
			return
		}
		ci.noWalk.Store(true)
	}
	if canBacktrack(p, a, b) && sc.bt.run(p, s, a, b, dst) {
		return
	}
	m := c.getPike(e)
	m.reset(len(dst))
	if m.exec(s, a, true, pikeEnd, b) {
		copy(dst, m.match)
	}
	e.pike.Put(m)
}

// stepClasses partitions the ASCII bytes so that each instruction of p
// accepts all the bytes of a class or none of them. A walk step from an
// instruction is then the same on every byte of a class. It returns the
// number of classes.
func stepClasses(p *prog, cls *[utf8.RuneSelf]uint8) int {
	var all [256]uint8
	n := 1
	for i := range p.insts {
		in := &p.insts[i]
		var set bset
		switch in.op {
		case opRune1:
			if in.arg >= utf8.RuneSelf {
				continue
			}
			set.addRange(byte(in.arg), byte(in.arg))
		case opAnyNotNL:
			set.addRange('\n', '\n')
		case opFold1, opClass:
			rs := p.instRanges(in)
			for k := 0; k+1 < len(rs) && rs[k] < utf8.RuneSelf; k += 2 {
				set.addRange(byte(rs[k]), byte(min(rs[k+1], utf8.RuneSelf-1)))
			}
		default:
			continue
		}
		n = refineBy(&all, n, &set)
	}
	n = 0
	for c := range cls {
		cls[c] = all[c]
		n = max(n, int(all[c])+1)
	}
	return n
}

// fixedCaptures fills dst[2:] from fixed offsets and the match dst[0:2].
func fixedCaptures(f []int32, dst []int) {
	a, b := dst[0], dst[1]
	for i, off := range f {
		if off >= 0 {
			dst[i+2] = a + int(off)
		} else {
			dst[i+2] = b - int(^off)
		}
	}
}

// capOffsets returns the fixed offsets of the capture boundaries of re (see
// capInfo.fixed), or nil if some boundary has none. Only groups reached
// through concatenations and other groups qualify: they take part in every
// match exactly once.
func capOffsets(re *syntax.Regexp, numSubexp int) []int32 {
	if numSubexp == 0 {
		return nil
	}
	n := 2 * numSubexp
	left := make([]int32, 2*n)
	right := left[n:]
	left = left[:n]
	for i := range left {
		left[i], right[i] = -1, -1
	}
	ok := true
	var walk func(re *syntax.Regexp, off int, fromEnd bool)
	walk = func(re *syntax.Regexp, off int, fromEnd bool) {
		switch re.Op {
		case syntax.OpCapture:
			first, second := 2*re.Cap-2, 2*re.Cap-1 // start and end slots, minus 2
			out := left
			if fromEnd {
				first, second = second, first
				out = right
			}
			out[first] = int32(off)
			walk(re.Sub[0], off, fromEnd)
			if off >= 0 {
				if k, fixed := fixedBytes(re.Sub[0]); fixed {
					out[second] = int32(off + k)
				}
			}
		case syntax.OpConcat:
			for i := range re.Sub {
				sub := re.Sub[i]
				if fromEnd {
					sub = re.Sub[len(re.Sub)-1-i]
				}
				walk(sub, off, fromEnd)
				if off >= 0 {
					if k, fixed := fixedBytes(sub); fixed {
						off += k
					} else {
						off = -1
					}
				}
			}
		default:
			if hasCapture(re) {
				ok = false
			}
		}
	}
	walk(re, 0, false)
	walk(re, 0, true)
	if !ok {
		return nil
	}
	for i := range left {
		switch {
		case left[i] >= 0:
		case right[i] >= 0:
			left[i] = ^right[i]
		default:
			return nil
		}
	}
	return left
}

func hasCapture(re *syntax.Regexp) bool {
	if re.Op == syntax.OpCapture {
		return true
	}
	for _, s := range re.Sub {
		if hasCapture(s) {
			return true
		}
	}
	return false
}

// walker follows the single thread of a one-pass window.
type walker struct {
	seen       []uint32
	mark       uint32
	stack      []pikeFrame
	caps, next []int
	memoCaps   []int
}

// run computes the captures of the highest-priority path from a that ends
// at b, following the one instruction that can consume each character. ok
// is false when two instructions can consume the same character; a general
// engine must decide then. Steps on ASCII bytes that test no empty-width
// assertion depend only on the instruction and the byte, so they are
// memoized in ci.steps, per class of ci.cls.
func (w *walker) run(p *prog, ci *capInfo, s string, a, b int, dst []int) (found, ok bool) {
	n := len(dst)
	if cap(w.caps) < n {
		w.caps, w.next = make([]int, n), make([]int, n)
	}
	caps, next := w.caps[:n], w.next[:n]
	if len(w.seen) < len(p.insts) {
		w.seen = make([]uint32, len(p.insts))
		w.mark = 0
	}
	for i := range caps {
		caps[i] = -1
	}
	caps[0] = a
	steps := ci.steps
	if n != 2*(p.numCap+1) {
		steps = nil
	}
	pc := p.start
	pos := a
	for {
		if pos < b && steps != nil && s[pos] < utf8.RuneSelf {
			c := s[pos]
			row := steps[pc].Load()
			if row == nil {
				row = &make([]atomic.Uint32, ci.ncls)[0]
				if !steps[pc].CompareAndSwap(nil, row) {
					row = steps[pc].Load()
				}
			}
			memo := (*atomic.Uint32)(unsafe.Add(unsafe.Pointer(row), 4*uintptr(ci.cls[c])))
			v := memo.Load()
			if v == 0 {
				v = w.memo(p, pc, c, n)
				memo.Store(v)
			}
			switch k := v & 0xffff; k {
			case stepNone:
				return false, true
			case stepTwo:
				return false, false
			case stepEmpty:
			default:
				for m := v >> 16; m != 0; m &= m - 1 {
					caps[bits.TrailingZeros32(m)] = pos
				}
				pc = k - stepOne
				pos++
				continue
			}
		}
		r, wd := decodeAt(s, pos)
		flag := syntax.EmptyOpContext(runeBefore(s, pos), r)
		res, npc := w.step(p, pc, pos, b, r, wd, flag, false, caps, next)
		switch res {
		case stepMatch:
			copy(dst, caps)
			dst[1] = b
			return true, true
		case stepNone:
			return false, true
		case stepTwo:
			return false, false
		}
		caps, next = next, caps
		pc = npc
		pos += wd
	}
}

// memo returns the memoized form of the step from pc on the ASCII byte c.
func (w *walker) memo(p *prog, pc uint32, c byte, n int) uint32 {
	const pos = -2 // a position no capture holds
	if cap(w.memoCaps) < 2*n {
		w.memoCaps = make([]int, 2*n)
	}
	caps, next := w.memoCaps[:n], w.memoCaps[n:2*n]
	for i := range caps {
		caps[i] = -1
	}
	res, npc := w.step(p, pc, pos, pos+1, rune(c), 1, 0, true, caps, next)
	if res != stepOne {
		return uint32(res)
	}
	var m uint32
	for i, v := range next {
		if v == pos {
			m |= 1 << i
		}
	}
	return m<<16 | (stepOne + npc)
}

// step follows, in priority order, the instructions reachable from pc0
// without consuming input, at pos where the character r of width wd
// starts. It returns stepOne and the instruction after the one that
// consumes r, with next holding the captures on the path to it; stepMatch
// with caps holding the captures on the path to a match at b; stepNone or
// stepTwo. With memo set, it returns stepEmpty at the first empty-width
// assertion instead of testing it against flag.
func (w *walker) step(p *prog, pc0 uint32, pos, b int, r rune, wd int, flag syntax.EmptyOp, memo bool, caps, next []int) (uint32, uint32) {
	n := len(caps)
	insts := p.insts
	w.mark++
	if w.mark == 0 {
		clear(w.seen)
		w.mark = 1
	}
	res, npc := uint32(stepNone), uint32(0)
	stk := append(w.stack[:0], pikeFrame{pc: pc0, slot: -1})
walk:
	for len(stk) > 0 {
		f := stk[len(stk)-1]
		stk = stk[:len(stk)-1]
		if f.slot >= 0 {
			caps[f.slot] = f.val
			continue
		}
		pc := f.pc
		for pc != 0 && w.seen[pc] != w.mark {
			w.seen[pc] = w.mark
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
				if memo {
					res, npc = stepEmpty, 0
					break walk
				}
				if syntax.EmptyOp(in.arg)&^flag == 0 {
					pc = in.out
					continue
				}
			case opSave:
				if int(in.arg) < n {
					stk = append(stk, pikeFrame{slot: int32(in.arg), val: caps[in.arg]})
					caps[in.arg] = pos
				}
				pc = in.out
				continue
			case opMatch:
				if pos == b {
					res, npc = stepMatch, 0
					break walk
				}
			default:
				if pos+wd <= b && wd > 0 && p.matchRune(in, r) {
					if npc != 0 {
						res, npc = stepTwo, 0
						break walk
					}
					res, npc = stepOne, in.out
					copy(next, caps)
				}
			}
			break
		}
	}
	w.stack = stk[:0]
	return res, npc
}
