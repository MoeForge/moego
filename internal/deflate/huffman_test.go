package deflate

import (
	"math/rand"
	"slices"
	"testing"
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
	h.build(freq, lens, limit)
	used := 0
	for i, f := range freq {
		if f != 0 {
			used++
			if lens[i] == 0 {
				t.Fatalf("used symbol %d has no code", i)
			}
		}
		if int(lens[i]) > limit {
			t.Fatalf("symbol %d has length %d over %d", i, lens[i], limit)
		}
	}
	if used >= 2 {
		for i, f := range freq {
			if f == 0 && lens[i] != 0 {
				t.Fatalf("unused symbol %d has length %d", i, lens[i])
			}
		}
	}
	if k := kraft(lens, limit); k != 1<<limit {
		t.Fatalf("Kraft sum %d/%d with %d symbols used", k, 1<<limit, used)
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
	// In an optimal code the lengths are non-decreasing as frequencies
	// fall, so only sorted length vectors need trying.
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
	fib := make([]uint32, numLitLen)
	a, b := uint32(1), uint32(1)
	for i := 0; i < 26; i++ {
		fib[i] = a
		a, b = b, a+b
	}
	for _, limit := range []int{7, 9, 15} {
		checkCode(t, &h, fib[:numCodeLen], maxCodeLenBits)
		checkCode(t, &h, fib, limit)
	}
	rng := rand.New(rand.NewSource(8))
	for iter := 0; iter < 2000; iter++ {
		n := []int{numCodeLen, numDist, numLitLen}[iter%3]
		limit := []int{maxCodeLenBits, maxCodeBits, maxCodeBits}[iter%3]
		freq := make([]uint32, n)
		used := rng.Intn(n + 1)
		for i := 0; i < used; i++ {
			freq[rng.Intn(n)] = uint32(1) << rng.Intn(20) >> rng.Intn(20)
		}
		checkCode(t, &h, freq, limit)
	}
}

func TestHuffmanFewSymbols(t *testing.T) {
	var h huffman
	for n := 1; n <= 5; n++ {
		checkCode(t, &h, make([]uint32, max(n, 2)), 15)
		for i := 0; i < n; i++ {
			freq := make([]uint32, max(n, 2))
			freq[i] = 9
			lens := checkCode(t, &h, freq, 15)
			if lens[i] != 1 {
				t.Fatalf("lone symbol %d of %d gets length %d", i, n, lens[i])
			}
		}
	}
}
