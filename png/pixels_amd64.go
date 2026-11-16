//go:build amd64 && !purego

package png

//go:noescape
func rgbaFlagsAVX2(p *byte, n int) uint64

//go:noescape
func shuffleAVX2(dst, src *byte, blocks int, ctl *[32]byte, perm *[8]uint32, step int)

// rgbaFlags reports whether every pixel of an RGBA8 row is opaque and
// whether every pixel is gray.
func rgbaFlags(p []byte) (opaque, gray bool) {
	n := len(p) &^ 31
	if !haveAVX2 || n == 0 {
		return rgbaFlagsGeneric(p)
	}
	m := rgbaFlagsAVX2(&p[0], n)
	opaque = uint32(m)&0x88888888 == 0x88888888
	gray = uint32(m>>32)&0x33333333 == 0x33333333
	o, g := rgbaFlagsGeneric(p[n:])
	return opaque && o, gray && g
}

// Shuffles from 32 bytes of RGBA8 (two lanes of four pixels) to RGB8,
// gray with alpha and gray.
var (
	rgbCtl  = laneCtl(0, 1, 2, 4, 5, 6, 8, 9, 10, 12, 13, 14)
	rgbPerm = [8]uint32{0, 1, 2, 4, 5, 6, 3, 7}
	gaCtl   = laneCtl(0, 3, 4, 7, 8, 11, 12, 15)
	gaPerm  = [8]uint32{0, 1, 4, 5, 2, 3, 6, 7}
	gCtl    = laneCtl(0, 4, 8, 12)
	gPerm   = [8]uint32{0, 4, 1, 2, 3, 5, 6, 7}
)

// laneCtl returns a VPSHUFB control that gathers the bytes at idx to the
// front of each 16-byte lane and zeroes the rest.
func laneCtl(idx ...byte) (c [32]byte) {
	for i := range c {
		c[i] = 0x80
	}
	copy(c[:], idx)
	copy(c[16:], idx)
	return c
}

// shuffle runs a conversion kernel over the whole 32-byte blocks of src
// that fit dst's capacity and returns the source bytes it converted.
func shuffle(d, p []byte, ctl *[32]byte, perm *[8]uint32, step int) int {
	if !haveAVX2 || len(p) < 32 || cap(d) < 32 {
		return 0
	}
	blocks := min(len(p)/32, (cap(d)-32)/step+1)
	shuffleAVX2(&d[:1][0], &p[0], blocks, ctl, perm, step)
	return 32 * blocks
}

// toRGB writes the RGB of an RGBA8 row.
func toRGB(d, p []byte) {
	i := shuffle(d, p, &rgbCtl, &rgbPerm, 24)
	toRGBGeneric(d[i/4*3:], p[i:])
}

// toGA writes the R and A of an RGBA8 row.
func toGA(d, p []byte) {
	i := shuffle(d, p, &gaCtl, &gaPerm, 16)
	toGAGeneric(d[i/2:], p[i:])
}

// toG writes the R of an RGBA8 row.
func toG(d, p []byte) {
	i := shuffle(d, p, &gCtl, &gPerm, 8)
	toGGeneric(d[i/4:], p[i:])
}
