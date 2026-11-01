package diff

import "testing"

// The documents and paths of gjson's public README and SYNTAX.md, plus
// one case per modifier and literal form.
const readme = `{
  "name": {"first": "Tom", "last": "Anderson"},
  "age":37,
  "children": ["Sara","Alex","Jack"],
  "fav.movie": "Deer Hunter",
  "friends": [
    {"first": "Dale", "last": "Murphy", "age": 44, "nets": ["ig", "fb", "tw"]},
    {"first": "Roger", "last": "Craig", "age": 68, "nets": ["fb", "tw"]},
    {"first": "Jane", "last": "Murphy", "age": 47, "nets": ["ig", "tw"]}
  ]
}`

const tilde = `{
  "vals": [
    { "a": 1, "b": "data" },
    { "a": 2, "b": true },
    { "a": 3, "b": false },
    { "a": 4, "b": "0" },
    { "a": 5, "b": 0 },
    { "a": 6, "b": "1" },
    { "a": 7, "b": 1 },
    { "a": 8, "b": "true" },
    { "a": 9, "b": false },
    { "a": 10, "b": null },
    { "a": 11 }
  ]
}`

const lines = `{"name": "Gilbert", "age": 61}
{"name": "Alexa", "age": 34}
{"name": "May", "age": 57}
{"name": "Deloise", "age": 44}
`

const nested = `{"a":{"b":[1,[2,[3,4]],{"c":5}],"d":{"e":"x","f":["y","z"]}},"s":"{\"k\":[1,2]}","g":{"a":[1,2],"b":["x","y"]},"t":"2006-01-02T15:04:05Z"}`

type example struct{ doc, path string }

var examples = func() []example {
	var es []example
	for _, p := range []string{
		"name.last", "age", "children", "children.#", "children.1", "child*.2", "c?ildren.0", `fav\.movie`,
		"friends.#.first", "friends.1.last", `friends.#(last=="Murphy").first`, `friends.#(last=="Murphy")#.first`,
		`friends.#(age>45)#.last`, `friends.#(first%"D*").last`, `friends.#(first!%"D*").last`,
		`friends.#(nets.#(=="fb"))#.first`, `friends.#(first="Jane")|last`, `friends.#(age>=47)#`,
		`friends.#(first!="Dale")#.age`, `friends.#(age<47)#.first`, `friends.#(age<=47)#.first`,
		"children|@reverse", "children|@reverse|0", "children.@reverse", "children.@reverse.0",
		`{name.first,age,"the_murphys":friends.#(last="Murphy")#.first}`, `[name.first,age,children.0]`,
		"friends.0.nets|#", "friends.#.nets|@flatten", "@pretty", `@pretty:{"sortKeys":true}`,
		`@pretty:{"indent":"\t","prefix":"> ","width":40}`, "@ugly", "@this", "@valid", "friends.@reverse",
		"friends.#.age|@reverse", "name|@keys", "name|@values", "name.@tostr", "@keys", "@values",
		`friends.#(nets.#(=="ig"))#|#`, "friends.#.nets.#", "friends.#.nets.0", "friends.#.#",
		`{"a":!true,"b":name.first,"c":!null,"d":!"x","e":!-1.5,"f":!{"z":[1]}}`, `[!true,!false,!null,!"hello",!123,!{"a":1},![1]]`,
		"!true", `!"x"`, "!12", `!{"a":1}.a`, "friends.#.{first,age}", "friends.#.[first,nets.0]",
		"friends|#", "friends.#|0", "children.*", "na?e.*", "*.first", "friends.*", `friends.#(last=="Murphy")#|#`,
		`friends.#(first=="Dale").nets|@reverse`, "children.-1", "children.3", `friends.#(age==44)`, `friends.#(age=="44")`,
		"@dig:first", "@dig:age", `friends.@join`, `friends.@join:{"preserve":true}`, "friends.#.first|@tostr",
	} {
		es = append(es, example{readme, p})
	}
	for _, p := range []string{
		`vals.#(b==~true)#.a`, `vals.#(b==~false)#.a`, `vals.#(b!=~true)#.a`, `vals.#(b!=~false)#.a`,
		`vals.#(b==~null)#.a`, `vals.#(b!=~null)#.a`, `vals.#(b==~*)#.a`, `vals.#(b!=~*)#.a`, `vals.#(b)#.a`,
		`vals.#(b==true)#.a`, `vals.#(b>0)#.a`, `vals.#(b<"1")#.a`, `vals.#(b%"*")#.a`, `vals.#(b!%"t*")#.a`,
	} {
		es = append(es, example{tilde, p})
	}
	for _, p := range []string{"..#", "..0", "..1", "..3", "..4", "..#.name", `..#(name="May").age`, `..#(age>40)#.name`, "..1.age", "..#|@reverse", "..#.age|@reverse"} {
		es = append(es, example{lines, p})
	}
	for _, p := range []string{
		"a.b|@flatten", `a.b|@flatten:{"deep":true}`, "s|@fromstr", "s|@fromstr|k.1", "s.@fromstr.k", "g|@group",
		"a|@dig:c", "a|@dig:e", "a.d|@tostr", "a.d|@join", "[a.d,g]|@join", `[a.d,a.d]|@join:{"preserve":true}`,
		"t", "a.b.#", "a.b.1.1.0", "a.*.e", "a.d.f.#(==\"z\")", "a.d.f.#(%\"?\")#", "a.b.#(c==5).c", "a.b.#(#>1)",
		`a.b.#(#==2)#`, "a.d.@keys", "a.d.@values", "@reverse", "a|@reverse", "a.b|@reverse", "a.b.@reverse.0",
		"a.b.@valid", "a.b.@this", "a.b.@ugly", "a.b.@pretty", `a.b.@pretty:{"width":3}`, "g.@group.0",
	} {
		es = append(es, example{nested, p})
	}
	// gjson issue #400: Path on a result from a malformed document.
	es = append(es, example{`{"b:"2}`, "b:"}, example{`{"a":1}`, "@nonexistent"}, example{`[1,2]`, "@"})
	return es
}()

func TestExamples(t *testing.T) {
	k := newCollector()
	for i, e := range examples {
		c := gcase{Doc: e.doc, Path: e.path, Sub: "0", Raw: `7`}
		ms := runCase(c)
		if len(ms) == 1 && ms[0] == errOracle {
			continue
		}
		for _, m := range ms {
			if isDeviation(m) {
				continue
			}
			t.Errorf("%s %s.%s path %q\n got  %s\n want %s", area(m, c), m.Op, m.Field, e.path, clip(m.Got), clip(m.Want))
		}
		k.add(c, ms, 0, uint64(i), "example")
	}
	k.write(t, false)
}
