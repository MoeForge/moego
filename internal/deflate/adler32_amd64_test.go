//go:build amd64 && !purego

package deflate

import (
	"os"
	"testing"
)

// requireVec skips tests of the AVX2 kernel on CPUs without it, or fails
// them when MOEGO_REQUIRE_AVX2=1.
func requireVec(t *testing.T) {
	t.Helper()
	if !haveAVX2 {
		if os.Getenv("MOEGO_REQUIRE_AVX2") == "1" {
			t.Fatal("MOEGO_REQUIRE_AVX2=1 but CPU/OS AVX2 support is unavailable")
		}
		t.Skip("AVX2 unavailable on this host")
	}
}
