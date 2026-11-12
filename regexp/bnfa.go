package regexp

import (
	"math/bits"
	"regexp/syntax"
	"unicode/utf8"
	"unsafe"
)

// The byte NFA is what the lazy DFA determinizes. It is derived from the
// rune program on first use: every rune instruction becomes a small byte
// automaton over the UTF-8 encodings of the runes it accepts.
//
// Invalid UTF-8: the standard library decodes each byte that does not start
// a valid sequence as U+FFFD of width 1. Byte automata built from valid
// encodings alone agree with that everywhere except where a class contains
// U+FFFD. Such classes, and the unanchored prefix of patterns that have them,
// also run a validator that leads to a quit node on invalid input; the
// search then falls back to the exact rune-level engine.

type bkind uint8

const (
	bFail  bkind = iota // no way forward
	bByte               // byte transitions in tr
	bSplit              // epsilon to next, then alt
	bEps                // epsilon to next
	bLook               // empty-width assertion, then next
	bMatch
	bQuit // invalid UTF-8 where it matters: give up
)

type btr struct {
	lo, hi byte
	next   uint32
}

// bnode is a node of the byte NFA. The transitions of a bByte node are
// trs[next:alt] of its program; the other kinds use next and alt as edges.
// Nodes hold no pointers, so the collector never scans them.
type bnode struct {
	kind bkind
	// quitAtEnd marks validator states inside a multibyte sequence: the end
	// of input there means the sequence was truncated.
	quitAtEnd bool
	look      syntax.EmptyOp
	next, alt uint32
}

type bprog struct {
	nodes   []bnode
	trs     []btr  // the transitions of the bByte nodes, node by node
	start   uint32 // anchored start
	ustart  uint32 // unanchored start (forward only)
	looks   syntax.EmptyOp
	fffd    bool
	reverse bool
	vstart  uint32 // validator entry, 0 if unused
}

func (b *bprog) node(n bnode) uint32 {
	b.nodes = append(b.nodes, n)
	return uint32(len(b.nodes) - 1)
}

// trans returns the transitions of node n, none unless it is a bByte node.
func (b *bprog) trans(n uint32) []btr {
	if nd := &b.nodes[n]; nd.kind == bByte {
		return b.trs[nd.next:nd.alt]
	}
	return nil
}

// byteNode makes n a bByte node with the transitions tr.
func (b *bprog) byteNode(n uint32, tr ...btr) {
	start := len(b.trs)
	b.trs = append(b.trs, tr...)
	b.nodes[n] = bnode{kind: bByte, quitAtEnd: b.nodes[n].quitAtEnd, next: uint32(start), alt: uint32(len(b.trs))}
}

// alloc sizes the nodes and transitions of a byte NFA for p, with the null
// node in place. There is room for one node per instruction, the byte tries
// of classes, the UTF-8 validator and the unanchored prefix, so they rarely
// grow.
func (b *bprog) alloc(p *prog) {
	n, t := len(p.insts)+24, 48
	for i := range p.insts {
		switch in := &p.insts[i]; in.op {
		case opRune1:
			t++
			if in.arg >= utf8.RuneSelf {
				n, t = n+3, t+3
			}
		case opClass, opFold1, opAny, opAnyNotNL:
			r := p.instRanges(in)
			t += len(r) / 2
			if r[len(r)-1] >= utf8.RuneSelf {
				n, t = n+20, t+24
			}
		}
	}
	const size, tsize = int(unsafe.Sizeof(bnode{})), int(unsafe.Sizeof(btr{}))
	b.nodes = make([]bnode, 1, roundAlloc(n*size)/size)
	b.trs = make([]btr, 0, roundAlloc(t*tsize)/tsize)
}

// roundAlloc rounds an allocation of n bytes up to a power of two, or past
// 8 KB to whole 8 KB pages. The allocator serves such sizes from spans
// they fill, while other sizes can reserve spans several times larger
// than one object.
func roundAlloc(n int) int {
	if n <= 8<<10 {
		return 1 << bits.Len(uint(n-1))
	}
	return (n + 8<<10 - 1) &^ (8<<10 - 1)
}

// reachable lists the instructions reachable from the program start.
func (p *prog) reachable() []bool {
	seen := make([]bool, len(p.insts))
	stk := []uint32{p.start}
	for len(stk) > 0 {
		pc := stk[len(stk)-1]
		stk = stk[:len(stk)-1]
		if pc == 0 || seen[pc] {
			continue
		}
		seen[pc] = true
		in := &p.insts[pc]
		switch in.op {
		case opMatch, opFail:
		case opAlt:
			stk = append(stk, in.out, in.out1)
		default:
			stk = append(stk, in.out)
		}
	}
	return seen
}

// instRanges returns the rune class accepted by a consuming instruction.
func (p *prog) instRanges(in *inst) []rune {
	switch in.op {
	case opRune1:
		r := rune(in.arg)
		return []rune{r, r}
	case opClass:
		return p.classes[in.arg]
	case opFold1:
		return asciiFold[in.arg]
	case opAnyNotNL:
		return anyNotNLClass
	case opAny:
		return anyClass
	}
	return nil
}

var (
	anyClass      = []rune{0, 0x10FFFF}
	anyNotNLClass = []rune{0, '\n' - 1, '\n' + 1, 0x10FFFF}
)

func isConsuming(op opcode) bool {
	return op == opRune1 || op == opClass || op == opFold1 || op == opAny || op == opAnyNotNL
}

// progHasFFFD reports whether any reachable class accepts U+FFFD.
func (p *prog) hasFFFD(reach []bool) bool {
	for pc := range p.insts {
		in := &p.insts[pc]
		if reach[pc] && isConsuming(in.op) && classHasFFFD(p.instRanges(in)) {
			return true
		}
	}
	return false
}

// validator builds the UTF-8 validator once and returns its entry.
func (b *bprog) validator() uint32 {
	if b.vstart != 0 {
		return b.vstart
	}
	q := b.node(bnode{kind: bQuit})
	mk := func() uint32 { return b.node(bnode{kind: bByte, quitAtEnd: true}) }
	v1, v2, v2a, v2b, v3, v3a, v3b := mk(), mk(), mk(), mk(), mk(), mk(), mk()
	// After the lead byte: a valid continuation leads on (or ends the
	// sequence by having no transition); anything else is invalid.
	b.byteNode(v1, btr{0x00, 0x7F, q}, btr{0xC0, 0xFF, q})
	b.byteNode(v2, btr{0x00, 0x7F, q}, btr{0x80, 0xBF, v1}, btr{0xC0, 0xFF, q})
	b.byteNode(v2a, btr{0x00, 0x9F, q}, btr{0xA0, 0xBF, v1}, btr{0xC0, 0xFF, q})
	b.byteNode(v2b, btr{0x00, 0x7F, q}, btr{0x80, 0x9F, v1}, btr{0xA0, 0xFF, q})
	b.byteNode(v3, btr{0x00, 0x7F, q}, btr{0x80, 0xBF, v2}, btr{0xC0, 0xFF, q})
	b.byteNode(v3a, btr{0x00, 0x8F, q}, btr{0x90, 0xBF, v2}, btr{0xC0, 0xFF, q})
	b.byteNode(v3b, btr{0x00, 0x7F, q}, btr{0x80, 0x8F, v2}, btr{0x90, 0xFF, q})
	v0 := b.node(bnode{})
	b.byteNode(v0, btr{0x80, 0xC1, q}, btr{0xC2, 0xDF, v1}, btr{0xE0, 0xE0, v2a}, btr{0xE1, 0xEC, v2},
		btr{0xED, 0xED, v2b}, btr{0xEE, 0xEF, v2}, btr{0xF0, 0xF0, v3a}, btr{0xF1, 0xF3, v3},
		btr{0xF4, 0xF4, v3b}, btr{0xF5, 0xFF, q})
	b.vstart = v0
	return v0
}

// fillTrie turns node root into a byte automaton for the runes in class,
// with every complete encoding leading to target. Forward sequences from
// sorted disjoint rune ranges are aligned, so at each node a new range either
// equals an existing one or overlaps none.
func (b *bprog) fillTrie(root uint32, class []rune, target uint32) {
	// ASCII-only classes need a single node.
	if class[len(class)-1] < 0x80 {
		start := len(b.trs)
		for i := 0; i < len(class); i += 2 {
			b.trs = append(b.trs, btr{byte(class[i]), byte(class[i+1]), target})
		}
		b.nodes[root] = bnode{kind: bByte, next: uint32(start), alt: uint32(len(b.trs))}
		return
	}
	b.buildTrie(root, class, target, false)
}

// fillRevTrie is fillTrie for reversed encodings. Reversed sequences are not
// aligned, so overlapping ranges become separate (nondeterministic) branches;
// the DFA construction resolves them.
func (b *bprog) fillRevTrie(root uint32, class []rune, target uint32) {
	if class[len(class)-1] < 0x80 {
		b.fillTrie(root, class, target)
		return
	}
	b.buildTrie(root, class, target, true)
}

// trieEnt is a transition of a byte trie under construction, in a list of
// the transitions of its node. A node's transitions are only complete once
// the whole class is in, so they move to the program's contiguous
// transitions at the end.
type trieEnt struct {
	tr   btr
	next int32 // the node's next entry, -1 at the end
}

// buildTrie builds the trie of fillTrie (rev false) or fillRevTrie (rev
// true). Trie node 0 is root; node k > 0 is program node base+k-1.
func (b *bprog) buildTrie(root uint32, class []rune, target uint32, rev bool) {
	// Most tries fit the stack buffers; those of large Unicode classes
	// move to the heap.
	var entBuf [128]trieEnt
	var headBuf, tailBuf [64]int32
	ent, head, tail := entBuf[:0], append(headBuf[:0], -1), append(tailBuf[:0], -1)
	base := uint32(len(b.nodes))
	local := func(n uint32) int32 {
		if n == root {
			return 0
		}
		return int32(n - base + 1)
	}
	for i := 0; i+1 < len(class); i += 2 {
		utf8Seqs(class[i], class[i+1], func(sq utf8Seq) {
			seq := sq.seq[:sq.n]
			n := int32(0)
			for k := range seq {
				r, last := seq[k], k == len(seq)-1
				if rev {
					r, last = seq[len(seq)-1-k], k == len(seq)-1
				}
				found := int32(-1)
				if !last {
					for e := head[n]; e >= 0; e = ent[e].next {
						if t := ent[e].tr; t.lo == r.lo && t.hi == r.hi && t.next != target {
							found = local(t.next)
							break
						}
					}
				}
				if found < 0 {
					nx := target
					if !last {
						nx = b.node(bnode{kind: bByte})
						found = local(nx)
						head, tail = append(head, -1), append(tail, -1)
					}
					e := int32(len(ent))
					ent = append(ent, trieEnt{btr{r.lo, r.hi, nx}, -1})
					if tail[n] < 0 {
						head[n] = e
					} else {
						ent[tail[n]].next = e
					}
					tail[n] = e
				}
				n = found
			}
		})
	}
	for k := range head {
		start := len(b.trs)
		for e := head[k]; e >= 0; e = ent[e].next {
			b.trs = append(b.trs, ent[e].tr)
		}
		n := root
		if k > 0 {
			n = base + uint32(k) - 1
		}
		b.nodes[n] = bnode{kind: bByte, next: uint32(start), alt: uint32(len(b.trs))}
	}
}

func buildForward(p *prog) *bprog {
	reach := p.reachable()
	b := &bprog{fffd: p.hasFFFD(reach)}
	b.alloc(p)
	ids := make([]uint32, len(p.insts))
	for pc := range p.insts {
		if reach[pc] {
			ids[pc] = b.node(bnode{})
		}
	}
	for pc := range p.insts {
		if !reach[pc] {
			continue
		}
		in := &p.insts[pc]
		id := ids[pc]
		switch in.op {
		case opFail:
			b.nodes[id] = bnode{kind: bFail}
		case opMatch:
			b.nodes[id] = bnode{kind: bMatch}
		case opAlt:
			b.nodes[id] = bnode{kind: bSplit, next: ids[in.out], alt: ids[in.out1]}
		case opNop, opSave:
			b.nodes[id] = bnode{kind: bEps, next: ids[in.out]}
		case opEmpty:
			b.looks |= syntax.EmptyOp(in.arg)
			b.nodes[id] = bnode{kind: bLook, look: syntax.EmptyOp(in.arg), next: ids[in.out]}
		default:
			class := p.instRanges(in)
			if classHasFFFD(class) {
				v := b.validator()
				t := b.node(bnode{})
				b.fillTrie(t, class, ids[in.out])
				b.nodes[id] = bnode{kind: bSplit, next: v, alt: t}
			} else {
				b.fillTrie(id, class, ids[in.out])
			}
		}
	}
	b.start = ids[p.start]
	// Unanchored prefix: lowest priority, restarting the pattern at every
	// position. A pattern that never matches U+FFFD or the empty string can
	// skip single bytes: its matches begin with a whole encoded rune, at an
	// ASCII or lead byte, which is always where the standard decoder is
	// aligned. Other patterns step whole runes, so empty matches stay on rune
	// boundaries.
	u := b.node(bnode{kind: bSplit, next: b.start})
	if b.fffd || p.nullable() {
		t := b.node(bnode{})
		b.fillTrie(t, anyClass, u)
		b.nodes[u].alt = b.node(bnode{kind: bSplit, next: b.validator(), alt: t})
	} else {
		any := b.node(bnode{})
		b.byteNode(any, btr{0, 0xFF, u})
		b.nodes[u].alt = any
	}
	b.ustart = u
	return b
}

func buildReverse(p *prog) *bprog {
	reach := p.reachable()
	b := &bprog{reverse: true}
	b.alloc(p)
	// predecessor lists
	preds := make([][]uint32, len(p.insts))
	matchPC := uint32(0)
	for pc := range p.insts {
		if !reach[pc] {
			continue
		}
		in := &p.insts[pc]
		switch in.op {
		case opMatch:
			matchPC = uint32(pc)
		case opFail:
		case opAlt:
			preds[in.out] = append(preds[in.out], uint32(pc))
			preds[in.out1] = append(preds[in.out1], uint32(pc))
		default:
			preds[in.out] = append(preds[in.out], uint32(pc))
		}
	}
	ids := make([]uint32, len(p.insts))
	for pc := range p.insts {
		if reach[pc] {
			ids[pc] = b.node(bnode{})
		}
	}
	// R(i): reached in reverse after undoing everything that follows inst i.
	// From R(j), each forward edge i -> j is undone: epsilon edges directly,
	// assertions as assertions, consuming edges through a reversed unit.
	match := b.node(bnode{kind: bMatch})
	for pc := range p.insts {
		if !reach[pc] {
			continue
		}
		var alts []uint32
		if uint32(pc) == p.start {
			alts = append(alts, match)
		}
		for _, i := range preds[pc] {
			in := &p.insts[i]
			switch in.op {
			case opAlt, opNop, opSave:
				alts = append(alts, ids[i])
			case opEmpty:
				b.looks |= syntax.EmptyOp(in.arg)
				alts = append(alts, b.node(bnode{kind: bLook, look: syntax.EmptyOp(in.arg), next: ids[i]}))
			default:
				t := b.node(bnode{})
				b.fillRevTrie(t, p.instRanges(in), ids[i])
				alts = append(alts, t)
			}
		}
		id := ids[pc]
		switch len(alts) {
		case 0:
			b.nodes[id] = bnode{kind: bFail}
		case 1:
			b.nodes[id] = bnode{kind: bEps, next: alts[0]}
		default:
			// chain of splits in order
			cur := id
			for k := 0; k < len(alts)-1; k++ {
				var nx uint32
				if k == len(alts)-2 {
					nx = alts[k+1]
				} else {
					nx = b.node(bnode{})
				}
				b.nodes[cur] = bnode{kind: bSplit, next: alts[k], alt: nx}
				cur = nx
			}
		}
	}
	b.start = ids[matchPC]
	return b
}

// nullable reports whether the program can match without consuming input
// (assertions aside).
func (p *prog) nullable() bool {
	seen := make([]bool, len(p.insts))
	stk := []uint32{p.start}
	for len(stk) > 0 {
		pc := stk[len(stk)-1]
		stk = stk[:len(stk)-1]
		if pc == 0 || seen[pc] {
			continue
		}
		seen[pc] = true
		in := &p.insts[pc]
		switch in.op {
		case opMatch:
			return true
		case opAlt:
			stk = append(stk, in.out, in.out1)
		case opNop, opSave, opEmpty:
			stk = append(stk, in.out)
		}
	}
	return false
}
