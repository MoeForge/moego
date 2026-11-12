//go:build !(amd64 || arm64) || purego

package regexp

// load64 returns the 8 bytes at s[o:] as a little-endian word; o+8 <=
// len(s).
func load64(s string, o int) uint64 {
	s = s[o : o+8]
	return uint64(s[0]) | uint64(s[1])<<8 | uint64(s[2])<<16 | uint64(s[3])<<24 |
		uint64(s[4])<<32 | uint64(s[5])<<40 | uint64(s[6])<<48 | uint64(s[7])<<56
}
