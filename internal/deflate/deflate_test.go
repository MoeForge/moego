package deflate

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"fmt"
	"io"
	"math/rand"
	"os"
	"testing"
)

// testInputs returns named inputs that cover the encoder's edge cases.
func testInputs() []struct {
	name string
	data []byte
} {
	rng := rand.New(rand.NewSource(1))
	random := func(n int) []byte {
		b := make([]byte, n)
		rng.Read(b)
		return b
	}
	var out []struct {
		name string
		data []byte
	}
	add := func(name string, data []byte) {
		out = append(out, struct {
			name string
			data []byte
		}{name, data})
	}
	add("empty", nil)
	for _, n := range []int{1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17} {
		add(fmt.Sprintf("random%d", n), random(n))
		add(fmt.Sprintf("zero%d", n), make([]byte, n))
	}
	add("zero100k", make([]byte, 100_000))
	add("zero1m+", make([]byte, maxChunk+windowSize+12345))
	add("random100k", random(100_000))
	add("random3m", random(3<<20+17))
	add("text", text(rng, 300_000))
	add("text3m", text(rng, 3<<20))
	for _, period := range []int{32767, 32768, 32769, 40000} {
		block := random(period)
		add(fmt.Sprintf("period%d", period), bytes.Repeat(block, 5))
	}
	// Runs separated by single bytes, so match lengths land on 3, 4, 257,
	// 258, 259 and their neighbors.
	for _, run := range []int{3, 4, 5, 256, 257, 258, 259, 260, 515, 516, 517} {
		var b []byte
		for i := 0; i < 200; i++ {
			b = append(b, bytes.Repeat([]byte{'a'}, run)...)
			b = append(b, byte('b'+i%20))
		}
		add(fmt.Sprintf("runs%d", run), b)
	}
	short := random(4)
	var b []byte
	for i := 0; i < 5000; i++ {
		b = append(b, short[:3]...)
		b = append(b, byte(i), byte(i>>8))
	}
	add("matches3", b)
	add("rows", filteredRows(rng, 4, 333, 200))
	add("rows-rgb", filteredRows(rng, 3, 1001, 700))
	add("rows-gray", filteredRows(rng, 1, 64, 30))
	// Few symbols: one literal only, then two.
	add("one-symbol", bytes.Repeat([]byte{7}, 70_000))
	add("two-symbols", func() []byte {
		b := make([]byte, 70_000)
		for i := range b {
			b[i] = byte(rng.Intn(2))
		}
		return b
	}())
	// Skewed frequencies push code lengths past 15 bits.
	add("skewed", func() []byte {
		var b []byte
		f := 1
		for c := 0; c < 30; c++ {
			for i := 0; i < f && len(b) < 2_000_000; i++ {
				b = append(b, byte(c))
			}
			f = f*3/2 + 1
		}
		rng.Shuffle(len(b), func(i, j int) { b[i], b[j] = b[j], b[i] })
		return b
	}())
	return out
}

var words = []string{"the", "quick", "brown", "fox", "jumps", "over", "lazy", "dog", "moego", "deflate", "png", "image", "pixel", "row", "filter", "Huffman", "code", "length", "distance", "\n", ", ", ". "}

func text(rng *rand.Rand, n int) []byte {
	b := make([]byte, 0, n+16)
	for len(b) < n {
		b = append(b, words[rng.Intn(len(words))]...)
		b = append(b, ' ')
	}
	return b[:n]
}

// filteredRows returns rows like a PNG encoder feeds to zlib: a smooth
// image with noise, each row Sub or Up filtered behind its filter byte.
func filteredRows(rng *rand.Rand, bpp, width, height int) []byte {
	stride := bpp * width
	prev := make([]byte, stride)
	cur := make([]byte, stride)
	var out []byte
	for y := 0; y < height; y++ {
		for x := 0; x < stride; x++ {
			v := (x/bpp)*3 + y*2 + x%bpp*40
			if rng.Intn(8) == 0 {
				v += rng.Intn(5)
			}
			cur[x] = byte(v)
		}
		f := byte(y % 3)
		out = append(out, f)
		for x := 0; x < stride; x++ {
			switch f {
			case 0:
				out = append(out, cur[x])
			case 1:
				var a byte
				if x >= bpp {
					a = cur[x-bpp]
				}
				out = append(out, cur[x]-a)
			default:
				out = append(out, cur[x]-prev[x])
			}
		}
		prev, cur = cur, prev
	}
	return out
}

func compress(t testing.TB, w *Writer, level int, data []byte, split func(int) int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if w == nil {
		w = NewWriter(&buf, level)
	} else {
		w.Reset(&buf, level)
	}
	for p := data; len(p) > 0; {
		n := len(p)
		if split != nil {
			n = min(n, split(len(data)-len(p)))
		}
		if k, err := w.Write(p[:n]); k != n || err != nil {
			t.Fatalf("Write = %d, %v", k, err)
		}
		p = p[n:]
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// checkDecode inflates z with compress/zlib and its payload with
// compress/flate and compares both with want.
func checkDecode(t testing.TB, z, want []byte) {
	t.Helper()
	r, err := zlib.NewReader(bytes.NewReader(z))
	if err != nil {
		t.Fatalf("zlib header: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("zlib: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("zlib output differs: %d bytes, want %d", len(got), len(want))
	}
	if len(z) < 6 {
		t.Fatalf("stream of %d bytes", len(z))
	}
	got, err = io.ReadAll(flate.NewReader(bytes.NewReader(z[2 : len(z)-4])))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("flate: %v", err)
	}
}

func TestMain(m *testing.M) {
	verifyMatches = true
	os.Exit(m.Run())
}

func TestRoundTrip(t *testing.T) {
	inputs := testInputs()
	for level := -1; level <= 9; level++ {
		for i, in := range inputs {
			if testing.Short() && len(in.data) > 1<<20 && level != 1 {
				continue
			}
			if raceEnabled {
				// The race detector slows encoding about ten times: each
				// level gets a quarter of the inputs and each input a
				// quarter of the levels, inputs over 1 MB one level.
				k := 4
				if len(in.data) > 1<<20 {
					k = 11
				}
				if (level+1+i)%k != 0 {
					continue
				}
			}
			t.Run(fmt.Sprintf("%d/%s", level, in.name), func(t *testing.T) {
				checkDecode(t, compress(t, nil, level, in.data, nil), in.data)
			})
		}
	}
}

func TestSplitWrites(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	data := filteredRows(rng, 4, 500, 600)
	if !raceEnabled {
		data = append(data, text(rng, 1<<20)...)
	}
	names := []string{"1", "7", "row", "random"}
	splits := map[string]func(int) int{
		"1":      func(int) int { return 1 },
		"7":      func(int) int { return 7 },
		"row":    func(int) int { return 2001 },
		"random": func(int) int { return 1 + rng.Intn(70_000) },
	}
	for level := 0; level <= 9; level++ {
		whole := compress(t, nil, level, data, nil)
		for si, name := range names {
			split := splits[name]
			if name == "1" && (raceEnabled || testing.Short()) {
				continue
			}
			if raceEnabled && (level+si)%3 != 0 {
				// One of the other three splits per level, each split at
				// three or four levels.
				continue
			}
			t.Run(fmt.Sprintf("%d/%s", level, name), func(t *testing.T) {
				z := compress(t, nil, level, data, split)
				checkDecode(t, z, data)
				// Writes are buffered until the window is full, so the
				// output does not depend on how the input is cut.
				if !bytes.Equal(z, whole) {
					t.Fatal("output depends on the Write sizes")
				}
			})
		}
	}
}

func TestHint(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for _, bpp := range []int{1, 2, 3, 4, 6, 8} {
		data := filteredRows(rng, bpp, 97, 300)
		for _, level := range []int{1, 2, 6, 9} {
			var buf bytes.Buffer
			w := NewWriter(&buf, level)
			w.SetHint(bpp, 97*bpp+1)
			w.Write(data)
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			checkDecode(t, buf.Bytes(), data)
		}
	}
}

func TestDeterministicReset(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	inputs := [][]byte{filteredRows(rng, 3, 400, 400), text(rng, 2<<20), nil, {1}}
	w := NewWriter(io.Discard, 1)
	for level := 0; level <= 9; level++ {
		for i, data := range inputs {
			if raceEnabled && i == 1 && level != 1 && level != 6 {
				// The 2 MB text at levels 1 and 6 only; the rows cover
				// every level.
				continue
			}
			fresh := compress(t, nil, level, data, nil)
			again := compress(t, nil, level, data, nil)
			reused := compress(t, w, level, data, nil)
			if !bytes.Equal(fresh, again) || !bytes.Equal(fresh, reused) {
				t.Fatalf("level %d input %d: output differs between runs or after Reset", level, i)
			}
		}
	}
}

func TestLevels(t *testing.T) {
	for _, level := range []int{-2, 10, 100} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("level %d did not panic", level)
				}
			}()
			NewWriter(io.Discard, level)
		}()
	}
}

type failWriter struct{ after int }

func (f *failWriter) Write(p []byte) (int, error) {
	if f.after < len(p) {
		return 0, io.ErrShortWrite
	}
	f.after -= len(p)
	return len(p), nil
}

func TestWriteErrors(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	data := make([]byte, 3<<20)
	rng.Read(data)
	for _, after := range []int{0, 100, 1 << 20} {
		w := NewWriter(&failWriter{after}, 1)
		_, err := w.Write(data)
		if err == nil {
			err = w.Close()
		}
		if err != io.ErrShortWrite {
			t.Fatalf("after %d: error %v", after, err)
		}
		if _, err := w.Write(data[:10]); err != io.ErrShortWrite {
			t.Fatalf("error not sticky: %v", err)
		}
		if err := w.Close(); err != io.ErrShortWrite {
			t.Fatalf("Close error not sticky: %v", err)
		}
	}
	w := NewWriter(io.Discard, 6)
	w.Close()
	if _, err := w.Write([]byte{1}); err == nil {
		t.Fatal("Write after Close succeeded")
	}
}

func TestAllocs(t *testing.T) {
	if raceEnabled || checkptrEnabled() {
		t.Skip("allocations differ under the race detector and checkptr")
	}
	rng := rand.New(rand.NewSource(6))
	data := append(filteredRows(rng, 4, 700, 500), text(rng, 1<<20)...)
	for level := 0; level <= 9; level++ {
		w := NewWriter(io.Discard, level)
		run := func() {
			w.Reset(io.Discard, level)
			w.SetHint(4, 2801)
			w.Write(data)
			w.Close()
		}
		run()
		if n := testing.AllocsPerRun(3, run); n != 0 {
			t.Errorf("level %d: %v allocations per warm encode", level, n)
		}
	}
}
