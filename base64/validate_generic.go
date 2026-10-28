//go:build (!arm64 && !amd64) || purego

package base64

func validBlocks(src []byte, url bool) int { return 0 }
