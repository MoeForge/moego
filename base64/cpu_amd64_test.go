//go:build amd64 && !purego

package base64

// limitCPU makes the codec run as on a CPU without the features that drop
// names, and returns a function that undoes it.
func limitCPU(drop string) (undo func()) {
	prev := haveAVX2
	if dropsAVX2(drop) {
		haveAVX2 = false
	}
	return func() { haveAVX2 = prev }
}
