package regexp

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

func TestIndexPair(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	buf := make([]byte, 300)
	for iter := 0; iter < randomIterations(20000); iter++ {
		n := r.IntN(len(buf))
		for i := range buf[:n] {
			buf[i] = "abAB-x"[r.IntN(6)]
		}
		s := string(buf[:n])
		off := r.IntN(n + 1)
		s = s[off:]
		o1, o2 := r.IntN(40), r.IntN(40)
		r1, r2 := "abAB-x"[r.IntN(6)], "abAB-x"[r.IntN(6)]
		var m1, m2 byte
		if r.IntN(2) == 0 && isLower(r1) {
			m1 = 0x20
		}
		if r.IntN(2) == 0 && isLower(r2) {
			m2 = 0x20
		}
		if len(s) <= max(o1, o2) {
			continue
		}
		got := indexPair(s, o1, o2, r1, r2, m1, m2)
		want := -1
		for i := 0; i+max(o1, o2) < len(s); i++ {
			if s[i+o1]|m1 == r1 && s[i+o2]|m2 == r2 {
				want = i
				break
			}
		}
		if got != want {
			t.Fatalf("indexPair(len %d, o %d %d, %q %q %#x %#x) = %d, want %d", len(s), o1, o2, r1, r2, m1, m2, got, want)
		}
	}
}

func BenchmarkIndexPair(b *testing.B) {
	for _, n := range []int{64, 214, 4096, 1 << 20} {
		buf := make([]byte, n)
		for i := range buf {
			buf[i] = 'a' + byte(i%7)
		}
		buf[n-3] = ':'
		buf[n-13] = '"'
		s := string(buf)
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			b.SetBytes(int64(n))
			for i := 0; i < b.N; i++ {
				indexPair(s, 10, 0, ':', '"', 0, 0)
			}
		})
	}
}

func TestWordLit(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 8))
	for iter := 0; iter < randomIterations(50000); iter++ {
		n := r.IntN(20)
		lit := make([]byte, n)
		for i := range lit {
			lit[i] = "ab"[r.IntN(2)]
		}
		w := newWordLit(string(lit))
		m := n + r.IntN(20)
		buf := make([]byte, m)
		for i := range buf {
			buf[i] = "ab"[r.IntN(2)]
		}
		s := string(buf)
		o := r.IntN(m - n + 1)
		if got, want := w.at(s, o), s[o:o+n] == string(lit); got != want {
			t.Fatalf("at(%q, %d) with %q = %v", s, o, lit, got)
		}
	}
}

func TestSpan(t *testing.T) {
	r := rand.New(rand.NewPCG(11, 12))
	alpha := "ab =\n\x80\xffzZ09+/"
	for iter := 0; iter < randomIterations(20000); iter++ {
		var in [256]bool
		for k := r.IntN(6); k > 0; k-- {
			in[alpha[r.IntN(len(alpha))]&0x7F] = true
		}
		lo, hi := nibbleTables(&in)
		n := r.IntN(200)
		buf := make([]byte, n)
		for i := range buf {
			buf[i] = alpha[r.IntN(len(alpha))]
		}
		s := string(buf)
		for _, member := range []bool{true, false} {
			if got, want := span(s, &lo, &hi, &in, member), spanScalar(s, &in, member); got != want {
				t.Fatalf("span(%q, member=%v) = %d, want %d", s, member, got, want)
			}
		}
	}
}

func BenchmarkSpan(b *testing.B) {
	var in [256]bool
	for _, c := range "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/" {
		in[c] = true
	}
	lo, hi := nibbleTables(&in)
	buf := make([]byte, 1<<20)
	for i := range buf {
		buf[i] = "ABCxyz019+/"[i%11]
	}
	s := string(buf)
	b.SetBytes(int64(len(s)))
	for i := 0; i < b.N; i++ {
		span(s, &lo, &hi, &in, true)
	}
}

func TestClassMasksRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(51, 61))
	for iter := 0; iter < randomIterations(10000); iter++ {
		var in [256]bool
		for b := 0; b < 128; b++ {
			in[b] = rng.IntN(2) == 0
		}
		lo, hi := nibbleTables(&in)
		buf := make([]byte, 64*rng.IntN(17))
		for i := range buf {
			buf[i] = byte(rng.Uint32())
		}
		s := string(buf)
		got, want := make([]uint64, len(buf)/64), make([]uint64, len(buf)/64)
		classMasks(s, &lo, &hi, &in, got)
		classMasksScalar(s, &in, want)
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("iter=%d block=%d: got=%x want=%x", iter, i, got[i], want[i])
			}
		}
	}
}
