//go:build (amd64 || arm64) && !purego

package ojson

import (
	"encoding/binary"
	"math/bits"
	"math/rand"
	"strings"
	"testing"
	"unsafe"
)

// The reference model of skipAVX2: the same block algorithm in Go.

// blockMasks holds one bit per byte of a 64-byte block, lowest bit first.
type blockMasks struct {
	quote, slash, open, close, space, colon, comma uint64
}

// clip keeps the bits of the first n bytes. Bytes past the end of the input
// count as whitespace and nothing else.
func (m *blockMasks) clip(n int) {
	keep := uint64(1)<<uint(n) - 1
	m.quote &= keep
	m.slash &= keep
	m.open &= keep
	m.close &= keep
	m.colon &= keep
	m.comma &= keep
	m.space |= ^keep
}

// classifyGo returns the masks of the 64 bytes of s from i, eight bytes at a
// time. Past the end of s the bytes count as whitespace.
func classifyGo(s string, i int, m *blockMasks) {
	var w [8]uint64
	n := len(s) - i
	if n >= 64 {
		for k := range w {
			w[k] = load64(s, i+8*k)
		}
	} else {
		var buf [64]byte
		copy(buf[:], s[i:])
		for k := range w {
			w[k] = binary.LittleEndian.Uint64(buf[8*k:])
		}
	}
	*m = blockMasks{}
	for k, x := range w {
		sh := uint(8 * k)
		y := x | lsb*0x20
		m.quote |= gatherHigh(eqBytes(x, '"')) << sh
		m.slash |= gatherHigh(eqBytes(x, '\\')) << sh
		m.open |= gatherHigh(eqBytes(y, '{')) << sh
		m.close |= gatherHigh(eqBytes(y, '}')) << sh
		m.space |= gatherHigh(eqBytes(x, ' ')|eqBytes(x, '\t')|eqBytes(x, '\n')|eqBytes(x, '\r')) << sh
		m.colon |= gatherHigh(eqBytes(x, ':')) << sh
		m.comma |= gatherHigh(eqBytes(x, ',')) << sh
	}
	if n < 64 {
		m.clip(n)
	}
}

// eqBytes sets the high bit of each byte of x that equals c, and no other
// bits.
func eqBytes(x uint64, c byte) uint64 {
	y := x ^ lsb*uint64(c)
	return ^((y&^msb + ^uint64(msb)) | y) & msb
}

// gatherHigh packs the high bits of the eight bytes of m into one byte.
func gatherHigh(m uint64) uint64 { return (m >> 7) * 0x0102040810204080 >> 56 }

// prefixXorRef replaces each bit of x with the XOR of it and all lower bits.
// Applied to the quote bits it marks the bytes inside strings, opening quote
// included and closing quote excluded.
func prefixXorRef(x uint64) uint64 {
	x ^= x << 1
	x ^= x << 2
	x ^= x << 4
	x ^= x << 8
	x ^= x << 16
	x ^= x << 32
	return x
}

// escapedBits returns the bytes that a backslash escapes, other than
// backslashes. carry reports whether the first byte is escaped; the second
// result reports whether the byte after the block is.
func escapedBits(slash uint64, carry bool) (uint64, bool) {
	var esc uint64
	if carry {
		esc = 1
		slash &^= 1
	}
	for slash != 0 {
		s := uint(bits.TrailingZeros64(slash))
		e := s + uint(bits.TrailingZeros64(^(slash >> s)))
		if e >= 64 {
			return esc, (e-s)&1 == 1
		}
		if (e-s)&1 == 1 {
			esc |= 1 << e
		}
		slash &^= 1<<e - 1
	}
	return esc, false
}

func TestClassify(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	alphabet := []byte(" \t\n\r\"\\{}[],:abc019\x00\x7f\x80\xff\x0b\x0c\x1d\x1a\x2a\x5b\x5d\x7b\x7d\x3a")
	for n := 0; n < 20000; n++ {
		size := r.Intn(200)
		b := make([]byte, size)
		for i := range b {
			b[i] = alphabet[r.Intn(len(alphabet))]
		}
		s := string(b)
		for i := 0; i < len(s); i += 1 + r.Intn(40) {
			var got, want blockMasks
			classifyRef(s, i, &want)
			classifyGo(s, i, &got)
			if got != want {
				t.Fatalf("classifyGo(%q, %d) = %+v, want %+v", s, i, got, want)
			}
		}
	}
}

// classifyRef is classifyGo one byte at a time.
func classifyRef(s string, i int, m *blockMasks) {
	*m = blockMasks{}
	for k := 0; k < 64; k++ {
		bit := uint64(1) << k
		if i+k >= len(s) {
			m.space |= bit
			continue
		}
		switch s[i+k] {
		case '"':
			m.quote |= bit
		case '\\':
			m.slash |= bit
		case '{', '[':
			m.open |= bit
		case '}', ']':
			m.close |= bit
		case ' ', '\t', '\n', '\r':
			m.space |= bit
		case ':':
			m.colon |= bit
		case ',':
			m.comma |= bit
		}
	}
}

func TestEscapedBits(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for n := 0; n < 100000; n++ {
		slash := r.Uint64() & r.Uint64()
		if n%3 == 0 {
			slash |= r.Uint64()
		}
		carry := n%5 == 0
		var want uint64
		esc := carry
		for k := 0; k < 64; k++ {
			if esc {
				want |= 1 << k
				esc = false
			} else if slash>>k&1 != 0 {
				esc = true
			}
		}
		// Escaped backslashes are not reported; only the other bytes.
		want &^= slash
		got, out := escapedBits(slash, carry)
		if got&^slash != want || out != esc {
			t.Fatalf("escapedBits(%064b, %v) = %064b, %v, want %064b, %v", slash, carry, got&^slash, out, want, esc)
		}
	}
}

// refStates runs the skipAVX2 block algorithm in Go and returns the state
// before each whole block, stopping when the container closes.
func refStates(s string, o int) []skipState {
	var out []skipState
	st := skipState{pos: o}
	for st.pos+64 <= len(s) {
		out = append(out, st)
		var m blockMasks
		classifyGo(s, st.pos, &m)
		q := m.quote
		var carry bool
		if m.slash != 0 || st.esc != 0 {
			var e uint64
			e, carry = escapedBits(m.slash, st.esc != 0)
			q &^= e
		}
		str := prefixXorRef(q) ^ st.inStr
		op, cl := m.open&^str, m.close&^str
		for cl != 0 {
			low := cl & -cl
			before := op & (low - 1)
			st.depth += bits.OnesCount64(before) - 1
			if st.depth == 0 {
				return out
			}
			op &^= before
			cl ^= low
		}
		st.depth += bits.OnesCount64(op)
		st.inStr = uint64(int64(str) >> 63)
		st.esc = 0
		if carry {
			st.esc = 1
		}
		st.pos += 64
	}
	return out
}

// TestSkipAVX2States ends the input after each whole block and compares the
// depth, string and escape state skipAVX2 reports with refStates.
func TestSkipAVX2States(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for n := 0; n < 20000; n++ {
		doc, _ := randomDoc(r)
		if doc == "" || (doc[0] != '{' && doc[0] != '[') {
			continue
		}
		ref := refStates(doc, 0)
		for k := 1; k < len(ref); k++ {
			st := skipState{}
			res := skipAVX2(unsafe.StringData(doc), 64*k, &st)
			if res != -1 {
				break
			}
			w := ref[k]
			if st.depth != w.depth || st.inStr != w.inStr || st.esc != w.esc || st.pos != w.pos {
				var m blockMasks
				classifyGo(doc, 64*(k-1), &m)
				t.Fatalf("block %d of %q:\nasm %+v\nref %+v\nprev block %q masks %+v", k, doc, st, w, doc[64*(k-1):64*k], m)
			}
		}
	}
}

// pathDotsRef is pathDots one byte at a time.
func pathDotsRef(path string) uint64 {
	n := len(path)
	if n < 16 || n >= 64 {
		return 0
	}
	var dots uint64
	for i := 0; i < n; i++ {
		switch path[i] {
		case '.':
			dots |= 1 << i
		case '\\', '|', '#', '*', '?', '@', '"', '[', '{':
			return 0
		}
	}
	return dots | 1<<n
}

func TestPathDots(t *testing.T) {
	// Every byte value, in each half of the 64-byte block and at the ends
	// of a path, after the page check and without it.
	page := make([]byte, 8192)
	off := 8192 - int(uintptr(unsafe.Pointer(&page[0]))%4096) // a page end
	if off > len(page) {
		off -= 4096
	}
	for c := 0; c < 256; c++ {
		for _, n := range []int{16, 33, 63} {
			for _, at := range []int{0, 5, 31, 32, n - 1} {
				if at >= n {
					continue
				}
				b := []byte(strings.Repeat("a.", n)[:n])
				b[at] = byte(c)
				for _, p := range []string{string(b), unsafe.String(&page[off-n], n)} {
					copy(page[off-n:off], b)
					if got, want := pathDots(p), pathDotsRef(p); got != want {
						t.Fatalf("pathDots(%q) = %b, want %b", p, got, want)
					}
				}
			}
		}
	}
	r := rand.New(rand.NewSource(5))
	alphabet := "abcxyz019_-..........\\|#*?@\" \x00\xff:[]{}!"
	for n := 0; n < 100000; n++ {
		b := make([]byte, r.Intn(70))
		for i := range b {
			b[i] = alphabet[r.Intn(len(alphabet))]
			if r.Intn(16) == 0 {
				b[i] = byte(r.Intn(256))
			}
			if r.Intn(3) > 0 && (b[i] == '\\' || b[i] == '|' || b[i] == '#' || b[i] == '*' || b[i] == '?' || b[i] == '@' || b[i] == '"' || b[i] == '[' || b[i] == '{') {
				b[i] = 'k'
			}
		}
		p := string(b)
		if got, want := pathDots(p), pathDotsRef(p); got != want {
			t.Fatalf("pathDots(%q) = %b, want %b", p, got, want)
		}
	}
}

// TestMembersAVX2 compares memberValue with memberScalar from every
// position after a brace or a comma of valid and malformed documents, for
// keys of the documents and others.
func TestMembersAVX2(t *testing.T) {
	r := rand.New(rand.NewSource(12))
	keys := []string{"", "a", "b", "x", "model", "k\"q", "a\\b", `a`, "messages", "a.b", "long key past thirty-two bytes!!", "é"}
	for n := 0; n < 30000; n++ {
		doc, _ := randomDoc(r)
		if doc == "" {
			continue
		}
		if n%3 == 0 {
			b := []byte(doc)
			b[r.Intn(len(b))] = "{}[],:\"\\ x\t"[r.Intn(11)]
			doc = string(b)
		}
		if n%5 == 0 {
			doc = doc[:r.Intn(len(doc))]
		}
		for i := 0; i < len(doc); i++ {
			if doc[i] != '{' && doc[i] != ',' {
				continue
			}
			key := keys[r.Intn(len(keys))]
			if r.Intn(2) == 0 {
				// A key that the document holds.
				if j := strings.IndexByte(doc[i:], '"'); j >= 0 {
					if e := strings.IndexByte(doc[i+j+1:], '"'); e >= 0 {
						key = doc[i+j+1 : i+j+1+e]
					}
				}
			}
			kw, km := keyWord(key, 0, len(key))
			got := memberValue(doc, i+1, key, kw, km)
			want, _ := memberScalar(doc, i+1, key, kw, km, -1)
			if got != want {
				t.Fatalf("memberValue(%q, %d, %q) = %d, want %d", doc, i+1, key, got, want)
			}
			v, at := membersFast(doc, i+1, key)
			if v == membersSlow {
				v, _ = memberScalar(doc, at, key, kw, km, -1)
			}
			if v != want {
				t.Fatalf("membersAVX2(%q, %d, %q) = %d, want %d", doc, i+1, key, v, want)
			}
		}
	}
}

// TestMembersAVX2NearKeys looks up keys of every length up to 40 in objects
// whose first key differs from them in one byte, at each position, so that
// every word and tail comparison of membersAVX2 meets a mismatch.
func TestMembersAVX2NearKeys(t *testing.T) {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO"
	for n := 0; n <= 40; n++ {
		key := letters[:n]
		kw, km := keyWord(key, 0, len(key))
		for j := -1; j < n; j++ {
			near := []byte(key)
			if j >= 0 {
				near[j] = '_'
			}
			for pad := 0; pad < 16; pad++ {
				head := "{" + strings.Repeat(" ", pad) + `"` + string(near) + `":1`
				for _, doc := range []string{head + "}", head + `,"` + key + `":2}`} {
					want := strings.IndexByte(doc, '1')
					if j >= 0 {
						want = strings.IndexByte(doc, '2')
					}
					if got, _ := memberScalar(doc, 1, key, kw, km, -1); got != want {
						t.Fatalf("memberScalar(%q, 1, %q) = %d, want %d", doc, key, got, want)
					}
					v, at := membersFast(doc, 1, key)
					if v == membersSlow {
						v, _ = memberScalar(doc, at, key, kw, km, -1)
					}
					if v != want {
						t.Fatalf("membersAVX2(%q, 1, %q) = %d, want %d", doc, key, v, want)
					}
				}
			}
		}
	}
}
