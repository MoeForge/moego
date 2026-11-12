//go:build (amd64 || arm64) && !purego && !moego_zeroalloc && !race

package json

import (
	"sync"
	_ "unsafe" // for go:linkname
)

// procPin and procUnpin pin the goroutine to its processor and return the
// processor's id, as sync.Pool does. Go keeps these linkname targets stable
// because widely used packages depend on them.
//
//go:linkname procPin runtime.procPin
func procPin() int

//go:linkname procUnpin runtime.procUnpin
func procUnpin()

// stateCache keeps the states of each processor in its own row, which
// only a goroutine pinned to that processor reads or writes.
type stateCache[T any] struct {
	procs [stateProcs]stateRow[T]
	pool  sync.Pool
}

type stateRow[T any] struct {
	x [stateDepth]*T
	_ [64 - stateDepth*8%64]byte // a cache line of its own
}

// get returns a free state, new or zero when none is kept.
func (c *stateCache[T]) get() *T {
	if p := procPin(); p < stateProcs {
		xs := &c.procs[p].x
		for k, x := range xs {
			if x != nil {
				xs[k] = nil
				procUnpin()
				return x
			}
		}
	}
	procUnpin()
	if x, _ := c.pool.Get().(*T); x != nil {
		return x
	}
	return new(T)
}

// put keeps x, which no call uses any longer: with the states of the
// processor when small is set and there is room, else in the pool.
func (c *stateCache[T]) put(x *T, small bool) {
	if small {
		if p := procPin(); p < stateProcs {
			xs := &c.procs[p].x
			for k := range xs {
				if xs[k] == nil {
					xs[k] = x
					procUnpin()
					return
				}
			}
		}
		procUnpin()
	}
	c.pool.Put(x)
}
