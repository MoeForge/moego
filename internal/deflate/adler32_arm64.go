//go:build arm64 && !purego

package deflate

func adler32(adler uint32, p []byte) uint32 {
	if len(p) < 64 {
		return adler32Generic(adler, p)
	}
	return adler32Vec(adler, p)
}
