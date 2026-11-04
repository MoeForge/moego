//go:build (!amd64 && !arm64) || purego

package ojson

const haveAVX2 = false

func containerEndFast(s string, o int) int { return containerEndScalar(s, o) }

// pathDots is 0 without AVX2: getComp reads every path.
func pathDots(path string) uint64 { return 0 }

const membersSlow = -3

func membersFast(json string, i int, key string) (v, at int) { return membersSlow, i }
