package json

import (
	"reflect"
	"strconv"
	"strings"
)

// Type errors on encoding/json/v2 name the value by its place in the input:
// UnmarshalTypeError.Field is the path of object member names and array
// indexes that lead to it, and Struct is the name of the type Unmarshal
// decodes into. Finding the path reads the input again from the start of
// the value, which only an error pays for.

// pathInput reads the bytes of the value being decoded in order.
type pathInput struct {
	s   string       // the input, when it is all in memory
	si  *streamInput // else the reader of a large value
	buf []byte
	off int64 // offset of buf[0]
	end int64
}

// at returns the byte at offset i, or -1 past the end.
func (in *pathInput) at(i int64) int {
	if in.si == nil {
		if i >= int64(len(in.s)) {
			return -1
		}
		return int(in.s[i])
	}
	if i >= in.end {
		return -1
	}
	if i < in.off || i >= in.off+int64(len(in.buf)) {
		n := int(min(int64(32<<10), in.end-i))
		if cap(in.buf) < n {
			in.buf = make([]byte, n)
		}
		in.buf = in.buf[:n]
		if in.si.readAt(in.buf, i) != nil {
			in.end = i
			return -1
		}
		in.off = i
	}
	return int(in.buf[i-in.off])
}

// pathLevel is an object or array that holds the place being looked for:
// for an object, the member name before it.
type pathLevel struct {
	obj     bool
	wantKey bool
	key     string
	index   int
}

// pathTo returns the objects and arrays that hold the value or member name
// at offset t of in, and the offset of the token that holds t. Each member
// name is decoded.
func pathTo(in *pathInput, t int64) ([]pathLevel, int64) {
	var stack []pathLevel
	var key []byte
	start, run := t, int64(-1)
	for i := int64(0); i <= t; i++ {
		c := in.at(i)
		if c < 0 {
			break
		}
		if c == '"' {
			start, run = i, -1
			// Find the end of the string, decoding it when it is a name.
			isKey := len(stack) > 0 && stack[len(stack)-1].wantKey
			key = key[:0]
			j := i + 1
			for {
				b := in.at(j)
				if b < 0 || b == '"' {
					break
				}
				if isKey {
					key = append(key, byte(b))
				}
				if b == '\\' {
					j++
					if isKey {
						if b := in.at(j); b >= 0 {
							key = append(key, byte(b))
						}
					}
				}
				j++
			}
			if isKey {
				top := &stack[len(stack)-1]
				top.key, top.wantKey = string(unquote(nil, bytesView(key))), false
			}
			i = j
			if i >= t {
				return stack, start
			}
			continue
		}
		switch {
		case c > ' ' && c != ',' && c != ':' && c != '{' && c != '}' && c != '[' && c != ']':
			if run < 0 {
				run = i
			}
		default:
			run = -1
		}
		if i == t {
			start = t
			if run >= 0 {
				start = run
			}
			break
		}
		switch c {
		case '{':
			stack = append(stack, pathLevel{obj: true, wantKey: true})
		case '[':
			stack = append(stack, pathLevel{})
		case '}', ']':
			stack = stack[:len(stack)-1]
		case ',':
			top := &stack[len(stack)-1]
			if top.obj {
				top.wantKey = true
			} else {
				top.index++
			}
		}
	}
	return stack, start
}

// formatPath writes the member names and array indexes of path, with ~
// written as ~0 and / as ~1 as in a JSON Pointer, each after sep but for
// the first when first is unset.
func formatPath(path []pathLevel, sep byte, first bool) string {
	var b strings.Builder
	for k, l := range path {
		if k > 0 || first {
			b.WriteByte(sep)
		}
		if !l.obj {
			b.WriteString(strconv.Itoa(l.index))
			continue
		}
		if !strings.ContainsAny(l.key, "~/") {
			b.WriteString(l.key)
			continue
		}
		for _, r := range l.key {
			switch r {
			case '~':
				b.WriteString("~0")
			case '/':
				b.WriteString("~1")
			default:
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// v2Field returns the path of the value or member name at offset t of the
// input, the place of a type error: the member names and indexes joined
// by dots. It also returns the offset of the token that holds t.
func (d *decodeState) v2Field(t int64) (string, int64) {
	in := pathInput{s: d.s}
	if d.stream != nil {
		in = pathInput{si: d.stream, end: d.stream.end}
	}
	path, start := pathTo(&in, t)
	return formatPath(path, '.', false), start
}

// v2TypeError returns the type error for a value or member name at offset
// target of the input, of JSON kind value, that type t can not hold, with
// offset off, on encoding/json/v2. The offsets are those of the input of d.
func (d *decodeState) v2TypeError(value string, t reflect.Type, off, target int) *UnmarshalTypeError {
	o, at := int64(off), int64(target)
	if d.stream != nil {
		o, at = d.stream.offset(off), d.stream.offset(target)
	}
	e := &UnmarshalTypeError{Value: value, Type: t, Offset: o}
	field, start := d.v2Field(at)
	if !go127 {
		// Before Go 1.27 the offset is that of the start of the token.
		e.Offset = start
	}
	if e.Field = field; e.Field != "" && d.root != nil {
		e.Struct = typeOfRtype(d.root).Elem().Name()
	}
	return e
}
