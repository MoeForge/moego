package json

import (
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

type noCopyDoc struct {
	Name string                `json:"name"`
	Esc  string                `json:"esc"`
	Long string                `json:"long"`
	Num  Number                `json:"num"`
	Raw  RawMessage            `json:"raw"`
	Tags map[string]string     `json:"tags"`
	Keys map[noCopyKey]int     `json:"keys"`
	Any  any                   `json:"any"`
	Raws map[string]RawMessage `json:"raws"`
	List []string              `json:"list"`
}

type noCopyKey string

// TestUnmarshalNoCopy checks that UnmarshalNoCopy stores what Unmarshal
// stores, with the strings that need no unescaping viewing the input.
func TestUnmarshalNoCopy(t *testing.T) {
	long := strings.Repeat("x", 3000)
	// The padding makes the input long enough to be validated first.
	for _, pad := range []int{0, 70 << 10} {
		doc := []byte(`{"name":"moe","esc":"a\nb","long":"` + long + `","num":12.5,"raw":{"a":[1, 2]},` +
			`"tags":{"k":"v"},"keys":{"kk":1},"any":{"s":"str","l":["x"]},"raws":{"r":[3]},"list":["p","q"],` +
			`"pad":"` + strings.Repeat("y", pad) + `"}`)
		var got, want noCopyDoc
		if err := UnmarshalNoCopy(doc, &got); err != nil {
			t.Fatal(err)
		}
		if err := Unmarshal(doc, &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("pad %d:\n got %#v\nwant %#v", pad, got, want)
		}
		inDoc := func(s string) bool {
			p := uintptr(unsafe.Pointer(unsafe.StringData(s)))
			lo := uintptr(unsafe.Pointer(&doc[0]))
			return p >= lo && p+uintptr(len(s)) <= lo+uintptr(len(doc))
		}
		anyMap := got.Any.(map[string]any)
		views := map[string]string{
			"name": got.Name, "long": got.Long, "num": string(got.Num), "raw": bytesView(got.Raw),
			"tags value": got.Tags["k"], "any string": anyMap["s"].(string), "any element": anyMap["l"].([]any)[0].(string),
			"raws value": bytesView(got.Raws["r"]), "list element": got.List[1],
		}
		for k := range got.Tags {
			views["tags key"] = k
		}
		for k := range got.Keys {
			views["named key"] = string(k)
		}
		for k := range anyMap {
			views["any key "+k] = k
		}
		for what, s := range views {
			if !inDoc(s) {
				t.Errorf("pad %d: %s %q is a copy", pad, what, s)
			}
		}
		if inDoc(got.Esc) {
			t.Errorf("pad %d: an unescaped string views the input", pad)
		}
		if inDoc(want.Name) || inDoc(want.Long) || inDoc(bytesView(want.Raw)) {
			t.Errorf("pad %d: Unmarshal kept a view of the input", pad)
		}
		if cap(got.Raw) != len(got.Raw) || cap(got.Raws["r"]) != len(got.Raws["r"]) {
			t.Errorf("pad %d: a RawMessage can grow into the input", pad)
		}
	}
}
