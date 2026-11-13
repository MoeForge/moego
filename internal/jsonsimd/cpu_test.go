package jsonsimd

import (
	"os"
	"testing"
)

// TestMain runs the tests as on a CPU without the features that the
// environment variable MOEGO_TEST_CPU names, such as "avx2" or "avx,aes":
// see LimitCPU.
func TestMain(m *testing.M) {
	undo := LimitCPU(os.Getenv("MOEGO_TEST_CPU"))
	code := m.Run()
	undo()
	os.Exit(code)
}
