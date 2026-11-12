package jsonsimd

import (
	"slices"
	"unsafe"
)

// Results of StreamCheck.Feed.
const (
	StreamMore    = iota // more bytes are needed
	StreamEnd            // the value ended: StreamCheck.End is set
	StreamInvalid        // the bytes are not a valid value
	StreamTooLong        // a number or a run of whitespace is at least Long bytes
)

// A StreamString is the position of a string of at least StreamCheck.Long
// bytes, counted with its quotes, in the stream.
type StreamString struct {
	Start int64 // offset of the opening quote
	End   int64 // offset of the closing quote
}

// A StreamContainer is the position of an array or object of at least
// StreamCheck.Long bytes in the stream.
type StreamContainer struct {
	Off int64 // offset of the opening bracket
	End int64 // offset of the closing bracket
	N   int   // number of elements or members
}

// Grammar states of a StreamCheck.
const (
	sValue    uint8 = iota // a value must follow
	sArrFirst              // an element or the closing bracket
	sObjFirst              // a key or the closing bracket
	sKey                   // a key must follow
	sColon                 // the colon after a key
	sAfter                 // a comma or the closing bracket of the container
)

// A StreamCheck validates one JSON value that arrives in pieces, with
// memory for the open containers and the positions it lists.
//
// The zero value lists nothing and has no limit on depth.
type StreamCheck struct {
	// Long, when positive, makes the check list the strings and containers
	// of at least this many bytes, and stop with StreamTooLong at a number
	// or a run of whitespace that long. MaxDepth, when positive, bounds the
	// nesting. Both are set before the first Feed.
	Long     int
	MaxDepth int

	Strings    []StreamString    // the long strings, in stream order
	Containers []StreamContainer // the long containers, by opening bracket, after StreamEnd
	End        int64             // the offset after the value, after StreamEnd

	off      int64 // offset of the next byte fed
	state    uint8
	depth    int
	objs     []uint64 // one bit per open container: 1 for an object
	frames   []longFrame
	inStr    bool
	isKey    bool // the string being read is a key
	strStart int64
	wsRun    int
	scanned  int // bytes of the number or literal cut by the end of the last piece
}

// longFrame is an open container when Long is set.
type longFrame struct {
	off int64
	n   int
}

// Feed checks b, the bytes that follow the ones it consumed so far, and
// returns how many of them it consumed and the result. With StreamMore,
// the caller feeds the bytes not consumed again with the ones after them;
// fewer than 7 are left, unless a number or literal is cut by the end of b.
// With atEOF, no bytes follow b. A value that does not end with b is then
// StreamInvalid.
func (c *StreamCheck) Feed(b []byte, atEOF bool) (int, int) {
	s := unsafe.String(unsafe.SliceData(b), len(b))
	i := 0
	st := StreamMore
	for st == StreamMore {
		if c.inStr {
			var ok bool
			if i, ok = c.stringBody(s, i, atEOF); !ok {
				st = StreamInvalid
				break
			}
			if c.inStr {
				break
			}
			if c.isKey {
				c.state = sColon
			} else {
				st = c.valueDone(i)
			}
			continue
		}
		if i >= len(s) {
			break
		}
		ch := s[i]
		if ch == ' ' || ch == '\n' || ch == '\t' || ch == '\r' {
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\n' || s[j] == '\t' || s[j] == '\r') {
				j++
			}
			c.wsRun += j - i
			i = j
			if c.Long > 0 && c.wsRun >= c.Long {
				st = StreamTooLong
			}
			continue
		}
		c.wsRun = 0
		switch c.state {
		case sColon:
			if ch != ':' {
				st = StreamInvalid
				continue
			}
			i++
			c.state = sValue
		case sAfter:
			switch ch {
			case ',':
				i++
				if c.objs[(c.depth-1)>>6]>>((c.depth-1)&63)&1 != 0 {
					c.state = sKey
				} else {
					c.state = sValue
				}
				if c.Long > 0 {
					c.frames[c.depth-1].n++
				}
			case ']', '}':
				st = c.closeContainer(ch, i)
				i++
			default:
				st = StreamInvalid
			}
		case sKey, sObjFirst:
			switch {
			case ch == '"':
				if c.state == sObjFirst && c.Long > 0 {
					c.frames[c.depth-1].n = 1
				}
				c.isKey, c.inStr, c.strStart = true, true, c.off+int64(i)
				i++
			case ch == '}' && c.state == sObjFirst:
				st = c.closeContainer(ch, i)
				i++
			default:
				st = StreamInvalid
			}
		default: // sValue, sArrFirst
			if c.state == sArrFirst {
				if ch == ']' {
					st = c.closeContainer(ch, i)
					i++
					continue
				}
				if c.Long > 0 {
					c.frames[c.depth-1].n = 1
				}
			}
			switch ch {
			case '"':
				c.isKey, c.inStr, c.strStart = false, true, c.off+int64(i)
				i++
			case '{', '[':
				st = c.open(ch, i)
				i++
			default:
				j := i + c.scanned
				for j < len(s) && !validDelim[s[j]] {
					j++
				}
				if j == len(s) && !atEOF {
					if c.Long > 0 && j-i >= c.Long {
						st = StreamTooLong
					}
					c.off += int64(i)
					c.scanned = j - i
					return i, st
				}
				c.scanned = 0
				if j == i || !validScalar(s[:j], i) {
					st = StreamInvalid
					continue
				}
				i = j
				st = c.valueDone(i)
			}
		}
	}
	c.off += int64(i)
	if st == StreamMore && atEOF {
		st = StreamInvalid
	}
	if st == StreamEnd {
		slices.SortFunc(c.Containers, func(a, b StreamContainer) int {
			if a.Off < b.Off {
				return -1
			}
			return 1
		})
	}
	return i, st
}

// open enters the container whose bracket ch is at s[i].
func (c *StreamCheck) open(ch byte, i int) int {
	k := c.depth
	if c.MaxDepth > 0 && k >= c.MaxDepth {
		return StreamInvalid
	}
	if k>>6 == len(c.objs) {
		c.objs = append(c.objs, 0)
	}
	if ch == '{' {
		c.objs[k>>6] |= 1 << (k & 63)
		c.state = sObjFirst
	} else {
		c.objs[k>>6] &^= 1 << (k & 63)
		c.state = sArrFirst
	}
	if c.Long > 0 {
		c.frames = append(c.frames, longFrame{off: c.off + int64(i)})
	}
	c.depth = k + 1
	return StreamMore
}

// closeContainer leaves the innermost container at its bracket ch at s[i].
func (c *StreamCheck) closeContainer(ch byte, i int) int {
	k := c.depth - 1
	if (c.objs[k>>6]>>(k&63)&1 != 0) != (ch == '}') {
		return StreamInvalid
	}
	if c.Long > 0 {
		f := c.frames[k]
		c.frames = c.frames[:k]
		if end := c.off + int64(i); end-f.off+1 >= int64(c.Long) {
			c.Containers = append(c.Containers, StreamContainer{f.off, end, f.n})
		}
	}
	c.depth = k
	return c.valueDone(i + 1)
}

// valueDone notes the end of a value, with i the index after it.
func (c *StreamCheck) valueDone(i int) int {
	if c.depth == 0 {
		c.End = c.off + int64(i)
		return StreamEnd
	}
	c.state = sAfter
	return StreamMore
}

// stringBody moves through the string being read from s[i]. It returns the
// index reached and false for a malformed string. c.inStr stays set when
// the string does not end in s.
func (c *StreamCheck) stringBody(s string, i int, atEOF bool) (int, bool) {
	for {
		i = PlainEnd(s, i)
		if i >= len(s) {
			return i, true
		}
		switch s[i] {
		case '"':
			c.inStr = false
			i++
			if c.Long > 0 && c.off+int64(i)-c.strStart >= int64(c.Long) {
				c.Strings = append(c.Strings, StreamString{c.strStart, c.off + int64(i) - 1})
			}
			return i, true
		case '\\':
			n := 2
			if i+1 < len(s) && s[i+1] == 'u' {
				n = 6
			}
			if i+n > len(s) {
				return i, !atEOF
			}
			if n == 6 {
				for _, x := range []byte(s[i+2 : i+6]) {
					if !isHexDigit(x) {
						return i, false
					}
				}
			} else if !validEscapeByte(s[i+1]) {
				return i, false
			}
			i += n
		default:
			return i, false
		}
	}
}

func isHexDigit(c byte) bool {
	return c-'0' < 10 || (c|0x20)-'a' < 6
}

func validEscapeByte(c byte) bool {
	switch c {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return true
	}
	return false
}
