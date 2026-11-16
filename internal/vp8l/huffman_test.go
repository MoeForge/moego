package vp8l

import (
	"math/rand"
	"slices"
	"testing"

	"github.com/MoeForge/moego/internal/vp8l/vp8ltest"
)

// kraft returns the Kraft sum of lens scaled by 1<<limit.
func kraft(lens []uint8, limit int) int {
	s := 0
	for _, l := range lens {
		if l != 0 {
			s += 1 << (limit - int(l))
		}
	}
	return s
}

func codeCost(freq []uint32, lens []uint8) int {
	c := 0
	for i, f := range freq {
		c += int(f) * int(lens[i])
	}
	return c
}

func checkCode(t *testing.T, h *huffman, freq []uint32, limit int) []uint8 {
	t.Helper()
	lens := make([]uint8, len(freq))
	used := h.build(freq, lens, limit)
	n := 0
	for i, f := range freq {
		if f != 0 {
			n++
		}
		if int(lens[i]) > limit {
			t.Fatalf("symbol %d has length %d over %d", i, lens[i], limit)
		}
		if used >= 2 && (f != 0) != (lens[i] != 0) {
			t.Fatalf("symbol %d: frequency %d, length %d", i, f, lens[i])
		}
	}
	if n != used {
		t.Fatalf("build counts %d used symbols, there are %d", used, n)
	}
	if used >= 2 {
		if k := kraft(lens, limit); k != 1<<limit {
			t.Fatalf("Kraft sum %d/%d with %d symbols used", k, 1<<limit, used)
		}
	} else if slices.ContainsFunc(lens, func(l uint8) bool { return l != 0 }) {
		t.Fatalf("one-leaf code got lengths %v", lens)
	}
	return lens
}

// bruteForce returns the least cost of a prefix code with lengths at most
// limit for the nonzero frequencies, by trying every complete code.
func bruteForce(freq []uint32, limit int) int {
	var f []int
	for _, v := range freq {
		if v != 0 {
			f = append(f, int(v))
		}
	}
	slices.Sort(f)
	slices.Reverse(f)
	best := -1
	lens := make([]int, len(f))
	var rec func(i, minLen, room int)
	rec = func(i, minLen, room int) {
		if i == len(f) {
			if room == 0 {
				c := 0
				for j := range f {
					c += f[j] * lens[j]
				}
				if best < 0 || c < best {
					best = c
				}
			}
			return
		}
		for l := minLen; l <= limit; l++ {
			w := 1 << (limit - l)
			if w > room {
				continue
			}
			lens[i] = l
			rec(i+1, l, room-w)
		}
	}
	rec(0, 1, 1<<limit)
	return best
}

func TestHuffmanBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var h huffman
	for iter := 0; iter < 3000; iter++ {
		n := 2 + rng.Intn(9)
		limit := 4
		if n > 8 {
			limit = 5
		}
		freq := make([]uint32, n+rng.Intn(3))
		for i := 0; i < n; i++ {
			switch rng.Intn(3) {
			case 0:
				freq[i] = uint32(1 + rng.Intn(4))
			case 1:
				freq[i] = uint32(1 + rng.Intn(1000))
			default:
				freq[i] = uint32(1) << rng.Intn(16)
			}
		}
		rng.Shuffle(len(freq), func(i, j int) { freq[i], freq[j] = freq[j], freq[i] })
		lens := checkCode(t, &h, freq, limit)
		if got, want := codeCost(freq, lens), bruteForce(freq, limit); got != want {
			t.Fatalf("freq %v limit %d: cost %d, optimal %d (lens %v)", freq, limit, got, want, lens)
		}
	}
}

func TestHuffmanLimits(t *testing.T) {
	var h huffman
	// Fibonacci frequencies give the deepest unlimited trees.
	fib := make([]uint32, maxGreen)
	a, b := uint32(1), uint32(1)
	for i := 0; i < 40; i++ {
		fib[i] = a
		a, b = b, a+b
	}
	checkCode(t, &h, fib[:numCodeLenSyms], maxCodeLenLen)
	checkCode(t, &h, fib, maxCodeLen)
	rng := rand.New(rand.NewSource(8))
	for iter := 0; iter < 2000; iter++ {
		n := []int{numCodeLenSyms, numDistCodes, numLiteral, maxGreen}[iter%4]
		limit := maxCodeLen
		if n == numCodeLenSyms {
			limit = maxCodeLenLen
		}
		freq := make([]uint32, n)
		used := rng.Intn(n + 1)
		for i := 0; i < used; i++ {
			freq[rng.Intn(n)] = uint32(1) << rng.Intn(24) >> rng.Intn(24)
		}
		checkCode(t, &h, freq, limit)
	}
}

func TestHuffmanFewSymbols(t *testing.T) {
	var h huffman
	for n := 1; n <= 5; n++ {
		checkCode(t, &h, make([]uint32, n), 15)
		for i := 0; i < n; i++ {
			freq := make([]uint32, n)
			freq[i] = 9
			checkCode(t, &h, freq, 15)
		}
	}
}

// TestHeaders writes prefix codes of many shapes as the green code of a
// stream whose pixels use exactly those symbols, so the decoder checks the
// header and the code.
func TestHeaders(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	var e Encoder
	for iter := 0; iter < 400; iter++ {
		w := 1 + rng.Intn(80)
		h := 1 + rng.Intn(20)
		px := make([]uint32, w*h)
		// A few greens, spread or clustered, with skewed counts.
		k := 1 + rng.Intn(1+[]int{2, 8, 40, 255}[iter%4])
		base := rng.Intn(256)
		greens := make([]uint32, k)
		for i := range greens {
			greens[i] = uint32(base+i*(1+rng.Intn(3))) & 0xff
		}
		for i := range px {
			j := rng.Intn(k)
			if rng.Intn(2) == 0 {
				j = rng.Intn(1 + j)
			}
			px[i] = 0xff000000 | greens[j]<<8
		}
		checkRoundTrip(t, &e, rng.Intn(10), vp8ltest.Case{Name: "headers", W: w, H: h, ARGB: px})
	}
}

// TestCodeLengthCoding checks the header writer against the tokens it
// claims to write, on random length vectors.
func TestCodeLengthCoding(t *testing.T) {
	rng := rand.New(rand.NewSource(12))
	var hw headerWriter
	for iter := 0; iter < 3000; iter++ {
		n := []int{40, 256, 280, 280 + 1<<rng.Intn(12)}[iter%4]
		lens := make([]uint8, n)
		for i := 0; i < n; {
			r := 1 + rng.Intn(40)
			l := uint8(0)
			if rng.Intn(3) != 0 {
				l = uint8(rng.Intn(16))
			}
			for ; r > 0 && i < n; r-- {
				lens[i] = l
				i++
			}
		}
		hw.tokenize(lens)
		// Expand the tokens back.
		var got []uint8
		prev := uint8(8)
		for j, tk := range hw.tokens[:hw.ntok] {
			s, x := tk&31, int(tk>>5)
			switch {
			case s < 16:
				got = append(got, uint8(s))
				if s != 0 {
					prev = uint8(s)
				}
			case s == 16:
				if x > 3 {
					t.Fatalf("token %d: 16 with extra %d", j, x)
				}
				for r := 0; r < 3+x; r++ {
					got = append(got, prev)
				}
			case s == 17:
				if x > 7 {
					t.Fatalf("token %d: 17 with extra %d", j, x)
				}
				got = append(got, make([]uint8, 3+x)...)
			case s == 18:
				if x > 127 {
					t.Fatalf("token %d: 18 with extra %d", j, x)
				}
				got = append(got, make([]uint8, 11+x)...)
			default:
				t.Fatalf("token %d: symbol %d", j, s)
			}
			if j+1 == hw.end {
				last := len(lens) - 1
				for last >= 0 && lens[last] == 0 {
					last--
				}
				if len(got) <= last {
					t.Fatalf("end token %d covers %d lengths, the last nonzero is %d", hw.end, len(got), last)
				}
			}
		}
		if !slices.Equal(got, lens) {
			t.Fatalf("lengths %v\ntokens give %v", lens, got)
		}
	}
}
