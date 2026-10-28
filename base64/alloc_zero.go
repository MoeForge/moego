//go:build (!amd64 && !arm64) || purego || moego_zeroalloc

package base64

// makeNoZero uses an ordinary zeroed allocation for builds without the
// runtime allocator linkname.
func makeNoZero(n int) []byte { return make([]byte, n) }
