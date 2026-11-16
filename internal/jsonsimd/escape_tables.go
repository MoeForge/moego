//go:build (amd64 || arm64) && !purego

package jsonsimd

// escapeTab[c] holds the escape of byte c in a JSON string in its low
// bytes and its length in its top byte: for the bytes escapeAVX2/escapeNEON
// escapes, and for the other ASCII bytes the byte itself.
var escapeTab = func() (t [256]uint64) {
	const hex = "0123456789abcdef"
	for c := range t {
		e := []byte{'\\', 'u', '0', '0', hex[c>>4], hex[c&15]}
		if c >= 0x20 && c < 0x80 && c != '<' && c != '>' && c != '&' {
			e = []byte{byte(c)}
		}
		switch c {
		case '"', '\\':
			e = []byte{'\\', byte(c)}
		case '\b':
			e = []byte{'\\', 'b'}
		case '\f':
			e = []byte{'\\', 'f'}
		case '\n':
			e = []byte{'\\', 'n'}
		case '\r':
			e = []byte{'\\', 'r'}
		case '\t':
			e = []byte{'\\', 't'}
		}
		for k, b := range e {
			t[c] |= uint64(b) << (8 * k)
		}
		t[c] |= uint64(len(e)) << 56
	}
	return t
}()

// escapeExp[m] shuffles eight bytes, followed by eight backslashes, into
// the eight bytes with a backslash before each byte whose bit is set in m:
// the VPSHUFB/VTBL control of escape and quote kernels.
var escapeExp = func() (t [256][16]byte) {
	for m := range t {
		k := 0
		for j := range 8 {
			if m>>j&1 != 0 {
				t[m][k] = 8
				k++
			}
			t[m][k] = byte(j)
			k++
		}
		for ; k < 16; k++ {
			t[m][k] = 0x80
		}
	}
	return t
}()

// unescapeTables are the tables of unescapeAVX2/unescapeNEON, which find
// comp 256 bytes after tab and hex 4352 bytes after it.
type unescapeTables struct {
	// tab[c] is the byte that the escape of a backslash and c stands for,
	// or 0 for c that does not make an escape of two bytes.
	tab [256]byte
	// comp[m] shuffles the bytes of eight whose bit is clear in m to the
	// start.
	comp [256][16]byte
	// hex[c] is the value of the hexadecimal digit c, or 0xffff0000 for c
	// that is none.
	hex [256]uint32
}

var unescapeTabs = func() (t unescapeTables) {
	for c, b := range map[byte]byte{'"': '"', '\\': '\\', '/': '/', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t'} {
		t.tab[c] = b
	}
	for c := range t.hex {
		switch {
		case '0' <= c && c <= '9':
			t.hex[c] = uint32(c - '0')
		case 'a' <= c|0x20 && c|0x20 <= 'f':
			t.hex[c] = uint32(c|0x20-'a') + 10
		default:
			t.hex[c] = 0xffff0000
		}
	}
	for m := range t.comp {
		k := 0
		for j := range 8 {
			if m>>j&1 == 0 {
				t.comp[m][k] = byte(j)
				k++
			}
		}
		for ; k < 16; k++ {
			t.comp[m][k] = 0x80
		}
	}
	return t
}()
