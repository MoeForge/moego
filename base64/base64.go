// Package base64 implements RFC 4648 encoding with SIMD acceleration for media
// payloads. Its Encoding API follows encoding/base64.
package base64

import (
	std "encoding/base64"
	"io"
	"slices"
	"unsafe"
)

const (
	StdPadding       = std.StdPadding
	NoPadding        = std.NoPadding
	standardAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	urlAlphabet      = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
)

// CorruptInputError reports the byte offset of invalid base64 input.
type CorruptInputError = std.CorruptInputError

// Encoding describes a base64 alphabet, padding and strict decoding rules.
// Configured encodings can be shared by concurrent callers.
type Encoding struct {
	stdlib   *std.Encoding
	alphabet [64]byte
	mode     uint8 // 0: custom alphabet, 1: standard, 2: URL
	strict   bool
	padding  rune
}

var (
	StdEncoding    = NewEncoding(standardAlphabet)
	URLEncoding    = NewEncoding(urlAlphabet)
	RawStdEncoding = StdEncoding.WithPadding(NoPadding)
	RawURLEncoding = URLEncoding.WithPadding(NoPadding)
)

// NewEncoding creates an encoding from 64 distinct bytes. It panics for invalid
// alphabets according to encoding/base64.NewEncoding.
func NewEncoding(alphabet string) *Encoding {
	e := &Encoding{stdlib: std.NewEncoding(alphabet), padding: StdPadding}
	copy(e.alphabet[:], alphabet)
	switch alphabet {
	case standardAlphabet:
		e.mode = 1
	case urlAlphabet:
		e.mode = 2
	}
	return e
}

// WithPadding returns a copy using padding, or NoPadding for unpadded output.
func (e Encoding) WithPadding(padding rune) *Encoding {
	e.stdlib = e.stdlib.WithPadding(padding)
	e.padding = padding
	return &e
}

// Strict returns a copy that rejects nonzero trailing padding bits.
func (e Encoding) Strict() *Encoding {
	e.stdlib = e.stdlib.Strict()
	e.strict = true
	return &e
}

// EncodedLen returns the number of bytes needed to encode n source bytes.
func (e *Encoding) EncodedLen(n int) int { return e.stdlib.EncodedLen(n) }

// DecodedLen returns the maximum decoded length for n encoded bytes.
func (e *Encoding) DecodedLen(n int) int { return e.stdlib.DecodedLen(n) }

// Encode writes EncodedLen(len(src)) bytes to dst. A short dst causes a panic.
func (e *Encoding) Encode(dst, src []byte) {
	if len(src) < encodeMin || e.mode == 0 {
		e.stdlib.Encode(dst, src)
		return
	}
	_ = dst[e.EncodedLen(len(src))-1]
	n := encodeBlocks(dst, src, &e.alphabet)
	if len(src)-n >= 3 {
		e.stdlib.Encode(dst[n/3*4:], src[n:])
		return
	}
	e.encodeTail(dst[n/3*4:], src[n:])
}

// encodeTail encodes the one or two bytes left after whole groups.
func (e *Encoding) encodeTail(dst, src []byte) {
	switch len(src) {
	case 1:
		v := uint(src[0])
		_ = dst[1]
		dst[0] = e.alphabet[v>>2]
		dst[1] = e.alphabet[v<<4&0x3f]
		if e.padding != NoPadding {
			_ = dst[3]
			dst[2] = byte(e.padding)
			dst[3] = byte(e.padding)
		}
	case 2:
		v := uint(src[0])<<8 | uint(src[1])
		_ = dst[2]
		dst[0] = e.alphabet[v>>10]
		dst[1] = e.alphabet[v>>4&0x3f]
		dst[2] = e.alphabet[v<<2&0x3f]
		if e.padding != NoPadding {
			dst[3] = byte(e.padding)
		}
	}
}

// AppendEncode appends the encoding of src to dst.
func (e *Encoding) AppendEncode(dst, src []byte) []byte {
	n := e.EncodedLen(len(src))
	start := len(dst)
	dst = slices.Grow(dst, n)[:start+n]
	e.Encode(dst[start:], src)
	return dst
}

// EncodeToString returns an immutable encoding of src using one allocation.
func (e *Encoding) EncodeToString(src []byte) string {
	buf := makeNoZero(e.EncodedLen(len(src)))
	e.Encode(buf, src)
	return ownedString(buf)
}

// Decode writes decoded bytes to dst and returns their count. On corrupt input,
// it returns the valid prefix and a CorruptInputError at the original offset.
// CR and LF are ignored. dst must have enough space for the decoded output.
func (e *Encoding) Decode(dst, src []byte) (int, error) {
	if len(src) < decodeMin || e.mode == 0 {
		return e.stdlib.Decode(dst, src)
	}
	body, tail, ok := e.finalQuantum(src)
	n := decodeBlocks(dst, src[:body], e.mode == 2)
	written := n / 4 * 3
	if ok && n == body {
		if m, ok := e.decodeTail(dst[written:], src[body:body+tail]); ok {
			return written + m, nil
		}
	}
	m, err := e.stdlib.Decode(dst[written:], src[n:])
	if corrupt, ok := err.(std.CorruptInputError); ok {
		err = std.CorruptInputError(int64(corrupt) + int64(n))
	}
	return written + m, err
}

// finalQuantum splits src into whole quanta and the data characters of a final
// partial quantum. ok is false when only encoding/base64 can classify the end
// of src, such as padded input whose length is not a multiple of four.
func (e *Encoding) finalQuantum(src []byte) (body, tail int, ok bool) {
	n := len(src)
	if e.padding == NoPadding {
		return n &^ 3, n & 3, n&3 != 1
	}
	if n&3 != 0 || n < 4 {
		return n &^ 3, 0, false
	}
	pad := byte(e.padding)
	switch {
	case src[n-1] != pad:
		return n, 0, true
	case src[n-2] != pad:
		return n - 4, 3, true
	default:
		return n - 4, 2, true
	}
}

// decodeTail decodes the data characters of a final partial quantum. It
// reports false when encoding/base64 must produce the result or error.
func (e *Encoding) decodeTail(dst, src []byte) (int, bool) {
	table := &decodeMaps[e.mode&1]
	switch len(src) {
	case 0:
		return 0, true
	case 2:
		a, b := table[src[0]], table[src[1]]
		if a|b > 63 || e.strict && b&15 != 0 {
			return 0, false
		}
		dst[0] = a<<2 | b>>4
		return 1, true
	case 3:
		a, b, c := table[src[0]], table[src[1]], table[src[2]]
		if a|b|c > 63 || e.strict && c&3 != 0 {
			return 0, false
		}
		_ = dst[1]
		dst[0] = a<<2 | b>>4
		dst[1] = b<<4 | c>>2
		return 2, true
	}
	return 0, false
}

// decodeMaps maps URL (index 0) and standard (index 1) characters to sextets.
// Other bytes map to 0xff.
var decodeMaps = [2][256]byte{makeDecodeMap(urlAlphabet), makeDecodeMap(standardAlphabet)}

func makeDecodeMap(alphabet string) [256]byte {
	var table [256]byte
	for i := range table {
		table[i] = 0xff
	}
	for i := range len(alphabet) {
		table[alphabet[i]] = byte(i)
	}
	return table
}

// AppendDecode appends decoded bytes to dst, including the valid prefix when
// the input is corrupt.
func (e *Encoding) AppendDecode(dst, src []byte) ([]byte, error) {
	n := len(src)
	if e.padding != NoPadding {
		for n > 0 && rune(src[n-1]) == e.padding {
			n--
		}
	}
	// This bound also fits inputs with padding removed; Decode handles CR/LF.
	bound := n/4*3 + n%4*6/8
	start := len(dst)
	dst = slices.Grow(dst, bound)
	written, err := e.Decode(dst[start:][:bound], src)
	return dst[:start+written], err
}

// DecodeString decodes s without making a temporary copy of its input.
func (e *Encoding) DecodeString(s string) ([]byte, error) {
	size := e.DecodedLen(len(s))
	// Padded media sizes often land exactly on an allocator page boundary.
	// Avoid crossing that boundary for the one or two padding bytes.
	if e.mode != 0 && e.padding != NoPadding && len(s) >= 4 && len(s)%4 == 0 && s[len(s)-1] == byte(e.padding) {
		size--
		if s[len(s)-2] == byte(e.padding) {
			size--
		}
	}
	dst := makeNoZero(size)
	n, err := e.Decode(dst, unsafe.Slice(unsafe.StringData(s), len(s)))
	// Corrupt input and CR/LF leave bytes past n that must not keep old
	// heap contents in the returned capacity.
	clear(dst[n:])
	return dst[:n], err
}

// NewEncoder returns a streaming encoder. Close it to flush the final block.
// Streaming currently uses the standard library's buffered implementation.
func NewEncoder(enc *Encoding, w io.Writer) io.WriteCloser {
	return std.NewEncoder(enc.stdlib, w)
}

// NewDecoder returns the standard library's streaming decoder for enc.
func NewDecoder(enc *Encoding, r io.Reader) io.Reader {
	return std.NewDecoder(enc.stdlib, r)
}

// ownedString transfers a private allocation to a string. Callers must discard
// every mutable reference to buf after this call.
func ownedString(buf []byte) string {
	return unsafe.String(unsafe.SliceData(buf), len(buf))
}
