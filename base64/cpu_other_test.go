//go:build !amd64 || purego

package base64

// limitCPU only checks drop: no kernel here needs the features it names.
func limitCPU(drop string) (undo func()) {
	dropsAVX2(drop)
	return func() {}
}
