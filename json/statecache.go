package json

// Encoder and decoder states are kept between calls, so that a call does
// not allocate one. A sync.Pool empties at each garbage collection and
// then allocates its lists again, which a program that collects every few
// calls pays for again and again. Small states are therefore kept where
// collections leave them alone: up to stateDepth of each kind for each of
// the first stateProcs processors, so at most 16 encoder and 16 decoder
// states. Other states go to a sync.Pool. A state counts as small when its
// buffers hold at most stateCacheMax bytes, so the kept encoder states
// hold at most about 68 KiB and the kept decoder states about 118 KiB,
// plus at most about 6.5 KiB each for interned strings (see intern.go),
// for the life of the program. A state is kept only while no call uses it,
// and nothing a call returns shares memory with it, other than interned
// strings, which are never written again.
const (
	stateProcs    = 4
	stateDepth    = 4 // states kept for each processor, for nested calls
	stateCacheMax = 4 << 10
)
