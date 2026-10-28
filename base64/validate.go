package base64

import "unsafe"

// ValidateString returns the decoded byte count and error that DecodeString
// would return. On corrupt input, the count covers the successfully decoded
// prefix and CorruptInputError identifies the original input offset.
//
// On amd64 and arm64, uninterrupted standard and URL input is checked without
// producing decoded bytes; only the last 64 characters are decoded into stack
// storage. Large custom alphabets, CR/LF inputs and scalar fallbacks may
// allocate remaining output.
func (e *Encoding) ValidateString(s string) (decodedLen int, err error) {
	src := unsafe.Slice(unsafe.StringData(s), len(s))
	if e.mode == 0 || len(src) < decodeMin {
		return e.validateSlow(src, 0)
	}
	url := e.mode == 2
	body, tail, ok := e.finalQuantum(src)
	n := validBlocks(src[:body], url)
	var scratch [48]byte
	if body-n <= 64 {
		// Fewer than 64 characters remain; the decoder checks them, along
		// with up to 64 characters that were already checked.
		start := max(0, body-64)
		if v := start + decodeBlocks(scratch[:], src[start:body], url); v > n {
			n = v
		}
	}
	if ok && n == body {
		if m, ok := e.decodeTail(scratch[:], src[body:body+tail]); ok {
			return body/4*3 + m, nil
		}
	}
	return e.validateSlow(src, n)
}

// validateSlow decodes src after a validated whole-quantum prefix into bounded
// stack storage, allocating only for long scalar remainders.
func (e *Encoding) validateSlow(src []byte, consumed int) (decodedLen int, err error) {
	decodedLen = consumed / 4 * 3
	src = src[consumed:]
	var scratch [768]byte
	for e.mode != 0 && len(src) >= decodeMin {
		n := decodeBlocks(scratch[:], src, e.mode == 2)
		if n == 0 {
			break
		}
		decodedLen += n / 4 * 3
		consumed += n
		src = src[n:]
	}
	if len(src) == 0 {
		return decodedLen, nil
	}
	dst := scratch[:]
	if bound := e.DecodedLen(len(src)); bound > len(dst) {
		dst = make([]byte, bound)
	}
	n, err := e.stdlib.Decode(dst, src)
	if corrupt, ok := err.(CorruptInputError); ok {
		err = CorruptInputError(int64(corrupt) + int64(consumed))
	}
	return decodedLen + n, err
}
