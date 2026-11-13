//go:build amd64 && !purego

package jsonsimd

import (
	"fmt"
	"math/rand"
	"testing"
	"unsafe"
)

var hashNames = [...]string{hashNone: "maphash", hashSSE: "SSE", hashVEX: "VEX"}

// TestHashPick checks the kernel picked for each CPU, and that LimitCPU
// picks as for a CPU without the features it drops, from those in use.
func TestHashPick(t *testing.T) {
	t.Logf("AES %v, AVX %v, in use %v, %v: kernel %s", cpuAES, cpuAVX, useAES, useAVX, hashNames[hashKernel])
	for _, c := range []struct {
		aes, avx bool
		want     int
	}{{false, false, hashNone}, {false, true, hashNone}, {true, false, hashSSE}, {true, true, hashVEX}} {
		if got := pickHash(c.aes, c.avx); got != c.want {
			t.Errorf("pickHash(%v, %v) = %s, want %s", c.aes, c.avx, hashNames[got], hashNames[c.want])
		}
	}
	if hashKernel != pickHash(useAES, useAVX) {
		t.Fatalf("kernel %s with AES %v, AVX %v in use", hashNames[hashKernel], useAES, useAVX)
	}
	for _, c := range []struct {
		drop string
		aes  bool
		avx  bool
	}{{"", useAES, useAVX}, {"avx2", useAES, useAVX}, {"avx", useAES, false}, {"aes", false, useAVX}, {"avx,aes", false, false}} {
		undo := LimitCPU(c.drop)
		got, fast := hashKernel, FastSum()
		undo()
		if want := pickHash(c.aes, c.avx); got != want || fast != (want != hashNone) {
			t.Errorf("LimitCPU(%q): kernel %s, FastSum %v; want %s", c.drop, hashNames[got], fast, hashNames[want])
		}
	}
	if hashKernel != pickHash(useAES, useAVX) {
		t.Fatal("undo did not restore the kernel")
	}
}

// TestHashKernels checks hashAES and hashAESSSE, where the CPU runs them,
// against refHashAES, for random keys, lengths around multiples of 128 and
// every alignment of the bytes.
func TestHashKernels(t *testing.T) {
	if !cpuAES {
		t.Skip("no AES")
	}
	if !cpuAVX {
		t.Log("no AVX: hashAES not run")
	}
	r := rand.New(rand.NewSource(17))
	lens := []int{128, 129, 255, 256, 257, 383, 384, 1023, 1024, 1025, 4095, 4096, 4097, 4224, 4999, 64 << 10, 64<<10 + 77}
	for range 12 {
		lens = append(lens, 128+r.Intn(5000))
	}
	keys := 4
	if raceEnabled {
		keys, lens = 1, lens[:10]
	}
	buf := make([]byte, 64<<10+128+16)
	for range keys {
		h := NewHashKey()
		for _, n := range lens {
			for _, fill := range []string{"random", "zero", "ff"} {
				b := make([]byte, n)
				switch fill {
				case "random":
					r.Read(b)
				case "ff":
					for i := range b {
						b[i] = 0xff
					}
				}
				want := refHashAES(&h.k, b)
				for off := range 16 {
					c := buf[off : off+n]
					copy(c, b)
					m := n &^ 127
					var tail [128]byte
					copy(tail[:], c[m:])
					p := unsafe.SliceData(c)
					if got := hashAESSSE(&h.k, p, m, &tail, n); got != want {
						t.Fatalf("n %d, %s, offset %d: hashAESSSE gives %#x, want %#x", n, fill, off, got, want)
					}
					if cpuAVX {
						if got := hashAES(&h.k, p, m, &tail, n); got != want {
							t.Fatalf("n %d, %s, offset %d: hashAES gives %#x, want %#x", n, fill, off, got, want)
						}
					}
				}
			}
		}
	}
}

func BenchmarkHashSum(b *testing.B) {
	h := NewHashKey()
	buf := make([]byte, 256<<10)
	rand.New(rand.NewSource(1)).Read(buf)
	for _, k := range []struct {
		kernel int
		drop   string
	}{{hashVEX, ""}, {hashSSE, "avx"}, {hashNone, "aes"}} {
		for _, n := range []int{1 << 10, 16 << 10, 256 << 10} {
			b.Run(fmt.Sprintf("%s/%dKiB", hashNames[k.kernel], n>>10), func(b *testing.B) {
				undo := LimitCPU(k.drop)
				defer undo()
				if hashKernel != k.kernel {
					b.Skip("the CPU does not run it")
				}
				b.SetBytes(int64(n))
				for range b.N {
					h.Sum(buf[:n])
				}
			})
		}
	}
}
