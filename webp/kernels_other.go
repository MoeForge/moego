//go:build !(amd64 || arm64) || purego

package webp

func residuals(m int, dst, img []uint32, i, w int) { residualsGeneric(m, dst, img, i, w) }

func subtractGreenRow(dst, src []uint32) { subtractGreenRowGeneric(dst, src) }

func colorTransformRow(dst, src []uint32, c ctElem) { colorTransformRowGeneric(dst, src, c) }

func addGreenRow(dst, src []uint32) { addGreenRowGeneric(dst, src) }

func undoColorTransformRow(dst, src []uint32, c ctElem) { undoColorTransformRowGeneric(dst, src, c) }

func addTopRow(m int, cur, top []uint32) { addTopRowGeneric(m, cur, top) }

func (m *model) pixelCosts(out, res []uint32, set int) { m.pixelCostsGeneric(out, res, set) }

func ctCostSum(tab *[256]uint32, x, y []int8, m int32) uint32 {
	return ctCostGeneric(tab, x, y, m)
}

func fromNRGBA(d []uint32, p []byte) uint32 { return fromNRGBAGeneric(d, p) }

// haveCTTile reports whether ctTile takes tiles.
var haveCTTile = false

func ctTile(img []uint32, i, stride, w, rows int, g, r, b []int8, sums *[5]int32) bool {
	return false
}
