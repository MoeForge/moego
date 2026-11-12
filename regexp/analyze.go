package regexp

import (
	"regexp/syntax"
	"strings"
	"unicode/utf8"
	"unsafe"
)

// plan is what compile-time analysis decides about a pattern.
type plan struct {
	kind        uint8
	anchorStart bool // every match starts at the beginning of the text
	anchorEnd   bool // every match ends at the end of the text
	// fffd: the pattern can match U+FFFD, which stands for each byte of
	// invalid UTF-8. Reverse automata model valid UTF-8 only, so plans that
	// scan backwards check that the bytes they scanned are valid.
	fffd      bool
	mode      syntax.Flags // parse flags, to parse the expression again
	lit       string       // planLiteral: the whole pattern
	reqPrefix string       // anchorStart: literal the text must start with
	reqSuffix string       // anchorEnd: literal the text must end with
	// Word rejects for anchored patterns: with len(text) >= 8, the first
	// (last) 8 bytes OR rejOr, masked with rejMask, must equal rejWant.
	// They cover case-folded literals too (letters are ORed with 0x20).
	rejPre, rejSuf rejWord
	fixedLen       int // byte length of every match, or -1
	minLen         int // minimum byte length of a match
	// lenSpan bounds the length of texts that can match: a text s can
	// only match if uint(len(s)-minLen) <= lenSpan. It is MaxInt unless
	// every match spans the whole text and has a maximum length.
	lenSpan uint
	pf      *prefilter
	// sub is the plan of kind: *runPlan, *wildPlan, *delimPlan,
	// *tailRunPlan or *innerPlan. The kinds are exclusive, so they share
	// one field; the accessors below give it its type.
	sub unsafe.Pointer
}

func (p *plan) run() *runPlan           { return (*runPlan)(p.sub) }
func (p *plan) wild() *wildPlan         { return (*wildPlan)(p.sub) }
func (p *plan) delim() *delimPlan       { return (*delimPlan)(p.sub) }
func (p *plan) tailRun() *tailRunPlan   { return (*tailRunPlan)(p.sub) }
func (p *plan) inner() *innerPlan       { return (*innerPlan)(p.sub) }
func (p *plan) onePassLit() *onePassLit { return (*onePassLit)(p.sub) }
func (p *plan) litRun() *litRunPlan     { return (*litRunPlan)(p.sub) }

// Plans between planDFA and planEnd decide without the DFA.
const (
	planDFA        uint8 = iota
	planLiteral          // a case-sensitive literal
	planFold             // an ASCII case-insensitive literal
	planEmpty            // matches the empty string at every position
	planRun              // a run of one character class: C+, C{n,m}
	planWild             // ^P.*S$ with literals P and S
	planDelim            // L1.*?L2 and L1.*L2 with (?s)
	planTailRun          // C{n,}D{0,k} for disjoint ASCII classes C and D
	planLitRun           // L C{n,m}: a literal, then a greedy run of an ASCII class
	planEnd              // anchored at the end: search backwards from it
	planInner            // P L S: search from the literal L
	planOnePassLit       // one-pass, \A and a literal with a surrogate: see onePassLit
)

func (c *core) analyze(re *syntax.Regexp, rp *runPlan) {
	c.fixedLen = -1
	c.minLen = minBytes(re)
	c.lenSpan = ^uint(0) >> 1
	inner := unwrapCaptures(re)
	switch {
	case inner.Op == syntax.OpLiteral && len(inner.Rune) > 0 && !hasSpecialRune(inner.Rune):
		if inner.Flags&syntax.FoldCase == 0 {
			c.kind = planLiteral
			c.lit = string(inner.Rune)
			c.pf = newLitPrefilter(c.lit)
			c.fixedLen = len(c.lit)
			return
		}
		if lit := foldPrefix(inner); len(lit) == len(inner.Rune) {
			c.kind = planFold
			c.lit = lit
			c.pf = newFoldPrefilter(lit)
			c.fixedLen = len(lit)
			return
		}
	case inner.Op == syntax.OpEmptyMatch:
		c.kind = planEmpty
		c.fixedLen = 0
		return
	}
	if rp != nil && rp.init(inner) {
		c.kind = planRun
		c.sub = unsafe.Pointer(rp)
		return
	}
	if tp := newTailRunPlan(inner); tp != nil {
		c.kind = planTailRun
		c.sub = unsafe.Pointer(tp)
		return
	}
	if dp := newDelimPlan(inner); dp != nil {
		c.kind = planDelim
		c.sub = unsafe.Pointer(dp)
		dp.caps = capOffsets(re, c.numSubexp)
		return
	}
	if w := newWildPlan(re); w != nil {
		c.kind = planWild
		c.sub = unsafe.Pointer(w)
		return
	}
	if lr := newLitRunPlan(inner); lr != nil {
		c.kind = planLitRun
		c.sub = unsafe.Pointer(lr)
		c.pf = newLitPrefilter(lr.lit)
		// Submatches then need no engines.
		lr.caps = capOffsets(re, c.numSubexp)
		return
	}
	// The plans above never run the automata. The others compile the
	// program from the tree when the engines are first built.
	c.tree.Store(re)
	if hasUnencodable(re) {
		p := compileProg(re, c.numSubexp)
		c.fffd = p.hasFFFD(p.reachable())
		c.anchorStart = p.anchoredStart()
		if ol := p.onePassLitPlan(); ol != nil {
			c.kind = planOnePassLit
			c.sub = unsafe.Pointer(ol)
			return
		}
	} else {
		c.fffd = mayMatchFFFD(re)
		c.anchorStart = startAnchored(re)
	}
	c.anchorEnd = endAnchored(re)
	if c.anchorStart && c.anchorEnd {
		if m := maxBytes(re); m >= c.minLen {
			c.lenSpan = uint(m - c.minLen)
		}
	}
	if c.anchorStart {
		c.reqPrefix = anchoredPrefix(re)
		c.rejPre = prefixWord(anchoredLit(re, false))
	}
	if c.anchorEnd {
		c.reqSuffix = anchoredSuffix(re)
		c.rejSuf = suffixWord(anchoredLit(re, true))
	}
	if n, ok := fixedBytes(re); ok {
		c.fixedLen = n
	}
	if !c.anchorStart && c.minLen > 0 {
		c.pf = choosePrefilter(re)
	}
	if c.anchorEnd && !c.anchorStart {
		c.kind = planEnd
		return
	}
	if (c.pf == nil || c.pf.kind == pfSet) && !c.anchorStart {
		if ip := newInnerPlan(inner); ip != nil {
			c.kind = planInner
			c.sub = unsafe.Pointer(ip)
			c.pf = nil
		}
	}
}

// startAnchored reports whether every match of re starts at the beginning
// of the text: every path through it passes a begin-text assertion before
// reading. It may report false for some anchored patterns, never true for
// others.
func startAnchored(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpBeginText:
		return true
	case syntax.OpCapture, syntax.OpPlus:
		return startAnchored(re.Sub[0])
	case syntax.OpRepeat:
		return re.Min >= 1 && startAnchored(re.Sub[0])
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			if startAnchored(sub) {
				return true
			}
			if !isZeroWidth(sub) {
				return false
			}
		}
	case syntax.OpAlternate:
		for _, sub := range re.Sub {
			if !startAnchored(sub) {
				return false
			}
		}
		return len(re.Sub) > 0
	}
	return false
}

// mayMatchFFFD reports whether re has a part that can match U+FFFD, and so
// also single bytes of invalid UTF-8.
func mayMatchFFFD(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpLiteral:
		for _, r := range re.Rune {
			if r == utf8.RuneError {
				return true
			}
		}
		return false
	case syntax.OpCharClass:
		return classHasFFFD(re.Rune)
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return true
	}
	for _, sub := range re.Sub {
		if mayMatchFFFD(sub) {
			return true
		}
	}
	return false
}

// hasUnencodable reports whether re has a literal rune without a UTF-8
// encoding (a surrogate), as a literal or a one-rune class.
func hasUnencodable(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpLiteral:
		for _, r := range re.Rune {
			if !utf8.ValidRune(r) {
				return true
			}
		}
		return false
	case syntax.OpCharClass:
		return len(re.Rune) == 2 && re.Rune[0] == re.Rune[1] && !utf8.ValidRune(re.Rune[0])
	}
	for _, sub := range re.Sub {
		if hasUnencodable(sub) {
			return true
		}
	}
	return false
}

func unwrapCaptures(re *syntax.Regexp) *syntax.Regexp {
	for re.Op == syntax.OpCapture {
		re = re.Sub[0]
	}
	return re
}

// anchoredStart reports whether every path from the start passes a
// begin-text assertion before consuming input or matching.
func (p *prog) anchoredStart() bool {
	seen := make([]bool, len(p.insts))
	var walk func(pc uint32) bool
	walk = func(pc uint32) bool {
		for {
			if pc == 0 || seen[pc] {
				return true
			}
			seen[pc] = true
			in := &p.insts[pc]
			switch in.op {
			case opEmpty:
				if syntax.EmptyOp(in.arg)&syntax.EmptyBeginText != 0 {
					return true
				}
				pc = in.out
			case opNop, opSave:
				pc = in.out
			case opAlt:
				if !walk(in.out) {
					return false
				}
				pc = in.out1
			default:
				return false
			}
		}
	}
	return walk(p.start)
}

// endAnchored reports whether every match must end at the end of the text.
func endAnchored(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpEndText:
		return true
	case syntax.OpCapture:
		return endAnchored(re.Sub[0])
	case syntax.OpConcat:
		for i := len(re.Sub) - 1; i >= 0; i-- {
			sub := re.Sub[i]
			if sub.Op == syntax.OpEndText {
				return true
			}
			if isZeroWidth(sub) {
				continue
			}
			return endAnchored(sub)
		}
	case syntax.OpAlternate:
		for _, sub := range re.Sub {
			if !endAnchored(sub) {
				return false
			}
		}
		return len(re.Sub) > 0
	}
	return false
}

// anchoredPrefix returns the case-sensitive literal right after a leading
// begin-text assertion.
func anchoredPrefix(re *syntax.Regexp) string {
	re = unwrapCaptures(re)
	if re.Op != syntax.OpConcat {
		return ""
	}
	i := 0
	for i < len(re.Sub) && re.Sub[i].Op != syntax.OpBeginText {
		if !isZeroWidth(re.Sub[i]) {
			return ""
		}
		i++
	}
	for i < len(re.Sub) && isZeroWidth(re.Sub[i]) {
		i++
	}
	if i == len(re.Sub) {
		return ""
	}
	return literalRunes(unwrapCaptures(re.Sub[i]), false)
}

// anchoredSuffix returns the case-sensitive literal right before a trailing
// end-text assertion.
func anchoredSuffix(re *syntax.Regexp) string {
	re = unwrapCaptures(re)
	if re.Op != syntax.OpConcat {
		return ""
	}
	i := len(re.Sub) - 1
	for i >= 0 && re.Sub[i].Op != syntax.OpEndText {
		if !isZeroWidth(re.Sub[i]) {
			return ""
		}
		i--
	}
	for i >= 0 && isZeroWidth(re.Sub[i]) {
		i--
	}
	if i < 0 {
		return ""
	}
	return literalRunes(unwrapCaptures(re.Sub[i]), true)
}

// literalRunes returns a case-sensitive literal's text without U+FFFD
// (which also stands for invalid bytes): the part before the first one, or
// with fromEnd the part after the last one.
func literalRunes(re *syntax.Regexp, fromEnd bool) string {
	if re.Op != syntax.OpLiteral || re.Flags&syntax.FoldCase != 0 {
		return ""
	}
	rs := re.Rune
	for i, r := range rs {
		if !plainRune(r) {
			if fromEnd {
				return literalRunes(&syntax.Regexp{Op: syntax.OpLiteral, Rune: rs[i+1:]}, true)
			}
			rs = rs[:i]
			break
		}
	}
	return string(rs)
}

// runeBytes returns the encoded length shared by every rune in a class, or
// -1 if lengths differ. Classes containing U+FFFD also match single invalid
// bytes, so they have no fixed length.
func runeBytes(class []rune) int {
	if classHasFFFD(class) {
		return -1
	}
	n := -1
	for i := 0; i+1 < len(class); i += 2 {
		a, b := utf8.RuneLen(class[i]), utf8.RuneLen(class[i+1])
		if a < 0 || a != b {
			return -1
		}
		if n >= 0 && a != n {
			return -1
		}
		n = a
	}
	return n
}

// asciiFoldWidth marks the ASCII runes whose case forms, if any, are all
// ASCII: k and s also fold to non-ASCII runes (Kelvin sign, long s).
var asciiFoldWidth = func() (t [utf8.RuneSelf]uint8) {
	for r := range t {
		if o := foldOrbit(rune(r)); o == nil || o[len(o)-1] < utf8.RuneSelf {
			t[r] = 1
		}
	}
	return t
}()

// fixedBytes reports the byte length of every match, if it is fixed.
func fixedBytes(re *syntax.Regexp) (int, bool) {
	switch re.Op {
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText,
		syntax.OpEndText, syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return 0, true
	case syntax.OpLiteral:
		n := 0
		for _, r := range re.Rune {
			if r < utf8.RuneSelf && asciiFoldWidth[r] != 0 {
				n++ // ASCII without case forms outside ASCII
				continue
			}
			if re.Flags&syntax.FoldCase != 0 {
				if cls := foldOrbit(r); cls != nil {
					k := runeBytes(cls)
					if k < 0 {
						return 0, false
					}
					n += k
					continue
				}
			}
			if !plainRune(r) {
				return 0, false
			}
			n += utf8.RuneLen(r)
		}
		return n, true
	case syntax.OpCharClass:
		k := runeBytes(re.Rune)
		return k, k >= 0
	case syntax.OpCapture:
		return fixedBytes(re.Sub[0])
	case syntax.OpConcat:
		n := 0
		for _, s := range re.Sub {
			k, ok := fixedBytes(s)
			if !ok {
				return 0, false
			}
			n += k
		}
		return n, true
	case syntax.OpAlternate:
		n := -1
		for _, s := range re.Sub {
			k, ok := fixedBytes(s)
			if !ok || n >= 0 && k != n {
				return 0, false
			}
			n = k
		}
		return n, n >= 0
	case syntax.OpRepeat:
		if re.Min == re.Max {
			k, ok := fixedBytes(re.Sub[0])
			return k * re.Min, ok
		}
	}
	return 0, false
}

// minBytes returns a lower bound on the byte length of any match.
func minBytes(re *syntax.Regexp) int {
	switch re.Op {
	case syntax.OpLiteral:
		n := 0
		for _, r := range re.Rune {
			if re.Flags&syntax.FoldCase != 0 {
				n++ // folds may change the encoded length
				continue
			}
			if !plainRune(r) {
				n++
				continue
			}
			n += utf8.RuneLen(r)
		}
		return n
	case syntax.OpCharClass, syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return 1
	case syntax.OpCapture, syntax.OpPlus:
		return minBytes(re.Sub[0])
	case syntax.OpRepeat:
		return re.Min * minBytes(re.Sub[0])
	case syntax.OpConcat:
		n := 0
		for _, s := range re.Sub {
			n += minBytes(s)
		}
		return n
	case syntax.OpAlternate:
		n := -1
		for _, s := range re.Sub {
			if k := minBytes(s); n < 0 || k < n {
				n = k
			}
		}
		if n < 0 {
			n = 0
		}
		return n
	}
	return 0
}

// maxBytes returns an upper bound on the byte length of any match, or -1
// if there is none.
func maxBytes(re *syntax.Regexp) int {
	switch re.Op {
	case syntax.OpLiteral:
		n := 0
		for _, r := range re.Rune {
			if r < utf8.RuneSelf && (re.Flags&syntax.FoldCase == 0 || asciiFoldWidth[r] != 0) {
				n++
				continue
			}
			if re.Flags&syntax.FoldCase != 0 {
				if o := foldOrbit(r); o != nil {
					r = o[len(o)-1]
				}
			}
			n += maxRuneLen(r)
		}
		return n
	case syntax.OpCharClass:
		if len(re.Rune) == 0 {
			return 0
		}
		return maxRuneLen(re.Rune[len(re.Rune)-1])
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return utf8.UTFMax
	case syntax.OpCapture, syntax.OpQuest:
		return maxBytes(re.Sub[0])
	case syntax.OpStar, syntax.OpPlus:
		if maxBytes(re.Sub[0]) == 0 {
			return 0
		}
		return -1
	case syntax.OpRepeat:
		m := maxBytes(re.Sub[0])
		switch {
		case m == 0:
			return 0
		case m < 0 || re.Max < 0:
			return -1
		}
		return re.Max * m
	case syntax.OpConcat:
		n := 0
		for _, s := range re.Sub {
			k := maxBytes(s)
			if k < 0 {
				return -1
			}
			n += k
		}
		return n
	case syntax.OpAlternate:
		n := 0
		for _, s := range re.Sub {
			k := maxBytes(s)
			if k < 0 {
				return -1
			}
			n = max(n, k)
		}
		return n
	}
	return 0
}

// maxRuneLen is the longest encoding of a rune up to r. U+FFFD and runes
// without an encoding count as the three bytes of U+FFFD.
func maxRuneLen(r rune) int {
	switch {
	case r < utf8.RuneSelf:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	}
	return 4
}

// plainRune reports whether r matches exactly its UTF-8 encoding. U+FFFD
// also matches each invalid byte, and runes without an encoding
// (surrogates) match nothing, so literal searches stop before either.
func plainRune(r rune) bool {
	return r != utf8.RuneError && utf8.ValidRune(r)
}

// hasSpecialRune reports whether runes include one that is not plain.
func hasSpecialRune(rs []rune) bool {
	for _, r := range rs {
		if !plainRune(r) {
			return true
		}
	}
	return false
}

// wildPlan matches ^P.*S$ (also .+, (?s) and lazy forms) for case-sensitive
// literals P and S: the text must start with P, end with S, and have no
// newline in between unless dot matches it. The byte checks are exact for
// any input: P and S are valid UTF-8 and S starts with a byte that never
// continues a sequence, so the decoder is aligned where S begins, and a
// newline byte is never part of a longer encoding.
type wildPlan struct {
	// Quick reject: the length bounds, and for texts of 8 bytes or more
	// the first and last words masked to the literal bytes they hold.
	minLen, maxLen int
	pa, pm, sa, sm uint64

	prefix, suffix string
	pre, suf       wordLit
	minMid         int  // 0 for .*, 1 for .+
	dotNL          bool // dot matches newline
	exact          bool // ^P$: the text must equal P

	// words is set when every text of at least minLen bytes, minLen >= 8,
	// is decided by the masked words below and a newline test of the
	// whole text: P and S are at most 16 bytes and hold no newline. The
	// second word of P ends at len(P) and the second word of S starts at
	// len(s)-len(S); their masks are zero when one word covers the literal.
	words        bool
	pa2, pm2     uint64
	sa2, sm2     uint64
	pOff2, sOff2 int // offset of the second word of P; of S, from the end
}

func newWildPlan(re *syntax.Regexp) *wildPlan {
	re = unwrapCaptures(re)
	if re.Op != syntax.OpConcat {
		return nil
	}
	subs := re.Sub
	if len(subs) < 3 || subs[0].Op != syntax.OpBeginText || subs[len(subs)-1].Op != syntax.OpEndText {
		return nil
	}
	subs = subs[1 : len(subs)-1]
	lit := func(r *syntax.Regexp) []rune {
		r = unwrapCaptures(r)
		if r.Op != syntax.OpLiteral || r.Flags&syntax.FoldCase != 0 || hasSpecialRune(r.Rune) {
			return nil
		}
		return r.Rune
	}
	pre := lit(subs[0])
	if pre != nil {
		subs = subs[1:]
	}
	var suf []rune
	if len(subs) > 0 {
		if suf = lit(subs[len(subs)-1]); suf != nil {
			subs = subs[:len(subs)-1]
		}
	}
	exact := len(subs) == 0 && suf == nil
	var minMid int
	var dotNL bool
	if !exact {
		if len(subs) != 1 {
			return nil
		}
		mid := unwrapCaptures(subs[0])
		switch mid.Op {
		case syntax.OpStar:
		case syntax.OpPlus:
			minMid = 1
		default:
			return nil
		}
		switch dot := unwrapCaptures(mid.Sub[0]); dot.Op {
		case syntax.OpAnyChar:
			dotNL = true
		case syntax.OpAnyCharNotNL:
		default:
			return nil
		}
	}
	// P and S share one string.
	var buf [64]byte
	b := buf[:0]
	for _, r := range pre {
		b = utf8.AppendRune(b, r)
	}
	n := len(b)
	for _, r := range suf {
		b = utf8.AppendRune(b, r)
	}
	both := string(b)
	w := &wildPlan{prefix: both[:n], suffix: both[n:], minMid: minMid, dotNL: dotNL, exact: exact}
	w.pre, w.suf = newWordLit(w.prefix), newWordLit(w.suffix)
	w.maxLen = int(^uint(0) >> 1)
	for i := 0; i < min(8, len(w.prefix)); i++ {
		w.pa |= uint64(w.prefix[i]) << (8 * i)
		w.pm |= 0xFF << (8 * i)
	}
	for i := 0; i < min(8, len(w.suffix)); i++ {
		w.sa |= uint64(w.suffix[len(w.suffix)-1-i]) << (56 - 8*i)
		w.sm |= 0xFF << (56 - 8*i)
	}
	if exact {
		w.minLen, w.maxLen = len(w.prefix), len(w.prefix)
		return w
	}
	w.minLen = len(w.prefix) + w.minMid + len(w.suffix)
	if w.minLen >= 8 && len(w.prefix) <= 16 && len(w.suffix) <= 16 &&
		strings.IndexByte(w.prefix, '\n') < 0 && strings.IndexByte(w.suffix, '\n') < 0 {
		w.words = true
		w.pOff2, w.sOff2 = 0, 8
		if n := len(w.prefix); n > 8 {
			w.pOff2 = n - 8
			w.pa2, w.pm2 = load64(w.prefix, n-8), ^uint64(0)
		}
		if n := len(w.suffix); n > 8 {
			w.sOff2 = n
			w.sa2, w.sm2 = load64(w.suffix, 0), ^uint64(0)
		}
	}
	return w
}

func (w *wildPlan) match(s string) bool {
	if len(s) < w.minLen || len(s) > w.maxLen ||
		len(s) >= 8 && (load64(s, 0)&w.pm != w.pa || load64(s, len(s)-8)&w.sm != w.sa) {
		return false
	}
	return w.matchAll(s)
}

// matchWild is the match function of planWild patterns, with the quick
// reject of wildPlan.match inlined.
func matchWild(c *core, s string) bool {
	w := c.wild()
	n := len(s)
	if n < w.minLen || n > w.maxLen {
		return false
	}
	if n >= 8 {
		if load64(s, 0)&w.pm != w.pa || load64(s, n-8)&w.sm != w.sa {
			return false
		}
		if w.words {
			if load64(s, w.pOff2)&w.pm2 != w.pa2 || load64(s, n-w.sOff2)&w.sm2 != w.sa2 {
				return false
			}
			return w.dotNL || noNewline(s)
		}
	}
	return w.matchAll(s)
}

func (w *wildPlan) matchAll(s string) bool {
	n := len(s) - len(w.suffix)
	if n-len(w.prefix) < w.minMid || !w.pre.at(s, 0) || !w.suf.at(s, n) {
		return false
	}
	if w.exact {
		return n == len(w.prefix)
	}
	if w.dotNL {
		return true
	}
	mid := s[len(w.prefix):n]
	if len(mid) > 16 {
		return strings.IndexByte(mid, '\n') < 0
	}
	for i := 0; i < len(mid); i++ {
		if mid[i] == '\n' {
			return false
		}
	}
	return true
}

// rejWord is a masked 8-byte check on the first or last word of a text.
type rejWord struct{ or, mask, want uint64 }

func (w *rejWord) ok(x uint64) bool { return (x|w.or)&w.mask == w.want }

// anchoredLit returns the literal right after the leading begin-text (or
// before the trailing end-text) assertion, case-folded or not.
func anchoredLit(re *syntax.Regexp, fromEnd bool) *syntax.Regexp {
	re = unwrapCaptures(re)
	if re.Op != syntax.OpConcat {
		return nil
	}
	subs := re.Sub
	if fromEnd {
		i := len(subs) - 1
		for i >= 0 && subs[i].Op != syntax.OpEndText {
			if !isZeroWidth(subs[i]) {
				return nil
			}
			i--
		}
		for i >= 0 && isZeroWidth(subs[i]) {
			i--
		}
		if i < 0 {
			return nil
		}
		return unwrapCaptures(subs[i])
	}
	i := 0
	for i < len(subs) && subs[i].Op != syntax.OpBeginText {
		if !isZeroWidth(subs[i]) {
			return nil
		}
		i++
	}
	for i < len(subs) && isZeroWidth(subs[i]) {
		i++
	}
	if i == len(subs) {
		return nil
	}
	return unwrapCaptures(subs[i])
}

// litBytes returns the bytes a literal matches with their fold masks
// (0x20 for an ASCII letter matching both cases), up to the first rune it
// cannot express as one byte pattern: U+FFFD (which also stands for
// invalid bytes) or a rune whose case forms are not an ASCII pair. whole
// reports whether it reached the end of the literal.
func litBytes(re *syntax.Regexp) (b, or []byte, whole bool) {
	if re == nil || re.Op != syntax.OpLiteral {
		return nil, nil, false
	}
	fold := re.Flags&syntax.FoldCase != 0
	var buf [utf8.UTFMax]byte
	for _, r := range re.Rune {
		if !plainRune(r) {
			return b, or, false
		}
		if fold && foldOrbit(r) != nil {
			if !asciiFoldOnly(r) {
				return b, or, false
			}
			b = append(b, byte(r)|0x20)
			or = append(or, 0x20)
			continue
		}
		n := utf8.EncodeRune(buf[:], r)
		b = append(b, buf[:n]...)
		or = append(or, make([]byte, n)...)
	}
	return b, or, true
}

func prefixWord(re *syntax.Regexp) rejWord {
	b, or, _ := litBytes(re)
	var w rejWord
	for i := 0; i < len(b) && i < 8; i++ {
		w.or |= uint64(or[i]) << (8 * i)
		w.mask |= 0xFF << (8 * i)
		w.want |= uint64(b[i]) << (8 * i)
	}
	return w
}

// suffixWord checks the literal's last bytes, so it needs the whole
// literal.
func suffixWord(re *syntax.Regexp) rejWord {
	b, or, whole := litBytes(re)
	var w rejWord
	if !whole {
		return w
	}
	for i := 0; i < len(b) && i < 8; i++ {
		k := len(b) - 1 - i
		w.or |= uint64(or[k]) << (56 - 8*i)
		w.mask |= 0xFF << (56 - 8*i)
		w.want |= uint64(b[k]) << (56 - 8*i)
	}
	return w
}
