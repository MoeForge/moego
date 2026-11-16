//go:build !(amd64 || arm64) || purego

package png

func rgbaFlags(p []byte) (opaque, gray bool) { return rgbaFlagsGeneric(p) }

func toRGB(d, p []byte) { toRGBGeneric(d, p) }

func toGA(d, p []byte) { toGAGeneric(d, p) }

func toG(d, p []byte) { toGGeneric(d, p) }
