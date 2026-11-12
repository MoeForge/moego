package json

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// scanner is a byte-at-a-time JSON grammar checker. It runs only on input
// that failed validation, or where a stream needs the state after each
// byte, and gives the error messages and offsets of encoding/json.
type scanner struct {
	state  uint8
	stack  []uint8 // the open containers: inObjKey, inObjVal or inArr
	endTop bool    // a top-level value is complete
	err    string  // the message of the first error
	bytes  int64   // bytes counted by the caller

	// The byte that failed and its state, for the messages of
	// encoding/json on encoding/json/v2, which need the bytes around it.
	errC     byte
	errState uint8
}

// Containers on the scanner stack.
const (
	inObjKey uint8 = iota // reading a key, or the colon after it
	inObjVal              // reading a member value
	inArr                 // reading an element
)

// Scanner states. stEndValue follows any complete value.
const (
	stBeginValue uint8 = iota
	stBeginValueOrEmpty
	stBeginStringOrEmpty
	stBeginString
	stEndValue
	stEndTop
	stInString
	stInStringEsc
	stInStringEscU
	stInStringEscU1
	stInStringEscU12
	stInStringEscU123
	stNeg
	st1
	st0
	stDot
	stDot0
	stE
	stESign
	stE0
	stT
	stTr
	stTru
	stF
	stFa
	stFal
	stFals
	stN
	stNu
	stNul
	stError
)

// maxNestingDepth is the deepest nesting encoding/json accepts.
const maxNestingDepth = 10000

func (s *scanner) reset() {
	s.state = stBeginValue
	s.stack = s.stack[:0]
	s.endTop = false
	s.err = ""
}

// Results of step.
const (
	scanContinue     = iota // the byte continues the input
	scanSkip                // whitespace or a byte inside a literal
	scanBeginLiteral        // the first byte of a string, number or literal
	scanBeginObject
	scanObjectKey   // the colon after a key
	scanObjectValue // the comma after a member
	scanEndObject
	scanBeginArray
	scanArrayValue // the comma after an element
	scanEndArray
	scanEnd // the byte after a top-level value; not consumed
	scanError
)

func isSpaceByte(c byte) bool {
	return c <= ' ' && (c == ' ' || c == '\t' || c == '\r' || c == '\n')
}

func (s *scanner) fail(c byte, context string) int {
	s.errC, s.errState = c, s.state
	s.state = stError
	if jsonv2 {
		s.err = v2Message(c, context)
	} else {
		s.err = "invalid character " + quoteChar(c) + " " + context
	}
	return scanError
}

// v2Message is the message of encoding/json on encoding/json/v2 for the
// byte c in context. Messages that show the bytes after c, for an escape
// or a character of more than one byte, come from v2SyntaxError.
func v2Message(c byte, context string) string {
	switch context {
	case "exceeded max depth":
		return context
	case "after decimal point in numeric literal", "in exponent of numeric literal":
		context = "in numeric literal"
	case "in string literal":
		context = "in string"
	}
	return "invalid character " + quoteChar(c) + " " + context
}

// quoteChar formats c as a quoted character literal.
func quoteChar(c byte) string {
	if c == '\'' {
		return `'\''`
	}
	if c == '"' {
		return `'"'`
	}
	q := strconv.Quote(string(rune(c)))
	return "'" + q[1:len(q)-1] + "'"
}

func (s *scanner) push(c byte, kind uint8) int {
	s.stack = append(s.stack, kind)
	if len(s.stack) > maxNestingDepth {
		return s.fail(c, "exceeded max depth")
	}
	return scanContinue
}

func (s *scanner) pop() {
	s.stack = s.stack[:len(s.stack)-1]
	if len(s.stack) == 0 {
		s.state = stEndTop
		s.endTop = true
	} else {
		s.state = stEndValue
	}
}

// step reads the byte c and returns what it was.
func (s *scanner) step(c byte) int {
	switch s.state {
	case stBeginValueOrEmpty:
		if isSpaceByte(c) {
			return scanSkip
		}
		if c == ']' {
			return s.endValue(c)
		}
		return s.beginValue(c)
	case stBeginValue:
		return s.beginValue(c)
	case stBeginStringOrEmpty:
		if isSpaceByte(c) {
			return scanSkip
		}
		if c == '}' {
			s.stack[len(s.stack)-1] = inObjVal
			return s.endValue(c)
		}
		return s.beginString(c)
	case stBeginString:
		return s.beginString(c)
	case stEndValue:
		return s.endValue(c)
	case stEndTop:
		if !isSpaceByte(c) {
			s.fail(c, "after top-level value")
		}
		return scanEnd
	case stInString:
		if c == '"' {
			s.state = stEndValue
			return scanContinue
		}
		if c == '\\' {
			s.state = stInStringEsc
			return scanContinue
		}
		if c < 0x20 {
			return s.fail(c, "in string literal")
		}
		return scanContinue
	case stInStringEsc:
		switch c {
		case 'b', 'f', 'n', 'r', 't', '\\', '/', '"':
			s.state = stInString
			return scanContinue
		case 'u':
			s.state = stInStringEscU
			return scanContinue
		}
		return s.fail(c, "in string escape code")
	case stInStringEscU, stInStringEscU1, stInStringEscU12, stInStringEscU123:
		if '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' {
			if s.state == stInStringEscU123 {
				s.state = stInString
			} else {
				s.state++
			}
			return scanContinue
		}
		return s.fail(c, "in \\u hexadecimal character escape")
	case stNeg:
		if c == '0' {
			s.state = st0
			return scanContinue
		}
		if '1' <= c && c <= '9' {
			s.state = st1
			return scanContinue
		}
		return s.fail(c, "in numeric literal")
	case st1:
		if '0' <= c && c <= '9' {
			return scanContinue
		}
		return s.after0(c)
	case st0:
		return s.after0(c)
	case stDot:
		if '0' <= c && c <= '9' {
			s.state = stDot0
			return scanContinue
		}
		return s.fail(c, "after decimal point in numeric literal")
	case stDot0:
		if '0' <= c && c <= '9' {
			return scanContinue
		}
		if c == 'e' || c == 'E' {
			s.state = stE
			return scanContinue
		}
		return s.endValue(c)
	case stE:
		if c == '+' || c == '-' {
			s.state = stESign
			return scanContinue
		}
		return s.eSign(c)
	case stESign:
		return s.eSign(c)
	case stE0:
		if '0' <= c && c <= '9' {
			return scanContinue
		}
		return s.endValue(c)
	case stT:
		return s.lit(c, 'r', stTr, "in literal true (expecting 'r')")
	case stTr:
		return s.lit(c, 'u', stTru, "in literal true (expecting 'u')")
	case stTru:
		return s.lit(c, 'e', stEndValue, "in literal true (expecting 'e')")
	case stF:
		return s.lit(c, 'a', stFa, "in literal false (expecting 'a')")
	case stFa:
		return s.lit(c, 'l', stFal, "in literal false (expecting 'l')")
	case stFal:
		return s.lit(c, 's', stFals, "in literal false (expecting 's')")
	case stFals:
		return s.lit(c, 'e', stEndValue, "in literal false (expecting 'e')")
	case stN:
		return s.lit(c, 'u', stNu, "in literal null (expecting 'u')")
	case stNu:
		return s.lit(c, 'l', stNul, "in literal null (expecting 'l')")
	case stNul:
		return s.lit(c, 'l', stEndValue, "in literal null (expecting 'l')")
	}
	return scanError
}

func (s *scanner) lit(c, want byte, next uint8, context string) int {
	if c == want {
		s.state = next
		return scanContinue
	}
	return s.fail(c, context)
}

func (s *scanner) after0(c byte) int {
	if c == '.' {
		s.state = stDot
		return scanContinue
	}
	if c == 'e' || c == 'E' {
		s.state = stE
		return scanContinue
	}
	return s.endValue(c)
}

func (s *scanner) eSign(c byte) int {
	if '0' <= c && c <= '9' {
		s.state = stE0
		return scanContinue
	}
	return s.fail(c, "in exponent of numeric literal")
}

func (s *scanner) beginValue(c byte) int {
	if isSpaceByte(c) {
		return scanSkip
	}
	switch c {
	case '{':
		s.state = stBeginStringOrEmpty
		if s.push(c, inObjKey) == scanError {
			return scanError
		}
		return scanBeginObject
	case '[':
		s.state = stBeginValueOrEmpty
		if s.push(c, inArr) == scanError {
			return scanError
		}
		return scanBeginArray
	case '"':
		s.state = stInString
		return scanBeginLiteral
	case '-':
		s.state = stNeg
		return scanBeginLiteral
	case '0':
		s.state = st0
		return scanBeginLiteral
	case 't':
		s.state = stT
		return scanBeginLiteral
	case 'f':
		s.state = stF
		return scanBeginLiteral
	case 'n':
		s.state = stN
		return scanBeginLiteral
	}
	if '1' <= c && c <= '9' {
		s.state = st1
		return scanBeginLiteral
	}
	return s.fail(c, "looking for beginning of value")
}

func (s *scanner) beginString(c byte) int {
	if isSpaceByte(c) {
		return scanSkip
	}
	if c == '"' {
		s.state = stInString
		return scanBeginLiteral
	}
	return s.fail(c, "looking for beginning of object key string")
}

func (s *scanner) endValue(c byte) int {
	n := len(s.stack)
	if n == 0 {
		s.state = stEndTop
		s.endTop = true
		if !isSpaceByte(c) {
			s.fail(c, "after top-level value")
		}
		return scanEnd
	}
	if isSpaceByte(c) {
		s.state = stEndValue
		return scanSkip
	}
	switch s.stack[n-1] {
	case inObjKey:
		if c == ':' {
			s.stack[n-1] = inObjVal
			s.state = stBeginValue
			return scanObjectKey
		}
		return s.fail(c, "after object key")
	case inObjVal:
		if c == ',' {
			s.stack[n-1] = inObjKey
			s.state = stBeginString
			return scanObjectValue
		}
		if c == '}' {
			s.pop()
			return scanEndObject
		}
		return s.fail(c, "after object key:value pair")
	default:
		if c == ',' {
			s.state = stBeginValue
			return scanArrayValue
		}
		if c == ']' {
			s.pop()
			return scanEndArray
		}
		return s.fail(c, "after array element")
	}
}

// eof reports the end of the input. It returns scanEnd when a complete
// value was read, else scanError with s.err set.
func (s *scanner) eof() int {
	if s.state == stError {
		return scanError
	}
	if s.endTop {
		return scanEnd
	}
	s.step(' ')
	if s.endTop {
		return scanEnd
	}
	if s.state != stError {
		s.state = stError
		s.err = "unexpected end of JSON input"
	}
	return scanError
}

// Callers of firstSyntaxError.
const (
	synUnmarshal = iota // Unmarshal, Valid and Indent
	synCompact          // Compact, and values checked by Marshal
	synIndent
)

// firstSyntaxError returns the message and offset of the first syntax error in
// data, which must not be valid JSON, as the function of mode reports it.
// Compact does not count bytes: its errors are at offset 0.
func firstSyntaxError(data []byte, mode int) (string, int64) {
	var s scanner
	s.reset()
	for i, c := range data {
		s.step(c)
		if s.state == stError {
			if jsonv2 {
				return s.v2SyntaxError(data, i, mode)
			}
			if mode == synCompact {
				return s.err, 0
			}
			return s.err, int64(i + 1)
		}
	}
	if jsonv2 {
		return s.v2SyntaxError(data, len(data), mode)
	}
	s.eof()
	if s.err == "" {
		s.err = "unexpected end of JSON input"
	}
	if mode == synCompact {
		return s.err, 0
	}
	return s.err, int64(len(data))
}

// v2SyntaxError returns the message and offset of the syntax error at
// data[i], or at the end of data when i is len(data), as encoding/json on
// encoding/json/v2 reports them in the function of mode.
func (s *scanner) v2SyntaxError(data []byte, i, mode int) (string, int64) {
	if i == len(data) {
		const eof = "unexpected end of JSON input"
		switch s.state {
		case stInStringEsc, stInStringEscU, stInStringEscU1, stInStringEscU12, stInStringEscU123:
			// At the backslash.
			return eof, int64(i - 1 - int(s.state-stInStringEsc))
		case stNeg, stDot, stE, stESign:
			if mode != synUnmarshal {
				// After the digits before the incomplete part.
				n := i - 1
				if s.state == stESign {
					n--
				}
				return eof, int64(n)
			}
			// At the start of the number.
			n := i
			for n > 0 && (data[n-1] >= '0' && data[n-1] <= '9' || data[n-1] == '.' || data[n-1] == 'e' || data[n-1] == 'E' || data[n-1] == '+' || data[n-1] == '-') {
				n--
			}
			return eof, int64(n)
		case stInString:
			// At a character of more than one byte that the input cuts
			// short.
			for k := i - 1; k >= max(0, i-3) && data[k] >= utf8.RuneSelf; k-- {
				if data[k] >= 0xc0 {
					if !utf8.FullRune(data[k:]) {
						return eof, int64(k)
					}
					break
				}
			}
		}
		return eof, int64(i)
	}
	// From Go 1.27 the offset is after what is wrong, before at its start.
	switch s.errState {
	case stInStringEsc:
		msg := "invalid escape sequence " + quoteSeq(data[i-1:i+1]) + " in string"
		if !go127 {
			return msg, int64(i - 1)
		}
		return msg, int64(i + 1)
	case stInStringEscU, stInStringEscU1, stInStringEscU12, stInStringEscU123:
		bs := i - 2 - int(s.errState-stInStringEscU)
		seq := data[bs:min(bs+6, len(data))]
		msg := "invalid escape sequence " + quoteSeq(seq) + " in string"
		if !go127 {
			return msg, int64(bs)
		}
		return msg, int64(bs + len(seq))
	}
	at := int64(i)
	msg := s.err
	if mode != synUnmarshal && strings.HasSuffix(msg, " after array element") {
		msg = strings.TrimSuffix(msg, "element") + "value"
	}
	if c := data[i]; c >= utf8.RuneSelf && s.errState != stInString {
		r, n := utf8.DecodeRune(data[i:])
		q := fmt.Sprintf(`'\x%02x'`, c)
		if r != utf8.RuneError || n > 1 {
			q = strconv.QuoteRune(r)
		} else {
			n = 1
		}
		if go127 {
			at += int64(n)
		}
		return strings.Replace(msg, quoteChar(c), q, 1), at
	}
	if go127 {
		at++
	}
	return msg, at
}

// quoteSeq quotes an escape sequence as encoding/json on encoding/json/v2
// shows it: in back quotes when it is valid UTF-8 of printable characters
// other than spaces and back quotes, else as a Go string.
func quoteSeq(b []byte) string {
	s := string(b)
	if !utf8.ValidString(s) {
		return strconv.Quote(s)
	}
	for _, r := range s {
		if r == '`' || unicode.IsSpace(r) || !strconv.IsPrint(r) {
			return strconv.Quote(s)
		}
	}
	return "`" + s + "`"
}

// run steps through the bytes of b from i until the top-level value ends
// or a byte is not valid. It returns where it stopped with scanEnd when
// the value ended before b[j], scanError when b[j] is not valid, or
// scanContinue when it read all of b. Within strings it skips plain bytes
// without stepping.
func (s *scanner) run(b []byte, i int) (int, int) {
	for i < len(b) {
		if s.state == stInString {
			if i = jsonsimd.PlainEnd(bytesView(b), i); i == len(b) {
				break
			}
		}
		switch s.step(b[i]) {
		case scanContinue:
			if jsonv2 && s.state == stEndValue && len(s.stack) == 0 {
				// A string or literal ends the value on encoding/json/v2.
				return i + 1, scanEnd
			}
		case scanEnd:
			return i, scanEnd
		case scanEndObject, scanEndArray:
			if len(s.stack) == 0 {
				return i + 1, scanEnd
			}
		case scanError:
			return i, scanError
		}
		i++
	}
	return len(b), scanContinue
}
