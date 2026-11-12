package regexp

import (
	"regexp/syntax"
	"strings"
	"unicode/utf8"
)

// delimPlan matches L1(?s:.*?)L2 and L1(?s:.*)L2 (also with + and
// captures) for case-sensitive literals L1 and L2. The leftmost match
// starts at the first L1: any L2 that would close a later L1 also closes
// it. It ends at the first L2 after it (lazy) or the last (greedy). Both
// literals are valid UTF-8 starting with a byte that never continues a
// sequence, so byte search positions are rune boundaries of the standard
// decoder.
type delimPlan struct {
	open, close string
	minMid      int
	greedy      bool
	pfOpen      prefilter
	pfClose     prefilter
	caps        []int32 // fixed capture offsets, nil if none or not fixed
}

func newDelimPlan(re *syntax.Regexp) *delimPlan {
	if re.Op != syntax.OpConcat || len(re.Sub) != 3 {
		return nil
	}
	mid := unwrapCaptures(re.Sub[1])
	minMid := 0
	switch mid.Op {
	case syntax.OpStar:
	case syntax.OpPlus:
		minMid = 1
	default:
		return nil
	}
	if unwrapCaptures(mid.Sub[0]).Op != syntax.OpAnyChar {
		return nil
	}
	lit := func(r *syntax.Regexp) []rune {
		r = unwrapCaptures(r)
		if r.Op != syntax.OpLiteral || r.Flags&syntax.FoldCase != 0 || hasSpecialRune(r.Rune) {
			return nil
		}
		return r.Rune
	}
	open, close := lit(re.Sub[0]), lit(re.Sub[2])
	if len(open) == 0 || len(close) == 0 {
		return nil
	}
	// Both literals share one string.
	var buf [64]byte
	b := buf[:0]
	for _, r := range open {
		b = utf8.AppendRune(b, r)
	}
	n := len(b)
	for _, r := range close {
		b = utf8.AppendRune(b, r)
	}
	both := string(b)
	p := &delimPlan{open: both[:n], close: both[n:], minMid: minMid, greedy: mid.Flags&syntax.NonGreedy == 0}
	p.pfOpen.initLit(p.open)
	if !p.greedy {
		p.pfClose.initLit(p.close)
	}
	return p
}

func (p *delimPlan) find(s string, pos int) (int, int) {
	a := p.pfOpen.find(s, pos)
	if a < 0 {
		return -1, -1
	}
	from := a + len(p.open) + p.minMid
	if from > len(s) {
		return -1, -1
	}
	if p.greedy {
		b := strings.LastIndex(s[from:], p.close)
		if b < 0 {
			return -1, -1
		}
		return a, from + b + len(p.close)
	}
	b := p.pfClose.find(s, from)
	if b < 0 {
		return -1, -1
	}
	return a, b + len(p.close)
}

// innerPlan searches from a literal inside the pattern, for patterns with
// no literal to start from, such as e-mail addresses: P L S where the
// prefix P can never match the first byte of L. A match's L is then the
// first L at or after its start, so matches are found in the order of
// their L: the reverse DFA of P finds the leftmost start before an L
// (never crossing an earlier L), and the forward DFA anchored there
// confirms the match and finds its end. The reverse scans cover disjoint
// stretches; forward scans are bounded by a work budget, after which the
// search continues on the general path.
type innerPlan struct {
	lit string
	pf  prefilter
	at  int // index of L in the concatenation; P is the part before it
}

// newInnerPlan picks the rarest suitable literal of a top-level
// concatenation.
func newInnerPlan(re *syntax.Regexp) *innerPlan {
	if re.Op != syntax.OpConcat {
		return nil
	}
	best, bestRank := -1, 1<<30
	var buf [utf8.UTFMax]byte
	for k := 1; k < len(re.Sub); k++ {
		sub := unwrapCaptures(re.Sub[k])
		if sub.Op != syntax.OpLiteral || sub.Flags&syntax.FoldCase != 0 || hasSpecialRune(sub.Rune) || sub.Rune[0] >= 0x80 {
			continue
		}
		first := byte(sub.Rune[0])
		ok := true
		for _, p := range re.Sub[:k] {
			if mayContainByte(p, first) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		r := 1 << 30
		for _, x := range sub.Rune {
			n := utf8.EncodeRune(buf[:], x)
			for _, b := range buf[:n] {
				r = min(r, int(byteRank[b]))
			}
		}
		if r < bestRank {
			best, bestRank = k, r
		}
	}
	if best < 0 {
		return nil
	}
	ip := &innerPlan{lit: string(unwrapCaptures(re.Sub[best]).Rune), at: best}
	ip.pf.initLit(ip.lit)
	return ip
}

// mayContainByte reports whether a string matched by re can contain the
// ASCII byte b.
func mayContainByte(re *syntax.Regexp, b byte) bool {
	switch re.Op {
	case syntax.OpLiteral:
		for _, r := range re.Rune {
			if r == rune(b) {
				return true
			}
			if re.Flags&syntax.FoldCase != 0 {
				for _, f := range foldOrbit(r) {
					if f == rune(b) {
						return true
					}
				}
			}
		}
		return false
	case syntax.OpCharClass:
		for i := 0; i+1 < len(re.Rune); i += 2 {
			if re.Rune[i] <= rune(b) && rune(b) <= re.Rune[i+1] {
				return true
			}
		}
		return false
	case syntax.OpAnyChar:
		return true
	case syntax.OpAnyCharNotNL:
		return b != '\n'
	}
	for _, sub := range re.Sub {
		if mayContainByte(sub, b) {
			return true
		}
	}
	return false
}

func (c *core) innerRev(e *engines) *lazyDFA {
	if d := e.inner.Load(); d != nil {
		return d
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := e.inner.Load(); d != nil {
		return d
	}
	// P is found again in a fresh parse: plans keep no syntax trees.
	sub := unwrapCaptures(c.parse()).Sub
	at := c.inner().at
	pre := &syntax.Regexp{Op: syntax.OpConcat, Sub: sub[:at:at]}
	d := newLazyDFA(buildReverse(compileProg(pre, c.numSubexp)), true)
	e.inner.Store(d)
	return d
}

// findInner is find for planInner.
func (c *core) findInner(e *engines, s string, pos int) (int, int) {
	in := c.inner()
	rev := c.innerRev(e)
	budget := 4*(len(s)-pos) + 256
	for p := pos; ; {
		q := in.pf.find(s, p)
		if q < 0 {
			return -1, -1
		}
		t, stop, res := rev.rfindStop(s, pos, q)
		if res != resOK {
			return c.pikeFind(s, pos)
		}
		if c.fffd && !utf8.ValidString(s[max(stop, pos):q]) {
			return c.findDFA(e, s, pos)
		}
		if t >= 0 {
			end, _, stop, res := c.forward(e).search(s, t, true, false, nil)
			if res != resOK {
				return c.pikeFind(s, pos)
			}
			if end >= 0 {
				return t, end
			}
			if budget -= stop - t; budget < 0 {
				return c.findDFA(e, s, q+1)
			}
		}
		p = q + 1
	}
}
