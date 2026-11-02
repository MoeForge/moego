//go:build arm64 && !purego

package ojson

import "unsafe"

//go:noescape
func validMasksNEON(p *byte, nb int, out *vmask)

// validMasks fills out with the masks of the 64-byte blocks of s from i, at
// most validBatch of them, and returns how many. A last block shorter than
// 64 bytes is read from tail, padded with spaces.
func validMasks(s string, i int, out *[validBatch]vmask, tail *[64]byte) int {
	n := (len(s) - i) >> 6
	if n == 0 {
		k := copy(tail[:], s[i:])
		for ; k < len(tail); k++ {
			tail[k] = ' '
		}
		validMasksNEON(&tail[0], 1, &out[0])
		return 1
	}
	n = min(n, validBatch)
	validMasksNEON((*byte)(unsafe.Add(unsafe.Pointer(unsafe.StringData(s)), i)), n, &out[0])
	return n
}
