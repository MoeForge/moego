//go:build arm64 && !purego

package ojson

import "unsafe"

// containerEnd reads 64-byte blocks with NEON. Each block becomes bit masks
// of quotes, backslashes and brackets, so skipping a container takes a few
// bit operations per block instead of a branch per byte.

// blockKernels reports whether the block kernels run: always, on NEON.
func blockKernels() bool { return true }

// skipState is the state of a container skip at a block boundary.
type skipState struct {
	depth int
	inStr uint64 // all ones inside a string
	esc   uint64 // 1 when the byte at pos is escaped
	pos   int
}

// skipSlow is the result of skipNEON at a backslash outside a string.
const skipSlow = -4

//go:noescape
func skipNEON(p *byte, n int, st *skipState) int

// skipAVX2 is an alias for skipNEON used by shared block tests.
func skipAVX2(p *byte, n int, st *skipState) int {
	return skipNEON(p, n, st)
}

// membersSlow is the result of membersNEON at a member it leaves to the
// scalar loop.
const membersSlow = -3

//go:noescape
func membersNEON(p *byte, n, i int, key *byte, klen int) (v, at int)

// membersFast runs memberValue on NEON from json[i]. When it returns
// membersSlow, memberValue goes on from the member at at.
func membersFast(json string, i int, key string) (v, at int) {
	return membersNEON(unsafe.StringData(json), len(json), i, unsafe.StringData(key), len(key))
}

// containerEndFast is containerEnd on 64-byte blocks. Malformed text that
// the block view could read differently goes to containerEndScalar.
func containerEndFast(s string, o int) int {
	st := skipState{pos: o}
	if r := skipNEON(unsafe.StringData(s), len(s), &st); r != skipSlow {
		return r
	}
	return containerEndScalar(s, o)
}

//go:noescape
func pathMasksNEON(p *byte, n int) (dots, special uint64)

// pathDots returns the positions of the dots in a plain path of 16 to 63
// bytes, plus a bit at len(path) for its end, so that each component ends at
// the next set bit. It returns 0 for other paths, which getComp reads one
// byte at a time: short ones, for which that is faster, long ones, and those
// with escapes, query bytes, quotes, or the brackets that start multipaths.
func pathDots(path string) uint64 {
	n := len(path)
	if n < 16 || n >= 64 {
		return 0
	}
	dots, special := pathMasksNEON(unsafe.StringData(path), n)
	if special != 0 {
		return 0
	}
	return dots | 1<<uint(n)
}
