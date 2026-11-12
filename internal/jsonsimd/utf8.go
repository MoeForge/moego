package jsonsimd

import "unicode/utf8"

// The results of CheckUTF8.
const (
	UTF8Invalid = 1 << iota // a byte is not part of valid UTF-8
	UTF8LineSep             // U+2028 or U+2029 appears
)

// checkUTF8 is CheckUTF8 without the kernel.
func checkUTF8(s string) int {
	r := 0
	for i := 0; i < len(s); {
		if s[i] < utf8.RuneSelf {
			i++
			continue
		}
		c, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case c == utf8.RuneError && n == 1:
			r |= UTF8Invalid
		case c == 0x2028 || c == 0x2029:
			r |= UTF8LineSep
		}
		i += n
	}
	return r
}
