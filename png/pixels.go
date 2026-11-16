package png

// rgbaFlagsGeneric reports whether every pixel of an RGBA8 row is opaque
// and whether every pixel is gray.
func rgbaFlagsGeneric(p []byte) (opaque, gray bool) {
	opaque, gray = true, true
	for i := 0; i+3 < len(p); i += 4 {
		opaque = opaque && p[i+3] == 0xff
		gray = gray && p[i] == p[i+1] && p[i] == p[i+2]
	}
	return opaque, gray
}

func toRGBGeneric(d, p []byte) {
	for i, j := 0, 0; j+3 < len(p); i, j = i+3, j+4 {
		d[i], d[i+1], d[i+2] = p[j], p[j+1], p[j+2]
	}
}

func toGAGeneric(d, p []byte) {
	for i, j := 0, 0; j+3 < len(p); i, j = i+2, j+4 {
		d[i], d[i+1] = p[j], p[j+3]
	}
}

func toGGeneric(d, p []byte) {
	for i, j := 0, 0; j+3 < len(p); i, j = i+1, j+4 {
		d[i] = p[j]
	}
}
