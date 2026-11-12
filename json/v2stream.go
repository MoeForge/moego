package json

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// The Decoder of encoding/json on encoding/json/v2 reads no further than
// the token it returns, keeps the errors in the input, and counts offsets
// from the start of the input. These methods run only when jsonv2 is set.
//
// dec.keep is where Buffered starts and what a read keeps: the end of the
// last token or value. More moves dec.scanp, and so InputOffset, past the
// space after it, but not dec.keep.

// fetchV2 reads more input into dec.buf, first dropping what is before
// dec.keep, and returns how many bytes it dropped. The buffer starts with
// 64 bytes and doubles when a quarter of it or less is free, or, when it
// dropped bytes, when more than twice its size has been dropped in all.
// It reads until it gets a byte or an error.
func (dec *Decoder) fetchV2() (int, error) {
	shift := dec.keep
	if shift > 0 {
		n := copy(dec.buf, dec.buf[shift:])
		dec.buf = dec.buf[:n]
		dec.scanned += int64(shift)
		dec.scanp -= shift
		dec.keep = 0
		if dec.scanned/2 > int64(cap(dec.buf)) {
			dec.grow(2 * cap(dec.buf))
		}
	} else if c := cap(dec.buf); c-len(dec.buf) <= c/4 {
		dec.grow(max(64, 2*c))
	}
	// A read of nothing is tried again, into the same space.
	n, err := 0, error(nil)
	for k := 0; n == 0 && err == nil; k++ {
		if k == 100 {
			return shift, io.ErrNoProgress
		}
		n, err = dec.r.Read(dec.buf[len(dec.buf):cap(dec.buf)])
	}
	dec.buf = dec.buf[:len(dec.buf)+n]
	if n > 0 {
		// An error that comes with data is read again.
		err = nil
	}
	return shift, err
}

func (dec *Decoder) grow(n int) {
	b := make([]byte, len(dec.buf), n)
	copy(b, dec.buf)
	dec.buf = b
}

// spaceEndV2 returns the index of the first byte at or after index i that
// is not space, reading more input as needed.
func (dec *Decoder) spaceEndV2(i int) (int, error) {
	for {
		for ; i < len(dec.buf); i++ {
			if !isSpaceByte(dec.buf[i]) {
				return i, nil
			}
		}
		shift, err := dec.fetchV2()
		i -= shift
		if err != nil {
			return i, err
		}
	}
}

// posV2 is the offset of a syntax error at index i of dec.buf: after the
// byte from Go 1.27, at it before.
func (dec *Decoder) posV2(i int) int64 {
	if go127 {
		i++
	}
	return dec.scanned + int64(i)
}

// unexpectedEnd is the error of a token stream that ends after a comma or
// colon, or within a token.
func (dec *Decoder) unexpectedEnd() error {
	return newSyntaxError("unexpected end of JSON input", dec.scanned+int64(len(dec.buf)))
}

// readValueV2 reads the value that starts at dec.scanp until it is
// complete, and returns its length. It reports whether t holds the tables
// of the value. An array, object, string or literal is complete at its
// last byte, a number at the byte after it or at the end of the input.
func (dec *Decoder) readValueV2(t *jsonsimd.Tables) (int, bool, error) {
	dec.scan.reset()
	dec.scan.bytes = dec.scanned + int64(dec.scanp)
	whole := true // the scanner has not started
	checked := 0  // with whole, the bytes validated so far
	scanp := dec.scanp
	for {
		if whole {
			s := dec.buf[dec.scanp:]
			whole = false
			if (s[0] == '{' || s[0] == '[') && len(s) >= 2*checked {
				end := jsonsimd.ValidatePrefix(bytesView(s), maxNestingDepth, t)
				if end >= 0 {
					return end, true, nil
				}
				if end == jsonsimd.PrefixIncomplete {
					checked = len(s)
					whole = true
				}
			}
		}
		if !whole {
			j, res := dec.scan.run(dec.buf, scanp)
			dec.scan.bytes += int64(j - scanp)
			switch res {
			case scanEnd:
				return j - dec.scanp, false, nil
			case scanError:
				msg, off := dec.scan.v2SyntaxError(dec.buf, j, synUnmarshal)
				return 0, false, newSyntaxError(msg, dec.scan.bytes-int64(j)+off)
			}
			scanp = j
		}
		n := scanp - dec.scanp
		if len(dec.buf)-dec.scanp >= streamTrigger && !dec.noLarge {
			if rs, ok := dec.r.(io.ReadSeeker); ok && startsLarge(dec.buf[dec.scanp:]) {
				ok, lerr := dec.checkLarge(rs)
				if ok {
					return 0, false, errStreamed
				}
				if lerr != nil {
					return 0, false, lerr
				}
			}
		}
		_, err := dec.fetchV2()
		scanp = dec.scanp + n
		if err != nil {
			if err == io.EOF && dec.numberEndV2(whole) {
				// The value goes on in what a later read gives.
				whole, scanp = false, len(dec.buf)
				continue
			}
			if err != io.EOF {
				return 0, false, dec.readErrorV2(err)
			}
			if !whole && dec.scan.step(' ') == scanEnd {
				return scanp - dec.scanp, false, nil
			}
			return 0, false, io.ErrUnexpectedEOF
		}
	}
}

// numberEndV2 reports whether the end of the input ends a number in an
// array or object of the value at dec.scanp: encoding/json on
// encoding/json/v2 then reads on, as it does past a space after it. When
// whole is set the scanner has not read the buffered bytes yet.
//
//go:noinline
func (dec *Decoder) numberEndV2(whole bool) bool {
	if whole {
		j, res := dec.scan.run(dec.buf, dec.scanp)
		dec.scan.bytes += int64(j - dec.scanp)
		if res != scanContinue {
			return false
		}
	}
	switch dec.scan.state {
	case st1, st0, stDot0, stE0:
		if len(dec.scan.stack) > 0 {
			dec.scan.step(' ')
			return true
		}
	}
	return false
}

// readErrorV2 is the error of reading the value at dec.scanp when the
// reader fails with err. Before Go 1.27, an array or object reports a
// syntax error where the end of the input would be, unless the input ends
// between its members.
func (dec *Decoder) readErrorV2(err error) error {
	s := dec.buf[dec.scanp:]
	if go127 || s[0] != '[' && s[0] != '{' {
		return err
	}
	var sc scanner
	sc.reset()
	for _, c := range s {
		sc.step(c)
	}
	switch sc.state {
	case st1, st0, stDot0, stE0:
		if dec.scan.state == stEndValue {
			// An end of the input before the read ended the number.
			sc.step(' ')
		}
	}
	switch top := sc.stack[len(sc.stack)-1]; {
	case len(sc.stack) == 1 && top == inArr && (sc.state == stBeginValueOrEmpty || sc.state == stBeginValue || sc.state == stEndValue),
		len(sc.stack) == 1 && top == inObjKey && sc.state != stEndValue,
		len(sc.stack) == 1 && top == inObjVal && sc.state == stEndValue:
		return err
	}
	// Where the end of the input would be reported, or at the start of a
	// number the input may continue.
	_, off := sc.v2SyntaxError(s, len(s), synUnmarshal)
	switch sc.state {
	case st1, st0, stDot0, stE0:
		off = int64(len(s))
		for off > 0 && strings.IndexByte("0123456789.eE+-", s[off-1]) >= 0 {
			off--
		}
	}
	return newSyntaxError("jsontext: read error: "+err.Error(), dec.scanned+int64(dec.scanp)+off)
}

// valueV2 reads the value at index i of dec.buf and decodes it into v. It
// returns the error of reading it, after which the value is not consumed,
// or of decoding it.
func (dec *Decoder) valueV2(i int, v any) (rerr, derr error) {
	dec.scanp = i
	d := getDecodeState()
	n, haveTab, err := dec.readValueV2(&d.tab)
	if err == errStreamed {
		d.useNumber, d.disallowUnknown = dec.useNumber, dec.disallowUnknown
		err = dec.decodeLarge(d, v)
		putDecodeState(d)
		dec.keep = dec.scanp
		if err == errInputChanged {
			return err, nil
		}
		return nil, err
	}
	if err != nil {
		putDecodeState(d)
		return err, nil
	}
	data := dec.buf[dec.scanp : dec.scanp+n]
	dec.scanp += n
	dec.keep = dec.scanp
	if !haveTab {
		jsonsimd.Validate(bytesView(data), maxNestingDepth, &d.tab)
	}
	d.useNumber, d.disallowUnknown = dec.useNumber, dec.disallowUnknown
	err = d.decodeValid(data, v)
	putDecodeState(d)
	return nil, err
}

// isSeparator reports whether the token state expects c, a comma or colon.
func (dec *Decoder) isSeparator(c byte) bool {
	switch dec.tokenState {
	case tokenArrayComma, tokenObjectComma:
		return c == ','
	case tokenObjectColon:
		return c == ':'
	}
	return false
}

// separatorV2 checks the byte at index i of dec.buf where the token state
// expects a comma or colon, and returns the index of the token after it
// and the state after it. A closing bracket after a comma is an error at
// the comma.
func (dec *Decoder) separatorV2(i int) (int, int, error) {
	state := dec.tokenState
	c := dec.buf[i]
	switch {
	case c == ',' && state == tokenArrayComma:
		state = tokenArrayValue
	case c == ',' && state == tokenObjectComma:
		state = tokenObjectKey
	case c == ':' && state == tokenObjectColon:
		state = tokenObjectValue
	default:
		return i, state, nil
	}
	at := dec.posV2(i)
	next := dec.scanned + int64(i) + 1
	i, err := dec.spaceEndV2(i + 1)
	if err != nil {
		// The separator stays read.
		dec.tokenState, dec.sepNext = state, next
		return i, state, err
	}
	if c := dec.buf[i]; (c == ']' || c == '}') && state != tokenObjectValue {
		return i, state, newSyntaxError("invalid character ',' looking for beginning of value", at)
	}
	return i, state, nil
}

// nextV2 returns the index of the next token for Token or Decode, past a
// comma or colon, and the token state there.
func (dec *Decoder) nextV2() (int, int, error) {
	if dec.sepNext != 0 {
		return dec.afterSeparatorV2()
	}
	i, err := dec.spaceEndV2(dec.scanp)
	if err != nil {
		return i, dec.tokenState, err
	}
	return dec.separatorV2(i)
}

// afterSeparatorV2 is nextV2 after a read past a comma or colon failed. A
// closing bracket after a comma is then an error at the comma, as it is
// for separatorV2.
func (dec *Decoder) afterSeparatorV2() (int, int, error) {
	i, err := dec.spaceEndV2(int(dec.sepNext - dec.scanned))
	if err == nil && (dec.buf[i] == ']' || dec.buf[i] == '}') && dec.tokenState != tokenObjectValue {
		err = newSyntaxError("invalid character ',' looking for beginning of value", dec.posV2(int(dec.sepNext-dec.scanned)-1))
	}
	return i, dec.tokenState, err
}

// pastSeparatorV2 reads on to the next byte that is not space when the
// byte at index i of dec.buf is a comma or colon the token state does not
// take, unless it comes just after one it took: encoding/json on
// encoding/json/v2 reads that far before it reports the byte, whatever
// the read gives, but not once a Decode or More did, for Decode when
// decode is set. It returns the index of the byte, and notes it in
// dec.sepAt.
func (dec *Decoder) pastSeparatorV2(i, state int, decode bool) int {
	switch c := dec.buf[i]; {
	case c != ',' && c != ':',
		state == tokenArrayValue, state == tokenObjectKey, state == tokenObjectValue:
		dec.sepRead = false
		return i
	}
	at := dec.scanned + int64(i)
	dec.sepAt = at + 1
	if !dec.sepRead {
		dec.spaceEndV2(i + 1)
	}
	dec.sepRead = dec.sepRead || decode
	return int(at - dec.scanned)
}

// keptErrorV2 returns the kept error for Token, or Decode when decode is
// set. When it is at a comma or colon the token state does not take, it
// first reads on past the space after it as pastSeparatorV2 does.
func (dec *Decoder) keptErrorV2(decode bool) error {
	if dec.errSep != 0 {
		if !dec.sepRead {
			dec.spaceEndV2(int(dec.errSep - dec.scanned))
		}
		dec.sepRead = dec.sepRead || decode
	}
	return dec.err
}

// unexpectedV2 is the syntax error for the byte at index i of dec.buf in
// the token state, for Decode when decode is set, else for Token. A byte
// that can start a value but not a member name is told apart from one that
// can start nothing.
func (dec *Decoder) unexpectedV2(i, state int, decode bool) error {
	c := dec.buf[i]
	var context string
	switch state {
	case tokenArrayComma, tokenObjectComma:
		if c == ':' {
			// A colon before a closing bracket is taken as a separator
			// with no value after it. pastSeparatorV2 or More read on past
			// the space after it.
			j := i + 1
			for j < len(dec.buf) && isSpaceByte(dec.buf[j]) {
				j++
			}
			if j < len(dec.buf) && (dec.buf[j] == ']' || dec.buf[j] == '}') {
				context = " looking for beginning of value"
				break
			}
		}
		if state == tokenArrayComma {
			context = " after array element"
		} else {
			context = " after object key:value pair"
		}
	case tokenObjectColon:
		context = " after object key"
	case tokenObjectStart, tokenObjectKey:
		if startsValue(c) {
			// A literal or number, and for Decode an array or object, is
			// read first, and its own syntax error wins.
			err := newSyntaxError("object member name must be a string", dec.posV2(i))
			if c != '{' && c != '[' || decode {
				dec.scanp = i
				noLarge := dec.noLarge
				dec.noLarge = true
				_, _, rerr := dec.readValueV2(nil)
				dec.noLarge = noLarge
				if rerr != nil {
					return closerContext(rerr, state)
				}
			}
			return err
		}
		fallthrough
	default:
		context = " looking for beginning of value"
	}
	q, n := quoteCharV2(dec.buf[i:])
	at := dec.scanned + int64(i)
	if go127 {
		at += int64(n)
	}
	return newSyntaxError("invalid character "+q+context, at)
}

// openV2 reports whether the syntax error of the byte at index i of
// dec.buf in the token state depends on what a later read gives. The byte
// after a colon after an element or member with no byte but space after
// it decides whether unexpectedV2 reports the colon after an element or
// member or where a value should start. A number where a member name
// should start that runs to the end of the buffer is read to its end
// first, and that read may fail.
//
//go:noinline
func (dec *Decoder) openV2(i, state int) bool {
	switch c := dec.buf[i]; {
	case c == ':' && (state == tokenArrayComma || state == tokenObjectComma):
		for i++; i < len(dec.buf); i++ {
			if !isSpaceByte(dec.buf[i]) {
				return false
			}
		}
		return true
	case (c == '-' || '0' <= c && c <= '9') && (state == tokenObjectStart || state == tokenObjectKey):
		for i++; i < len(dec.buf); i++ {
			if strings.IndexByte("0123456789.eE+-", dec.buf[i]) < 0 {
				return false
			}
		}
		return true
	}
	return false
}

// keepColonV2 is More past a comma or colon at dec.scanp that the token
// state does not take. It keeps the error of a colon after an element or
// member, which the byte after it, read or not, decides, unless an error
// is kept, and reports what More reports: true, but for the end of the
// input kept.
//
// It and openV2 are kept out of line, so that the frames and the code of
// Token and More around them stay as they are.
//
//go:noinline
func (dec *Decoder) keepColonV2() bool {
	if dec.err == nil && dec.buf[dec.scanp] == ':' && (dec.tokenState == tokenArrayComma || dec.tokenState == tokenObjectComma) {
		dec.err = dec.unexpectedV2(dec.scanp, dec.tokenState, false)
	}
	return dec.err != io.EOF
}

// eofKept is dec.sepNext once Decode or More kept the end of the input,
// which they meet only outside an array or object.
const eofKept = -1

// moreEOFV2 is More once the end of the input is kept. A byte that starts
// no value then ends the input for encoding/json on encoding/json/v2 too.
//
//go:noinline
func (dec *Decoder) moreEOFV2() bool {
	dec.sepNext = 0
	more := dec.moreV2()
	dec.sepNext = eofKept
	return more && startsValue(dec.buf[dec.scanp])
}

// endOffsetV2 is InputOffset once a Token met the end of the input in an
// array or object, until a Token or Decode reads on. From Go 1.27,
// encoding/json on encoding/json/v2 then counts the space after the last
// token, and a comma or colon after it, when a byte other than space has
// been read after it.
//
//go:noinline
func (dec *Decoder) endOffsetV2() int64 {
	if dec.eofAt != dec.scanned+int64(dec.keep)+1 {
		return dec.scanned + int64(dec.scanp)
	}
	i := dec.keep
	for i < len(dec.buf) && isSpaceByte(dec.buf[i]) {
		i++
	}
	switch {
	case i == len(dec.buf):
		i = dec.keep
	case dec.buf[i] == ',' || dec.buf[i] == ':':
		i++
	}
	return dec.scanned + int64(i)
}

// closerContext returns err, the error of reading a value that Decode
// takes in the token state, with the context of a closing bracket where a
// value should start as encoding/json on encoding/json/v2 gives it: that of
// the state Decode was called in, not that of the value.
func closerContext(err error, state int) error {
	se, ok := err.(*SyntaxError)
	if !ok {
		return err
	}
	switch msg := se.Error(); {
	case msg == "invalid character ']' looking for beginning of value" && (state == tokenObjectValue || state == tokenObjectKey):
		return newSyntaxError("invalid character ']' after object key:value pair", se.Offset)
	case msg == "invalid character '}' looking for beginning of value" && state == tokenArrayValue:
		return newSyntaxError("invalid character '}' after array element", se.Offset)
	}
	return err
}

// closerV2 is the syntax error for the closing bracket at index i of
// dec.buf where the token state expects a value, for Token when token is
// set, else for Decode.
func (dec *Decoder) closerV2(i, state int, token bool) error {
	c := dec.buf[i]
	msg := "invalid character " + quoteChar(c) + " looking for beginning of value"
	if state == tokenObjectValue {
		switch {
		case c == ']':
			msg = "invalid character ']' after object key:value pair"
		case token:
			msg = "missing value after object key"
		}
	}
	return newSyntaxError(msg, dec.posV2(i))
}

func startsValue(c byte) bool {
	switch c {
	case '{', '[', '"', '-', 't', 'f', 'n':
		return true
	}
	return '0' <= c && c <= '9'
}

// quoteCharV2 quotes the character at the start of b as encoding/json on
// encoding/json/v2 does in a syntax error, and returns its length: a byte
// that is not part of valid UTF-8 as a hexadecimal escape.
func quoteCharV2(b []byte) (string, int) {
	if b[0] < utf8.RuneSelf {
		return quoteChar(b[0]), 1
	}
	r, n := utf8.DecodeRune(b)
	if r == utf8.RuneError && n == 1 {
		return fmt.Sprintf(`'\x%02x'`, b[0]), 1
	}
	return strconv.QuoteRune(r), n
}

func valueAllowed(state int) bool {
	switch state {
	case tokenTopValue, tokenArrayStart, tokenArrayValue, tokenObjectValue:
		return true
	}
	return false
}

// decodeV2 is Decode. Every error but that of decoding a value it read is
// kept in dec.err and returned again, by Token too.
func (dec *Decoder) decodeV2(v any) error {
	if dec.err != nil {
		return dec.keptErrorV2(true)
	}
	off := dec.scanned + int64(dec.scanp)
	dec.sepAt = 0
	fail := func(err error) error {
		dec.scanp = int(off - dec.scanned)
		dec.err, dec.errSep = err, dec.sepAt
		return err
	}
	i, state, err := dec.nextV2()
	if err != nil {
		if err == io.EOF && len(dec.tokenStack) > 0 {
			err = io.ErrUnexpectedEOF
		} else if err == io.EOF {
			dec.sepNext = eofKept
		}
		return fail(err)
	}
	i = dec.pastSeparatorV2(i, state, true)
	key := state == tokenObjectStart || state == tokenObjectKey
	switch c := dec.buf[i]; {
	case key && c != '"':
		if c == '}' && state == tokenObjectStart {
			return fail(newSyntaxError("invalid character '}' looking for beginning of value", dec.posV2(i)))
		}
		return fail(dec.unexpectedV2(i, state, true))
	case !key && !valueAllowed(state) || c == ']' || c == '}':
		switch {
		case (c == ']' || c == '}') && valueAllowed(state):
			return fail(dec.closerV2(i, state, false))
		case c == ']' && state == tokenArrayComma, c == '}' && state == tokenObjectComma:
			return fail(newSyntaxError("invalid character "+quoteChar(c)+" looking for beginning of value", dec.posV2(i)))
		}
		return fail(dec.unexpectedV2(i, state, true))
	}
	rerr, derr := dec.valueV2(i, v)
	if rerr != nil {
		return fail(closerContext(rerr, state))
	}
	dec.tokenState, dec.sepNext = state, 0
	if key {
		dec.tokenState = tokenObjectColon
	} else {
		dec.tokenValueEnd()
	}
	return derr
}

// tokenV2 is Token. A syntax error is kept and returned again, but for one
// at a bracket where Decode would read a value, or at a colon after an
// element or member with no byte read after it; the end of the input or an
// error of the reader is not.
func (dec *Decoder) tokenV2() (Token, error) {
	if dec.err != nil {
		return nil, dec.keptErrorV2(false)
	}
	off := dec.scanned + int64(dec.scanp)
	dec.sepAt = 0
	fail := func(err error) (Token, error) {
		dec.scanp = int(off - dec.scanned)
		if _, ok := err.(*SyntaxError); ok {
			dec.err, dec.errSep = err, dec.sepAt
		}
		return nil, err
	}
	// The error of a bracket where Decode would read a value is not kept:
	// a Decode reads the value, and reports what it finds.
	pass := func(err error) (Token, error) {
		dec.scanp = int(off - dec.scanned)
		return nil, err
	}
	i, state, err := dec.nextV2()
	if err != nil {
		if err == io.EOF && go127 && len(dec.tokenStack) > 0 {
			// From Go 1.27 InputOffset then counts what follows.
			dec.eofAt = dec.scanned + int64(dec.keep) + 1
		}
		return fail(err)
	}
	i = dec.pastSeparatorV2(i, state, false)
	c := dec.buf[i]
	switch c {
	case '[', '{':
		if !valueAllowed(state) {
			if state == tokenObjectStart || state == tokenObjectKey {
				return pass(dec.unexpectedV2(i, state, false))
			}
			return fail(dec.unexpectedV2(i, state, false))
		}
		dec.tokenStack = append(dec.tokenStack, state)
		dec.tokenState = tokenArrayStart
		if c == '{' {
			dec.tokenState = tokenObjectStart
		}
		dec.scanp, dec.keep, dec.sepNext = i+1, i+1, 0
		return Delim(c), nil
	case ']', '}':
		open, after := tokenArrayStart, tokenArrayComma
		if c == '}' {
			open, after = tokenObjectStart, tokenObjectComma
		}
		if state != open && state != after {
			if valueAllowed(state) {
				if state == tokenObjectValue && c == '}' {
					// "missing value after object key".
					return pass(dec.closerV2(i, state, true))
				}
				return fail(dec.closerV2(i, state, true))
			}
			return fail(dec.unexpectedV2(i, state, false))
		}
		dec.tokenState = dec.tokenStack[len(dec.tokenStack)-1]
		dec.tokenStack = dec.tokenStack[:len(dec.tokenStack)-1]
		dec.tokenValueEnd()
		dec.scanp, dec.keep, dec.sepNext = i+1, i+1, 0
		return Delim(c), nil
	}
	key := state == tokenObjectStart || state == tokenObjectKey
	if key && c != '"' || !key && !valueAllowed(state) {
		if dec.openV2(i, state) {
			// The error depends on what a later call may read.
			return pass(dec.unexpectedV2(i, state, false))
		}
		return fail(dec.unexpectedV2(i, state, false))
	}
	var x any
	var s string
	var rerr, derr error
	start := dec.scanned + int64(i)
	if key {
		rerr, derr = dec.valueV2(i, &s)
		x = s
	} else {
		rerr, derr = dec.valueV2(i, &x)
	}
	if rerr != nil {
		return fail(rerr)
	}
	dec.tokenState, dec.sepNext = state, 0
	if key {
		dec.tokenState = tokenObjectColon
	} else {
		dec.tokenValueEnd()
	}
	if derr != nil && go127 {
		// A number too large for a float64, whose offset is from the
		// start of the input. Before Go 1.27 the token is the largest
		// float64 of its sign.
		if e, ok := derr.(*UnmarshalTypeError); ok {
			e.Offset += start
		}
		return nil, derr
	}
	return x, nil
}

// moreV2 is More. It looks at the next token, past a comma or colon,
// reading as needed even after an error. The first error it meets is kept
// in dec.err for Token and Decode, and it reports true for any but the end
// of the input outside an array or object. Otherwise it reports whether
// the token is there and does not end an array or object; a closing
// bracket after a colon ends it, one after a comma does not.
func (dec *Decoder) moreV2() bool {
	start := dec.scanp
	if dec.sepNext != 0 {
		if dec.sepNext == eofKept {
			return dec.moreEOFV2()
		}
		// A Token or Decode read the comma or colon: the offset moves to
		// it, past the space before it.
		start = int(dec.sepNext - dec.scanned)
		dec.scanp = start - 1
	}
	i, err := dec.spaceEndV2(start)
	if err == nil {
		if dec.sepNext == 0 {
			dec.scanp = i
		}
		switch c := dec.buf[i]; {
		case c == ']' || c == '}':
			// After a member name, or after a comma a Token or Decode
			// read, the bracket is an error for later.
			if dec.sepNext != 0 {
				return dec.tokenState != tokenObjectValue
			}
			return dec.tokenState == tokenObjectColon
		case dec.sepNext == 0 && (c == ',' || c == ':'):
			// It looks past any, but keeps an error only past one that
			// the state expects.
			sep := dec.isSeparator(c)
			dec.sepRead = dec.sepRead || !sep
			var j int
			if j, err = dec.spaceEndV2(i + 1); err == io.EOF && sep {
				err = dec.unexpectedEnd()
			} else if !sep {
				return dec.keepColonV2()
			} else if err == nil && c == ':' && (dec.buf[j] == ']' || dec.buf[j] == '}') {
				return false
			}
		}
		if err == nil {
			return true
		}
	}
	if err == io.EOF && len(dec.tokenStack) > 0 {
		err = dec.unexpectedEnd()
	}
	if dec.err == nil {
		dec.err = err
		if err == io.EOF {
			dec.sepNext = eofKept
		}
	}
	return dec.err != io.EOF
}
