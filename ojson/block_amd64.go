//go:build amd64 && !purego

package ojson

import (
	"unsafe"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// containerEnd reads 64-byte blocks with AVX2 when the CPU has it. Each
// block becomes bit masks of quotes, backslashes and brackets, so skipping a
// container takes a few bit operations per block instead of a branch per
// byte.

// blockKernels reports whether the block kernels run: with AVX2.
func blockKernels() bool { return jsonsimd.HaveAVX2 }

// skipState is the state of a container skip at a block boundary.
type skipState struct {
	depth int
	inStr uint64 // all ones inside a string
	esc   uint64 // 1 when the byte at pos is escaped
	pos   int
}

// skipSlow is the result of skipAVX2 at a backslash outside a string.
const skipSlow = -4

//go:noescape
func skipAVX2(p *byte, n int, st *skipState) int

// membersSlow is the result of membersAVX2 at a member it leaves to the
// scalar loop.
const membersSlow = -3

//go:noescape
func membersAVX2(p *byte, n, i int, key *byte, klen int) (v, at int)

// membersFast runs memberValue on AVX2 from json[i]. When it returns
// membersSlow, memberValue goes on from the member at at.
func membersFast(json string, i int, key string) (v, at int) {
	return membersAVX2(unsafe.StringData(json), len(json), i, unsafe.StringData(key), len(key))
}

// containerEndFast is containerEnd on 64-byte blocks. Malformed text that
// the block view could read differently goes to containerEndScalar.
func containerEndFast(s string, o int) int {
	if !jsonsimd.HaveAVX2 {
		return containerEndScalar(s, o)
	}
	st := skipState{pos: o}
	if r := skipAVX2(unsafe.StringData(s), len(s), &st); r != skipSlow {
		return r
	}
	return containerEndScalar(s, o)
}

//go:noescape
func pathMasksAVX2(p *byte, n int) (dots, special uint64)

// pathDots returns the positions of the dots in a plain path of 16 to 63
// bytes, plus a bit at len(path) for its end, so that each component ends at
// the next set bit. It returns 0 for other paths, which getComp reads one
// byte at a time: short ones, for which that is faster, long ones, and those
// with escapes, query bytes, quotes, or the brackets that start multipaths.
func pathDots(path string) uint64 {
	n := len(path)
	if !jsonsimd.HaveAVX2 || n < 16 || n >= 64 {
		return 0
	}
	dots, special := pathMasksAVX2(unsafe.StringData(path), n)
	if special != 0 {
		return 0
	}
	return dots | 1<<uint(n)
}
