package codec

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// invalidTarget has a RawMessage member and typed members, for documents
// that are not valid JSON.
type invalidTarget struct {
	Raw json.RawMessage `json:"raw"`
	N   int             `json:"n"`
	S   string          `json:"s"`
	B   bool            `json:"b"`
	M   map[string]any  `json:"m"`
}

// invalidInputs are documents that encoding/json rejects, with the error
// in a RawMessage member, in a typed member, or in a member the target
// does not have.
var invalidInputs = []struct{ name, doc string }{
	{"raw [1,}", `{"raw":[1,}}`},
	{"raw tru", `{"raw":tru}`},
	{"raw nul", `{"raw":[nul]}`},
	{"raw control byte", "{\"raw\":\"a\x01b\"}"},
	{"raw bad escape", `{"raw":"a\qb"}`},
	{"raw unterminated string", `{"raw":"abc`},
	{"raw missing colon", `{"raw":{"a" 1}}`},
	{"raw missing comma", `{"raw":[1 2]}`},
	{"raw object trailing comma", `{"raw":{"a":1,}}`},
	{"raw leading zero", `{"raw":[01]}`},
	{"raw number 1.", `{"raw":[1.]}`},
	{"raw array trailing comma", `{"raw":[1,2,]}`},
	{"raw double comma", `{"raw":["a",,"b"]}`},
	{"raw ] closes object", `{"raw":{"a":1]}`},
	{"raw } closes array", `{"raw":[1}}`},
	{"raw number key", `{"raw":{1:2}}`},
	{"raw members without comma", `{"raw":{"a":1 "b":2}}`},
	{"raw number -", `{"raw":[-]}`},
	{"raw number 1e", `{"raw":[1e]}`},
	{"raw number +1", `{"raw":[+1]}`},
	{"raw number .5", `{"raw":[.5]}`},
	{"raw hex number", `{"raw":[0x10]}`},
	{"raw single quotes", `{"raw":['a']}`},
	{"raw bad u escape", `{"raw":"\u12g4"}`},
	{"int leading zero", `{"n":01}`},
	{"string bad escape", `{"s":"a\qb"}`},
	{"string control byte", "{\"s\":\"a\x01b\"}"},
	{"bool tru", `{"b":tru}`},
	{"int 1e", `{"n":1e}`},
	{"map value [1,]", `{"m":{"a":[1,]}}`},
	{"unknown member [1,}", `{"x":[1,},"n":1}`},
	{"unknown member tru", `{"x":tru,"n":1}`},
	{"struct trailing comma", `{"n":1,}`},
}

// TestInvalidInput records which engines accept a document that
// encoding/json rejects. moego must reject every one of them; the others
// are reported only, as observed.
func TestInvalidInput(t *testing.T) {
	var accepted []string
	for _, in := range invalidInputs {
		if json.Valid([]byte(in.doc)) {
			t.Fatalf("%s: %q is valid JSON", in.name, in.doc)
		}
		var got []string
		for _, e := range DecodeEngines {
			res := unmarshalResult(e, in.doc)
			got = append(got, e.Name+"="+res)
			if res == "accepts" || res == "panics" {
				accepted = append(accepted, fmt.Sprintf("%s: %s", e.Name, in.name))
			}
			if (e.Name == "moego" || e.Name == "moego-nocopy") && res != "rejects" {
				t.Errorf("%s %s %q", e.Name, res, in.doc)
			}
		}
		t.Logf("invalid %-28s %-26q %s", in.name, in.doc, strings.Join(got, " "))
	}
	t.Logf("accepted invalid input: %d", len(accepted))
	for _, a := range accepted {
		t.Logf("accepts %s", a)
	}
}

func unmarshalResult(e Engine, doc string) (res string) {
	defer func() {
		if recover() != nil {
			res = "panics"
		}
	}()
	var v invalidTarget
	if err := e.Unmarshal([]byte(doc), &v); err != nil {
		return "rejects"
	}
	return "accepts"
}
