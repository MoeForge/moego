package json

import stdjson "encoding/json"

// The value and interface types are those of encoding/json, so values,
// type switches and errors.As work the same with either package.
type (
	// RawMessage is a raw encoded JSON value. See encoding/json.RawMessage.
	RawMessage = stdjson.RawMessage
	// A Number represents a JSON number literal. See encoding/json.Number.
	Number = stdjson.Number
	// Marshaler is implemented by types that can marshal themselves into
	// valid JSON.
	Marshaler = stdjson.Marshaler
	// Unmarshaler is implemented by types that can unmarshal a JSON
	// description of themselves.
	Unmarshaler = stdjson.Unmarshaler
	// A Delim is a JSON array or object delimiter, one of [ ] { or }.
	Delim = stdjson.Delim
	// A Token holds a value of one of these types: Delim, bool, float64,
	// Number, string, nil.
	Token = stdjson.Token
	// A SyntaxError is a description of a JSON syntax error.
	SyntaxError = stdjson.SyntaxError
	// An UnmarshalTypeError describes a JSON value that was not
	// appropriate for a value of a specific Go type.
	UnmarshalTypeError = stdjson.UnmarshalTypeError
	// An InvalidUnmarshalError describes an invalid argument passed to
	// Unmarshal.
	InvalidUnmarshalError = stdjson.InvalidUnmarshalError
	// An UnsupportedTypeError is returned by Marshal when attempting to
	// encode an unsupported value type.
	UnsupportedTypeError = stdjson.UnsupportedTypeError
	// An UnsupportedValueError is returned by Marshal when attempting to
	// encode an unsupported value.
	UnsupportedValueError = stdjson.UnsupportedValueError
	// A MarshalerError represents an error from calling a MarshalJSON or
	// MarshalText method.
	MarshalerError = stdjson.MarshalerError
	// Before Go 1.2, an InvalidUTF8Error was returned by Marshal when
	// attempting to encode a string value with invalid UTF-8 sequences.
	// As of Go 1.2, Marshal instead coerces the string to valid UTF-8 by
	// replacing invalid bytes with the Unicode replacement rune U+FFFD.
	//
	// Deprecated: No longer used; kept for compatibility.
	InvalidUTF8Error = stdjson.InvalidUTF8Error
	// An UnmarshalFieldError describes a JSON object key that led to an
	// unexported (and therefore unwritable) struct field.
	//
	// Deprecated: No longer used; kept for compatibility.
	UnmarshalFieldError = stdjson.UnmarshalFieldError
)
