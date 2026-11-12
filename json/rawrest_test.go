//go:build go1.26

package json

import (
	"math/rand"
	"strings"
	"testing"
)

type rawHeavy struct {
	Model    string       `json:"model"`
	Meta     RawMessage   `json:"metadata"`
	Text     RawMessage   `json:"text"`
	Tools    RawMessage   `json:"tools"`
	N        int          `json:"n"`
	Inner    *rawHeavy    `json:"inner"`
	List     []RawMessage `json:"list"`
	Input    RawMessage   `json:"input"`
	Stream   bool         `json:"stream"`
	Quoted   int          `json:"q,string"`
	Callback *upper       `json:"cb"`
	Fn       *counted     `json:"fn"`
	Fs       []counted    `json:"fs"`
}

// TestRawRest decodes short inputs with several RawMessage values, which a
// fused decode validates at once from the first of them, valid and with
// errors at each place.
func TestRawRest(t *testing.T) {
	docs := []string{
		`{"model":"m","metadata":{"user_id":"u-42"},"text":{"format":{"type":"text"}},"tools":[{"type":"function","name":"w","parameters":{"type":"object","properties":{"location":{"type":"string"}}}}],"stream":true,"input":[{"role":"user","content":[{"type":"input_text","text":"What is in this image?"}]}]}`,
		`{"metadata":{"a":[1,2,{"b":null}]},"n":5,"inner":{"metadata":{"x":1},"text":[1,[2,[3]]],"list":[{},[],{"k":"v"}]},"tools":{"t":"` + strings.Repeat("x", 300) + `"},"list":[[1],{"a":2}],"input":{"z":[]}}`,
		`[{"metadata":{"a":1},"text":{"b":2}},{"text":[3],"tools":{"c":[4]},"input":{"d":5}},{"metadata":[],"q":"7","cb":"AbC"}]`,
		`{"model":"m","metadata":{"a":1},"q":"12","text":{"b":"` + strings.Repeat("\\u00e9", 40) + `"},"cb":"XyZ","tools":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20]}`,
	}
	// Values that methods get, which the decode validates at once from the
	// first of them, with other values to skip around them.
	docs = append(docs,
		`{"model":"m","fn":{"a":[1,{"b":2}]},"metadata":{"x":[]},"tools":[{"type":"function","function":{"name":"w","parameters":{"type":"object"}}}],"x":{"y":[1,2]},"fs":[{"p":1},[2,{"q":3}],"s",{"r":[]}],"inner":{"fn":[{}],"text":{"t":1},"fs":[{"u":{}}]}}`,
		`[{"fn":{"k":1},"tools":{"t":[1]}},{"metadata":{"m":2},"fn":[3,{"v":4}],"text":{"z":5}},{"fs":[{"a":1},{"b":2},{"c":3}],"input":[[],{}]}]`,
		`{"fs":[`+strings.Repeat(`{"e":[1,{"f":"g"}]},`, 20)+`{}],"metadata":{"h":1},"unknown":{"i":[2]},"input":{"j":3}}`,
	)
	// Eighteen RawMessage values at one depth, more than the ends kept.
	var b strings.Builder
	b.WriteString(`{"metadata":{"a":1},"list":[`)
	for i := range 18 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"k":[1,{"x":"y"}]}`)
	}
	b.WriteString(`],"text":{"e":"f"},"input":[{"g":1}]}`)
	docs = append(docs, b.String())
	rng := rand.New(rand.NewSource(3))
	bad := []string{",", "]", "}", "x", `"`, "[", "{", ":", "\x01", `\q`, "tru", "01", " "}
	for _, doc := range docs {
		checkUnmarshal[rawHeavy](t, doc)
		checkUnmarshal[[]rawHeavy](t, doc)
		checkUnmarshal[map[string]RawMessage](t, doc)
		for range 400 {
			at := rng.Intn(len(doc))
			var m string
			switch rng.Intn(3) {
			case 0:
				m = doc[:at] + bad[rng.Intn(len(bad))] + doc[at:]
			case 1:
				m = doc[:at] + doc[at+1:]
			default:
				m = doc[:at]
			}
			checkUnmarshal[rawHeavy](t, m)
			checkUnmarshal[[]rawHeavy](t, m)
		}
	}
}
