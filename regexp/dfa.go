package regexp

import (
	"encoding/binary"
	"hash/maphash"
	"math/bits"
	"regexp/syntax"
	"sync"
	"sync/atomic"
	"unsafe"
)

// lazyDFA determinizes a byte NFA during matching. The transition table is
// shared by all goroutines: entries are written once with atomic stores
// under mu and read with atomic loads (plain loads on amd64 and arm64), so a
// cached transition costs one load and no locking. Only a cache miss takes
// mu. When the cache exceeds its budget a new generation replaces it; a
// search still holding the old table re-enters through the new one.
//
// A state is the ordered list of NFA nodes alive before the next byte plus
// the context of the byte just read (newline, ASCII word byte, or edge of
// input). Empty-width assertions are evaluated during the transition, when
// the following byte is known, so a match is reported one byte late: the
// transition taken on byte i carries fMatch when a match ended at i.

const (
	ctxOther = 0
	ctxWord  = 1
	ctxNL    = 2
	ctxEdge  = 3
	nctx     = 4
)

const (
	fUnknown = 1 << 31 // not computed yet
	fMatch   = 1 << 30 // a match ended just before this byte
	fDead    = 1 << 29 // no thread survives
	fQuit    = 1 << 28 // the DFA cannot decide (invalid UTF-8 inside a U+FFFD class)
	fStart   = 1 << 27 // target is an unanchored start state
	fLoop    = 1 << 26 // a self-loop of an accelerated state; the low bits index dgen.loops
	fSpecial = fLoop
	offMask  = fLoop - 1
)

// Accelerated states. A state that stays in place on many bytes, such as
// the body of [a-z0-9._-]* or the gap before an unanchored literal, has its
// self-loop transitions marked fLoop; the search then skips the whole run
// with independent table loads or the vector span scan instead of one
// dependent transition per byte. Searches turn a loop back into plain
// transitions when its runs prove short.
const (
	maxLoops     = 8
	minLoopBytes = 8
)

type dloop struct {
	in  [256]bool
	nib runNibbles
	st  uint32 // the state
	v   uint32 // its plain self-loop transition
}

// skip returns the end of the run of loop bytes starting at i. Short
// tails are tested four bytes at a time, without a branch per byte.
func (l *dloop) skip(s string, i int) int {
	in := &l.in
	for k := 0; k < 4 && i+4 <= len(s); k++ {
		a, b, c, d := in[s[i]], in[s[i+1]], in[s[i+2]], in[s[i+3]]
		if !(a && b && c && d) {
			switch {
			case !a:
				return i
			case !b:
				return i + 1
			case !c:
				return i + 2
			}
			return i + 3
		}
		i += 4
	}
	if len(s)-i < 32 {
		for i < len(s) && in[s[i]] {
			i++
		}
		return i
	}
	if hasSpanKernel {
		// The vector scan stops at non-ASCII members too; the DFA then
		// takes that byte and the loop resumes.
		return i + span(s[i:], &l.nib.lo, &l.nib.hi, &l.in, true)
	}
	for i < len(s) && l.in[s[i]] {
		i++
	}
	return i
}

type lazyDFA struct {
	bp      *bprog
	cls     [256]uint8
	rep     []byte // a representative byte per class
	ncls    int
	stride  int
	longest bool // reverse searches: keep going after a match
	// usePF tags transitions back into the unanchored start state so the
	// search can hand over to the prefilter.
	usePF   bool
	wordCtx bool
	nlCtx   bool
	edgeCtx bool

	maxBytes int
	mu       sync.Mutex
	tab      atomic.Pointer[dtable]
}

type dtable struct {
	trans  []uint32
	starts [2 * nctx]uint32 // [anchored*nctx + ctx] -> offset+1, 0 if unknown
	gen    *dgen
}

type dgen struct {
	// index finds states by key: an open-addressing table of state numbers
	// plus one (zero marks a free slot), at most half full.
	index  []uint32
	keys   []byte   // the state keys, concatenated in state order
	ends   []uint32 // ends[i] is the end of the key of state i in keys
	nstate int
	bytes  int
	clears int
	loops  [maxLoops]atomic.Pointer[dloop]
	nloops int
	tried  []uint32 // states considered for acceleration; under mu
}

func newLazyDFA(bp *bprog, longest bool) *lazyDFA {
	d := &lazyDFA{bp: bp, longest: longest}
	looks := bp.looks
	d.wordCtx = looks&(syntax.EmptyWordBoundary|syntax.EmptyNoWordBoundary) != 0
	d.nlCtx = looks&(syntax.EmptyBeginLine|syntax.EmptyEndLine) != 0
	d.edgeCtx = looks&(syntax.EmptyBeginLine|syntax.EmptyEndLine|syntax.EmptyBeginText|syntax.EmptyEndText) != 0
	// Byte classes: two bytes share a class when every node sends them to
	// the same target and they have the same context. Each distinct target
	// byte set refines the partition, so case-folded letters such as 'a'
	// and 'A' stay in one class.
	var buf [32]bset
	sets := buf[:0]
	var seen map[bset]struct{} // only with many sets; a scan finds few
	add := func(b bset) {
		if seen == nil {
			for i := range sets {
				if sets[i] == b {
					return
				}
			}
			sets = append(sets, b)
			if len(sets) == 32 {
				seen = make(map[bset]struct{}, 64)
				for _, x := range sets {
					seen[x] = struct{}{}
				}
			}
			return
		}
		if _, ok := seen[b]; !ok {
			seen[b] = struct{}{}
			sets = append(sets, b)
		}
	}
	for i := range bp.nodes {
		tr := bp.trans(uint32(i))
	next:
		for j := range tr {
			for k := 0; k < j; k++ {
				if tr[k].next == tr[j].next {
					continue next // already part of this target's set
				}
			}
			var b bset
			for _, t := range tr[j:] {
				if t.next == tr[j].next {
					b.addRange(t.lo, t.hi)
				}
			}
			add(b)
		}
	}
	if d.nlCtx {
		var b bset
		b.addRange('\n', '\n')
		add(b)
	}
	if d.wordCtx {
		var b bset
		b.addRange('0', '9')
		b.addRange('A', 'Z')
		b.addRange('_', '_')
		b.addRange('a', 'z')
		add(b)
	}
	c := refine(&d.cls, sets)
	d.rep = make([]byte, c)
	for b := 255; b >= 0; b-- {
		d.rep[d.cls[b]] = byte(b)
	}
	d.ncls = c
	d.stride = d.ncls + nctx
	d.maxBytes = dfaBudget(len(bp.nodes), d.stride)
	d.tab.Store(d.newTable(nil))
	return d
}

// bset is a set of bytes.
type bset [4]uint64

func (b *bset) addRange(lo, hi byte) {
	for c := int(lo); c <= int(hi); c++ {
		b[c>>6] |= 1 << (c & 63)
	}
}

func (b *bset) has(c byte) bool { return b[c>>6]&(1<<(c&63)) != 0 }

// refine partitions the bytes so that no class straddles a set, numbering
// classes by their smallest byte. It returns the number of classes.
func refine(cls *[256]uint8, sets []bset) int {
	*cls = [256]uint8{}
	n := 1
	for k := range sets {
		if n = refineBy(cls, n, &sets[k]); n == 256 {
			break
		}
	}
	return n
}

// refineBy splits the n classes of cls by s, keeping them numbered by
// their smallest byte, and returns the new number of classes.
func refineBy(cls *[256]uint8, n int, s *bset) int {
	var ids [512]int16
	for i := 0; i < 2*n; i++ {
		ids[i] = -1
	}
	m := 0
	for b := 0; b < 256; b++ {
		key := int(cls[b]) << 1
		if s.has(byte(b)) {
			key |= 1
		}
		if ids[key] < 0 {
			ids[key] = int16(m)
			m++
		}
		cls[b] = uint8(ids[key])
	}
	return m
}

func (d *lazyDFA) newTable(prev *dtable) *dtable {
	t := &dtable{}
	if prev == nil {
		// Tables take power-of-two sizes, which share the allocator's
		// size classes with each other, so the tables of many patterns
		// fill few spans. The first has room for about one state per four
		// NFA nodes, at least eight, up to 4 KB, so that most searches
		// never grow it; the index and keys are sized to match.
		n := max(8, len(d.bp.nodes)/4) * d.stride
		n = min(1<<bits.Len(uint(n-1)), max(1<<10, 1<<(bits.Len(uint(8*d.stride))-1)))
		t.trans = d.alloc(nil, n)
		fit := n / d.stride
		t.gen = &dgen{
			index: make([]uint32, 1<<bits.Len(uint(2*fit-1))),
			keys:  make([]byte, 0, 8*fit),
			ends:  make([]uint32, 0, fit),
		}
	} else {
		t.gen = prev.gen
		t.trans = d.alloc(prev.trans, 2*len(prev.trans))
		for i := range prev.starts {
			t.starts[i] = atomic.LoadUint32(&prev.starts[i])
		}
	}
	return t
}

func (d *lazyDFA) alloc(old []uint32, n int) []uint32 {
	tr := make([]uint32, n)
	k := 0
	for ; k < len(old); k++ {
		tr[k] = atomic.LoadUint32(&old[k])
	}
	for ; k < n; k++ {
		tr[k] = fUnknown
	}
	return tr
}

// ctxOf classifies a byte for empty-width assertions, collapsing distinctions
// the pattern does not use.
func (d *lazyDFA) ctxOf(b byte) uint8 {
	if b == '\n' && d.nlCtx {
		return ctxNL
	}
	if d.wordCtx && isWordByte(b) {
		return ctxWord
	}
	return ctxOther
}

func (d *lazyDFA) edge() uint8 {
	if d.edgeCtx {
		return ctxEdge
	}
	return ctxOther
}

func isWordByte(b byte) bool {
	return 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9' || b == '_'
}

// ctxBefore is the context of the byte before position i.
func (d *lazyDFA) ctxBefore(s string, i int) uint8 {
	if i <= 0 {
		return d.edge()
	}
	return d.ctxOf(s[i-1])
}

// ctxAt is the context of the byte at position i.
func (d *lazyDFA) ctxAt(s string, i int) uint8 {
	if i >= len(s) {
		return d.edge()
	}
	return d.ctxOf(s[i])
}

func lookFlags(left, right uint8) syntax.EmptyOp {
	var f syntax.EmptyOp
	switch left {
	case ctxEdge:
		f |= syntax.EmptyBeginText | syntax.EmptyBeginLine
	case ctxNL:
		f |= syntax.EmptyBeginLine
	}
	switch right {
	case ctxEdge:
		f |= syntax.EmptyEndText | syntax.EmptyEndLine
	case ctxNL:
		f |= syntax.EmptyEndLine
	}
	if (left == ctxWord) != (right == ctxWord) {
		f |= syntax.EmptyWordBoundary
	} else {
		f |= syntax.EmptyNoWordBoundary
	}
	return f
}

// builder holds scratch for computing transitions; used only under mu.
type builder struct {
	seen   []uint32 // generation marks per NFA node
	mark   uint32
	stack  []uint32
	cons   []uint32
	next   []uint32
	key    []byte
	nodes0 []uint32
}

var builderPool = sync.Pool{New: func() any { return new(builder) }}

func (bd *builder) reset(n int) {
	if len(bd.seen) < n {
		bd.seen = make([]uint32, n)
		bd.mark = 0
	}
	bd.mark++
	if bd.mark == 0 {
		for i := range bd.seen {
			bd.seen[i] = 0
		}
		bd.mark = 1
	}
}

func encodeKey(dst []byte, ctx uint8, nodes []uint32) []byte {
	dst = append(dst[:0], ctx)
	for _, n := range nodes {
		dst = binary.AppendUvarint(dst, uint64(n))
	}
	return dst
}

func decodeKey(key []byte, nodes []uint32) (uint8, []uint32) {
	ctx := key[0]
	b := key[1:]
	nodes = nodes[:0]
	for len(b) > 0 {
		v, n := binary.Uvarint(b)
		nodes = append(nodes, uint32(v))
		b = b[n:]
	}
	return ctx, nodes
}

// stateSeed seeds the hash of state keys.
var stateSeed = maphash.MakeSeed()

// key returns the key of state i.
func (g *dgen) key(i uint32) []byte {
	lo := uint32(0)
	if i > 0 {
		lo = g.ends[i-1]
	}
	return g.keys[lo:g.ends[i]]
}

// intern returns the offset of the state with the given key, adding it.
// It may replace d.tab (growth); offsets stay valid within a generation.
func (d *lazyDFA) intern(t *dtable, key []byte) (*dtable, uint32) {
	g := t.gen
	mask := uint64(len(g.index) - 1)
	i := maphash.Bytes(stateSeed, key) & mask
	for ; g.index[i] != 0; i = (i + 1) & mask {
		if st := g.index[i] - 1; string(g.key(st)) == string(key) {
			return t, st * uint32(d.stride)
		}
	}
	off := uint32(g.nstate * d.stride)
	if int(off)+d.stride > len(t.trans) {
		t = d.newTable(t)
		d.tab.Store(t)
	}
	g.index[i] = uint32(g.nstate) + 1
	g.keys = append(g.keys, key...)
	g.ends = append(g.ends, uint32(len(g.keys)))
	g.nstate++
	g.bytes += d.stride*4 + 2*len(key) + 48
	if 2*g.nstate > len(g.index) {
		g.rehash()
	}
	return t, off
}

// rehash doubles the index.
func (g *dgen) rehash() {
	index := make([]uint32, 2*len(g.index))
	mask := uint64(len(index) - 1)
	for st := uint32(0); st < uint32(g.nstate); st++ {
		i := maphash.Bytes(stateSeed, g.key(st)) & mask
		for index[i] != 0 {
			i = (i + 1) & mask
		}
		index[i] = st + 1
	}
	g.index = index
}

// compute returns the transition value from the state with key nodes/ctx on
// byte class col (< ncls) or on the end of input with the given next-side
// context (col = ncls + ctx).
func (d *lazyDFA) compute(t *dtable, bd *builder, ctx uint8, nodes []uint32, col int) (*dtable, uint32) {
	bp := d.bp
	var nextCtx uint8
	var b byte
	end := col >= d.ncls
	if end {
		nextCtx = uint8(col - d.ncls)
	} else {
		b = d.rep[col]
		nextCtx = d.ctxOf(b)
	}
	left, right := ctx, nextCtx
	if bp.reverse {
		left, right = nextCtx, ctx
	}
	flags := lookFlags(left, right)

	bd.reset(len(bp.nodes))
	cons := bd.cons[:0]
	matched := false
	stk := bd.stack[:0]
	for k := len(nodes) - 1; k >= 0; k-- {
		stk = append(stk, nodes[k])
	}
closure:
	for len(stk) > 0 {
		n := stk[len(stk)-1]
		stk = stk[:len(stk)-1]
		for {
			if bd.seen[n] == bd.mark {
				break
			}
			bd.seen[n] = bd.mark
			nd := &bp.nodes[n]
			switch nd.kind {
			case bSplit:
				stk = append(stk, nd.alt)
				n = nd.next
				continue
			case bEps:
				n = nd.next
				continue
			case bLook:
				if nd.look&^flags == 0 {
					n = nd.next
					continue
				}
			case bMatch:
				matched = true
				if !d.longest {
					break closure // lower-priority threads lose
				}
			case bByte:
				cons = append(cons, n)
			case bQuit:
				bd.cons, bd.stack = cons, stk
				return t, fQuit
			}
			break
		}
	}
	bd.cons, bd.stack = cons, stk[:0]
	var v uint32
	if matched {
		v = fMatch
	}
	if end {
		for _, n := range cons {
			if bp.nodes[n].quitAtEnd {
				return t, fQuit
			}
		}
		return t, v | fDead
	}
	bd.reset(len(bp.nodes))
	next := bd.next[:0]
	for _, n := range cons {
		for _, tr := range bp.trans(n) {
			if tr.lo <= b && b <= tr.hi {
				x := tr.next
				if bp.nodes[x].kind == bQuit {
					bd.next = next
					return t, fQuit
				}
				if x != 0 && bd.seen[x] != bd.mark {
					bd.seen[x] = bd.mark
					next = append(next, x)
				}
			}
		}
	}
	bd.next = next
	if len(next) == 0 {
		return t, v | fDead
	}
	bd.key = encodeKey(bd.key, nextCtx, next)
	t, off := d.intern(t, bd.key)
	if d.usePF && len(next) == 1 && next[0] == bp.ustart && bp.ustart != 0 {
		v |= fStart
	}
	return t, v | off
}

// slow computes and caches the transition from state st (an offset into t)
// on column col. It returns the current table, st translated into it, the
// transition value, and whether the cache had to be cleared.
func (d *lazyDFA) slow(t *dtable, st uint32, col int) (*dtable, uint32, uint32, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	bd := builderPool.Get().(*builder)
	defer builderPool.Put(bd)
	cur := d.tab.Load()
	if cur.gen != t.gen {
		// The cache was cleared: re-enter the same state in the new one.
		cur, st = d.intern(cur, t.gen.key(st/uint32(d.stride)))
	}
	t = cur
	if v := atomic.LoadUint32(&t.trans[int(st)+col]); v != fUnknown {
		return t, st, v, false
	}
	cleared := false
	if t.gen.bytes > d.maxBytes {
		key := t.gen.key(st / uint32(d.stride))
		nt := d.newTable(nil)
		nt.gen.clears = t.gen.clears + 1
		d.tab.Store(nt)
		t, st = d.intern(nt, key)
		cleared = true
	}
	ctx, nodes := decodeKey(t.gen.key(st/uint32(d.stride)), bd.nodes0[:0])
	bd.nodes0 = nodes
	t, v := d.compute(t, bd, ctx, nodes, col)
	if v&^(fMatch|offMask) == 0 && v&offMask == st && !d.bp.reverse {
		t, v = d.accelerate(t, bd, st, col, v, ctx, nodes)
	}
	atomic.StoreUint32(&t.trans[int(st)+col], v)
	return t, st, v, cleared
}

// accelerate is called with v, the self-loop transition of state st on
// column col. It computes the other columns of st and, if enough bytes keep
// st in place with the same flags, returns the fLoop form of v and stores
// it for those columns.
func (d *lazyDFA) accelerate(t *dtable, bd *builder, st uint32, col int, v uint32, ctx uint8, nodes []uint32) (*dtable, uint32) {
	g := t.gen
	if g.nloops == maxLoops || len(g.tried) >= 4*maxLoops {
		return t, v
	}
	for _, x := range g.tried {
		if x == st {
			return t, v
		}
	}
	g.tried = append(g.tried, st)
	var same [256]bool // by column
	for c := 0; c < d.ncls; c++ {
		w := v
		if c != col {
			w = atomic.LoadUint32(&t.trans[int(st)+c])
			if w == fUnknown {
				t, w = d.compute(t, bd, ctx, nodes, c)
				atomic.StoreUint32(&t.trans[int(st)+c], w)
			}
		}
		same[c] = w == v
	}
	l := &dloop{st: st, v: v}
	n := 0
	for b := 0; b < 256; b++ {
		if same[d.cls[b]] {
			l.in[b] = true
			n++
		}
	}
	if n < minLoopBytes {
		return t, v
	}
	l.nib.lo, l.nib.hi = nibbleTables(&l.in)
	k := g.nloops
	g.nloops++
	g.loops[k].Store(l)
	g.bytes += int(unsafe.Sizeof(*l))
	lv := fLoop | v&fMatch | uint32(k)
	for c := 0; c < d.ncls; c++ {
		if same[c] && c != col {
			atomic.StoreUint32(&t.trans[int(st)+c], lv)
		}
	}
	return t, lv
}

// unloop turns the accelerated loop lv back into plain transitions.
func (d *lazyDFA) unloop(t *dtable, lv uint32) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cur := d.tab.Load()
	if cur.gen != t.gen {
		return
	}
	l := cur.gen.loops[lv&offMask].Load()
	for c := 0; c < d.ncls; c++ {
		p := &cur.trans[int(l.st)+c]
		if atomic.LoadUint32(p) == lv {
			atomic.StoreUint32(p, l.v)
		}
	}
}

// loopStats tracks the runs a search skips, to give up on loops whose runs
// are too short to pay for the skip.
type loopStats struct{ runs, bytes int }

func (ls *loopStats) short(n int) bool {
	ls.runs++
	ls.bytes += n
	if ls.runs < 32 {
		return false
	}
	short := ls.bytes < 8*ls.runs
	*ls = loopStats{}
	return short
}

// start returns the start state for a search with the given anchoring and
// context before the first byte.
func (d *lazyDFA) start(t *dtable, anchored bool, ctx uint8) (*dtable, uint32) {
	i := int(ctx)
	if anchored {
		i += nctx
	}
	if v := atomic.LoadUint32(&t.starts[i]); v != 0 {
		return t, v - 1
	}
	return d.startSlow(i, anchored, ctx)
}

func (d *lazyDFA) startSlow(i int, anchored bool, ctx uint8) (*dtable, uint32) {
	d.mu.Lock()
	defer d.mu.Unlock()
	t := d.tab.Load()
	if v := atomic.LoadUint32(&t.starts[i]); v != 0 {
		return t, v - 1
	}
	n := d.bp.start
	if !anchored && !d.bp.reverse {
		n = d.bp.ustart
	}
	var buf [16]byte
	t, off := d.intern(t, encodeKey(buf[:0], ctx, []uint32{n}))
	atomic.StoreUint32(&t.starts[i], off+1)
	return t, off
}

// Search results.
const (
	resOK    = 0
	resQuit  = 1 // fall back to the exact engine
	resNoDFA = 2
)

// load reads transition entry off of a table without a bounds check:
// state offsets and class columns always lie inside the table they come
// from.
func load(base unsafe.Pointer, off uint32) uint32 {
	return atomic.LoadUint32((*uint32)(unsafe.Add(base, uintptr(off)*4)))
}

// scanFwd follows transitions from st over s[i:] until it reaches the end
// or a transition other than a plain or match-only one. It returns where it
// stopped, the state there, the stopping transition (0 at the end), and the
// last match end seen (or last unchanged). Kept small so the loop stays in
// registers.
func scanFwd(base unsafe.Pointer, cls *[256]uint8, s string, i int, st uint32, last int) (int, uint32, uint32, int) {
	for i < len(s) {
		v := load(base, st+uint32(cls[s[i]]))
		if v >= fSpecial {
			if v&^(fMatch|offMask) != 0 {
				return i, st, v, last
			}
			last = i
			v &= offMask
		}
		st = v
		i++
	}
	return i, st, 0, last
}

// scanRev is scanFwd for reverse searches, moving down to lo.
func scanRev(base unsafe.Pointer, cls *[256]uint8, s string, lo, i int, st uint32, last int) (int, uint32, uint32, int) {
	for i > lo {
		v := load(base, st+uint32(cls[s[i-1]]))
		if v >= fSpecial {
			if v&^(fMatch|offMask) != 0 {
				return i, st, v, last
			}
			last = i
			v &= offMask
		}
		st = v
		i--
	}
	return i, st, 0, last
}

// find runs the forward DFA from pos and returns the end of the
// leftmost-first match (-1 if none) and the position where the threads of
// that match could first have started (the last prefilter candidate, or
// pos). With earliest it stops at the first match end, which suffices to
// decide whether any match exists.
func (d *lazyDFA) find(s string, pos int, anchored, earliest bool, pf *prefilter) (end, from, res int) {
	end, from, _, res = d.search(s, pos, anchored, earliest, pf)
	return
}

// search is find that also reports where the scan stopped.
func (d *lazyDFA) search(s string, pos int, anchored, earliest bool, pf *prefilter) (end, from, stop, res int) {
	t := d.tab.Load()
	i := pos
	if pf != nil && !anchored {
		c := pf.find(s, i)
		if c < 0 {
			return -1, pos, len(s), resOK
		}
		i = c
	}
	from = i
	t, st := d.start(t, anchored, d.ctxBefore(s, i))
	base := unsafe.Pointer(unsafe.SliceData(t.trans))
	last := -1
	cls := &d.cls
	var th thrash
	th.last = i
	var ls loopStats
	n := len(s)
	for {
		var v uint32
		if earliest {
			i, st, v = scanFwdEarliest(base, cls, s, i, st)
		} else {
			i, st, v, last = scanFwd(base, cls, s, i, st, last)
		}
		if i >= n {
			break
		}
		if v == fUnknown {
			var cleared bool
			t, st, v, cleared = d.slow(t, st, int(cls[s[i]]))
			base = unsafe.Pointer(unsafe.SliceData(t.trans))
			if cleared && th.strike(i) {
				return -1, from, i, resQuit
			}
		}
		if v&fQuit != 0 {
			return -1, from, i, resQuit
		}
		if v&fMatch != 0 {
			last = i
			if earliest {
				return i, from, i, resOK
			}
		}
		if v&fDead != 0 {
			return last, from, i, resOK
		}
		if v&fStart != 0 && pf != nil {
			c := pf.find(s, i+1)
			if c < 0 {
				return last, from, i, resOK
			}
			i = c
			from = c
			t, st = d.start(t, false, d.ctxBefore(s, i))
			base = unsafe.Pointer(unsafe.SliceData(t.trans))
			continue
		}
		if v&fLoop != 0 {
			j := t.gen.loops[v&offMask].Load().skip(s, i+1)
			if v&fMatch != 0 {
				last = j - 1
			}
			if ls.short(j - i) {
				d.unloop(t, v)
			}
			i = j
			continue
		}
		st = v & offMask
		i++
	}
	col := d.ncls + int(d.edge())
	v := load(base, st+uint32(col))
	if v == fUnknown {
		t, st, v, _ = d.slow(t, st, col)
	}
	if v&fQuit != 0 {
		return -1, from, i, resQuit
	}
	if v&fMatch != 0 {
		last = n
	}
	return last, from, i, resOK
}

// isMatch decides whether s has a match for a search without a prefilter.
// It is find(s, 0, anchored, true, nil) with the common outcomes handled
// first, which matters for short strings.
func (d *lazyDFA) isMatch(s string, anchored bool) (bool, int) {
	t := d.tab.Load()
	k := int(d.edge())
	if anchored {
		k += nctx
	}
	sv := atomic.LoadUint32(&t.starts[k])
	if sv != 0 {
		base := unsafe.Pointer(unsafe.SliceData(t.trans))
		i, st := 0, sv-1
		var ls loopStats
		for {
			var v uint32
			i, st, v = scanFwdEarliest(base, &d.cls, s, i, st)
			if i >= len(s) {
				if v := load(base, st+uint32(d.ncls+int(d.edge()))); v != fUnknown && v&fQuit == 0 {
					return v&fMatch != 0, resOK
				}
				break
			}
			if v&(fUnknown|fQuit) != 0 {
				break
			}
			if v&fMatch != 0 {
				return true, resOK
			}
			if v&fDead != 0 {
				return false, resOK
			}
			if v&fLoop == 0 {
				break
			}
			j := t.gen.loops[v&offMask].Load().skip(s, i+1)
			if ls.short(j - i) {
				d.unloop(t, v)
			}
			i = j
		}
	}
	end, _, res := d.find(s, 0, anchored, true, nil)
	return end >= 0, res
}

// scanFwdEarliest is scanFwd for searches that stop at the first match.
func scanFwdEarliest(base unsafe.Pointer, cls *[256]uint8, s string, i int, st uint32) (int, uint32, uint32) {
	for i < len(s) {
		v := load(base, st+uint32(cls[s[i]]))
		if v >= fSpecial {
			return i, st, v
		}
		st = v
		i++
	}
	return i, st, 0
}

// rfind runs the reverse DFA from end down to lo and returns the smallest
// start of a match ending at end (-1 if none).
func (d *lazyDFA) rfind(s string, lo, end int) (int, int) {
	start, _, res := d.rfindStop(s, lo, end)
	return start, res
}

// rfindStop is rfind that also reports the lowest position the scan
// examined: s[stop:end] holds every byte the result depends on.
func (d *lazyDFA) rfindStop(s string, lo, end int) (start, stop, res int) {
	t := d.tab.Load()
	t, st := d.start(t, true, d.ctxAt(s, end))
	base := unsafe.Pointer(unsafe.SliceData(t.trans))
	last := -1
	cls := &d.cls
	i := end
	var th thrash
	for {
		var v uint32
		i, st, v, last = scanRev(base, cls, s, lo, i, st, last)
		if i <= lo {
			break
		}
		if v == fUnknown {
			var cleared bool
			t, st, v, cleared = d.slow(t, st, int(cls[s[i-1]]))
			base = unsafe.Pointer(unsafe.SliceData(t.trans))
			if cleared && th.strike(end-i) {
				return -1, i, resQuit
			}
		}
		if v&fQuit != 0 {
			return -1, i, resQuit
		}
		if v&fMatch != 0 {
			last = i
		}
		if v&fDead != 0 {
			return last, i - 1, resOK
		}
		st = v & offMask
		i--
	}
	col := d.ncls + int(d.ctxBefore(s, lo))
	v := load(base, st+uint32(col))
	if v == fUnknown {
		t, st, v, _ = d.slow(t, st, col)
	}
	if v&fQuit != 0 {
		return -1, lo, resQuit
	}
	if v&fMatch != 0 {
		last = lo
	}
	return last, lo, resOK
}

// thrash decides when a search should stop using the DFA: the cache keeps
// filling up before the search makes much progress, so building states costs
// more than simulating the NFA.
type thrash struct {
	last    int // progress at the previous clear
	strikes int
}

func (th *thrash) strike(progress int) bool {
	if progress-th.last < 16<<10 {
		th.strikes++
	}
	th.last = progress
	return th.strikes >= 3
}
