//go:build !amd64 || purego

package webp

import "testing"

// requireKernels runs the comparisons against the portable code itself.
func requireKernels(t *testing.T) {}
