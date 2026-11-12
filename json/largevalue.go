package json

import (
	"errors"
	"io"
	"math"
	"sort"
	"unicode/utf8"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// A value that runs past streamTrigger bytes of a reader that can seek is
// decoded in two passes, without holding the input in memory. The first
// checks the value through a window, and lists its long strings and long
// containers. The second seeks back and decodes through the same window,
// which holds a pair of quotes for each long string. A string is read from
// the reader into its own allocation when the decoder reaches it, and a long
// value that must be contiguous, for RawMessage or a method, is read whole.
//
// Every string, number and whitespace run left in the window is shorter
// than streamLong, so with streamMargin bytes ahead of the read position at
// each loop of the decoder, the tokens up to the next one are all there.
var (
	streamWindow  = 256 << 10
	streamTrigger = 128 << 10
	streamLong    = 16 << 10
	streamMargin  = 5*streamLong + 64
)

// spanHole marks a Span of the window as the quotes that stand for a long
// string, whose index in streamInput.strings is in the bits from spanHoleShift.
const (
	spanHole      = 1 << 2
	spanHoleShift = 8
)

// errInputChanged is the result of a decode that found the input different
// from what the first pass checked.
var errInputChanged = errors.New("json: input changed while it was decoded")

// errStreamed is returned by readValue when dec.large holds a value that
// the first pass checked.
var errStreamed = errors.New("json: value is checked in the stream")

// streamInput is the reader of the second pass. Offsets are from the start
// of the value, with its leading whitespace.
type streamInput struct {
	r          io.ReadSeeker
	base       int64 // reader offset of the value
	end        int64 // end of the value
	strings    []jsonsimd.StreamString
	containers []jsonsimd.StreamContainer
	nextStr    int   // the first string not yet in the window
	nextCont   int   // the first container not yet in the window
	fill       int64 // the next byte for the window
	pos        int64 // where the reader is
	win        []byte
	segs       []streamSeg // where the window follows the input
}

// streamSeg says that the window continues the input at off from index at.
type streamSeg struct {
	at  int
	off int64
}

// offset returns the offset in the value of index i of the window.
func (si *streamInput) offset(i int) int64 {
	k := sort.Search(len(si.segs), func(k int) bool { return si.segs[k].at > i }) - 1
	if k < 0 {
		return int64(i)
	}
	return si.segs[k].off + int64(i-si.segs[k].at)
}

// readAt fills b from offset off of the value.
func (si *streamInput) readAt(b []byte, off int64) error {
	if si.pos != off {
		if _, err := si.r.Seek(si.base+off, io.SeekStart); err != nil {
			return err
		}
		si.pos = off
	}
	n, err := io.ReadFull(si.r, b)
	si.pos += int64(n)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return errInputChanged
	}
	return err
}

// setFill makes the window continue at off.
func (si *streamInput) setFill(off int64) {
	si.fill = off
	si.nextStr = sort.Search(len(si.strings), func(k int) bool { return si.strings[k].Start >= off })
	si.nextCont = sort.Search(len(si.containers), func(k int) bool { return si.containers[k].Off >= off })
}

// fillWindow reads into the window after its first n bytes, and makes it
// the input of d.
func (d *decodeState) fillWindow(n int) {
	si := d.stream
	for n < len(si.win) && si.fill < si.end {
		stop := si.end
		hole := si.nextStr < len(si.strings)
		if hole {
			stop = si.strings[si.nextStr].Start
		}
		if want := int(min(stop-si.fill, int64(len(si.win)-n))); want > 0 {
			if err := si.readAt(si.win[n:n+want], si.fill); err != nil {
				d.fail(err)
			}
			si.segs = append(si.segs, streamSeg{n, si.fill})
			for ; si.nextCont < len(si.containers) && si.containers[si.nextCont].Off < si.fill+int64(want); si.nextCont++ {
				c := &si.containers[si.nextCont]
				d.tab.Containers = append(d.tab.Containers, jsonsimd.Container{
					Off: uint32(n + int(c.Off-si.fill)),
					N:   uint32(c.N),
					// The index of the long container, for skipLarge.
					Next: uint32(si.nextCont),
				})
			}
			n += want
			si.fill += int64(want)
		}
		if hole && si.fill == stop {
			if n+2 > len(si.win) {
				break
			}
			si.win[n], si.win[n+1] = '"', '"'
			d.tab.Spans = append(d.tab.Spans, jsonsimd.Span{
				Start: uint32(n),
				End:   uint32(n + 1),
				Flags: spanHole | uint32(si.nextStr)<<spanHoleShift,
			})
			n += 2
			si.fill = si.strings[si.nextStr].End + 1
			si.nextStr++
			si.segs = append(si.segs, streamSeg{n, si.fill})
		}
	}
	d.data = si.win[:n]
	d.s = bytesView(d.data)
	d.refillAt = n - streamMargin
	if si.fill >= si.end {
		d.refillAt = math.MaxInt
	}
}

// refill moves the rest of the window to its start and reads more. It is
// called where no position in the window is kept.
//
//go:noinline
func (d *decodeState) refill() {
	si := d.stream
	i := d.i
	n := copy(si.win, d.s[i:])

	spans := d.tab.Spans[:0]
	for _, sp := range d.tab.Spans {
		if int(sp.Start) >= i {
			sp.Start -= uint32(i)
			sp.End -= uint32(i)
			spans = append(spans, sp)
		}
	}
	d.tab.Spans = spans
	conts := d.tab.Containers[:0]
	for _, c := range d.tab.Containers {
		if int(c.Off) >= i {
			c.Off -= uint32(i)
			conts = append(conts, c)
		}
	}
	d.tab.Containers = conts
	d.ci, d.si = 0, 0

	off := si.offset(i)
	segs := si.segs[:0]
	segs = append(segs, streamSeg{0, off})
	for _, sg := range si.segs {
		if sg.at > i {
			segs = append(segs, streamSeg{sg.at - i, sg.off})
		}
	}
	si.segs = segs

	d.i = 0
	d.fillWindow(n)
}

// holeAt returns the long string of the Span at i of the window, if any.
func (d *decodeState) holeAt(i int) (jsonsimd.StreamString, bool) {
	if d.si < len(d.tab.Spans) {
		if sp := d.span(i); sp != nil && sp.Flags&spanHole != 0 {
			return d.stream.strings[sp.Flags>>spanHoleShift], true
		}
	}
	return jsonsimd.StreamString{}, false
}

// holeToken returns the bytes of the long string, with its quotes.
func (d *decodeState) holeToken(h jsonsimd.StreamString) []byte {
	b := makeNoZero(int(h.End - h.Start + 1))
	if err := d.stream.readAt(b, h.Start); err != nil {
		d.fail(err)
	}
	return b
}

// holeString returns the value of the long string with the given flags,
// read from the input into its own allocation.
func (d *decodeState) holeString(flags uint32) string {
	si := d.stream
	h := si.strings[flags>>spanHoleShift]
	n := int(h.End - h.Start - 1)
	raw := makeNoZero(n)
	if err := si.readAt(raw, h.Start+1); err != nil {
		d.fail(err)
	}
	var q [1]byte
	if err := si.readAt(q[:], h.End); err != nil {
		d.fail(err)
	}
	s := bytesView(raw)
	if q[0] != '"' {
		d.fail(errInputChanged)
	}
	if jsonsimd.PlainEnd(s, 0) == n && utf8.ValidString(s) {
		return s
	}
	if !validContents(raw) {
		d.fail(errInputChanged)
	}
	return bytesView(unquote(makeNoZero(n)[:0], s))
}

// validContents reports whether b is valid string contents.
func validContents(b []byte) bool {
	var c jsonsimd.StreamCheck
	c.Feed([]byte{'"'}, false)
	used, st := c.Feed(b, false)
	if st != jsonsimd.StreamMore || used != len(b) {
		return false
	}
	_, st = c.Feed([]byte{'"'}, true)
	return st == jsonsimd.StreamEnd
}

// largeAt returns the long container whose bracket is at i of the window.
func (d *decodeState) largeAt(i int) (jsonsimd.StreamContainer, bool) {
	if c := d.container(i); c != nil {
		return d.stream.containers[c.Next], true
	}
	return jsonsimd.StreamContainer{}, false
}

// rawLarge returns the bytes of the value at d.i and moves past it, when the
// value is a long string or a long container. The result is a new
// allocation of its exact size, and a long container takes the place of the
// window until the next refill.
func (d *decodeState) rawLarge() []byte {
	switch d.s[d.i] {
	case '"':
		if h, ok := d.holeAt(d.i); ok {
			d.i += 2
			b := d.holeToken(h)
			if !jsonsimd.Validate(bytesView(b), maxNestingDepth, nil) {
				d.fail(errInputChanged)
			}
			return b
		}
	case '{', '[':
		if c, ok := d.largeAt(d.i); ok {
			b := makeNoZero(int(c.End - c.Off + 1))
			si := d.stream
			if err := si.readAt(b, c.Off); err != nil {
				d.fail(err)
			}
			if !jsonsimd.Validate(bytesView(b), maxNestingDepth-d.depth, nil) {
				d.fail(errInputChanged)
			}
			d.useWhole(b, c.Off)
			return b
		}
	}
	return nil
}

// useWhole makes the bytes b, read from offset off, the input of d, with
// the read position after them.
func (d *decodeState) useWhole(b []byte, off int64) {
	si := d.stream
	d.data, d.s, d.i = b, bytesView(b), len(b)
	si.segs = append(si.segs[:0], streamSeg{0, off})
	d.tab.Containers, d.tab.Spans = d.tab.Containers[:0], d.tab.Spans[:0]
	d.ci, d.si = 0, 0
	si.setFill(off + int64(len(b)))
	d.refillAt = 0
	if si.fill >= si.end {
		d.refillAt = math.MaxInt
	}
}

// skipStream moves past the container at d.i in the second pass.
func (d *decodeState) skipStream() {
	si := d.stream
	if c, ok := d.largeAt(d.i); ok {
		if c.End < si.fill {
			// It ends in the window.
			for k := len(si.segs) - 1; k >= 0; k-- {
				if sg := si.segs[k]; sg.off <= c.End+1 {
					d.i = sg.at + int(c.End+1-sg.off)
					return
				}
			}
		}
		si.segs = append(si.segs[:0], streamSeg{len(d.s), c.End + 1})
		d.i = len(d.s)
		si.setFill(c.End + 1)
		d.refill()
		return
	}
	d.skipChecked()
}

// startsLarge reports whether a value that starts as b does, after optional
// whitespace, can be decoded in two passes.
func startsLarge(b []byte) bool {
	for _, c := range b {
		if !isSpaceByte(c) {
			return c == '{' || c == '[' || c == '"'
		}
	}
	return false
}

// checkLarge runs the first pass on the value at dec.scanp, which did not
// end in dec.buf. It reports false, with the reader and dec.buf as they
// were, when the value is not to be decoded in two passes; then
// dec.noLarge is set. The reads then go on as without the pass, so what is
// buffered at an error is what encoding/json has at hand.
func (dec *Decoder) checkLarge(rs io.ReadSeeker) (bool, error) {
	cur, err := rs.Seek(0, io.SeekCurrent)
	if err != nil {
		dec.noLarge = true
		return false, nil
	}
	base := cur - int64(len(dec.buf)-dec.scanp)
	if cap(dec.buf) < streamWindow {
		b := make([]byte, len(dec.buf), streamWindow)
		copy(b, dec.buf)
		dec.buf = b
	}
	c := jsonsimd.StreamCheck{Long: streamLong, MaxDepth: maxNestingDepth}
	win := dec.buf[:cap(dec.buf)]
	n := copy(win, dec.buf[dec.scanp:])
	var rerr error
	st := jsonsimd.StreamMore
	for {
		used, res := c.Feed(win[:n], rerr != nil)
		st = res
		if st != jsonsimd.StreamMore {
			break
		}
		n = copy(win, win[used:n])
		if rerr != nil {
			break
		}
		var k int
		k, rerr = rs.Read(win[n:])
		n += k
		if rerr != nil && rerr != io.EOF {
			// The bytes before the error may hold the end of the value or
			// a syntax error; a pass over them without the check finds out.
			break
		}
	}
	if st != jsonsimd.StreamEnd {
		// The pass used the space of dec.buf: its bytes are read again,
		// which leaves the reader where it was.
		dec.noLarge = true
		if _, err := rs.Seek(base-int64(dec.scanp), io.SeekStart); err != nil {
			dec.err = err
			return false, err
		}
		if _, err := io.ReadFull(rs, dec.buf); err != nil {
			dec.err = errInputChanged
			return false, errInputChanged
		}
		return false, nil
	}
	if _, err := rs.Seek(base, io.SeekStart); err != nil {
		dec.err = err
		return false, err
	}
	// The offsets in dec.scanned and dec.scanp stay valid for an empty
	// buffer at the start of the value.
	dec.scanned += int64(dec.scanp)
	dec.scanp, dec.keep = 0, 0
	dec.buf = dec.buf[:0]
	dec.large = &streamInput{
		r:          rs,
		base:       base,
		end:        c.End,
		strings:    c.Strings,
		containers: c.Containers,
		win:        win[:streamWindow],
	}
	return true, nil
}

// decodeLarge runs the second pass into v.
func (dec *Decoder) decodeLarge(d *decodeState, v any) error {
	si := dec.large
	dec.large = nil
	err := checkTarget(v)
	if err == nil {
		err = d.decodeLargeInto(si, v)
	}
	if si.pos != si.end {
		if _, serr := si.r.Seek(si.base+si.end, io.SeekStart); serr != nil && err == nil {
			err = serr
		}
	}
	dec.scanned += si.end
	dec.scan.bytes += si.end
	if err == errInputChanged {
		dec.err = err
	}
	return err
}
