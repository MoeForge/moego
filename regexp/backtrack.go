package regexp

import "regexp/syntax"

// backtracker finds submatches inside a known match window [a, b]: a depth
// first search over (instruction, position) in priority order that accepts
// only at b. The first path found is the highest-priority match ending at b,
// which is the match leftmost-first and leftmost-longest searches report.
// Each (instruction, position) pair is explored at most once, because
// whether a path from it reaches b does not depend on how it got there, so
// the search is linear in the window times the program size.
type backtracker struct {
	visited []uint32
	stack   []btFrame
	cap     []int
}

type btFrame struct {
	pc   uint32
	slot int32 // >= 0: restore cap[slot] = pos when popped
	pos  int
}

// maxBacktrackBits bounds the visited bitmap; larger windows use the Pike
// machine.
const maxBacktrackBits = 256 << 10

func canBacktrack(p *prog, a, b int) bool {
	return len(p.insts)*(b-a+1) <= maxBacktrackBits
}

// run fills dst with the captures of the highest-priority match of p from a
// that ends exactly at b. It reports false if there is none.
func (bt *backtracker) run(p *prog, s string, a, b int, dst []int) bool {
	width := b - a + 1
	nbits := len(p.insts) * width
	nw := (nbits + 31) / 32
	if cap(bt.visited) < nw {
		bt.visited = make([]uint32, nw)
	} else {
		bt.visited = bt.visited[:nw]
		clear(bt.visited)
	}
	if cap(bt.cap) < len(dst) {
		bt.cap = make([]int, len(dst))
	}
	caps := bt.cap[:len(dst)]
	for i := range caps {
		caps[i] = -1
	}
	caps[0] = a
	insts := p.insts
	stk := append(bt.stack[:0], btFrame{pc: p.start, slot: -1, pos: a})
	found := false
search:
	for len(stk) > 0 {
		f := stk[len(stk)-1]
		stk = stk[:len(stk)-1]
		if f.slot >= 0 {
			caps[f.slot] = f.pos
			continue
		}
		pc, pos := f.pc, f.pos
		for pc != 0 {
			k := int(pc)*width + pos - a
			if bt.visited[k>>5]&(1<<(k&31)) != 0 {
				break
			}
			bt.visited[k>>5] |= 1 << (k & 31)
			in := &insts[pc]
			switch in.op {
			case opMatch:
				if pos == b {
					caps[1] = pos
					found = true
					break search
				}
			case opAlt:
				stk = append(stk, btFrame{pc: in.out1, slot: -1, pos: pos})
				pc = in.out
				continue
			case opNop:
				pc = in.out
				continue
			case opEmpty:
				r, _ := decodeAt(s, pos)
				if syntax.EmptyOp(in.arg)&^syntax.EmptyOpContext(runeBefore(s, pos), r) == 0 {
					pc = in.out
					continue
				}
			case opSave:
				if int(in.arg) < len(caps) {
					stk = append(stk, btFrame{slot: int32(in.arg), pos: caps[in.arg]})
					caps[in.arg] = pos
				}
				pc = in.out
				continue
			default: // consuming instructions
				if pos < b {
					r, w := decodeAt(s, pos)
					if pos+w <= b && p.matchRune(in, r) {
						pc = in.out
						pos += w
						continue
					}
				}
			}
			break
		}
	}
	bt.stack = stk[:0]
	if found {
		copy(dst, caps)
	}
	return found
}
