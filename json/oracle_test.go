//go:build go1.26

package json

import (
	"bytes"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests compare the package with encoding/json, its oracle, on
// hand-picked cases. The randomized differential tests live in the bench
// module.

type inner struct {
	A int    `json:"a"`
	B string `json:"b,omitempty"`
}

type Embedded struct {
	E1 int
	E2 string `json:"e2"`
}

type embeddedPtr struct {
	P1 int
}

type outer struct {
	Embedded
	*embeddedPtr
	Name   string                `json:"name"`
	Age    int                   `json:"age,omitempty"`
	Score  float64               `json:"score"`
	F32    float32               `json:"f32"`
	Tags   []string              `json:"tags"`
	Arr    [3]int                `json:"arr"`
	Inner  inner                 `json:"inner"`
	PInner *inner                `json:"pinner"`
	M      map[string]int        `json:"m"`
	IM     map[int]string        `json:"im"`
	Any    any                   `json:"any"`
	Raw    RawMessage            `json:"raw"`
	Num    Number                `json:"num"`
	Bytes  []byte                `json:"bytes"`
	Quoted int                   `json:"quoted,string"`
	QStr   string                `json:"qstr,string"`
	QBool  bool                  `json:"qbool,string"`
	Skip   int                   `json:"-"`
	Dash   int                   `json:"-,"`
	U8     uint8                 `json:"u8"`
	I8     int8                  `json:"i8"`
	PP     **int                 `json:"pp"`
	Iface  fmt.Stringer          `json:"iface"`
	Time   time.Time             `json:"time"`
	Dur    time.Duration         `json:"dur"`
	SM     map[string]any        `json:"sm"`
	SR     map[string]RawMessage `json:"sr"`
	unexp  int
}

type textKey struct{ a, b string }

func (k textKey) MarshalText() ([]byte, error) { return []byte(k.a + "-" + k.b), nil }

func (k *textKey) UnmarshalText(b []byte) error {
	a, c, ok := strings.Cut(string(b), "-")
	if !ok {
		return errors.New("bad key")
	}
	k.a, k.b = a, c
	return nil
}

type upper string

func (u upper) MarshalJSON() ([]byte, error) {
	return []byte(`"` + strings.ToUpper(string(u)) + `"`), nil
}

func (u *upper) UnmarshalJSON(b []byte) error {
	var s string
	if err := Unmarshal(b, &s); err != nil {
		return err
	}
	*u = upper(strings.ToLower(s))
	return nil
}

type ptrMarshal struct{ X int }

func (p *ptrMarshal) MarshalJSON() ([]byte, error) {
	return []byte(` { "x" : ` + strconv.Itoa(p.X) + ` } `), nil
}

type badMarshal struct{}

func (badMarshal) MarshalJSON() ([]byte, error) { return []byte(`{`), nil }

type errMarshal struct{}

func (errMarshal) MarshalJSON() ([]byte, error) { return nil, errors.New("boom") }

type textOnly int

func (t textOnly) MarshalText() ([]byte, error) {
	return []byte("t<" + strconv.Itoa(int(t)) + ">"), nil
}

func (t *textOnly) UnmarshalText(b []byte) error {
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(string(b), "t<"), ">"))
	*t = textOnly(n)
	return err
}

type node struct {
	V    int   `json:"v"`
	Next *node `json:"next,omitempty"`
}

type fold struct {
	FooBar int
	K      int `json:"kelvin"`
	S      int `json:"ſ"`
}

type conflict struct {
	A1
	A2
	X int `json:"x"`
}

// foldDup has names that fold alike, foldDeep one of them at two depths,
// and ctxEmbed a field through an embedded pointer, for error contexts.
type foldDup struct {
	A int `json:"ab"`
	B int `json:"AB"`
	C int `json:"Ab"`
}

type foldDeep struct {
	FoldInner
	AB int `json:"AB"`
}

type FoldInner struct {
	Ab int `json:"ab"`
}

type ctxEmbed struct {
	*CtxInner
	X int `json:"x"`
}

type CtxInner struct {
	Y int `json:"y"`
	Z struct {
		W int `json:"w"`
	} `json:"z"`
}

type A1 struct {
	Dup int
	Y   int
}

type A2 struct {
	Dup int
	Y   int
}

func sameError(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if reflect.TypeOf(a) != reflect.TypeOf(b) || a.Error() != b.Error() {
		return false
	}
	var sa, sb *SyntaxError
	if errors.As(a, &sa) && errors.As(b, &sb) && sa.Offset != sb.Offset {
		return false
	}
	var ua, ub *UnmarshalTypeError
	if errors.As(a, &ua) && errors.As(b, &ub) && (ua.Offset != ub.Offset || ua.Field != ub.Field || ua.Value != ub.Value || ua.Type != ub.Type) {
		return false
	}
	return true
}

func describe(err error) string {
	if err == nil {
		return "<nil>"
	}
	s := fmt.Sprintf("%T %v", err, err)
	var se *SyntaxError
	if errors.As(err, &se) {
		s += fmt.Sprintf(" offset %d", se.Offset)
	}
	var ue *UnmarshalTypeError
	if errors.As(err, &ue) {
		s += fmt.Sprintf(" offset %d field %q value %q type %v", ue.Offset, ue.Field, ue.Value, ue.Type)
	}
	return s
}

var unmarshalDocs = []string{
	`{"name":"x","age":3,"score":1.5,"f32":3.4028235e38,"tags":["a","b"],"arr":[1,2,3,4],"inner":{"a":1,"b":"c"},"pinner":{"a":2},"m":{"k":1},"im":{"1":"a","-2":"b"},"any":{"x":[1,"y",true,null,{"z":1e400}]},"raw":  [1, 2 ] ,"num":-1.5e3,"bytes":"aGVsbG8=","quoted":"12","qstr":"\"q\"","qbool":"true","-":1,"u8":255,"i8":-128,"pp":5,"time":"2024-01-02T03:04:05Z","dur":123,"sm":{"a":1,"b":[{}]},"sr":{"a":{"b":1},"c":null}}`,
	`{"E1":1,"e2":"x","P1":2}`,
	`{"NAME":"ci","Age":2,"INNER":{"A":5}}`,
	`{"name":1}`,
	`{"age":"x","name":"y","score":true}`,
	`{"u8":256}`,
	`{"i8":-129}`,
	`{"age":1.5}`,
	`{"age":1e3}`,
	`{"tags":{"a":1}}`,
	`{"inner":[1]}`,
	`{"m":{"k":"v"}}`,
	`{"im":{"x":"a"}}`,
	`{"bytes":"!!"}`,
	`{"bytes":null,"tags":null,"m":null,"pinner":null,"pp":null,"any":null,"raw":null,"num":null}`,
	`{"quoted":12}`,
	`{"quoted":"x"}`,
	`{"quoted":"12.5"}`,
	`{"qstr":"q"}`,
	`{"qbool":"maybe"}`,
	`{"quoted":"null"}`,
	`{"quoted":null}`,
	`{"num":"12"}`,
	`{"num":"x"}`,
	`{"iface":"x"}`,
	`{"iface":null}`,
	`{"time":"bad"}`,
	`{"time":5}`,
	`{"name":"a\u00e9\ud83d\ude00\ud800x\\\/\b\f\n\r\t\"","tags":["\u2028"]}`,
	"{\"name\":\"\xff\xfe ok \xc3\xa9\"}",
	`{"arr":[1]}`,
	`[]`,
	`null`,
	`{"a":1,}`,
	`{"a" 1}`,
	`{"a":tru}`,
	`{"a":-}`,
	`{"a":01}`,
	`{"a":1.}`,
	`{"a":1e}`,
	`{"a":"\x01"}`,
	`{"a":"\u12"}`,
	`{"a":"\q"}`,
	`{"a":1}x`,
	`{"a":1} `,
	`  {"a":1}`,
	``,
	`   `,
	`{`,
	`[1,2`,
	`"abc`,
	`{"inner":{"a":1,"zzz":{"deep":[1,2,{"x":"y"}]},"b":"q"}}`,
	`{"score":1e400}`,
	`{"score":-0}`,
	`{"f32":1e39}`,
}

func checkUnmarshal[T any](t *testing.T, doc string) {
	t.Helper()
	var got, want T
	gerr := Unmarshal([]byte(doc), &got)
	werr := stdjson.Unmarshal([]byte(doc), &want)
	if !sameError(gerr, werr) {
		t.Errorf("Unmarshal %.200q into %T:\n got error %s\nwant error %s", doc, &got, describe(gerr), describe(werr))
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Unmarshal %.200q into %T:\n got %#v\nwant %#v", doc, &got, got, want)
	}
}

func TestUnmarshalOracle(t *testing.T) {
	for _, doc := range unmarshalDocs {
		checkUnmarshal[outer](t, doc)
		checkUnmarshal[any](t, doc)
		checkUnmarshal[map[string]any](t, doc)
		checkUnmarshal[map[string]RawMessage](t, doc)
		checkUnmarshal[[]any](t, doc)
		checkUnmarshal[fold](t, doc)
		checkUnmarshal[conflict](t, doc)
	}
	for _, doc := range []string{
		`{"FOOBAR":1,"KELVIN":2,"S":3}`, `{"foobar":1,"kelvin":2,"s":3}`,
		`{"x":1,"y":2,"Dup":3}`,
		`[1,2,3]`, `[]`, `[[1],[2,3]]`, `[1,"a"]`, `"str"`, `12`, `-0.0`, `1e-400`,
		`{"a-b":1,"c-d":2}`, `{"bad":1}`,
		`"up"`, `"T<5>"`, `"t<5>"`, `5`,
		`{"v":1,"next":{"v":2,"next":{"v":3}}}`,
		`{"aB":1}`, `{"AB":1,"aB":2}`, `{"ab":1,"Ab":2,"AB":3}`, `{"aB":"s"}`,
		`{"y":"s"}`, `{"Y":1,"x":"t"}`, `{"z":{"w":"s"}}`, `{"Z":{"W":true}}`,
	} {
		checkUnmarshal[fold](t, doc)
		checkUnmarshal[conflict](t, doc)
		checkUnmarshal[foldDup](t, doc)
		checkUnmarshal[foldDeep](t, doc)
		checkUnmarshal[ctxEmbed](t, doc)
		checkUnmarshal[[]int](t, doc)
		checkUnmarshal[[2]int](t, doc)
		checkUnmarshal[[][]int](t, doc)
		checkUnmarshal[[]any](t, doc)
		checkUnmarshal[any](t, doc)
		checkUnmarshal[float64](t, doc)
		checkUnmarshal[float32](t, doc)
		checkUnmarshal[int](t, doc)
		checkUnmarshal[uint](t, doc)
		checkUnmarshal[string](t, doc)
		checkUnmarshal[*int](t, doc)
		checkUnmarshal[map[textKey]int](t, doc)
		checkUnmarshal[map[string]int](t, doc)
		checkUnmarshal[upper](t, doc)
		checkUnmarshal[textOnly](t, doc)
		checkUnmarshal[node](t, doc)
		checkUnmarshal[[]byte](t, doc)
		checkUnmarshal[Number](t, doc)
	}
}

// TestUnmarshalUnquotedName checks inputs with a member name without
// quotes after a comma, which are syntax errors before any method runs.
func TestUnmarshalUnquotedName(t *testing.T) {
	for _, doc := range []string{
		`{"":0,"":"","":0,"":"","":"00","0":"","0":"","0":"0000000000",00:""}`,
		`{"a":0,0:""}`, `{"a":"b",2:1}`, `{"a":{},8 0}`,
		strings.Repeat(" ", 64) + `{"a":0,0:"x"}`,
	} {
		checkUnmarshal[map[string]*counted](t, doc)
		checkUnmarshal[map[string]RawMessage](t, doc)
		checkUnmarshal[map[string]any](t, doc)
		checkUnmarshal[any](t, doc)
		checkUnmarshal[outer](t, doc)
	}
}

// TestUnmarshalNullKeys checks map keys of number types that are a quoted
// null, which encoding/json/v2 reads as the zero key, by Unmarshal and by
// a Decoder.
func TestUnmarshalNullKeys(t *testing.T) {
	for _, doc := range []string{`{"null":null}`, `{"null":"x"}`, `{"nul\u006c":"y"}`, `{"0":"a","null":"b"}`, `{"null":"c","1":"d"}`, `{"null":1}`, `{"null ":"e"}`, `{"Null":"f"}`} {
		checkUnmarshal[map[int]string](t, doc)
		checkUnmarshal[map[uint8]string](t, doc)
		checkUnmarshal[map[uintptr]string](t, doc)
		checkUnmarshal[map[float64]string](t, doc)
		checkUnmarshal[map[float32]string](t, doc)
		checkUnmarshal[map[string]string](t, doc)
		checkUnmarshal[map[textKey]string](t, doc)
		var got, want map[int]string
		gerr := NewDecoder(strings.NewReader(doc)).Decode(&got)
		werr := stdjson.NewDecoder(strings.NewReader(doc)).Decode(&want)
		if !sameError(gerr, werr) || !reflect.DeepEqual(got, want) {
			t.Errorf("Decode %q into map[int]string:\n got %#v %s\nwant %#v %s", doc, got, describe(gerr), want, describe(werr))
		}
	}
}

// TestUnmarshalNumberKeys compares map keys of number types that are not
// JSON numbers, and the value errors after them, with encoding/json. NaN
// keys are compared as printed.
func TestUnmarshalNumberKeys(t *testing.T) {
	for _, k := range []string{
		"00", "01", "-00", "-01", "+1", ".5", "1.", "0.", "00.5", "00e1", "1E5", "-0", "1e", "1e+", "", "-",
		"0x10", "0X1p4", "0x1p-2", "+0x1p1", "0x_1p0", "0x1P+129", "1_0", "0_1", "1__0", "_1", "1_", "0b1", "0o7",
		"inf", "+Inf", "-inf", "infinity", "Infinity", "NaN", "nan", "-NaN", " 1", "1 ",
		"1e39", "3.5e38", "1e-50", "1e-400", "-1e-400", "1e308", "1.8e308", "1e400",
	} {
		for _, doc := range []string{fmt.Sprintf(`{%q:1}`, k), fmt.Sprintf(`{%q:"",%q:2}`, k, "1")} {
			for _, typ := range []struct {
				name string
				new  func() any
			}{
				{"float64", func() any { return new(map[float64]int) }},
				{"float32", func() any { return new(map[float32]int) }},
				{"int", func() any { return new(map[int]int) }},
				{"uint8", func() any { return new(map[uint8]int) }},
				{"struct", func() any { return new(map[float64]struct{ A int }) }},
			} {
				got, want := typ.new(), typ.new()
				gerr, werr := Unmarshal([]byte(doc), got), stdjson.Unmarshal([]byte(doc), want)
				if !sameError(gerr, werr) || fmt.Sprint(got) != fmt.Sprint(want) {
					t.Errorf("Unmarshal %q into %s keys: got %v, %s; want %v, %s", doc, typ.name, got, describe(gerr), want, describe(werr))
				}
			}
		}
	}
}

func TestUnmarshalMerge(t *testing.T) {
	doc := `{"tags":["x"],"m":{"b":2},"inner":{"b":"n"},"pinner":{"b":"m"},"arr":[9]}`
	init := func() outer {
		return outer{Tags: []string{"a", "b", "c"}, M: map[string]int{"a": 1}, Inner: inner{A: 7}, PInner: &inner{A: 8}, Arr: [3]int{1, 2, 3}}
	}
	got, want := init(), init()
	gerr := Unmarshal([]byte(doc), &got)
	werr := stdjson.Unmarshal([]byte(doc), &want)
	if !sameError(gerr, werr) || !reflect.DeepEqual(got, want) {
		t.Errorf("merge:\n got %#v %v\nwant %#v %v", got, gerr, want, werr)
	}
	var a, b any = map[string]any{"k": 1.0}, map[string]any{"k": 1.0}
	pa, pb := &a, &b
	Unmarshal([]byte(`{"j":2}`), pa)
	stdjson.Unmarshal([]byte(`{"j":2}`), pb)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("interface merge: got %#v want %#v", a, b)
	}
}

func TestUnmarshalInvalid(t *testing.T) {
	var x int
	for _, v := range []any{nil, x, (*int)(nil), map[string]int{}} {
		if g, w := Unmarshal([]byte(`1`), v), stdjson.Unmarshal([]byte(`1`), v); !sameError(g, w) {
			t.Errorf("Unmarshal into %T: got %s want %s", v, describe(g), describe(w))
		}
	}
	// A quoted string field whose content starts with a byte that is not
	// part of valid UTF-8.
	type quoted struct {
		S string `json:"s,string"`
	}
	in := []byte("{\"s\":\"\x83\"}")
	var g, w quoted
	if ge, we := Unmarshal(in, &g), stdjson.Unmarshal(in, &w); !sameError(ge, we) {
		t.Errorf("Unmarshal %q: got %s want %s", in, describe(ge), describe(we))
	}
}

// counted counts its UnmarshalJSON and UnmarshalText calls.
type counted struct{ N int }

var countedCalls int

func (c *counted) UnmarshalJSON(b []byte) error {
	countedCalls++
	c.N = len(b)
	return nil
}

type countedKey string

func (k *countedKey) UnmarshalText(b []byte) error {
	countedCalls++
	*k = countedKey(b)
	return nil
}

type countedHolder struct {
	A  []counted                 `json:"a"`
	M  map[string][]counted      `json:"m"`
	K  map[countedKey]int        `json:"k"`
	Q  *counted                  `json:"q,string"`
	X  any                       `json:"x"`
	S  []map[string]countedSlice `json:"s"`
	In struct{ C counted }       `json:"in"`
}

type countedSlice []counted

// TestUnmarshalMethodsAfterError checks that a syntax error anywhere in the
// input is reported before any UnmarshalJSON or UnmarshalText method runs,
// wherever the method's value is nested, and that the value is unchanged.
func TestUnmarshalMethodsAfterError(t *testing.T) {
	valid := []string{
		`{"a":[{"n":1},2]}`,
		`{"m":{"x":[1,[2],{"y":3}]}}`,
		`{"k":{"t1":1,"t2":2}}`,
		`{"q":"\"5\""}`,
		`{"x":[1,{"a":2}],"a":[3]}`,
		`{"s":[{"z":[1,2]},{"w":[]}]}`,
		`{"in":{"C":[[[]]]}}`,
		`[{"a":[1]}]`,
	}
	bad := []string{`,`, `]`, `}`, ` x`, `"`, `{`, `[`, `,"zz":`, `1`, `"\q"`, "\x01", `tru`}
	for _, doc := range valid {
		for _, b := range bad {
			for cut := len(doc) - 1; cut >= 0; cut-- {
				in := doc[:cut] + b + doc[cut:]
				if stdjson.Valid([]byte(in)) {
					continue
				}
				countedCalls = 0
				var got countedHolder
				var gotList []countedHolder
				var err error
				if doc[0] == '[' {
					err = Unmarshal([]byte(in), &gotList)
				} else {
					err = Unmarshal([]byte(in), &got)
				}
				var want any
				werr := stdjson.Unmarshal([]byte(in), &want)
				if !sameError(err, werr) || countedCalls != 0 || !reflect.DeepEqual(got, countedHolder{}) || gotList != nil {
					t.Fatalf("%s: got %s, %d calls, %+v %v; want %s", in, describe(err), countedCalls, got, gotList, describe(werr))
				}
			}
		}
		countedCalls = 0
		var got countedHolder
		if err := Unmarshal([]byte(doc), &got); doc[0] == '{' && err != nil {
			t.Errorf("%s: %v", doc, err)
		}
	}
}

// vecFloat is a float type of its own, which errors name.
type vecFloat float64

var marshalValues = []any{
	nil, true, false, 0, -1, int64(math.MaxInt64), uint64(math.MaxUint64), int8(-5), uintptr(7),
	0.0, math.Copysign(0, -1), 1.0, -1.5, 1e20, 1e21, 1e-6, 1e-7, 123456789.125, 5e-324, math.MaxFloat64,
	float32(0.1), float32(1e21), float32(3.4e38), float32(1e-7),
	math.NaN(), math.Inf(1), float32(math.Inf(-1)),
	[]float64(nil), []float64{0.1, -2, 1e21, 5e-324, 0.012345678, -0.0001}, []float64{1.5, math.NaN()}, []vecFloat{0.25, vecFloat(math.Inf(-1))},
	"", "plain", "<a&b>", "\u2028\u2029", "\x00\x1f\x7f", "\b\f\n\r\t\"\\/", "\xff\xfeé😀", strings.Repeat("long string ", 600), strings.Repeat("é<", 3000),
	[]byte(nil), []byte{}, []byte("hello world"), [3]byte{1, 2, 3},
	[]int(nil), []int{}, []int{1, 2}, [0]int{}, [2]string{"a", "b"},
	map[string]int(nil), map[string]int{}, map[string]int{"b": 2, "a": 1, "<": 3},
	map[int]string{10: "a", -1: "b", 2: "c"}, map[uint8]bool{3: true, 1: false},
	map[textKey]int{{"b", "1"}: 1, {"a", "2"}: 2},
	map[string]any{"z": []any{1, "x", nil, map[string]any{"q": 1.5}}, "a": true},
	map[string]RawMessage{"x": RawMessage(` {"a" : 1} `), "y": nil},
	[]any{1, "a", nil, true, 2.5, map[string]any{}, []any{}},
	RawMessage(nil), RawMessage(`  [1, 2]  `), RawMessage(`{bad`), &[]RawMessage{RawMessage(`"<>"`)},
	Number("12.5"), Number(""), Number("abc"),
	inner{A: 1, B: "x"}, &inner{A: 2},
	outer{Name: "n", Tags: []string{"t"}, M: map[string]int{"k": 1}, Embedded: Embedded{E1: 1}, Raw: RawMessage(`1`), Quoted: 5, QStr: "s", QBool: true},
	&outer{embeddedPtr: &embeddedPtr{P1: 3}, Any: map[string]any{"x": 1}, Iface: time.Second, Num: "3", Time: time.Unix(0, 0).UTC()},
	upper("abc"), []upper{"a", "b"}, map[string]upper{"k": "v"},
	ptrMarshal{X: 1}, &ptrMarshal{X: 2}, []ptrMarshal{{X: 3}}, struct{ P ptrMarshal }{ptrMarshal{X: 4}}, &struct{ P ptrMarshal }{ptrMarshal{X: 5}},
	badMarshal{}, errMarshal{}, []errMarshal{{}},
	RawMessage(`{"both":"json:",000:` + strings.Repeat(" ", 41) + `""}`), RawMessage(`{"a":1,0:2}`),
	textOnly(3), []textOnly{1, 2}, map[textOnly]int{1: 1},
	fold{FooBar: 1}, conflict{X: 1},
	&node{V: 1, Next: &node{V: 2}},
	make(chan int), func() {}, complex(1, 2), map[[2]int]int{{1, 2}: 3},
	struct {
		C chan int `json:"-"`
		X int      `json:",omitempty"`
		Y *int     `json:"y,omitempty"`
		Z string   `json:"z,string"`
		W float64  `json:"w,string"`
		V *int     `json:"v,string"`
		B bool     `json:",string"`
	}{Z: "zz", W: 1.5},
	time.Date(2024, 1, 2, 3, 4, 5, 6, time.UTC),
	// Pointer-shaped values, which interfaces may hold directly.
	struct {
		X struct{}
		M map[string]int
	}{M: map[string]int{"a": 1}},
	struct{ M map[string]int }{M: map[string]int{"b": 2}},
	[1]struct{ P *int }{{P: new(int)}},
	[]any{struct{ P *inner }{&inner{A: 3}}, [1]map[string]int{{"c": 3}}},
}

func TestMarshalOracle(t *testing.T) {
	for _, v := range marshalValues {
		got, gerr := Marshal(v)
		want, werr := stdjson.Marshal(v)
		if !sameError(gerr, werr) || !bytes.Equal(got, want) {
			t.Errorf("Marshal %T %.80v:\n got %.300s %s\nwant %.300s %s", v, v, got, describe(gerr), want, describe(werr))
		}
		got, gerr = MarshalIndent(v, ">", "\t")
		want, werr = stdjson.MarshalIndent(v, ">", "\t")
		if !sameError(gerr, werr) || !bytes.Equal(got, want) {
			t.Errorf("MarshalIndent %T %.80v:\n got %.300s %s\nwant %.300s %s", v, v, got, describe(gerr), want, describe(werr))
		}
	}
}

// TestLazyStrings checks long strings that are plain at their start and
// may need an escape later, which result finds as it copies them.
func TestLazyStrings(t *testing.T) {
	plain := strings.Repeat("abcdefghijklmnopqrstuvwxyz012345", lazyMin/32)
	var cases []string
	for _, at := range []int{lazyCheck - 1, lazyCheck, lazyCheck + 63, lazyMin - 1, lazyMin + 5} {
		for _, c := range []string{"<", "&", "\"", "\\", "\n", "\x7f", "é", "\u2028", "\xff"} {
			s := plain + plain[:10]
			cases = append(cases, s[:at]+c+s[at:])
		}
	}
	cases = append(cases, plain, plain+plain[:7], plain[:lazyMin-1])
	for i, s := range cases {
		for _, v := range []any{
			s,
			[]string{s, "x", s},
			[]string{plain, s, plain + "<", s},
			map[string]any{"a": s, "b": []any{plain, s}},
		} {
			got, gerr := Marshal(v)
			want, werr := stdjson.Marshal(v)
			if !sameError(gerr, werr) || !bytes.Equal(got, want) {
				t.Fatalf("Marshal %d %T: got %s", i, v, describe(gerr))
			}
			got, gerr = MarshalIndent(v, "", " ")
			want, werr = stdjson.MarshalIndent(v, "", " ")
			if !sameError(gerr, werr) || !bytes.Equal(got, want) {
				t.Fatalf("MarshalIndent %d %T: got %s", i, v, describe(gerr))
			}
			var buf, wbuf bytes.Buffer
			enc, wenc := NewEncoder(&buf), stdjson.NewEncoder(&wbuf)
			enc.SetEscapeHTML(false)
			wenc.SetEscapeHTML(false)
			gerr, werr = enc.Encode(v), wenc.Encode(v)
			if !sameError(gerr, werr) || !bytes.Equal(buf.Bytes(), wbuf.Bytes()) {
				t.Fatalf("Encode %d %T: got %s", i, v, describe(gerr))
			}
		}
	}
}

// randomIterations limits repeated random cases under the race detector.
// Table cases, boundary checks and concurrent tests keep their full coverage.
func randomIterations(n int) int {
	if raceEnabled {
		return max(1, n/10)
	}
	return n
}

// floatSamples returns floats of up to seventeen digits, neighbors of
// powers of ten and of the bounds of the plain form, and random bit
// patterns.
func floatSamples() []float64 {
	r := rand.New(rand.NewSource(1))
	out := []float64{0, math.Copysign(0, -1), 1e-6, 1e15, 1e21, 0.1, 0.3, 1.5, 2.5, 1.0 / 3, 99999999999999.9, 999999999999999.5}
	for e := -30; e <= 30; e++ {
		f, _ := strconv.ParseFloat("1e"+strconv.Itoa(e), 64)
		out = append(out, f, math.Nextafter(f, 0), math.Nextafter(f, math.Inf(1)), f*3, f*9.5)
	}
	for i := 0; i < randomIterations(400000); i++ {
		digits := 1 + r.Intn(17)
		m := r.Int63n(int64(math.Pow10(digits)))
		f, _ := strconv.ParseFloat(strconv.FormatInt(m, 10)+"e"+strconv.Itoa(r.Intn(32)-24), 64)
		if r.Intn(2) == 0 {
			f = -f
		}
		out = append(out, f, float64(float32(f)))
		if bits := math.Float64frombits(r.Uint64()); !math.IsNaN(bits) && !math.IsInf(bits, 0) {
			out = append(out, bits)
		}
	}
	return out
}

func TestFloatOracle(t *testing.T) {
	for _, f := range floatSamples() {
		want, _ := stdjson.Marshal(f)
		if got, err := Marshal(f); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("Marshal(%v) = %s, %v, want %s", f, got, err, want)
		}
		want32, _ := stdjson.Marshal(float32(f))
		if math.IsInf(float64(float32(f)), 0) {
			continue
		}
		if got, err := Marshal(float32(f)); err != nil || !bytes.Equal(got, want32) {
			t.Fatalf("Marshal(float32(%v)) = %s, %v, want %s", f, got, err, want32)
		}
		// Read back in plain notation, which goes through the fast parser.
		doc := strconv.FormatFloat(f, 'f', -1, 64)
		for _, in := range [][]byte{want, []byte(doc)} {
			var g, w float64
			gerr, werr := Unmarshal(in, &g), stdjson.Unmarshal(in, &w)
			if !sameError(gerr, werr) || math.Float64bits(g) != math.Float64bits(w) {
				t.Fatalf("Unmarshal(%s) = %v, %v, want %v, %v", in, g, gerr, w, werr)
			}
			var g32, w32 float32
			gerr, werr = Unmarshal(in, &g32), stdjson.Unmarshal(in, &w32)
			if !sameError(gerr, werr) || math.Float32bits(g32) != math.Float32bits(w32) {
				t.Fatalf("Unmarshal(%s) into float32 = %v, %v, want %v, %v", in, g32, gerr, w32, werr)
			}
		}
	}
}

func TestDigits(t *testing.T) {
	// Under the race detector, longer steps cover the same range.
	stride := uint64(7)
	if raceEnabled {
		stride = 79
	}
	for x := uint64(0); x < 100000000; x += 1 + x%stride {
		want := fmt.Sprintf("%08d", x)
		var b [8]byte
		storeWord(b[:], 0, digits8(x))
		if string(b[:]) != want {
			t.Fatalf("digits8(%d) = %q", x, b)
		}
		if got := parse8(digits8(x) ^ asciiZeros); got != x {
			t.Fatalf("parse8(%q) = %d", want, got)
		}
	}
}

func TestAppendInt(t *testing.T) {
	check := func(u uint64) {
		t.Helper()
		if got, want := string(appendUint([]byte("x"), u)), "x"+strconv.FormatUint(u, 10); got != want {
			t.Fatalf("appendUint(%d) = %q", u, got)
		}
		n := int64(u)
		if got, want := string(appendInt(nil, n)), strconv.FormatInt(n, 10); got != want {
			t.Fatalf("appendInt(%d) = %q", n, got)
		}
		// With and without room for eight more bytes.
		for room := range 10 {
			b := append(make([]byte, 0, 3+room), "abc"...)
			if got, want := string(appendIntField(b, n)), "abc"+strconv.FormatInt(n, 10); got != want {
				t.Fatalf("appendIntField(%d) with room %d = %q", n, room, got)
			}
		}
	}
	for p := uint64(1); p < 1e19; p *= 10 {
		for _, u := range []uint64{p - 1, p, p + 1, 2*p - 1, 9 * p, 10*p - 1} {
			check(u)
		}
	}
	for _, u := range []uint64{0, math.MaxUint64, math.MaxInt64, 1 << 63, 1<<63 + 1, 1e19, 1e19 + 1} {
		check(u)
	}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 1000000; i++ {
		check(r.Uint64() >> (r.Intn(64)))
	}
}

// TestMarshalIntFields checks int fields, which structValue writes itself,
// of every length, as the output buffer fills.
func TestMarshalIntFields(t *testing.T) {
	type ints struct {
		A int   `json:"a"`
		B int64 `json:"b"`
		C int   `json:"c,omitempty"`
		D int64 `json:"dddddddddddddddddddddddddddddd,omitempty"`
	}
	var vals []int64
	for p := int64(1); p < 1e18; p *= 10 {
		vals = append(vals, p-1, p, p+1, -p, -p+1, 9*p)
	}
	vals = append(vals, math.MaxInt64, math.MinInt64, math.MinInt64+1)
	for k := range len(vals) * 3 {
		v := make([]ints, 1+k%17)
		var want strings.Builder
		want.WriteByte('[')
		for i := range v {
			a, b := vals[(k+i)%len(vals)], vals[(k*7+i*3)%len(vals)]
			v[i] = ints{int(a), b, int(b), a}
			if i > 0 {
				want.WriteByte(',')
			}
			fmt.Fprintf(&want, `{"a":%d,"b":%d`, a, b)
			if b != 0 {
				fmt.Fprintf(&want, `,"c":%d`, b)
			}
			if a != 0 {
				fmt.Fprintf(&want, `,"dddddddddddddddddddddddddddddd":%d`, a)
			}
			want.WriteByte('}')
		}
		want.WriteByte(']')
		got, err := Marshal(v)
		if err != nil || string(got) != want.String() {
			t.Fatalf("Marshal(%v) = %s, %v; want %s", v, got, err, want.String())
		}
	}
}

func TestMarshalCycle(t *testing.T) {
	n := &node{V: 1}
	n.Next = n
	_, gerr := Marshal(n)
	_, werr := stdjson.Marshal(n)
	if !sameError(gerr, werr) {
		t.Errorf("cycle: got %s want %s", describe(gerr), describe(werr))
	}
	m := map[string]any{}
	m["m"] = m
	_, gerr = Marshal(m)
	_, werr = stdjson.Marshal(m)
	if !sameError(gerr, werr) {
		t.Errorf("map cycle: got %s want %s", describe(gerr), describe(werr))
	}
	s := []any{nil}
	s[0] = s
	_, gerr = Marshal(s)
	_, werr = stdjson.Marshal(s)
	if !sameError(gerr, werr) {
		t.Errorf("slice cycle: got %s want %s", describe(gerr), describe(werr))
	}
}

var formatDocs = []string{
	`{"a" : [1, 2, {"b":null}] , "c":"<x>\u2028"}`, " \n[ ]\n ", `{}`, `"x"`, `1 `, ` {"a":[[],{}]}` + "\n",
	`{"a":}`, `[1,]`, `{"a":1}}`, ``, `nul`, `"abc`, `[` + strings.Repeat(`[`, 10001) + strings.Repeat(`]`, 10002),
	"\"\xe2\x80\xa8<&>\"",
	// A member name without quotes after a comma.
	`[null,{"":0,0:[{"":true},{"0":1000},"000",true]  },1000,10,true]`, `{"a":0,0:""}`, `{"a":"b",2:1}`, `{"a":[],8 0}`,
	strings.Repeat(" ", 64) + `{"a":0,0:""}`,
}

func TestFormatOracle(t *testing.T) {
	for _, doc := range formatDocs {
		if Valid([]byte(doc)) != stdjson.Valid([]byte(doc)) {
			t.Errorf("Valid(%.80q) = %v", doc, !stdjson.Valid([]byte(doc)))
		}
		var g, w bytes.Buffer
		g.WriteString("pre")
		w.WriteString("pre")
		gerr := Compact(&g, []byte(doc))
		werr := stdjson.Compact(&w, []byte(doc))
		if !sameError(gerr, werr) || g.String() != w.String() {
			t.Errorf("Compact(%.80q):\n got %.200q %s\nwant %.200q %s", doc, g.String(), describe(gerr), w.String(), describe(werr))
		}
		g.Reset()
		w.Reset()
		gerr = Indent(&g, []byte(doc), "#", "  ")
		werr = stdjson.Indent(&w, []byte(doc), "#", "  ")
		if !sameError(gerr, werr) || g.String() != w.String() {
			t.Errorf("Indent(%.80q):\n got %.200q %s\nwant %.200q %s", doc, g.String(), describe(gerr), w.String(), describe(werr))
		}
		g.Reset()
		w.Reset()
		HTMLEscape(&g, []byte(doc))
		stdjson.HTMLEscape(&w, []byte(doc))
		if g.String() != w.String() {
			t.Errorf("HTMLEscape(%.80q):\n got %.200q\nwant %.200q", doc, g.String(), w.String())
		}
	}
}

func TestRoundTripLarge(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < 5000; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"name":"item %d \u00e9","vals":[%d.5,-%de-3,true,null],"s":"%s"}`, i, i, i, i, strings.Repeat("x", i%300))
	}
	b.WriteString(`]}`)
	doc := []byte(b.String())
	checkUnmarshal[any](t, string(doc))
	checkUnmarshal[map[string][]map[string]any](t, string(doc))
	var v any
	if err := Unmarshal(doc, &v); err != nil {
		t.Fatal(err)
	}
	got, _ := Marshal(v)
	want, _ := stdjson.Marshal(v)
	if !bytes.Equal(got, want) {
		t.Errorf("large round trip differs")
	}
}

// TestLargeErrors checks a large input, with a string longer than the
// element count reads through, and with a syntax error near its start,
// middle or end, before which no method may run.
func TestLargeErrors(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < 3000; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"name":"item %d","vals":[%d.5,true,null],"c":{"n":%d}}`, i, i, i, i)
		if i == 1500 {
			fmt.Fprintf(&b, `,{"id":-1,"name":"%s"}`, strings.Repeat("y", 100<<10))
		}
	}
	b.WriteString(`]}`)
	doc := b.String()
	type item struct {
		ID   int     `json:"id"`
		Name string  `json:"name"`
		Vals []any   `json:"vals"`
		C    counted `json:"c"`
	}
	type holder struct {
		Items []item `json:"items"`
	}
	checkUnmarshal[holder](t, doc)
	checkUnmarshal[any](t, doc)
	for _, at := range []int{20, len(doc) / 2, len(doc) - 3} {
		for _, bad := range []string{",", "x", `"`} {
			in := doc[:at] + bad + doc[at:]
			countedCalls = 0
			checkUnmarshal[holder](t, in)
			if countedCalls != 0 && !stdjson.Valid([]byte(in)) {
				t.Errorf("error at %d: %d method calls", at, countedCalls)
			}
			checkUnmarshal[any](t, in)
		}
	}
}

// TestLongAnyArrays decodes into interface values arrays around the length
// from which the rest of an array is counted, valid and not.
func TestLongAnyArrays(t *testing.T) {
	for _, n := range []int{63, 64, 65, 66, 130, 1000} {
		var b strings.Builder
		b.WriteString(`{"a":[`)
		for i := range n {
			if i > 0 {
				b.WriteByte(',')
			}
			switch i % 4 {
			case 0:
				fmt.Fprintf(&b, "%d", i)
			case 1:
				fmt.Fprintf(&b, `"s%d,]"`, i)
			case 2:
				fmt.Fprintf(&b, `[%d,{"k":[%d]}]`, i, i)
			default:
				fmt.Fprintf(&b, `{"x":%d,"y":"]"}`, i)
			}
		}
		b.WriteString(`],"b":[[1,2],[3]]}`)
		doc := b.String()
		checkUnmarshal[any](t, doc)
		checkUnmarshal[map[string][]any](t, doc)
		for _, at := range []int{len(doc) / 2, len(doc) - 30, len(doc) - 3} {
			for _, bad := range []string{",", "]", "x", `"`, "[", "}"} {
				checkUnmarshal[any](t, doc[:at]+bad+doc[at:])
			}
			checkUnmarshal[any](t, doc[:at])
		}
	}
}

// TestAnyStringsAfterGC checks that strings decoded into interface values
// keep their bytes through collections, after the input is overwritten.
func TestAnyStringsAfterGC(t *testing.T) {
	var b strings.Builder
	b.WriteString(`[`)
	for i := range 3000 {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"k%d":"%s","e":"a\né%d","l":"%s"}`, i, strings.Repeat("v", i%97), i, strings.Repeat("w", i%1500))
	}
	b.WriteString(`]`)
	doc := []byte(b.String())
	var want any
	if err := stdjson.Unmarshal(doc, &want); err != nil {
		t.Fatal(err)
	}
	var got any
	if err := Unmarshal(doc, &got); err != nil {
		t.Fatal(err)
	}
	clear(doc)
	for range 3 {
		runtime.GC()
		garbage := make([][]byte, 1000)
		for i := range garbage {
			garbage[i] = bytes.Repeat([]byte{0xff}, 64+i%512)
		}
		runtime.KeepAlive(garbage)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("decoded strings changed after collections")
	}
}

// TestLongStringsOwnMemory decodes strings and RawMessage values around
// the length from which the decoder copies them around the caches, at
// several alignments of the input, and checks that they keep their bytes
// after the input is overwritten.
func TestLongStringsOwnMemory(t *testing.T) {
	type target struct {
		S   string            `json:"s"`
		A   any               `json:"a"`
		M   map[string]string `json:"m"`
		Raw RawMessage        `json:"raw"`
	}
	for _, n := range []int{copyLongMin - 1, copyLongMin, copyLongMin + 33, 1<<20 + 7} {
		s := longText(n, "abcdefghijklmnopqrstuvwxyz0123456789")
		doc := `{"s":"` + s + `","a":"` + s + `","m":{"k":"` + s + `"},"raw":"` + s + `"}`
		for off := range 3 {
			buf := make([]byte, off+len(doc))
			in := buf[off:]
			copy(in, doc)
			var got, want target
			if err := stdjson.Unmarshal(in, &want); err != nil {
				t.Fatal(err)
			}
			if err := Unmarshal(in, &got); err != nil {
				t.Fatal(err)
			}
			clear(buf)
			runtime.GC()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("length %d offset %d: decoded values changed after the input did", n, off)
			}
		}
	}
}
