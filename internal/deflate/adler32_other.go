//go:build !(amd64 || arm64) || purego

package deflate

func adler32(adler uint32, p []byte) uint32 { return adler32Generic(adler, p) }
