//go:build (!amd64 && !arm64) || purego || moego_zeroalloc

package json

// zeroAlloc tells that the build allocates through reflect, whose
// MakeSlice also allocates the header of each slice it makes.
const zeroAlloc = true
