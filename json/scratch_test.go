//go:build go1.26

package json

import (
	"fmt"
	"strings"
	"testing"
)

type scrElem struct {
	S  string     `json:"s"`
	N  int        `json:"n"`
	Q  int        `json:"q,string"`
	R  RawMessage `json:"r"`
	P  *string    `json:"p"`
	U  *upper     `json:"u"`
	X  any        `json:"x"`
	In []scrElem  `json:"in"`
	F  []float64  `json:"f"`
}

type scrMethods struct {
	S string   `json:"s"`
	T textOnly `json:"t"`
}

func scrDoc(n int, bad string, at int) string {
	var b strings.Builder
	b.WriteString(`{"a":[`)
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		if i == at {
			b.WriteString(bad)
			continue
		}
		fmt.Fprintf(&b, `{"s":"s%d","n":%d,"q":"%d","r":[%d,"x"],"p":"p%d","x":{"k":"v%d"}`, i, i, i, i, i, i)
		if i%3 == 0 {
			fmt.Fprintf(&b, `,"in":[{"s":"i%d"},{"s":"j%d","in":[{"n":%d}]}],"f":[%d.5,2,3,4,5,6,7,8,9]`, i, i, i, i)
		}
		if i%5 == 0 {
			b.WriteString(`,"in":[]`)
		}
		b.WriteByte('}')
	}
	b.WriteString(`],"b":[[1,2],[3]]}`)
	return b.String()
}

// TestScratchArrays decodes arrays of unknown length, which are decoded
// into scratch space and then copied, with errors that stop the decode
// in an element at different depths, and arrays long enough to outgrow
// the scratch space.
func TestScratchArrays(t *testing.T) {
	type doc struct {
		A []scrElem `json:"a"`
		B [][]int   `json:"b"`
	}
	for _, n := range []int{1, 3, 4, 5, 8, 9, 17, 40, 600, 1300} {
		good := scrDoc(n, "", -1)
		checkUnmarshal[doc](t, good)
		checkUnmarshal[map[string][]scrElem](t, good)
		for _, at := range []int{0, n / 2, n - 1} {
			for _, bad := range []string{
				`{"s":"x","u":"AbC","in":[{"s":"y"}]}`,
				`{"s":"x","in":[{"s":"y","in":[{"u":"AbC"},{"s":"z"}]},{"s":"w"}]}`,
				`{"s":"x","q":"y"}`,
				`{"s":"x","u":5,"n":1}`,
				`{"s":"x","in":[{"s":"y"},{"q":"z"}]}`,
				`{"s":"x","in":[{"in":[{"u":[1]}]}]}`,
				`{"s":"x","n":"y","f":[1,"z"]}`,
				`{"s":"x",}`,
				`{"s":"x","in":[{"s":"y"},]}`,
				`{"s":"x","f":[1,2,]}`,
			} {
				checkUnmarshal[doc](t, scrDoc(n, bad, at))
			}
		}
		for _, cut := range []int{len(good) / 3, len(good) - 25} {
			checkUnmarshal[doc](t, good[:cut])
		}
	}
	for _, n := range []int{3, 4000, 5000, 9000} {
		list := func(f string) string {
			var b strings.Builder
			b.WriteByte('[')
			for i := range n {
				if i > 0 {
					b.WriteByte(',')
				}
				fmt.Fprintf(&b, f, i)
			}
			b.WriteByte(']')
			return b.String()
		}
		checkUnmarshal[[]string](t, list(`"v%d"`))
		checkUnmarshal[[]any](t, list(`"v%d"`))
		checkUnmarshal[[]float64](t, list(`%d`))
		checkUnmarshal[[]scrMethods](t, list(`{"s":"a","t":"t<%d>"}`))
	}
}
