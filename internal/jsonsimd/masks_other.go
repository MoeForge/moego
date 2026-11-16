//go:build (!amd64 && !arm64) || purego

package jsonsimd

func strSkip(s string, i int) (int, bool) { return i, false }

func strSkipHTML(s string, i int) (int, bool) { return i, false }

func strSkipBytes(s string, i int) (int, bool) { return i, false }

func strEnd(s string, i int) (int, bool) { return i, false }

func htmlSkip(s string, i int) int { return i }
