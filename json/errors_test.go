package json

import (
	"bytes"
	stdjson "encoding/json"
	"errors"
	"reflect"
	"testing"
)

// TestInvalidRawMessageError checks the error for a RawMessage that is not
// valid JSON against encoding/json's: the MarshalerError, the Type it
// names, the error it wraps and the text.
func TestInvalidRawMessageError(t *testing.T) {
	raw := RawMessage(`{bad`)
	for _, v := range []any{
		raw,
		&raw,
		struct{ A RawMessage }{raw},
		&struct{ A *RawMessage }{&raw},
		[]RawMessage{raw},
		map[string]RawMessage{"a": raw},
	} {
		_, got := Marshal(v)
		_, want := stdjson.Marshal(v)
		checkRawMessageError(t, "Marshal", v, got, want)
		var gb, wb bytes.Buffer
		got, want = NewEncoder(&gb).Encode(v), stdjson.NewEncoder(&wb).Encode(v)
		checkRawMessageError(t, "Encode", v, got, want)
	}
}

func checkRawMessageError(t *testing.T, fn string, v any, got, want error) {
	t.Helper()
	var g, w *MarshalerError
	if !errors.As(got, &g) || !errors.As(want, &w) {
		t.Errorf("%s %T: got %v, want %v", fn, v, got, want)
		return
	}
	if g.Type != w.Type || reflect.TypeOf(g.Err) != reflect.TypeOf(w.Err) || got.Error() != want.Error() {
		t.Errorf("%s %T: got %v (%T) %q, want %v (%T) %q", fn, v, g.Type, g.Err, got, w.Type, w.Err, want)
	}
}

// TestErrorFields checks that the package finds the fields of encoding/json's
// errors that it sets, on every toolchain it supports, so that its errors
// carry the message, source function and cause that encoding/json's do.
func TestErrorFields(t *testing.T) {
	f := errorFields()
	if !f.syntaxMsgOK || !f.marshalerSrcOK || f.typeErrErrOK != jsonv2 {
		t.Errorf("errorFields() = %+v, want each field found, Err with encoding/json/v2 only", f)
	}
}
