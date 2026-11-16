//go:build !(amd64 || arm64) || purego

package png

func filterCosts(cur, prev []byte, bpp int) [5]uint64 { return filterCostsGeneric(cur, prev, bpp) }

func filterRow(dst, cur, prev []byte, bpp int, f uint8) { filterRowGeneric(dst, cur, prev, bpp, f) }
