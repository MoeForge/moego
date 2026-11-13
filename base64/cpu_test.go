package base64

import (
	"os"
	"strings"
	"testing"
)

// TestMain runs the tests as on a CPU without the features that the
// environment variable MOEGO_TEST_CPU names, a list of "avx2", "avx" and
// "aes" separated by commas, as jsonsimd.LimitCPU takes: without AVX2 or
// AVX, the AVX2 kernels do not run.
func TestMain(m *testing.M) {
	undo := limitCPU(os.Getenv("MOEGO_TEST_CPU"))
	code := m.Run()
	undo()
	os.Exit(code)
}

// dropsAVX2 reports whether drop names AVX2 or AVX, and panics at a name
// it does not know.
func dropsAVX2(drop string) bool {
	avx2 := false
	for _, f := range strings.Split(drop, ",") {
		switch strings.TrimSpace(f) {
		case "", "aes":
		case "avx2", "avx":
			avx2 = true
		default:
			panic("base64: MOEGO_TEST_CPU: unknown feature " + f)
		}
	}
	return avx2
}
