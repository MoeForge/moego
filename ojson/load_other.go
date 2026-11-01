//go:build !amd64 && !arm64

package ojson

// load64 returns the eight bytes at s[i:] as a little-endian word, so the
// lowest byte is s[i]. The caller ensures i+8 <= len(s).
func load64(s string, i int) uint64 {
	s = s[i : i+8]
	return uint64(s[0]) | uint64(s[1])<<8 | uint64(s[2])<<16 | uint64(s[3])<<24 |
		uint64(s[4])<<32 | uint64(s[5])<<40 | uint64(s[6])<<48 | uint64(s[7])<<56
}
