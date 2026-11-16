//go:build arm64 && !purego

package deflate

import "testing"

// requireVec does nothing: every arm64 CPU has NEON.
func requireVec(t *testing.T) {}
