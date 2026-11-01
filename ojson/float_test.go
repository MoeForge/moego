package ojson

import (
	"math"
	"math/rand"
	"strconv"
	"testing"
)

func checkFloat(t *testing.T, f float64) {
	t.Helper()
	if got, want := string(appendFloat(nil, f)), strconv.FormatFloat(f, 'f', -1, 64); got != want {
		t.Fatalf("appendFloat(%v) [%#x] = %s, want %s", f, math.Float64bits(f), got, want)
	}
}

// TestAppendFloat compares appendFloat with strconv on short decimals of
// every size the short route takes and around its limits, on random bit
// patterns, and on the special values.
func TestAppendFloat(t *testing.T) {
	for _, f := range []float64{0, math.Copysign(0, -1), math.NaN(), math.Inf(1), math.Inf(-1),
		math.MaxFloat64, math.SmallestNonzeroFloat64, 1e15, 1e15 - 1, 1e15 - 0.5, 999999999999999.9,
		1e-8, 1e-9, 1.5e-8, 0.1, 0.2, 0.7, 4096, 1 << 53, 1<<53 - 1, 1<<50 + 0.25, 123456.78901234,
		float64(float32(0.1)), 5e-324, 2.2250738585072014e-308} {
		checkFloat(t, f)
		checkFloat(t, -f)
	}
	r := rand.New(rand.NewSource(11))
	for k := 0; k <= 12; k++ {
		for n := 0; n < 200000; n++ {
			var i int64
			switch n % 4 {
			case 0:
				i = r.Int63n(1000)
			case 1:
				i = r.Int63n(1e9)
			case 2:
				i = r.Int63n(1e15)
			default:
				i = r.Int63n(1e18)
			}
			s := strconv.FormatInt(i, 10)
			if k > 0 {
				for len(s) <= k {
					s = "0" + s
				}
				s = s[:len(s)-k] + "." + s[len(s)-k:]
			}
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				t.Fatal(err)
			}
			checkFloat(t, f)
			checkFloat(t, -f)
			checkFloat(t, math.Nextafter(f, 2*f+1))
			checkFloat(t, math.Nextafter(f, -1))
		}
	}
	for n := 0; n < 2000000; n++ {
		checkFloat(t, math.Float64frombits(r.Uint64()))
		checkFloat(t, r.Float64()*math.Pow(10, float64(r.Intn(24)-9)))
	}
}
