package json

import (
	"errors"
	"reflect"
	"sync"
	"unsafe"
)

// SyntaxError and MarshalerError keep their message and source function in
// unexported fields, and an UnmarshalTypeError built on encoding/json/v2
// has an Err field. errorFields finds them by name and type on the first
// error that needs them. TestErrorFields checks that it finds each of them
// on every toolchain the package supports.
var errorFields = sync.OnceValue(func() (f fieldOffsets) {
	if off, ok := unexportedString(reflect.TypeFor[SyntaxError](), "msg"); ok {
		e := &SyntaxError{Offset: 1}
		*(*string)(unsafe.Add(unsafe.Pointer(e), off)) = "x"
		f.syntaxMsg, f.syntaxMsgOK = off, e.Error() == "x" && e.Offset == 1
	}
	if off, ok := unexportedString(reflect.TypeFor[MarshalerError](), "sourceFunc"); ok {
		e := &MarshalerError{Type: reflect.TypeFor[int](), Err: errors.New("e")}
		*(*string)(unsafe.Add(unsafe.Pointer(e), off)) = "MarshalText"
		f.marshalerSrc, f.marshalerSrcOK = off, e.Error() == "json: error calling MarshalText for type int: e"
	}
	if sf, ok := reflect.TypeFor[UnmarshalTypeError]().FieldByName("Err"); ok && sf.Type == reflect.TypeFor[error]() && len(sf.Index) == 1 {
		f.typeErrErr, f.typeErrErrOK = sf.Offset, true
	}
	return f
})

// fieldOffsets holds the offsets errorFields finds, each with whether the
// field is there.
type fieldOffsets struct {
	syntaxMsg, marshalerSrc, typeErrErr       uintptr
	syntaxMsgOK, marshalerSrcOK, typeErrErrOK bool
}

// setTypeErrorErr sets the error that caused e, when e has a field for it.
func setTypeErrorErr(e *UnmarshalTypeError, err error) {
	if f := errorFields(); f.typeErrErrOK {
		*(*error)(unsafe.Add(unsafe.Pointer(e), f.typeErrErr)) = err
	}
}

func unexportedString(t reflect.Type, name string) (uintptr, bool) {
	f, ok := t.FieldByName(name)
	if !ok || f.Type.Kind() != reflect.String || len(f.Index) != 1 {
		return 0, false
	}
	return f.Offset, true
}

func newSyntaxError(msg string, off int64) *SyntaxError {
	e := &SyntaxError{Offset: off}
	if f := errorFields(); f.syntaxMsgOK {
		*(*string)(unsafe.Add(unsafe.Pointer(e), f.syntaxMsg)) = msg
	}
	return e
}

// syntaxErrorFor returns the syntax error of data, which is not valid
// JSON, as Unmarshal reports it.
func syntaxErrorFor(data []byte) error {
	return newSyntaxError(firstSyntaxError(data, synUnmarshal))
}

// compactSyntaxError returns the syntax error of src, which is not valid
// JSON, as Compact reports it.
func compactSyntaxError(src []byte) error {
	return newSyntaxError(firstSyntaxError(src, synCompact))
}

// indentSyntaxError returns the syntax error of src, which is not valid
// JSON, as Indent reports it.
func indentSyntaxError(src []byte) error {
	return newSyntaxError(firstSyntaxError(src, synIndent))
}

func newMarshalerError(t reflect.Type, err error, src string) *MarshalerError {
	e := &MarshalerError{Type: t, Err: err}
	if f := errorFields(); f.marshalerSrcOK && src != "MarshalJSON" {
		*(*string)(unsafe.Add(unsafe.Pointer(e), f.marshalerSrc)) = src
	}
	return e
}
