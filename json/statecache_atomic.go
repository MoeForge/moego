//go:build !(amd64 || arm64) || purego || moego_zeroalloc || race

package json

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// stateCache keeps states in slots that any goroutine may take from; a
// goroutine starts looking at a slot chosen by the address of its stack.
type stateCache[T any] struct {
	slots [stateProcs * stateDepth]stateSlot[T]
	pool  sync.Pool
}

type stateSlot[T any] struct {
	p atomic.Pointer[T]
	_ [56]byte // a cache line of its own
}

func slotStart() int {
	var b byte
	return int(uintptr(unsafe.Pointer(&b))>>13) * stateDepth
}

// get returns a free state, new or zero when none is kept.
func (c *stateCache[T]) get() *T {
	i := slotStart()
	for k := range stateDepth {
		s := &c.slots[(i+k)%len(c.slots)]
		if x := s.p.Load(); x != nil && s.p.CompareAndSwap(x, nil) {
			return x
		}
	}
	if x, _ := c.pool.Get().(*T); x != nil {
		return x
	}
	return new(T)
}

// put keeps x, which no call uses any longer: in a slot when small is set
// and one is free, else in the pool.
func (c *stateCache[T]) put(x *T, small bool) {
	if small {
		i := slotStart()
		for k := range stateDepth {
			s := &c.slots[(i+k)%len(c.slots)]
			if s.p.Load() == nil && s.p.CompareAndSwap(nil, x) {
				return
			}
		}
	}
	c.pool.Put(x)
}
