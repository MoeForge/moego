//go:build !amd64 && !arm64

package deflate

// sweep marks the head entries more than windowSize back from abs, which
// is at or past every inserted position, as stale: windowSize+1 back.
func (m *chainMatcher) sweep(abs int) {
	now, stale := uint16(abs), uint16(abs-windowSize-1)
	for i, v := range m.head {
		if now-v > windowSize {
			v = stale
		}
		m.head[i] = v
	}
}
