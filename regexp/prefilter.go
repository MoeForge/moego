package regexp

import (
	"strings"
	"unicode/utf8"
)

// A prefilter finds candidate positions cheaply: every match starts at a
// position it returns, so the DFA can skip everything in between.
type prefilter struct {
	kind uint8
	b0   byte
	lit  string    // pfLit: needle; pfFold: needle with ASCII letters lowercased
	set  [8]uint32 // pfSet: byte set bitmap
	// Rare bytes of the needle drive the scan: r1 at offset o1 is searched
	// for first, r2 at o2 confirms before the full comparison.
	r1, r2 byte
	o1, o2 int
	// pfFold: whether r1 / r2 are ASCII letters (searched in both cases),
	// and the OR masks that fold them for indexPair.
	f1, f2 bool
	m1, m2 byte
}

const (
	pfByte uint8 = iota + 1 // a single first byte
	pfLit                   // a literal every match starts with
	pfSet                   // a small set of first bytes
	pfFold                  // an ASCII case-insensitive literal
)

// byteRank orders bytes by how common they are in typical text (prose in
// several languages, source code, JSON, base64): lower is rarer.
var byteRank = func() (r [256]uint8) {
	for i := range r {
		r[i] = 40 // controls and other rare bytes
	}
	tiers := []struct {
		rank  uint8
		bytes string
	}{
		{70, "!#$%&*;<>?@[\\]^`|~"},
		{90, "'(){}"},
		{100, "-"},
		{110, "\t\r"},
		// Embedded base64 (images, files) can make up most of a JSON
		// request, so every byte of its alphabet counts as common.
		{120, "0123456789BCDEFGHIJKLMNOPQRSTUVWXYZjkqvxz+/=:"},
		{130, "AbfgpwyK,._\n"},
		{150, "\""}, // JSON
		{150, "cdhlmu"},
		{170, "inorst"},
		{190, "ae"},
		{210, " "},
	}
	for _, t := range tiers {
		for i := 0; i < len(t.bytes); i++ {
			r[t.bytes[i]] = t.rank
		}
	}
	for b := 0x80; b < 0xC0; b++ {
		r[b] = 120 // continuation bytes: common in CJK text
	}
	for b := 0xE4; b <= 0xE9; b++ {
		r[b] = 100 // CJK lead bytes
	}
	return r
}()

// newLitPrefilter returns a prefilter for a case-sensitive literal needle.
func newLitPrefilter(lit string) *prefilter {
	p := new(prefilter)
	p.initLit(lit)
	return p
}

func (p *prefilter) initLit(lit string) {
	if len(lit) == 1 {
		*p = prefilter{kind: pfByte, b0: lit[0]}
		return
	}
	*p = prefilter{kind: pfLit, lit: lit}
	p.o1, p.o2 = rarePair(lit, false)
	p.r1, p.r2 = lit[p.o1], lit[p.o2]
}

// newFoldPrefilter returns a prefilter for an ASCII case-insensitive needle,
// already lowercased, whose letters all fold only to their ASCII case pair.
func newFoldPrefilter(lit string) *prefilter {
	p := &prefilter{kind: pfFold, lit: lit}
	p.o1, p.o2 = rarePair(lit, true)
	p.r1, p.r2 = lit[p.o1], lit[p.o2]
	p.f1, p.f2 = isLower(p.r1), isLower(p.r2)
	if p.f1 {
		p.m1 = 0x20
	}
	if p.f2 {
		p.m2 = 0x20
	}
	return p
}

func isLower(b byte) bool { return 'a' <= b && b <= 'z' }

// rarePair picks the rarest byte, then the rarest byte of a different value
// (bytes that repeat in a needle tend to repeat together in text, as the r's
// of "error"), preferring the farthest on ties: distant bytes are less
// correlated. When folding, a letter ranks as its lowercase form plus half
// its uppercase form.
func rarePair(lit string, fold bool) (int, int) {
	var buf [32]uint16
	ranks := buf[:0]
	if len(lit) > len(buf) {
		ranks = make([]uint16, 0, len(lit))
	}
	for i := 0; i < len(lit); i++ {
		b := lit[i]
		r := uint16(byteRank[b])
		if fold && isLower(b) {
			r += uint16(byteRank[b-'a'+'A']) / 2
		}
		ranks = append(ranks, r)
	}
	o1 := 0
	for i := 1; i < len(lit); i++ {
		if ranks[i] < ranks[o1] {
			o1 = i
		}
	}
	o2 := -1
	for pass := 0; pass < 2 && o2 < 0; pass++ {
		for i := 0; i < len(lit); i++ {
			if i == o1 || pass == 0 && lit[i] == lit[o1] {
				continue
			}
			if o2 < 0 || ranks[i] < ranks[o2] || ranks[i] == ranks[o2] && dist(i, o1) > dist(o2, o1) {
				o2 = i
			}
		}
	}
	if o2 < 0 {
		o2 = o1
	}
	return o1, o2
}

func dist(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

// find returns the first candidate position >= i, or -1.
func (p *prefilter) find(s string, i int) int {
	if i > len(s) {
		return -1
	}
	switch p.kind {
	case pfByte:
		j := strings.IndexByte(s[i:], p.b0)
		if j < 0 {
			return -1
		}
		return i + j
	case pfLit:
		return p.findLit(s, i)
	case pfFold:
		return p.findFold(s, i)
	case pfSet:
		for j := i; j < len(s); j++ {
			c := s[j]
			if p.set[c>>5]&(1<<(c&31)) != 0 {
				return j
			}
		}
		return -1
	}
	return i
}

// findLit searches for the rarest byte of the needle, then confirms the
// second rarest byte and the whole needle.
func (p *prefilter) findLit(s string, i int) int {
	n := len(p.lit)
	last := len(s) - n // last possible start
	if hasPairKernel && last-i >= 32 {
		for i <= last {
			j := indexPair(s[i:last+max(p.o1, p.o2)+1], p.o1, p.o2, p.r1, p.r2, 0, 0)
			if j < 0 {
				return -1
			}
			c := i + j
			if s[c:c+n] == p.lit {
				return c
			}
			i = c + 1
		}
		return -1
	}
	for i <= last {
		j := strings.IndexByte(s[i+p.o1:last+p.o1+1], p.r1)
		if j < 0 {
			return -1
		}
		c := i + j // candidate start
		if s[c+p.o2] == p.r2 && s[c:c+n] == p.lit {
			return c
		}
		i = c + 1
	}
	return -1
}

func lower(b byte) byte {
	if 'A' <= b && b <= 'Z' {
		return b + 'a' - 'A'
	}
	return b
}

// findFold is findLit for ASCII case-insensitive needles: letters driving
// the scan are searched in both cases.
func (p *prefilter) findFold(s string, i int) int {
	n := len(p.lit)
	last := len(s) - n
	if hasPairKernel {
		for i <= last {
			j := indexPair(s[i:last+max(p.o1, p.o2)+1], p.o1, p.o2, p.r1, p.r2, p.m1, p.m2)
			if j < 0 {
				return -1
			}
			c := i + j
			if foldEq(s[c:c+n], p.lit) {
				return c
			}
			i = c + 1
		}
		return -1
	}
	lo, up := p.r1, p.r1
	if p.f1 {
		up = p.r1 - 'a' + 'A'
	}
	nl, nu := -2, -2 // next occurrence (as candidate start) of each case
	for i <= last {
		if nl < i && nl != -1 {
			nl = idx(s, i+p.o1, last+p.o1+1, lo)
			if nl >= 0 {
				nl -= p.o1
			}
		}
		if lo != up && nu < i && nu != -1 {
			nu = idx(s, i+p.o1, last+p.o1+1, up)
			if nu >= 0 {
				nu -= p.o1
			}
		}
		c := nl
		if lo != up && (c < 0 || nu >= 0 && nu < c) {
			c = nu
		}
		if c < 0 {
			return -1
		}
		if lower(s[c+p.o2]) == p.r2 && foldEq(s[c:c+n], p.lit) {
			return c
		}
		i = c + 1
	}
	return -1
}

func idx(s string, from, to int, b byte) int {
	if from >= to {
		return -1
	}
	j := strings.IndexByte(s[from:to], b)
	if j < 0 {
		return -1
	}
	return from + j
}

// foldEq compares s with an ASCII-lowercased needle, ignoring ASCII case.
func foldEq(s, lit string) bool {
	for k := 0; k < len(lit); k++ {
		if lower(s[k]) != lit[k] {
			return false
		}
	}
	return true
}

// asciiFoldOnly reports whether r's case-folding orbit is exactly its ASCII
// lower and upper case, so byte-level folding finds every match.
func asciiFoldOnly(r rune) bool {
	if r >= utf8.RuneSelf {
		return false
	}
	o := foldOrbit(r)
	if o == nil {
		return true
	}
	return len(o) == 4 && o[1] < utf8.RuneSelf && o[3] < utf8.RuneSelf && o[0] == o[1] && o[2] == o[3]
}
