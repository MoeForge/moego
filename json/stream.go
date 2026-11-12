package json

import (
	"bytes"
	"io"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// A Decoder reads and decodes JSON values from an input stream.
type Decoder struct {
	r       io.Reader
	buf     []byte
	scanp   int   // start of unread data in buf
	scanned int64 // bytes of r before buf[0]
	scan    scanner
	err     error

	useNumber       bool
	disallowUnknown bool

	tokenState int
	tokenStack []int

	large   *streamInput // a value that the first pass checked
	noLarge bool         // decode values from the buffer alone
	// On encoding/json/v2: whether a Decode or More read past the comma or
	// colon at the current place (see sepAt), which no Token or Decode then
	// reads past again.
	sepRead bool

	// On encoding/json/v2: where Buffered starts, and, when not 0, the
	// offset after a comma or colon that a Token or Decode read but failed
	// to read past, where the next one starts.
	keep    int
	sepNext int64
	// On encoding/json/v2: one more than the offset of a comma or colon
	// the token state does not take, at the byte of this call (sepAt) or
	// of the kept error (errSep), else 0.
	sepAt, errSep int64
	// On encoding/json/v2 from Go 1.27: one more than the offset of
	// dec.keep when a Token met the end of the input in an array or object,
	// which holds until a Token or Decode reads on, else 0.
	eofAt int64
}

// NewDecoder returns a new decoder that reads from r.
//
// The decoder introduces its own buffering and may read data from r beyond
// the JSON values requested.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: r}
}

// UseNumber causes the Decoder to unmarshal a number into an interface
// value as a Number instead of as a float64.
func (dec *Decoder) UseNumber() { dec.useNumber = true }

// DisallowUnknownFields causes the Decoder to return an error when the
// destination is a struct and the input contains object keys which do not
// match any non-ignored, exported fields in the destination.
func (dec *Decoder) DisallowUnknownFields() { dec.disallowUnknown = true }

// Decode reads the next JSON-encoded value from its input and stores it in
// the value pointed to by v.
//
// See the documentation for Unmarshal for details about the conversion of
// JSON into a Go value.
func (dec *Decoder) Decode(v any) error {
	if jsonv2 {
		return dec.decodeV2(v)
	}
	if dec.err != nil {
		return dec.err
	}
	if err := dec.tokenPrepareForDecode(); err != nil {
		return err
	}
	if !dec.tokenValueAllowed() {
		return newSyntaxError("not at beginning of value", dec.InputOffset())
	}
	d := getDecodeState()
	n, haveTab, err := dec.readValue(&d.tab)
	if err == errStreamed {
		d.useNumber, d.disallowUnknown = dec.useNumber, dec.disallowUnknown
		err = dec.decodeLarge(d, v)
		putDecodeState(d)
		dec.tokenValueEnd()
		return err
	}
	if err != nil {
		putDecodeState(d)
		return err
	}
	data := dec.buf[dec.scanp : dec.scanp+n]
	dec.scanp += n
	if !haveTab {
		jsonsimd.Validate(bytesView(data), maxNestingDepth, &d.tab)
	}
	d.useNumber, d.disallowUnknown = dec.useNumber, dec.disallowUnknown
	err = d.decodeValid(data, v)
	putDecodeState(d)
	dec.tokenValueEnd()
	return err
}

// Buffered returns a reader of the data remaining in the Decoder's buffer.
// The reader is valid until the next call to Decode.
func (dec *Decoder) Buffered() io.Reader {
	if jsonv2 {
		return bytes.NewReader(dec.buf[dec.keep:])
	}
	return bytes.NewReader(dec.buf[dec.scanp:])
}

// InputOffset returns the input stream byte offset of the current decoder
// position. The offset gives the location of the end of the most recently
// returned token and the beginning of the next token.
func (dec *Decoder) InputOffset() int64 {
	if jsonv2 && go127 && dec.eofAt != 0 {
		return dec.endOffsetV2()
	}
	return dec.scanned + int64(dec.scanp)
}

// readValue reads a whole JSON value into dec.buf and returns its length
// from dec.scanp. It reports whether t holds the tables of the value.
//
// While the value is an array or object and each read at least doubles
// the data buffered, the data is checked with one validating pass from the
// start of the value. Otherwise the scanner checks each byte once. Either
// way every buffered byte is checked before the next read, so a syntax
// error is reported as soon as it is read.
func (dec *Decoder) readValue(t *jsonsimd.Tables) (int, bool, error) {
	dec.scan.reset()
	startBytes := dec.scan.bytes
	whole := true // the scanner has not started
	checked := 0  // with whole, the bytes validated so far
	scanp := dec.scanp
	var err error
	for {
		if whole {
			s := dec.buf[dec.scanp:]
			k := 0
			for k < len(s) && isSpaceByte(s[k]) {
				k++
			}
			switch {
			case k == len(s) && err == nil:
				// Only space so far.
			case k < len(s) && (s[k] == '{' || s[k] == '[') && len(s) >= 2*checked:
				end := jsonsimd.ValidatePrefix(bytesView(s), maxNestingDepth, t)
				if end >= 0 {
					dec.scan.bytes += int64(end)
					return end, true, nil
				}
				if end == jsonsimd.PrefixIncomplete && err == nil {
					checked = len(s)
					break
				}
				whole = false
			default:
				whole = false
			}
		}
		if !whole {
			j, res := dec.scan.run(dec.buf, scanp)
			dec.scan.bytes += int64(j - scanp)
			switch res {
			case scanEnd:
				return j - dec.scanp, false, nil
			case scanError:
				dec.scan.bytes++
				dec.err = newSyntaxError(dec.scan.err, dec.scan.bytes)
				return 0, false, dec.err
			}
			scanp = j
		}
		if err != nil {
			if err == io.EOF {
				if !whole && dec.scan.step(' ') == scanEnd {
					return scanp - dec.scanp, false, nil
				}
				if nonSpace(dec.buf[dec.scanp:]) {
					err = io.ErrUnexpectedEOF
				}
			}
			dec.err = err
			return 0, false, err
		}
		n := scanp - dec.scanp
		if len(dec.buf)-dec.scanp >= streamTrigger && !dec.noLarge {
			if rs, ok := dec.r.(io.ReadSeeker); ok && startsLarge(dec.buf[dec.scanp:]) {
				ok, lerr := dec.checkLarge(rs)
				if ok {
					// decodeLarge counts the bytes of the value.
					dec.scan.bytes = startBytes
					return 0, false, errStreamed
				}
				if lerr != nil {
					return 0, false, lerr
				}
			}
		}
		err = dec.refill()
		scanp = dec.scanp + n
	}
}

// refill reads more data into dec.buf, first moving the unread data to the
// front and growing the buffer when less than 512 bytes are free.
func (dec *Decoder) refill() error {
	if dec.scanp > 0 {
		dec.scanned += int64(dec.scanp)
		n := copy(dec.buf, dec.buf[dec.scanp:])
		dec.buf = dec.buf[:n]
		dec.scanp = 0
	}
	const minRead = 512
	if cap(dec.buf)-len(dec.buf) < minRead {
		b := make([]byte, len(dec.buf), 2*cap(dec.buf)+minRead)
		copy(b, dec.buf)
		dec.buf = b
	}
	n, err := dec.r.Read(dec.buf[len(dec.buf):cap(dec.buf)])
	dec.buf = dec.buf[:len(dec.buf)+n]
	return err
}

func nonSpace(b []byte) bool {
	for _, c := range b {
		if !isSpaceByte(c) {
			return true
		}
	}
	return false
}

// Token states: what the decoder expects next.
const (
	tokenTopValue = iota
	tokenArrayStart
	tokenArrayValue
	tokenArrayComma
	tokenObjectStart
	tokenObjectKey
	tokenObjectColon
	tokenObjectValue
	tokenObjectComma
)

// tokenPrepareForDecode reads the comma or colon that must come before a
// value that Decode reads in the middle of a Token stream.
func (dec *Decoder) tokenPrepareForDecode() error {
	switch dec.tokenState {
	case tokenArrayComma:
		c, err := dec.peek()
		if err != nil {
			return err
		}
		if c != ',' {
			return newSyntaxError("expected comma after array element", dec.InputOffset())
		}
		dec.scanp++
		dec.tokenState = tokenArrayValue
	case tokenObjectColon:
		c, err := dec.peek()
		if err != nil {
			return err
		}
		if c != ':' {
			return newSyntaxError("expected colon after object key", dec.InputOffset())
		}
		dec.scanp++
		dec.tokenState = tokenObjectValue
	}
	return nil
}

func (dec *Decoder) tokenValueAllowed() bool {
	switch dec.tokenState {
	case tokenTopValue, tokenArrayStart, tokenArrayValue, tokenObjectValue:
		return true
	}
	return false
}

func (dec *Decoder) tokenValueEnd() {
	switch dec.tokenState {
	case tokenArrayStart, tokenArrayValue:
		dec.tokenState = tokenArrayComma
	case tokenObjectValue:
		dec.tokenState = tokenObjectComma
	}
}

// Token returns the next JSON token in the input stream. At the end of the
// input stream, Token returns nil, io.EOF.
//
// Token guarantees that the delimiters [ ] { } it returns are properly
// nested and matched: if Token encounters an unexpected delimiter in the
// input, it will return an error.
//
// The input stream consists of basic JSON values—bool, string, number, and
// null—along with delimiters [ ] { } of type Delim to mark the start and
// end of arrays and objects. Commas and colons are elided.
func (dec *Decoder) Token() (Token, error) {
	if jsonv2 {
		return dec.tokenV2()
	}
	for {
		c, err := dec.peek()
		if err != nil {
			return nil, err
		}
		switch c {
		case '[':
			if !dec.tokenValueAllowed() {
				return dec.tokenError(c)
			}
			dec.scanp++
			dec.tokenStack = append(dec.tokenStack, dec.tokenState)
			dec.tokenState = tokenArrayStart
			return Delim('['), nil
		case ']':
			if dec.tokenState != tokenArrayStart && dec.tokenState != tokenArrayComma {
				return dec.tokenError(c)
			}
			dec.scanp++
			dec.tokenState = dec.tokenStack[len(dec.tokenStack)-1]
			dec.tokenStack = dec.tokenStack[:len(dec.tokenStack)-1]
			dec.tokenValueEnd()
			return Delim(']'), nil
		case '{':
			if !dec.tokenValueAllowed() {
				return dec.tokenError(c)
			}
			dec.scanp++
			dec.tokenStack = append(dec.tokenStack, dec.tokenState)
			dec.tokenState = tokenObjectStart
			return Delim('{'), nil
		case '}':
			if dec.tokenState != tokenObjectStart && dec.tokenState != tokenObjectComma {
				return dec.tokenError(c)
			}
			dec.scanp++
			dec.tokenState = dec.tokenStack[len(dec.tokenStack)-1]
			dec.tokenStack = dec.tokenStack[:len(dec.tokenStack)-1]
			dec.tokenValueEnd()
			return Delim('}'), nil
		case ':':
			if dec.tokenState != tokenObjectColon {
				return dec.tokenError(c)
			}
			dec.scanp++
			dec.tokenState = tokenObjectValue
			continue
		case ',':
			switch dec.tokenState {
			case tokenArrayComma:
				dec.scanp++
				dec.tokenState = tokenArrayValue
				continue
			case tokenObjectComma:
				dec.scanp++
				dec.tokenState = tokenObjectKey
				continue
			}
			return dec.tokenError(c)
		case '"':
			if dec.tokenState == tokenObjectStart || dec.tokenState == tokenObjectKey {
				var x string
				old := dec.tokenState
				dec.tokenState = tokenTopValue
				err := dec.Decode(&x)
				dec.tokenState = old
				if err != nil {
					return nil, err
				}
				dec.tokenState = tokenObjectColon
				return x, nil
			}
		}
		if !dec.tokenValueAllowed() {
			return dec.tokenError(c)
		}
		var x any
		if err := dec.Decode(&x); err != nil {
			return nil, err
		}
		return x, nil
	}
}

func (dec *Decoder) tokenError(c byte) (Token, error) {
	var context string
	switch dec.tokenState {
	case tokenTopValue, tokenArrayStart, tokenArrayValue, tokenObjectValue:
		context = " looking for beginning of value"
	case tokenArrayComma:
		context = " after array element"
	case tokenObjectKey:
		context = " looking for beginning of object key string"
	case tokenObjectColon:
		context = " after object key"
	case tokenObjectComma:
		context = " after object key:value pair"
	}
	return nil, newSyntaxError("invalid character "+quoteChar(c)+context, dec.InputOffset())
}

// More reports whether there is another element in the current array or
// object being parsed.
func (dec *Decoder) More() bool {
	if jsonv2 {
		return dec.moreV2()
	}
	c, err := dec.peek()
	return err == nil && c != ']' && c != '}'
}

// peek returns the next byte that is not space, leaving it unread.
func (dec *Decoder) peek() (byte, error) {
	var err error
	for {
		for i := dec.scanp; i < len(dec.buf); i++ {
			if c := dec.buf[i]; !isSpaceByte(c) {
				dec.scanp = i
				return c, nil
			}
		}
		if err != nil {
			return 0, err
		}
		err = dec.refill()
	}
}

// An Encoder writes JSON values to an output stream.
type Encoder struct {
	w            io.Writer
	err          error
	escapeHTML   bool
	indentBuf    []byte
	indentPrefix string
	indentValue  string
}

// NewEncoder returns a new encoder that writes to w.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w: w, escapeHTML: true}
}

// Encode writes the JSON encoding of v to the stream, with insignificant
// space characters elided, followed by a newline character.
//
// See the documentation for Marshal for details about the conversion of Go
// values to JSON.
func (enc *Encoder) Encode(v any) error {
	if enc.err != nil {
		return enc.err
	}
	e := getEncodeState(enc.escapeHTML)
	b, err := e.marshal(e.buf[:0], v)
	if err != nil {
		putEncodeState(e, b)
		return err
	}
	b = append(b, '\n')
	out := b
	if len(e.refs) > 0 {
		out = e.result(b)
	}
	if enc.indentPrefix != "" || enc.indentValue != "" {
		enc.indentBuf = appendIndentValid(enc.indentBuf[:0], out, enc.indentPrefix, enc.indentValue)
		out = enc.indentBuf
	}
	if _, err = enc.w.Write(out); err != nil {
		enc.err = err
	}
	putEncodeState(e, b)
	return err
}

// SetIndent instructs the encoder to format each subsequent encoded value
// as if indented by the package-level function Indent(dst, src, prefix,
// indent). Calling SetIndent("", "") disables indentation.
func (enc *Encoder) SetIndent(prefix, indent string) {
	enc.indentPrefix = prefix
	enc.indentValue = indent
}

// SetEscapeHTML specifies whether problematic HTML characters should be
// escaped inside JSON quoted strings. The default behavior is to escape &,
// <, and > to \u0026, \u003c, and \u003e to avoid certain safety problems
// that can arise when embedding JSON in HTML.
//
// In non-HTML settings where the escaping interferes with the readability
// of the output, SetEscapeHTML(false) disables this behavior.
func (enc *Encoder) SetEscapeHTML(on bool) {
	enc.escapeHTML = on
}
