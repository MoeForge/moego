//go:build amd64 && !purego

package png

import (
	"os"
	"testing"
)

// requireKernels skips without AVX2, or fails when MOEGO_REQUIRE_AVX2=1.
func requireKernels(t *testing.T) {
	t.Helper()
	if !haveAVX2 {
		if os.Getenv("MOEGO_REQUIRE_AVX2") == "1" {
			t.Fatal("MOEGO_REQUIRE_AVX2=1 but CPU/OS AVX2 support is unavailable")
		}
		t.Skip("AVX2 unavailable on this host")
	}
}
