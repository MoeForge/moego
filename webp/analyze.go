package webp

// analysis is what one pass over the ARGB pixels finds out.
type analysis struct {
	opaque bool // every alpha is 255
	// ncolors is the number of distinct colors, or 257 for more than 256.
	ncolors int
	colors  colorSet
}

// colorSet collects up to 256 distinct ARGB colors and how often each one
// occurs, in an open-addressing table.
type colorSet struct {
	slot  [1024]uint16 // 0: empty, else 1 + index into list
	list  [256]uint32  // colors in the order they were first seen
	count [256]uint32  // occurrences of list[i]
	n     int          // colors seen; 257 means more than 256
}

func (c *colorSet) reset() {
	clear(c.slot[:])
	c.n = 0
}

func hashColor(v uint32) uint32 { return v * 0x9e3779b1 >> 22 }

// add records k occurrences of v. It returns false once there are more
// than 256 colors.
func (c *colorSet) add(v uint32, k uint32) bool {
	h := hashColor(v)
	for {
		s := c.slot[h]
		if s == 0 {
			break
		}
		if c.list[s-1] == v {
			c.count[s-1] += k
			return true
		}
		h = (h + 1) & 1023
	}
	if c.n == 256 {
		c.n = 257
		return false
	}
	c.list[c.n] = v
	c.count[c.n] = k
	c.n++
	c.slot[h] = uint16(c.n)
	return true
}

// lookup returns the index into list of a color in the set.
func (c *colorSet) lookup(v uint32) int {
	h := hashColor(v)
	for {
		s := c.slot[h]
		if c.list[s-1] == v {
			return int(s - 1)
		}
		h = (h + 1) & 1023
	}
}

// analyze scans e.argb. Runs of one color are counted once, so flat images
// are cheap; once there are more than 256 colors, only the alpha is
// checked.
func (e *encoder) analyze() {
	a := &e.an
	a.colors.reset()
	p := e.argb
	and := uint32(0xffffffff)
	i := 0
	for i < len(p) {
		v := p[i]
		j := i + 1
		for j < len(p) && p[j] == v {
			j++
		}
		and &= v
		ok := a.colors.add(v, uint32(j-i))
		i = j
		if !ok {
			break
		}
	}
	if e.loadAndOK {
		and = e.loadAnd
	} else {
		for _, v := range p[i:] {
			and &= v
		}
	}
	a.opaque = and>>24 == 0xff
	a.ncolors = a.colors.n
}
