package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type c struct {
	op, js, path string
	v            any
}

func show(s string, err error) string {
	if err != nil {
		return fmt.Sprintf("%q ERR=%q", s, err.Error())
	}
	return fmt.Sprintf("%q", s)
}

func res(r gjson.Result) string {
	return fmt.Sprintf("T=%d Raw=%q Str=%q Num=%v(%x) Idx=%d Idxs=%v", r.Type, r.Raw, r.Str, r.Num, math.Float64bits(r.Num), r.Index, r.Indexes)
}

func main() {
	group := os.Args[1]
	var cs []c
	switch group {
	case "ins":
		cs = []c{
			{"set", `{"a":1}`, "b", 2}, {"set", `{ "a" : 1 }`, "b", 2},
			{"set", `{"a":1 , "c":3 }`, "b", 2}, {"set", "{\n  \"a\": 1\n}", "b", 2},
			{"set", `{}`, "b", 2}, {"set", `{ }`, "b", 2}, {"set", "{\n}", "b", 2},
			{"set", `{"a":{"x":1}}`, "a.y", 2}, {"set", `{"a":{"x":1}}`, "a.b.c", 3},
			{"set", `{"name":{"last":"Anderson"}}`, "name.first", "Sara"},
			{"set", `[1,2]`, "2", 3}, {"set", `[1,2]`, "5", 3}, {"set", `[1,2]`, "-1", 3},
			{"set", `[1,2]`, "0", 9}, {"set", `[ ]`, "0", 1}, {"set", `[]`, "-1", 1},
			{"set", `[ 1 , 2 ]`, "-1", 3}, {"set", `{"a":[]}`, "a.1", "x"}, {"set", `[ 1 , 2 ]`, "4", 3},
			{"set", ``, "a", 1}, {"set", ``, "a.b", 1}, {"set", ``, "0", 1}, {"set", ``, "a.0", 1},
			{"set", ``, "a.-1", 1}, {"set", ``, ":0", 1}, {"set", ``, "a.:0", 1}, {"set", `  `, "a", 1},
			{"set", ``, "-1", 1}, {"set", ``, "2", 1}, {"set", ``, "a.2.b", 1}, {"set", `{}`, "a.1", 1},
			{"set", `{"a" : 1 , "b":2}`, "a", 5}, {"set", `  {"a":1}  `, "b", 2}, {"set", `{"a":1}`, "a.b", 2},
			{"set", `{"a":"x"}`, "a.0", 2}, {"set", `{"a":[1]}`, "a.b", 2}, {"set", `{"a":{}}`, "a.0", 2},
			{"set", `{"a":null}`, "a.b", 1}, {"set", `{"a":1,"a":2}`, "a", 3}, {"set", `{"a":1,"a":{"b":2}}`, "a.b", 3},
			{"set", `"x"`, "a", 1}, {"set", `123`, "a", 1}, {"set", `null`, "a", 1}, {"set", `[1]`, "a", 1},
			{"set", `{"a.b":1}`, `a\.b`, 2}, {"set", `{"a":1}`, "a", 2}, {"set", `{"a":1}`, "", 2},
			{"set", `{"a":1}`, "a.", 2}, {"set", `{"a":1}`, ".a", 2}, {"set", `{"a":1}`, "a..b", 2},
			{"set", `{"a":[1,2]}`, "a.-1", 3}, {"set", `{"a":[1,2]}`, "a.-2", 3}, {"set", `{"a":[1,2]}`, "a.01", 3},
			{"set", `{"a":1}`, `a\:b`, 2}, {"set", `{"a":1}`, `:1`, 2}, {"set", `[1]`, `:1`, 2},
		}
	case "del":
		cs = []c{
			{"del", `{"a":1,"b":2,"c":3}`, "a", nil}, {"del", `{"a":1,"b":2,"c":3}`, "b", nil},
			{"del", `{"a":1,"b":2,"c":3}`, "c", nil}, {"del", `{ "a" : 1 , "b" : 2 }`, "a", nil},
			{"del", `{ "a" : 1 , "b" : 2 }`, "b", nil}, {"del", `{"a":1}`, "a", nil}, {"del", `{ "a":1 }`, "a", nil},
			{"del", "{\n  \"a\": 1,\n  \"b\": 2\n}", "a", nil}, {"del", "{\n  \"a\": 1,\n  \"b\": 2\n}", "b", nil},
			{"del", `[1,2,3]`, "0", nil}, {"del", `[1,2,3]`, "1", nil}, {"del", `[1,2,3]`, "2", nil},
			{"del", `[1,2,3]`, "-1", nil}, {"del", `[1]`, "0", nil}, {"del", `[ 1 , 2 ]`, "1", nil},
			{"del", `{"a":1}`, "x", nil}, {"del", `{"a":1,"b":2}`, "a.x", nil}, {"del", `{"a":1}`, "", nil},
			{"del", `{"a":1}`, "a.#", nil}, {"del", `{"a":[1]}`, "a.#(=1)", nil}, {"del", `{"a":1}`, "a*", nil},
			{"del", `{"a":1,"a":2}`, "a", nil}, {"del", `{"a":{"b":1,"c":2}}`, "a.b", nil}, {"del", `1`, "a", nil},
		}
	case "val":
		vs := []any{"<>&", `q"b\s`, "a\nb\t\r\b\f", "\x01\x1f\x7f", "  ", "\xff\xfe", "é😀", "",
			0.1, 1e21, 1e20, 1e-7, 100.0, math.Copysign(0, -1), float32(0.1), float32(1e21), int64(math.MinInt64),
			uint64(math.MaxUint64), int8(-5), true, false, nil, []byte("hi"), []int{1, 2}, map[string]int{"z": 1, "a": 2},
			struct{ X int }{1}, json.RawMessage(`{"r":1}`), []string{"a"}, 3, uint(7), math.Inf(1), math.NaN(), "é\u0000"}
		for _, v := range vs {
			cs = append(cs, c{"set", `{"a":0}`, "a", v})
		}
		cs = append(cs, c{"raw", `{"a":1}`, "a", "  {\"x\":1}  "}, c{"raw", `{"a":1}`, "b", "  2  "},
			c{"raw", `{"a":1}`, "a", ""}, c{"raw", `{"a":1}`, "b", ""}, c{"raw", `[1]`, "-1", " 3 "},
			c{"raw", ``, "a", " 3 "}, c{"raw", ``, "a.b", " 3 "}, c{"raw", `{}`, "b", "\n3\n"})
	case "chars":
		for _, ch := range "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~ " {
			k := "a" + string(ch) + "b"
			kj, _ := json.Marshal(k)
			doc := `{"a":1,` + string(kj) + `:2}`
			s1, e1 := sjson.Set(doc, k, 9)
			s2, e2 := sjson.Delete(doc, k)
			s3, e3 := sjson.Set(`{"a":1}`, string(ch)+"a", 9)
			s4, e4 := sjson.Set(`{"a":1}`, "a"+string(ch), 9)
			fmt.Printf("ch %q | set %s | del %s | pre %s | suf %s | get %s\n", ch, show(s1, e1), show(s2, e2), show(s3, e3), show(s4, e4), res(gjson.Get(doc, k)))
		}
		return
	case "ws":
		cs = []c{
			{"set", `  {"a":1}  `, "a", 2}, {"set", `[ 1 , 2 ]`, "2", 3}, {"set", `[ ]`, "-1", 1}, {"set", `[ 1 ]`, "1", 2},
			{"set", `[ [1] , {"a" : 1} ]`, "3", 3}, {"set", `[ [ 1 ] , { "a" : 1 } ]`, "2", 3}, {"del", `[ 1 ]`, "0", nil},
			{"del", `{ "a" : 1 }`, "a", nil}, {"del", `{"a":1 , "b":2}`, "b", nil}, {"del", `{"a":1 ,"b":2 }`, "a", nil},
			{"del", `[1 ,2]`, "0", nil}, {"set", `{"a": { "x" : 1 } }`, "a.y", 2}, {"set", `{"a":{ }}`, "a.b", 1},
			{"set", `{"a":[ ]}`, "a.-1", 1}, {"set", `{"a":[ ]}`, "a.0", 1}, {"set", `{"a":[ 1 ]}`, "a.2", 2},
			{"set", ` [1] `, "-1", 2}, {"set", ` [1] `, "0", 2}, {"set", ` [1] `, "3", 2}, {"del", ` {"a":1} `, "a", nil},
			{"del", ` [1,2] `, "0", nil}, {"set", "{\"a\":1\n}", "b", 2}, {"set", "{\"a\":1}\n", "b", 2},
			{"set", `{"a":1}x`, "b", 2}, {"set", `{"a":1} {"c":3}`, "b", 2}, {"set", `{"a":[1,2]}`, "a.5", 9},
			{"set", `{"a":"x"}`, "a.-1", 9}, {"set", `{"a":[[1]]}`, "a.0.-1", 2}, {"del", `{"a":[]}`, "a.0", nil},
			{"del", `{"a":[]}`, "a.-1", nil}, {"set", `{"b":{"c":1}}`, "b.c.d.e", 1}, {"set", `{"a":1}`, "b.-1", 1},
			{"set", `{"a":1}`, "b.-1.c", 1}, {"set", `{"a":1}`, "b.-2", 1}, {"set", `{"a":1}`, "b.1.-1", 1},
			{"set", ``, "-2", 1}, {"set", ``, "a.-2", 1}, {"set", ``, "0.a", 1}, {"set", ``, "-1.a", 1},
			{"set", ``, `a\\b`, 1}, {"set", ``, `a\`, 1}, {"set", ``, `\:0`, 1}, {"set", ``, `::0`, 1},
			{"set", `{"a":1}`, "a.b.c", 1}, {"set", `{"a":{"b":1}}`, "a.b", map[string]any{"z": "<", "a": 1}},
		}
	case "wsdel":
		cs = []c{
			{"del", "{\t\"a\"\r:\r1\n}", "a", nil}, {"del", "{\t\"a\"\r:\r1\n,  \"b\"\r:\r2\n}", "a", nil},
			{"del", "{\t\"a\"\r:\r1\n,  \"b\"\r:\r2\n}", "b", nil}, {"del", "{\t\"a\":1\n,  \"b\":2\n,   \"c\":3\n}", "b", nil},
			{"del", "[\t1\n]", "0", nil}, {"del", "[\t1\n,  2\n]", "0", nil}, {"del", "[\t1\n,  2\n]", "1", nil},
			{"del", "[\t1\n,  2\n,   3\n]", "1", nil}, {"del", "[\t1\n,  2\n]", "-1", nil},
			{"set", "{\t\"a\"\r:\r1\n}", "b", 2}, {"set", "{\t}", "b", 2}, {"set", "[\t1\n]", "-1", 2},
			{"set", "[\t1\n]", "1", 2}, {"set", "[\t1\n]", "3", 2}, {"set", "[\t]", "-1", 2}, {"set", "[\t]", "0", 2},
			{"set", "[\t]", "2", 2}, {"set", "{\t\"a\"\r:\r1\n}", "a", 2}, {"set", "{\t\"a\"\r:\r1\n}", "a.b", 2},
			{"set", "{\t\"a\"\r:\r1\n}", "a.1", 2}, {"set", "\t{\"a\":1}\n", "a", 2}, {"set", "\t{\"a\":1}\n", "b", 2},
			{"del", "\t{\"a\":1}\n", "a", nil}, {"del", "\t{\"a\":1,\"b\":2}\n", "a", nil}, {"set", "\t[1]\n", "-1", 2},
			{"set", "\t[1]\n", "0", 2}, {"set", "\t[1]\n", "1", 2}, {"set", "\t[1]\n", "3", 2}, {"set", "\t1\n", "a", 2},
			{"set", "\t\n", "a", 2}, {"set", "{\"x\":\t{\t\"a\":1\n}\n}", "x.b", 2}, {"del", "{\"x\":\t{\t\"a\":1\n}\n}", "x.a", nil},
			{"set", "{\"x\":\t{\t\"a\":1\n}\n}", "x.a", 2}, {"set", "{\"x\":\t[\t1\n]\n}", "x.-1", 2},
			{"set", "{\"x\":\t[\t1\n]\n}", "x.3", 2}, {"set", "{\"x\":\t[\t]\n}", "x.0", 2}, {"set", "{\"x\":\t1\n}", "x.y.z", 2},
			{"set", `{"a":{"c":1},"a":{"b":2}}`, "a.b", 3}, {"set", `{"a":[1],"a":[1,2]}`, "a.1", 3},
			{"raw", `{"a":1}`, "a", "\t2\n"}, {"raw", `[1]`, "-1", "\t2\n"}, {"raw", `[1]`, "3", "\t2\n"},
			{"raw", `{"a":1}`, "b.c", "\t2\n"}, {"raw", `{"a":1}`, "a", `{"x" : [ 1 ]}`},
			{"set", `{"a":{"b":1}}`, "a.b.c", 2}, {"set", `[[1,2]]`, "0.5", 2}, {"set", `{"a":[ 1 , 2 ]}`, "a.2", 3},
			{"set", `{"a":[ 1 , 2 ]}`, "a.3", 3}, {"set", `{"a":[ [1] , {"b" : 2} ]}`, "a.3", 3},
		}
	case "valid":
		for _, v := range []string{"\"\xff\"", "\"\x01\"", "\"\x7f\"", `"\q"`, `"\u00g0"`, `"\u00"`, `"\ud800"`, "01", "-01", "1.", "-", "1e", "1e+", ".5", "+1", "-0", "0.0e-0",
			"NaN", "[1,]", `{"a":1,}`, `{a:1}`, " 1 ", "", " ", "[1]x", "\v1", "\u00a01", "\ufeff1", "tru", "true", "nul", "[", "]", "{", `{"a"}`, `{"a":}`,
			`[1 2]`, `{"a":1 "b":2}`, `"`, `"\"`, "1 2", "[]", "{}", "[[]]", `{"a":[{}]}`, "\t\n\r 1", "\f1", "1\x00", "[1,,2]", `{"a"::1}`, "-1.5E+10", "1E5", "Infinity", "\"\t\""} {
			fmt.Printf("valid %q => %v\n", v, gjson.Valid(v))
		}
		return
	case "build":
		cs = []c{
			{"set", `{}`, "b..c", 1}, {"set", ``, "01", 1}, {"set", ``, "1a", 1}, {"set", `[1,2]`, "", 1}, {"set", `{"a":[1]}`, "a.", 2},
			{"set", `{"a":[1]}`, "a.:0", 2}, {"set", `[]`, ":0", 1}, {"set", `{}`, "b.01", 1}, {"set", `{}`, "b.", 1}, {"set", `{}`, "b.:1", 1},
			{"set", `{"a":1}`, "a.01", 2}, {"set", `{"a":1}`, "a.:1", 2}, {"set", `{"a":[1,2]}`, "a.1.x", 2}, {"set", `{"a":[1,{"x":1}]}`, "a.1.y", 2},
			{"set", `[{"a":1}]`, "0.a", 2}, {"set", `[[1]]`, "0.-1", 2}, {"set", `{"a":{"b":[]}}`, "a.b.-1", "x"}, {"set", `{"a":"x"}`, "a.b.-1", 1},
			{"set", `{"a":1}`, "a.-1.b", 1}, {"set", `{"a":{"-1":5}}`, "a.-1", 1}, {"set", `[1]`, "-1.a", 1}, {"set", `[1]`, "0.-1", 1},
			{"del", `{"a":{"b":1}}`, "a.b.c", nil}, {"del", `[1]`, "5", nil}, {"del", `[1]`, "a", nil}, {"del", `{"a":1}`, "a.0", nil}, {"del", ``, "a", nil},
			{"set", `{"a":1}`, "a", "\u2028"}, {"set", `{"<":1}`, "<", 2}, {"set", `{"\u003c":1}`, "<", 2}, {"set", `{"a\u0062":1}`, "ab", 2},
			{"del", `{"a\u0062":1}`, "ab", nil},
		}
	case "dup":
		cs = []c{
			{"del", `{"a":{"c":1},"a":{"b":2}}`, "a.b", nil}, {"del", `{"a":1,"a":{"b":2}}`, "a.b", nil}, {"del", `{"a":[1],"a":[1,2]}`, "a.1", nil},
			{"set", `{"a":[{"b":1}]}`, "a.0.c", 2}, {"set", `{"a":[{"b":1}]}`, "a.0.b.c", 2}, {"set", `{"a":{"0":"x"}}`, "a.0", 2},
			{"set", `{}`, "a\bb", 1}, {"set", `{}`, "\u2028", 1}, {"set", `{}`, "\xffk", 1}, {"set", `{}`, "é", 1}, {"set", `{}`, "a\tb", 1},
			{"set", `{}`, "a\x7fb", 1}, {"set", `{}`, "a\\\\b", 1}, {"set", `{}`, "a\"b", 1}, {"set", `{}`, "a\x00b", 1},
			{"del", `{"a":1,"b":2}`, "a.", nil}, {"del", `{"a":[1,2]}`, "a.", nil}, {"del", `{"a":[1,2]}`, "a.:0", nil}, {"del", `{"a":[1,2]}`, "a.01", nil},
			{"del", `{"":1,"b":2}`, "", nil}, {"set", `{"a":1}`, "a.b", []any{1, "x"}}, {"set", `{"a":1}`, "a", map[string]any{}},
		}
	case "trim":
		cs = []c{
			{"set", `  {"a":{}}  `, "a.b", 1}, {"set", `  {"a":1}  `, "a.b", 1}, {"set", `  {"a":[1]}  `, "a.-1", 1},
			{"set", `  {"a":[1]}  `, "a.3", 1}, {"set", ` [[1]] `, "0.-1", 2}, {"set", `{"a":{}}x`, "a.b", 1}, {"set", `{"a":1}x`, "a", 2},
			{"set", `[1]x`, "-1", 2}, {"set", `{"a":1,"b":{}}  `, "b.c", 1}, {"set", `[1]`, "3.-1", 2}, {"set", `[1]`, "-1.-1", 2},
			{"set", `[1]`, "3.a", 2}, {"set", `{"a":[1]}`, "a.3.-1", 2}, {"set", ` {} `, "a", 1}, {"set", ` [] `, "-1", 1}, {"set", ` [] `, "0", 1},
			{"set", ` [1] `, "0.a", 1}, {"set", ` {"a":1} `, "a.b", 2}, {"del", ` {"a":1} `, "b", nil}, {"set", "{\"a\":1}", "b", "\u2028"},
		}
	case "get3":
		for _, g := range [][2]string{
			{`{"0":1,":0":2}`, ":0"}, {`[5]`, ":0"}, {`{"a":{"0":1}}`, "a.:0"}, {`{"::":1}`, "::"}, {`[1,2]`, "1."}, {`{"a":[1,2]}`, "a."},
			{`[1,2,3]`, "18446744073709551617"}, {`{"a":{"b":1}}`, "a.b."}, {`{"a b":1}`, "a b"},
		} {
			fmt.Printf("get %q %q => %s\n", g[0], g[1], res(gjson.Get(g[0], g[1])))
		}
		return
	case "idx":
		cs = []c{
			{"set", `[ 1 , 2 ]`, "", 3}, {"set", `{"a":[ 1 ]}`, "a.", 2}, {"set", `{"a":[ ]}`, "a.", 2}, {"set", `{"a":1}`, "a..", 2},
			{"set", ``, "", 1}, {"set", ``, ".", 1}, {"set", ``, "a..", 1}, {"set", `[1]`, "+1", 2}, {"set", `[1]`, "1a", 2}, {"set", `[1]`, "-0", 2},
			{"set", `[1]`, "00", 2}, {"set", `{"a":[ 1 ]}`, "a.1", 2}, {"set", `{"a":[ 1 ]}`, "a.0", 2}, {"set", `[[ 1 ]]`, "0.", 2},
			{"del", `[ 1 , 2 ]`, "", nil}, {"del", `[1,2]`, "+1", nil}, {"del", `[1,2]`, "-2", nil}, {"del", `[1,2]`, "00", nil},
			{"set", `{"a":[1]}`, "a.-1", nil}, {"raw", `{"a":[1]}`, "a.-1", ""}, {"set", `{"a":{"b":1}}`, "a", nil},
			{"set", `{"a":1}`, "b", []byte(nil)}, {"set", `{"a":1}`, "b", json.Number("12")}, {"set", `{"a":1}`, "b", complex(1, 2)},
		}
	case "misc":
		cs = []c{
			{"set", `{"a":1}`, "-1", 2}, {"set", `{"a":1}`, "a.-1", 2}, {"set", `{"a":1}`, "x@y", 2}, {"set", `{"a":1}`, "x#y", 2},
			{"set", `{"0":1}`, ":0", 2}, {"del", `{"0":1}`, ":0", nil}, {"del", `[1,2]`, ":1", nil}, {"set", `[1,2]`, ".", 3},
			{"set", `[1,2]`, ".a", 3}, {"set", `{"a":[1,2]}`, "a.1a", 3}, {"set", `{"a":[1,2]}`, `a.a\.b`, 3},

			{"set", `{"a":1}`, "a", []byte(`x"y`)}, {"set", `{"a":1}`, "a", float32(1.5)},
			{"set", `{"a":1}`, "a", 1e100}, {"set", `{"a":1}`, "a", 123456789.125}, {"set", `{"a":1}`, "a", int16(-3)},
		}
	case "methods":
		for _, raw := range []string{`1.50`, `-0`, `1e3`, `12345678901234567890`, `-9223372036854775809`, `9223372036854775807`, `1.9`, `-1.9`, `"12"`, `"1.5"`,
			`"true"`, `"abc"`, `"1e3"`, `"-7"`, `" 5"`, `"t"`, `"1"`, `"0"`, `"T"`, `"TRUE"`, `"yes"`, `true`, `false`, `null`, `{"a":1}`, `[1,"a"]`, `[]`, `"x\qz"`, `"xy\u00"`, `"\ud800\u0041"`, `"\ud800\udc00"`, `"a\u0000b"`, `0x10`, `-`, `1.5.2`, `"`, `x`} {
			r := gjson.Get(`{"k":`+raw+`}`, "k")
			fmt.Printf("m %s => T=%d Str=%q Num=%v S=%q I=%d U=%d F=%v B=%v E=%v O=%v A=%v Arr=%d\n", raw, r.Type, r.Str, r.Num, r.String(), r.Int(), r.Uint(), r.Float(), r.Bool(), r.Exists(), r.IsObject(), r.IsArray(), len(r.Array()))
		}
		for _, raw := range []string{`{"a":1, "b" : [2]}`, `[1, "a" ,{}]`, `5`, `null`, `""`, `[]`} {
			r := gjson.Parse(raw)
			var keys []string
			r.ForEach(func(k, v gjson.Result) bool {
				keys = append(keys, fmt.Sprintf("%s|%s|%d", k.Raw, v.Raw, v.Index))
				return true
			})
			var arr []string
			for _, a := range r.Array() {
				arr = append(arr, fmt.Sprintf("%s@%d", a.Raw, a.Index))
			}
			fmt.Printf("fe %q => %v arr=%v T=%d idx=%d\n", raw, keys, arr, r.Type, r.Index)
		}
		return
	case "methods2":
		for _, raw := range []string{`"99999999999999999999"`, `"-99999999999999999999"`, `"18446744073709551616"`, `"1e400"`, `"inf"`, `"NaN"`, `"0x10"`, `"1_0"`, `"+5"`, `"0x1p3"`,
			`1e19`, `-5`, `-1e19`, `1e400`, `0.5`, `-0.5`, `00012`, `-00`, `1E2`} {
			r := gjson.Get(`{"k":`+raw+`}`, "k")
			fmt.Printf("m %s => Raw=%q S=%q I=%d U=%d F=%v B=%v\n", raw, r.Raw, r.String(), r.Int(), r.Uint(), r.Float(), r.Bool())
		}
		for _, r := range []gjson.Result{{Type: gjson.Number, Num: 1.5}, {Type: gjson.Number, Num: 7}, {Type: gjson.Number, Raw: "x", Num: 2}, {Type: gjson.String, Str: "7"}, {Type: gjson.JSON}, {Type: gjson.Type(9)}, {Type: gjson.True}, {}} {
			fmt.Printf("c %+v => S=%q I=%d F=%v E=%v A=%d T=%q\n", r, r.String(), r.Int(), r.Float(), r.Exists(), len(r.Array()), r.Type.String())
		}
		return
	case "parse":
		for _, v := range []string{"  {\"a\":1}  ", " [1] x", " 5 x", " \"s\" ", "tru", "nul", "x", "", "  ", "-", "\"abc", "{\"a\":", "true1", "falsey", "null,1", "1,2", "\t-1.5e3,"} {
			fmt.Printf("parse %q => %s\n", v, res(gjson.Parse(v)))
		}
		r := gjson.Parse(` {"a":{"b":1},"c":[1,{"d":2}]}`)
		fmt.Printf("chain %s\n", res(r.Get("a").Get("b")))
		fmt.Printf("chain2 %s\n", res(r.Get("c").Get("1.d")))
		for _, a := range r.Get("c").Array() {
			fmt.Printf("arr %s\n", res(a))
		}
		for k, v := range r.Map() {
			fmt.Printf("map %q %s\n", k, res(v))
		}
		fmt.Printf("value %#v | %#v | %#v\n", r.Value(), gjson.Parse(`[1,"x",null,true,{"z":1.5}]`).Value(), gjson.Parse(`7`).Value())
		fmt.Printf("map-dup %v map-arr %v map-num %v\n", gjson.Parse(`{"a":1,"a":2}`).Map()["a"].Raw, len(gjson.Parse(`[1]`).Map()), gjson.Parse(`1`).Map() == nil)
		fmt.Printf("getbytes-idx %s\n", res(gjson.GetBytes([]byte(` {"a":{"b":1}}`), "a")))
		return
	case "esc":
		var sb strings.Builder
		for b := 0; b < 0x80; b++ {
			o, _ := sjson.Set(`{}`, "a", string(rune(b)))
			sb.WriteString(o[6 : len(o)-1])
			sb.WriteByte(' ')
		}
		fmt.Println(sb.String())
		for _, v := range []string{"\u2028\u2029", "\xff", "\xc3", "\xed\xa0\x80", "\xf4\x90\x80\x80", "é€😀", "\xe2\x82"} {
			o, _ := sjson.Set(`{}`, "a", v)
			k, _ := sjson.Set(`{}`, v, 1)
			fmt.Printf("v %q => %s key=> %s\n", v, o, k)
		}
		for _, f := range []float32{0.1, 1e20, 3.4e38, 1.0 / 3, 16777217} {
			o, _ := sjson.Set(`{}`, "a", f)
			fmt.Printf("f32 %v => %s\n", f, o)
		}
		b, err := sjson.SetBytes([]byte(`{"a":1}`), "b", complex(1, 2))
		fmt.Printf("setbytes-err nil=%v len=%d err=%v\n", b == nil, len(b), err)
		b, err = sjson.SetBytes([]byte(`{"a":1}`), "", 1)
		fmt.Printf("setbytes-empty nil=%v out=%q err=%v\n", b == nil, b, err)
		b, err = sjson.DeleteBytes([]byte(`{"a":1}`), "")
		fmt.Printf("delbytes-empty nil=%v out=%q err=%v\n", b == nil, b, err)
		return
	case "force":
		cs = []c{
			{"set", `[1,2]`, ":1", 3}, {"set", `[1]`, ":1", 3}, {"set", `[1]`, ":-1", 3}, {"set", `[1]`, ":", 3}, {"set", `[1,2]`, ":01", 3},
			{"set", `[1]`, ":a", 3}, {"set", `{"a":[1,2]}`, "a.:1.b", 3}, {"set", `[[1]]`, ":0.:0", 3}, {"del", `[1,2]`, ":-1", nil}, {"del", `[1,2]`, ":", nil},
			{"del", `[[1,2],[3]]`, "-1.0", nil}, {"del", `[[1,2],[3]]`, "1.-1", nil}, {"del", `{"a":[{"b":1}]}`, "a.-1.b", nil}, {"set", `[[1,2],[3]]`, "-1.0", 9},
			{"set", `{"a#b":1}`, `a\#b`, 2}, {"set", `{"a#b":1}`, "a#b", 2}, {"del", `{"a#b":1}`, `a\#b`, nil}, {"set", `{"a":1}`, `x\|y`, 2}, {"del", `{"a*b":1}`, `a\*b`, nil},
			{"set", `{"a":{"b":1}}`, "a.b", "x"}, {"set", `{"a":1}`, "a", true}, {"set", `{"a":1}`, "a", uint8(200)}, {"set", `{"a":1}`, "a", int64(-1 << 63)},
		}
	case "html":
		for _, v := range []string{"<", "a<b>&", "<\n", "<\u2028", "<\xff", "<\"", "<\\", "\u2028", "<\x01", "é<", "\x7f<"} {
			o, _ := sjson.Set(`{}`, "a", v)
			k, _ := sjson.Set(`{}`, v, 1)
			b, _ := sjson.Set(`{"x":1}`, "y."+strings.ReplaceAll(v, ".", "\\."), 1)
			r, _ := sjson.Set(`[1]`, "-1", v)
			fmt.Printf("%q val=> %s key=> %s nested=> %s arr=> %s\n", v, o, k, b, r)
		}
		return
	case "delws":
		cs = []c{
			{"del", `{ "a" : 1 }`, "a", nil}, {"del", `{"a":1}`, "a", nil}, {"del", `{"a":1 ,"b":2}`, "a", nil},
			{"del", ` { "a" : 1 , "b" : 2 , "c" : 3 } `, "b", nil}, {"del", ` { "a" : 1 , "b" : 2 , "c" : 3 } `, "c", nil},
			{"del", "{\n \"a\": 1 ,\n \"b\": 2\n}", "b", nil}, {"del", "{\n \"a\": 1 ,\n \"b\": 2\n}", "a", nil},
			{"del", `[ 1 ]`, "0", nil}, {"del", `[1]`, "0", nil}, {"del", `[ 1 , 2 , 3 ]`, "1", nil}, {"del", `[1 ,2]`, "0", nil},
			{"del", `{"x":{ "a" : 1 }}`, "x.a", nil}, {"del", `{"x":[ 1 , 2 ]}`, "x.0", nil}, {"del", `{"a":1,"a":2}`, "a", nil},
			{"del", `{"a":{"b":1},"a":{"c":2}}`, "a.c", nil},
		}
	case "bytesval":
		for _, v := range []any{[]byte("ab<"), []byte{}, []byte(nil), json.RawMessage(` {"a" : 1} `), struct{ A int }{1}} {
			o, err := sjson.Set(`{"x":1}`, "x", v)
			fmt.Printf("%T %q => %s err=%v\n", v, fmt.Sprint(v), o, err)
		}
		return
	case "get2":
		for _, g := range [][2]string{
			{`{"a":[1],"a":[1,2]}`, "a.1"}, {`{"a":{"b":1},"a":{"b":2}}`, "a.b"}, {`{"a":{"b":{}},"a":{"b":{"c":1}}}`, "a.b.c"},
			{`{"\u0061":1}`, "a"}, {`{"a":1}`, `\u0061`}, {"{\"a\"\t:\t1}", "a"}, {`{"a\/b":1}`, "a/b"}, {"[ 1 , 2 ]", "1"},
			{`{"a":{"b":1}}`, "a.b.c"}, {`{"a":[[1]]}`, "a.0.0"}, {`[{"a":1},{"a":2}]`, "1.a"}, {`{"a":"x"}`, "a.b"},
		} {
			fmt.Printf("get %q %q => %s\n", g[0], g[1], res(gjson.Get(g[0], g[1])))
		}
		return
	case "bytes":
		in := []byte(`{"a":[1]}`)
		out, err := sjson.SetBytes(in, "a.b", 1)
		fmt.Printf("seterr nil=%v same=%v out=%q err=%v\n", out == nil, len(out) > 0 && &out[0] == &in[0], out, err)
		out, err = sjson.DeleteBytes(in, "a.#")
		fmt.Printf("delerr nil=%v same=%v out=%q err=%v\n", out == nil, len(out) > 0 && &out[0] == &in[0], out, err)
		out, err = sjson.DeleteBytes(in, "x")
		fmt.Printf("delmiss nil=%v same=%v out=%q err=%v\n", out == nil, len(out) > 0 && &out[0] == &in[0], out, err)
		out, err = sjson.SetBytes(in, "a.0", 2)
		fmt.Printf("setok nil=%v same=%v out=%q err=%v in=%q\n", out == nil, len(out) > 0 && &out[0] == &in[0], out, err, in)
		return
	case "get":
		for _, g := range [][2]string{
			{`{"a":1}`, "a"}, {`{"a":"x\"yé😀"}`, "a"}, {`{"a":"\ud800x"}`, "a"}, {`{"a":"\udc00"}`, "a"},
			{`{"a":-0}`, "a"}, {`{"a":1.5e3}`, "a"}, {`{"a":123456789012345678901}`, "a"}, {`{"a":1e400}`, "a"},
			{`{"a":true}`, "a"}, {`{"a":false}`, "a"}, {`{"a":null}`, "a"}, {`{"a":{"b":[1,2]}}`, "a"}, {`{"a":1}`, "b"},
			{`[1,2,3]`, "1"}, {`[1,2,3]`, "01"}, {`[1,2,3]`, "-1"}, {`[1,2,3]`, "1.5"}, {`[1,2,3]`, "+1"},
			{`{"1":"x"}`, "1"}, {`{"a":1,"a":{"b":2}}`, "a.b"}, {`{"a":{"c":1},"a":{"b":2}}`, "a.b"}, {`{"a":1,"a":2}`, "a"},
			{` {"a" : "v" } `, "a"}, {`{"a.b":1}`, `a\.b`}, {`{"a":1}`, "a"}, {`{"a\"b":1}`, `a"b`}, {`{"":1}`, ""},
			{`{"a":{"":2}}`, "a."}, {`{"a":"\/\b\f\n\r\t\\"}`, "a"}, {`{"a":[1,{"b":"c"}]}`, "a.1.b"}, {`[[1,2],[3]]`, "1.0"},
			{`{"a":1}`, ".a"}, {`{"a":"x"}`, "a.0"}, {`"str"`, ""}, {`{"a":1}`, "a\\"}, {`{"a\\":1}`, "a\\\\"},
			{`{"a":1}`, "A"}, {`{"a":"\u0000"}`, "a"}, {`{"a":"\u00"}`, "a"}, {`{"a":"\q"}`, "a"},
		} {
			fmt.Printf("get %q %q => %s | bytes %s\n", g[0], g[1], res(gjson.Get(g[0], g[1])), res(gjson.GetBytes([]byte(g[0]), g[1])))
		}
		return
	}
	for _, x := range cs {
		var s string
		var err error
		switch x.op {
		case "set":
			s, err = sjson.Set(x.js, x.path, x.v)
		case "raw":
			s, err = sjson.SetRaw(x.js, x.path, x.v.(string))
		case "del":
			s, err = sjson.Delete(x.js, x.path)
		}
		vs := fmt.Sprintf("%#v", x.v)
		if len(vs) > 40 {
			vs = vs[:40]
		}
		fmt.Printf("%s %q %q %s => %s\n", x.op, x.js, x.path, strings.ReplaceAll(vs, "\n", ""), show(s, err))
	}
}
