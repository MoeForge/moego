package regexp

import (
	"hash/maphash"
	"regexp/syntax"
	"strings"
	"sync"
	"sync/atomic"
)

// The compile cache lets code that compiles the same expression repeatedly,
// for instance on every request, reuse the compiled form. A compiled
// expression is immutable apart from matching state that is built on demand
// and safe for concurrent use, so a cached one is shared: Compile still
// returns a new *Regexp each time, and Longest affects only that one.
//
// The cache admits an expression the second time it is compiled within a
// short window, so expressions compiled once neither displace others nor
// cost a copy. An admitted expression is copied, so the cache does not keep
// the caller's string, which may be part of a larger one, alive.

// DefaultCacheSize is the number of compiled expressions the cache keeps
// unless SetCacheSize changes it.
const DefaultCacheSize = 512

// maxCachedExpr is the length of the longest expression the cache keeps.
const maxCachedExpr = 4 << 10

const cacheShards = 16

type cacheKey struct {
	expr string
	mode syntax.Flags
}

type cacheShard struct {
	mu   sync.Mutex
	m    map[cacheKey]int // index into ents
	ents []cacheEnt
	hand int // CLOCK hand
	_    [64]byte
}

type cacheEnt struct {
	key cacheKey
	c   *core
	ref bool // used since the hand last passed
}

var cache struct {
	shards [cacheShards]cacheShard
	seen   [1024]atomic.Uint64 // hashes of recently compiled expressions
	limit  atomic.Int64        // entries in all shards together
	n      atomic.Int64
	seed   maphash.Seed
}

func init() {
	cache.seed = maphash.MakeSeed()
	cache.limit.Store(DefaultCacheSize)
}

// SetCacheSize sets the number of compiled expressions that Compile and the
// other compile functions keep for reuse, and returns the previous setting.
// Zero turns the cache off. Changing the size empties the cache.
// Expressions longer than 4 KiB are not kept.
func SetCacheSize(n int) int {
	if n < 0 {
		n = 0
	}
	prev := cache.limit.Swap(int64(n))
	for i := range cache.shards {
		sh := &cache.shards[i]
		sh.mu.Lock()
		cache.n.Add(-int64(len(sh.ents)))
		sh.m, sh.ents, sh.hand = nil, nil, 0
		sh.mu.Unlock()
	}
	return int(prev)
}

// cacheLookup returns the cached core for expr and mode, or nil and whether
// a core compiled now should be added with remember.
func cacheLookup(expr string, mode syntax.Flags) (c *core, admit bool) {
	if len(expr) > maxCachedExpr || cache.limit.Load() == 0 {
		return nil, false
	}
	h := maphash.String(cache.seed, expr) ^ uint64(mode)*0x9e3779b97f4a7c15
	sh := &cache.shards[h%cacheShards]
	sh.mu.Lock()
	if i, ok := sh.m[cacheKey{expr, mode}]; ok {
		e := &sh.ents[i]
		e.ref = true
		c = e.c
	}
	sh.mu.Unlock()
	if c != nil {
		return c, false
	}
	slot := &cache.seen[(h/cacheShards)%uint64(len(cache.seen))]
	if slot.Load() == h {
		return nil, true
	}
	slot.Store(h)
	return nil, false
}

// cacheExpr returns the copy of expr that an admitted core keeps.
func cacheExpr(expr string) string { return strings.Clone(expr) }

// remember adds a compiled core to the cache, evicting an entry that was
// not used since the CLOCK hand last passed it when the cache is full.
func remember(c *core, mode syntax.Flags) {
	key := cacheKey{c.expr, mode}
	h := maphash.String(cache.seed, c.expr) ^ uint64(mode)*0x9e3779b97f4a7c15
	sh := &cache.shards[h%cacheShards]
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if _, ok := sh.m[key]; ok {
		return
	}
	for {
		n, limit := cache.n.Load(), cache.limit.Load()
		if n >= limit {
			break
		}
		if cache.n.CompareAndSwap(n, n+1) {
			if sh.m == nil {
				sh.m = make(map[cacheKey]int)
			}
			sh.m[key] = len(sh.ents)
			sh.ents = append(sh.ents, cacheEnt{key: key, c: c})
			return
		}
	}
	if len(sh.ents) == 0 || cache.limit.Load() == 0 {
		return // full, with the other shards holding the whole budget
	}
	for {
		e := &sh.ents[sh.hand]
		if !e.ref {
			delete(sh.m, e.key)
			*e = cacheEnt{key: key, c: c}
			sh.m[key] = sh.hand
			sh.hand = (sh.hand + 1) % len(sh.ents)
			return
		}
		e.ref = false
		sh.hand = (sh.hand + 1) % len(sh.ents)
	}
}
