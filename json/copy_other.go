//go:build (!amd64 && !arm64) || purego

package json

// appendPadded appends s.
func appendPadded(b []byte, s string) []byte { return append(b, s...) }

// keyPad is the padding after each member key.
const keyPad = 24

// wordCopy tells whether unaligned word loads and stores are used.
const wordCopy = false

// appendKeyBytes appends key.
func appendKeyBytes(b []byte, key string) []byte { return append(b, key...) }

// storeWord writes the eight bytes of w at b[i:], lowest byte first.
func storeWord(b []byte, i int, w uint64) {
	b = b[i : i+8]
	for k := range b {
		b[k] = byte(w >> (8 * k))
	}
}
