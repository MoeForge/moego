package ojson

import (
	"os"
	"testing"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// TestMain runs the tests as on a CPU without the features that the
// environment variable MOEGO_TEST_CPU names, such as "avx2" or "avx,aes":
// see jsonsimd.LimitCPU.
func TestMain(m *testing.M) {
	undo := jsonsimd.LimitCPU(os.Getenv("MOEGO_TEST_CPU"))
	code := m.Run()
	undo()
	os.Exit(code)
}
