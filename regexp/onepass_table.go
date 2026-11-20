package regexp

import (
	"math/bits"
	"regexp/syntax"
	"slices"
	"unicode"
	"unicode/utf8"
)

// Repeated hits can amortize a table. A miss or a single-use expression
// pays only for the ordinary one-pass program.
const onePassTableHits = 64

// A step combines all the saves before a rune with the row to use after
// consuming it. The low stepBits hold the row offset. The higher bits are
// the capture slots to set at the current byte position.
type onePassTable struct {
	class    [256]uint8
	wide     []rune // sorted starts of non-ASCII intervals
	wideBase int    // column for wide[0]
	steps    []uint64
}

// buildTable specializes end-anchored programs with a small alphabet.
// Context-dependent assertions use the interpreter.
func (p *onePassProg) buildTable() *onePassTable {
	// Only the entry and successors of reading instructions need rows.
	var row [maxStepInsts]uint16
	var cuts [utf8.RuneSelf + 1]struct{ start, end int16 }
	var wide []rune
	row[p.entry] = 1
	nrows := 1
	for pc := range p.insts {
		in := &p.insts[pc]
		switch in.op {
		case opRune1, opFold1, opClass, opAny, opAnyNotNL:
			rs := p.instRunes(in)
			for i := 0; i < len(rs); i += 2 {
				lo, hi := rs[i], rs[i+1]
				if lo < utf8.RuneSelf {
					cuts[lo].start++
					cuts[min(hi+1, utf8.RuneSelf)].end++
				}
				if hi >= utf8.RuneSelf {
					if len(wide) >= 256 {
						return nil // bound construction for large Unicode classes
					}
					wide = append(wide, max(lo, utf8.RuneSelf))
					if hi < unicode.MaxRune {
						wide = append(wide, hi+1)
					}
				}
			}
			if row[in.out] == 0 {
				nrows++
				row[in.out] = uint16(nrows)
			}
		case opEmpty:
			if uint32(pc) != p.start && syntax.EmptyOp(in.arg) != syntax.EmptyEndText {
				return nil
			}
		}
	}

	// Between two range endpoints every instruction accepts the same
	// bytes. Column zero rejects uncovered ASCII and sends high bytes
	// to the rune lookup. Column one handles EOF.
	var t onePassTable
	var rep [utf8.RuneSelf + 2]rune
	rep[1] = -1
	stride := 2
	var active int16
	for b, cut := range cuts[:utf8.RuneSelf] {
		active += cut.start - cut.end
		if active == 0 {
			continue
		}
		if cut.start != 0 || cut.end != 0 {
			rep[stride] = rune(b)
			stride++
		}
		t.class[b] = uint8(stride - 1)
	}
	slices.Sort(wide)
	t.wide = slices.Compact(wide)
	t.wideBase = stride
	stride += len(t.wide)
	const maxEntries = 2048 // at most 16 KiB of transitions
	entries := 1 + nrows*stride
	if entries > maxEntries {
		return nil
	}
	t.steps = make([]uint64, entries)
	for pc, id := range row[:len(p.insts)] {
		if id != 0 {
			row[pc] = 1 + (id-1)*uint16(stride)
		}
	}
	for pc, base := range row[:len(p.insts)] {
		if base == 0 {
			continue
		}
		for c := 1; c < stride; c++ {
			var r rune
			if c < t.wideBase {
				r = rep[c]
			} else {
				r = t.wide[c-t.wideBase]
			}
			step, ok := p.tableStep(uint32(pc), r, &row)
			if !ok {
				return nil
			}
			t.steps[int(base)+c] = step
		}
	}
	return &t
}

// tableStep follows the one-pass choices for one representative rune.
// ok is false if the step needs context beyond that rune.
func (p *onePassProg) tableStep(pc uint32, r rune, row *[maxStepInsts]uint16) (uint64, bool) {
	var saves uint64
	for range len(p.insts) {
		in := &p.insts[pc]
		switch in.op {
		case opFail:
			return 0, true
		case opMatch:
			return saves | 1, r < 0
		case opAlt:
			pc = p.alts[in.arg].next(in, r)
			continue
		case opSave:
			saves |= uint64(1) << (stepBits + in.arg)
		case opEmpty:
			if syntax.EmptyOp(in.arg) != syntax.EmptyEndText {
				return 0, false // e.g. a loop back to the initial begin-text assertion
			}
			if r >= 0 {
				return 0, true
			}
		case opNop:
		default:
			if r >= 0 && p.matchRune(in, r) {
				return saves | uint64(row[in.out]), true
			}
			return 0, true
		}
		pc = in.out
	}
	return 0, false
}

func (t *onePassTable) match(s string, pos int, m []int) bool {
	row := 1
	for pos < len(s) {
		b := s[pos]
		step := t.steps[row+int(t.class[b])]
		width := 1
		if step == 0 {
			if b < utf8.RuneSelf || len(t.wide) == 0 {
				return false
			}
			r, w := utf8.DecodeRuneInString(s[pos:])
			width = w
			i, exact := slices.BinarySearch(t.wide, r)
			if !exact {
				i--
			}
			if i < 0 {
				return false
			}
			step = t.steps[row+t.wideBase+i]
			if step == 0 {
				return false
			}
		}
		setOnePassSlots(m, step, pos)
		row = int(uint16(step))
		pos += width
	}
	end := t.steps[row+1]
	if end == 0 {
		return false
	}
	setOnePassSlots(m, end, pos)
	m[0], m[1] = 0, pos
	return true
}

func setOnePassSlots(m []int, step uint64, pos int) {
	for slots := step >> stepBits; slots != 0; slots &= slots - 1 {
		m[bits.TrailingZeros64(slots)] = pos
	}
}
