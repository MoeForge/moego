package regexp

import (
	"regexp/syntax"
	"unicode"
	"unicode/utf8"
)

// The rune program is the compact form produced at compile time. Byte
// automata for the DFA are derived from it lazily, on first use, so Compile
// stays within the cost of the shared parser.

type opcode uint8

const (
	opFail     opcode = iota
	opMatch           // accept
	opRune1           // one rune: arg
	opClass           // rune class: classes[arg], sorted [lo, hi] pairs
	opFold1           // an ASCII letter in either case: arg is the lowercase letter
	opAnyNotNL        // any rune but '\n'
	opAny             // any rune
	opAlt             // epsilon to out (preferred) and out1
	opEmpty           // empty-width assertion: arg holds syntax.EmptyOp flags
	opSave            // capture slot: arg
	opNop             // epsilon to out
)

type inst struct {
	op   opcode
	arg  uint32
	out  uint32
	out1 uint32
}

type prog struct {
	insts   []inst
	classes [][]rune
	start   uint32
	numCap  int // number of capture groups, not counting the whole match
}

// patch list: linked through inst out (even) / out1 (odd) fields.
type patchList struct{ head, tail uint32 }

type frag struct {
	start    uint32
	out      patchList
	nullable bool
}

type compiler struct {
	p *prog
}

func compileProg(re *syntax.Regexp, numCap int) *prog {
	// The program, its instructions and class list share one allocation
	// when the program is small; larger ones size their instructions from
	// the estimate.
	var p *prog
	if n := estimateInsts(re); n <= 24 {
		a := &struct {
			p       prog
			insts   [24]inst
			classes [4][]rune
		}{}
		p = &a.p
		p.insts = a.insts[:1]
		p.classes = a.classes[:0]
	} else {
		a := &struct {
			p       prog
			classes [4][]rune
		}{}
		p = &a.p
		p.insts = make([]inst, 1, n)
		p.classes = a.classes[:0]
	}
	p.numCap = numCap
	p.insts[0] = inst{op: opFail}
	c := compiler{p: p}
	f := c.compile(re)
	m := c.emit(inst{op: opMatch})
	c.patch(f.out, m)
	p.start = f.start
	return p
}

// estimateInsts sizes the instruction slice so compile allocates once in the
// common case: the instructions of re plus the fail and match ones.
func estimateInsts(re *syntax.Regexp) int {
	return instCount(re) + 2
}

func instCount(re *syntax.Regexp) int {
	switch re.Op {
	case syntax.OpLiteral:
		return len(re.Rune)
	case syntax.OpCapture:
		return instCount(re.Sub[0]) + 2
	case syntax.OpStar:
		return instCount(re.Sub[0]) + 2
	case syntax.OpPlus, syntax.OpQuest:
		return instCount(re.Sub[0]) + 1
	case syntax.OpRepeat:
		k := re.Max
		if k < 0 {
			k = re.Min + 1
		}
		return k*(instCount(re.Sub[0])+1) + 1
	case syntax.OpConcat:
		n := 0
		for _, s := range re.Sub {
			n += instCount(s)
		}
		return n
	case syntax.OpAlternate:
		n := len(re.Sub) - 1
		for _, s := range re.Sub {
			n += instCount(s)
		}
		return n
	}
	return 1
}

func (c *compiler) emit(i inst) uint32 {
	c.p.insts = append(c.p.insts, i)
	return uint32(len(c.p.insts) - 1)
}

func (c *compiler) patch(l patchList, val uint32) {
	for h := l.head; h != 0; {
		in := &c.p.insts[h>>1]
		if h&1 == 0 {
			h = in.out
			in.out = val
		} else {
			h = in.out1
			in.out1 = val
		}
	}
}

func (c *compiler) appendList(l1, l2 patchList) patchList {
	if l1.head == 0 {
		return l2
	}
	if l2.head == 0 {
		return l1
	}
	in := &c.p.insts[l1.tail>>1]
	if l1.tail&1 == 0 {
		in.out = l2.head
	} else {
		in.out1 = l2.head
	}
	return patchList{l1.head, l2.tail}
}

func makeList(n uint32) patchList { return patchList{n, n} }

func (c *compiler) single(i inst, nullable bool) frag {
	n := c.emit(i)
	return frag{start: n, out: makeList(n << 1), nullable: nullable}
}

func (c *compiler) nop() frag  { return c.single(inst{op: opNop}, true) }
func (c *compiler) fail() frag { return frag{} }
func (c *compiler) empty(op syntax.EmptyOp) frag {
	return c.single(inst{op: opEmpty, arg: uint32(op)}, true)
}

func (c *compiler) save(slot int) frag {
	return c.single(inst{op: opSave, arg: uint32(slot)}, true)
}

func (c *compiler) cat(f1, f2 frag) frag {
	if f1.start == 0 || f2.start == 0 {
		return frag{}
	}
	c.patch(f1.out, f2.start)
	return frag{start: f1.start, out: f2.out, nullable: f1.nullable && f2.nullable}
}

func (c *compiler) alt(f1, f2 frag) frag {
	if f1.start == 0 {
		return f2
	}
	if f2.start == 0 {
		return f1
	}
	n := c.emit(inst{op: opAlt, out: f1.start, out1: f2.start})
	return frag{start: n, out: c.appendList(f1.out, f2.out), nullable: f1.nullable || f2.nullable}
}

func (c *compiler) quest(f1 frag, nongreedy bool) frag {
	n := c.emit(inst{op: opAlt})
	f := frag{start: n, nullable: true}
	if nongreedy {
		c.p.insts[n].out1 = f1.start
		f.out = makeList(n << 1)
	} else {
		c.p.insts[n].out = f1.start
		f.out = makeList(n<<1 | 1)
	}
	f.out = c.appendList(f.out, f1.out)
	return f
}

// loop returns the fragment for the main loop of a plus or star: f1 followed
// by a choice to repeat f1 or exit.
func (c *compiler) loop(f1 frag, nongreedy bool) frag {
	n := c.emit(inst{op: opAlt})
	f := frag{start: n, nullable: true}
	if nongreedy {
		c.p.insts[n].out1 = f1.start
		f.out = makeList(n << 1)
	} else {
		c.p.insts[n].out = f1.start
		f.out = makeList(n<<1 | 1)
	}
	c.patch(f1.out, f.start)
	return f
}

func (c *compiler) star(f1 frag, nongreedy bool) frag {
	if f1.nullable {
		// (f1+)? keeps the priority order right when f1 can match empty.
		return c.quest(c.plus(f1, nongreedy), nongreedy)
	}
	return c.loop(f1, nongreedy)
}

func (c *compiler) plus(f1 frag, nongreedy bool) frag {
	return frag{start: f1.start, out: c.loop(f1, nongreedy).out, nullable: f1.nullable}
}

func (c *compiler) rune1(r rune, fold bool) frag {
	if fold {
		if cls := foldOrbit(r); cls != nil {
			if r < utf8.RuneSelf && len(cls) == 4 && cls[3] < utf8.RuneSelf {
				return c.single(inst{op: opFold1, arg: uint32(cls[2])}, false)
			}
			c.p.classes = append(c.p.classes, cls)
			return c.single(inst{op: opClass, arg: uint32(len(c.p.classes) - 1)}, false)
		}
	}
	return c.single(inst{op: opRune1, arg: uint32(r)}, false)
}

// asciiFold holds the shared fold classes of ASCII letters, so compiling
// case-insensitive text does not allocate one class per letter.
var asciiFold = func() (t [128][]rune) {
	for r := rune('A'); r <= 'z'; r++ {
		if 'Z' < r && r < 'a' {
			continue
		}
		t[r] = computeOrbit(r)
	}
	return t
}()

// foldOrbit returns the case-folding orbit of r as a sorted class, or nil if r
// has no other case forms. The result must not be modified.
func foldOrbit(r rune) []rune {
	if r >= 0 && r < 128 {
		return asciiFold[r]
	}
	return computeOrbit(r)
}

func computeOrbit(r rune) []rune {
	f := unicode.SimpleFold(r)
	if f == r {
		return nil
	}
	var buf [8]rune
	rs := append(buf[:0], r)
	for f != r {
		rs = append(rs, f)
		f = unicode.SimpleFold(f)
	}
	// insertion sort; orbits are tiny
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && rs[j] < rs[j-1]; j-- {
			rs[j], rs[j-1] = rs[j-1], rs[j]
		}
	}
	out := make([]rune, 0, 2*len(rs))
	for _, x := range rs {
		if n := len(out); n > 0 && out[n-1]+1 == x {
			out[n-1] = x
			continue
		}
		out = append(out, x, x)
	}
	return out
}

func (c *compiler) compile(re *syntax.Regexp) frag {
	switch re.Op {
	case syntax.OpNoMatch:
		return c.fail()
	case syntax.OpEmptyMatch:
		return c.nop()
	case syntax.OpLiteral:
		if len(re.Rune) == 0 {
			return c.nop()
		}
		fold := re.Flags&syntax.FoldCase != 0
		var f frag
		for j, r := range re.Rune {
			f1 := c.rune1(r, fold)
			if j == 0 {
				f = f1
			} else {
				f = c.cat(f, f1)
			}
		}
		return f
	case syntax.OpCharClass:
		if len(re.Rune) == 0 {
			return c.fail()
		}
		if len(re.Rune) == 2 && re.Rune[0] == re.Rune[1] {
			return c.single(inst{op: opRune1, arg: uint32(re.Rune[0])}, false)
		}
		c.p.classes = append(c.p.classes, re.Rune)
		return c.single(inst{op: opClass, arg: uint32(len(c.p.classes) - 1)}, false)
	case syntax.OpAnyCharNotNL:
		return c.single(inst{op: opAnyNotNL}, false)
	case syntax.OpAnyChar:
		return c.single(inst{op: opAny}, false)
	case syntax.OpBeginLine:
		return c.empty(syntax.EmptyBeginLine)
	case syntax.OpEndLine:
		return c.empty(syntax.EmptyEndLine)
	case syntax.OpBeginText:
		return c.empty(syntax.EmptyBeginText)
	case syntax.OpEndText:
		return c.empty(syntax.EmptyEndText)
	case syntax.OpWordBoundary:
		return c.empty(syntax.EmptyWordBoundary)
	case syntax.OpNoWordBoundary:
		return c.empty(syntax.EmptyNoWordBoundary)
	case syntax.OpCapture:
		bra := c.save(2 * re.Cap)
		sub := c.compile(re.Sub[0])
		ket := c.save(2*re.Cap + 1)
		return c.cat(c.cat(bra, sub), ket)
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest:
		sub := re.Sub[0]
		ng := re.Flags&syntax.NonGreedy != 0
		// Same idempotence as regexp/syntax's simplifier: x** is x*.
		for (sub.Op == re.Op && sub.Flags&syntax.NonGreedy == re.Flags&syntax.NonGreedy) || isEmptyOnly(sub) {
			if isEmptyOnly(sub) {
				return c.nop()
			}
			sub = sub.Sub[0]
		}
		f := c.compile(sub)
		if f.start == 0 {
			if re.Op == syntax.OpPlus {
				return f
			}
			return c.nop()
		}
		switch re.Op {
		case syntax.OpStar:
			return c.star(f, ng)
		case syntax.OpPlus:
			return c.plus(f, ng)
		}
		return c.quest(f, ng)
	case syntax.OpRepeat:
		return c.repeat(re)
	case syntax.OpConcat:
		if len(re.Sub) == 0 {
			return c.nop()
		}
		var f frag
		for j, sub := range re.Sub {
			if j == 0 {
				f = c.compile(sub)
			} else {
				f = c.cat(f, c.compile(sub))
			}
		}
		return f
	case syntax.OpAlternate:
		var f frag
		for j, sub := range re.Sub {
			if j == 0 {
				f = c.compile(sub)
			} else {
				f = c.alt(f, c.compile(sub))
			}
		}
		return f
	}
	panic("regexp: unhandled op")
}

func isEmptyOnly(re *syntax.Regexp) bool { return re.Op == syntax.OpEmptyMatch }

// repeat expands x{n,m} with the same structure as the standard simplifier
// (n copies, then m-n nested optional copies), which fixes submatch priority.
func (c *compiler) repeat(re *syntax.Regexp) frag {
	sub := re.Sub[0]
	ng := re.Flags&syntax.NonGreedy != 0
	if re.Min == 0 && re.Max == 0 {
		return c.nop()
	}
	if re.Max == -1 {
		if re.Min == 0 {
			return c.compile(&syntax.Regexp{Op: syntax.OpStar, Flags: re.Flags, Sub: []*syntax.Regexp{sub}})
		}
		pf := &syntax.Regexp{Op: syntax.OpPlus, Flags: re.Flags, Sub: []*syntax.Regexp{sub}}
		if re.Min == 1 {
			return c.compile(pf)
		}
		f := c.compile(sub)
		for i := 1; i < re.Min-1; i++ {
			f = c.cat(f, c.compile(sub))
		}
		return c.cat(f, c.compile(pf))
	}
	if re.Min == 1 && re.Max == 1 {
		return c.compile(sub)
	}
	if re.Min > re.Max {
		return c.fail()
	}
	var f frag
	for i := 0; i < re.Min; i++ {
		if i == 0 {
			f = c.compile(sub)
		} else {
			f = c.cat(f, c.compile(sub))
		}
	}
	if re.Max == re.Min {
		return f
	}
	// (x(x(x)?)?)?: each optional copy's exit leads into the next optional
	// copy; every skip branch and the last copy's exit leave the repetition.
	suffix := frag{nullable: true}
	var prev patchList
	for i := 0; i < re.Max-re.Min; i++ {
		q := c.emit(inst{op: opAlt})
		x := c.compile(sub)
		var skip patchList
		if ng {
			c.p.insts[q].out1 = x.start
			skip = makeList(q << 1)
		} else {
			c.p.insts[q].out = x.start
			skip = makeList(q<<1 | 1)
		}
		if i == 0 {
			suffix.start = q
		} else {
			c.patch(prev, q)
		}
		suffix.out = c.appendList(suffix.out, skip)
		prev = x.out
		if x.start == 0 {
			prev = patchList{}
			break
		}
	}
	suffix.out = c.appendList(suffix.out, prev)
	if re.Min == 0 {
		return suffix
	}
	return c.cat(f, suffix)
}
