//go:build amd64 && !purego

package ojson

import "unsafe"

// uglyShuf[m] is the VPSHUFB control that moves the bytes of an 8-byte
// group whose bits are set in m to the front, in order.
var uglyShuf = func() (t [256]uint64) {
	for m := range t {
		p, k := uint64(0x8080808080808080), 0
		for b := 0; b < 8; b++ {
			if m>>b&1 != 0 {
				p = p&^(0xff<<(8*k)) | uint64(b)<<(8*k)
				k++
			}
		}
		t[m] = p
	}
	return t
}()

//go:noescape
func uglyAVX2(dst, src *byte, n, i, w int, in uint64, shuf *[256]uint64) (int, int, uint64)

// uglyBlocks runs @ugly on the 64-byte blocks of json from i that hold no
// backslash, with st not inside an escape. With out nil it only checks
// and stops at the first block with a byte to drop; otherwise it writes
// the kept bytes to out[w:], and out is as long as json and w <= i. It
// returns the new i and w, with st the string state at i.
func uglyBlocks(out []byte, json string, i, w int, st *uglyState) (int, int) {
	if !haveAVX2 || len(json)-i < 64 || w > i {
		return i, w
	}
	var dst *byte
	if out != nil {
		dst = unsafe.SliceData(out[:len(json)])
	}
	in := uint64(0)
	if st.in {
		in = ^uint64(0)
	}
	i, w, in = uglyAVX2(dst, unsafe.StringData(json), len(json), i, w, in, &uglyShuf)
	st.in = in != 0
	return i, w
}
