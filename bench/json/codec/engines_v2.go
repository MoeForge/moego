//go:build goexperiment.jsonv2 && go1.27

package codec

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// v2Engines holds encoding/json/v2 with its default options but one. It
// does not promise encoding/json's bytes: it neither sorts map keys nor
// escapes HTML characters, so its output is compared as values.
var v2Engines = []Engine{{"jsonv2", v2Marshal, v2Unmarshal, false, v2Valid, v2Compact, v2Indent}}

// v2MarshalOptions is the one option that encoding/json/v2 needs to encode
// the values encoding/json does. The types' omitempty members include
// numbers and bools, which encoding/json leaves out when they are zero;
// encoding/json/v2 by default leaves out only "", null, [] and {}.
var v2MarshalOptions = json.OmitEmptyWithLegacySemantics(true)

func v2Marshal(v any) ([]byte, error) { return jsonv2.Marshal(v, v2MarshalOptions) }

func v2Unmarshal(data []byte, v any) error { return jsonv2.Unmarshal(data, v) }

func v2Valid(data []byte) bool { return jsontext.Value(data).IsValid() }

// v2Compact and v2Indent format with the options that jsontext.Value's
// Compact and Indent use, but append to dst instead of formatting a copy in
// place.
func v2Compact(dst *bytes.Buffer, src []byte) error {
	out, err := jsontext.AppendFormat(dst.AvailableBuffer(), src,
		jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true), jsontext.PreserveRawStrings(true))
	dst.Write(out)
	return err
}

func v2Indent(dst *bytes.Buffer, src []byte, prefix, indent string) error {
	out, err := jsontext.AppendFormat(dst.AvailableBuffer(), src,
		jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true), jsontext.PreserveRawStrings(true),
		jsontext.Multiline(true), jsontext.WithIndentPrefix(prefix), jsontext.WithIndent(indent))
	dst.Write(out)
	return err
}
