package jdiff

import (
	"encoding/json"
	"math"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// gen makes random Go types, values and JSON documents from one source,
// so that a case is reproduced by its seed.
type gen struct {
	r      *rand.Rand
	finite bool // no NaN or infinite floats, so values compare equal
}

func newGen(seed int64) *gen { return &gen{r: rand.New(rand.NewSource(seed))} }

func (g *gen) n(k int) int       { return g.r.Intn(k) }
func (g *gen) chance(k int) bool { return g.r.Intn(k) == 0 }

var (
	anyType      = reflect.TypeFor[any]()
	stringerType = reflect.TypeFor[Stringer]()
)

var scalarTypes = []reflect.Type{
	reflect.TypeFor[bool](), reflect.TypeFor[int](), reflect.TypeFor[int8](), reflect.TypeFor[int16](),
	reflect.TypeFor[int32](), reflect.TypeFor[int64](), reflect.TypeFor[uint](), reflect.TypeFor[uint8](),
	reflect.TypeFor[uint16](), reflect.TypeFor[uint32](), reflect.TypeFor[uint64](), reflect.TypeFor[uintptr](),
	reflect.TypeFor[float32](), reflect.TypeFor[float64](), reflect.TypeFor[string](), reflect.TypeFor[[]byte](),
	reflect.TypeFor[json.Number](), reflect.TypeFor[json.RawMessage](), anyType,
}

var keyTypes = []reflect.Type{
	reflect.TypeFor[string](), reflect.TypeFor[KeyStr](), reflect.TypeFor[int](), reflect.TypeFor[int8](),
	reflect.TypeFor[uint16](), reflect.TypeFor[uint64](), reflect.TypeFor[KeyInt](), reflect.TypeFor[VText](),
	reflect.TypeFor[PText](), reflect.TypeFor[float64](), reflect.TypeFor[bool](),
}

// embeddable are the catalog structs without methods, which StructOf can
// embed anywhere. Outer and PtrUnexp cover the other embeddings.
var embeddable = []reflect.Type{reflect.TypeFor[Base](), reflect.TypeFor[Other](), reflect.TypeFor[Inner]()}

var fieldNames = []string{"a", "b", "name", "id", "x", "A", "Name", "ID", "kelvin", "ſ", "", "-", "a-b", "é", "<x>"}

// typ returns a random type nested at most depth levels.
func (g *gen) typ(depth int) reflect.Type {
	if depth <= 0 {
		if g.chance(4) {
			return reflect.TypeOf(catalog[g.n(len(catalog))])
		}
		return scalarTypes[g.n(len(scalarTypes))]
	}
	switch g.n(12) {
	case 0, 1:
		return reflect.SliceOf(g.typ(depth - 1))
	case 2:
		return reflect.ArrayOf(g.n(4), g.typ(depth-1))
	case 3, 4:
		return reflect.MapOf(keyTypes[g.n(len(keyTypes))], g.typ(depth-1))
	case 5:
		return reflect.PointerTo(g.typ(depth - 1))
	case 6, 7, 8:
		return g.structType(depth)
	case 9:
		return reflect.TypeOf(catalog[g.n(len(catalog))])
	}
	return scalarTypes[g.n(len(scalarTypes))]
}

func (g *gen) structType(depth int) reflect.Type {
	n := g.n(7)
	var fields []reflect.StructField
	used := map[string]bool{}
	for i := 0; i < n; i++ {
		if g.chance(6) {
			et := embeddable[g.n(len(embeddable))]
			if used[et.Name()] {
				continue
			}
			used[et.Name()] = true
			t := et
			if g.chance(2) {
				t = reflect.PointerTo(et)
			}
			fields = append(fields, reflect.StructField{Name: et.Name(), Type: t, Anonymous: true, Tag: g.tag(true)})
			continue
		}
		name := "F" + strconv.Itoa(i)
		if g.chance(3) {
			name = []string{"Name", "ID", "Dup", "X", "Kelvin", "A"}[g.n(6)]
		}
		if used[name] {
			continue
		}
		used[name] = true
		fields = append(fields, reflect.StructField{Name: name, Type: g.typ(depth - 1), Tag: g.tag(false)})
	}
	return reflect.StructOf(fields)
}

func (g *gen) tag(embedded bool) reflect.StructTag {
	if g.chance(3) {
		return ""
	}
	name := fieldNames[g.n(len(fieldNames))]
	if embedded && g.chance(2) {
		name = ""
	}
	opts := []string{"", ",omitempty", ",omitzero", ",string", ",omitempty,string", ",omitzero,omitempty", ",inline"}[g.n(7)]
	return reflect.StructTag(`json:"` + name + opts + `"`)
}

var stringPool = []string{
	"", "a", "hello world", "<&>", "  ", "é😀", "\x00\x1f\x7f", "\"\\/\b\f\n\r\t", "\xff\xfe", "\xed\xa0\x80",
	"1", "-0", "1e3", "true", "null", "t5", "k3", "a|b", "err", "Name", "NAME", "kelvin", "Kkelvin",
}

func (g *gen) str() string {
	switch g.n(12) {
	case 0:
		return strings.Repeat("x", 4096+g.n(200))
	case 1:
		return strings.Repeat("é<a>\\\"", 100+g.n(800))
	case 2:
		b := make([]byte, g.n(40))
		for i := range b {
			b[i] = byte(g.n(256))
		}
		return string(b)
	}
	return stringPool[g.n(len(stringPool))]
}

func (g *gen) float() float64 {
	switch g.n(14) {
	case 0:
		return math.Copysign(0, -1)
	case 1:
		return math.MaxFloat64
	case 2:
		return math.SmallestNonzeroFloat64
	case 3:
		return 1e21
	case 4:
		return 1e-7
	case 5:
		return float64(g.r.Int63())
	case 6:
		if !g.finite && g.chance(8) {
			return math.NaN()
		}
		return 0.1
	case 7:
		if !g.finite && g.chance(8) {
			return math.Inf(1 - 2*g.n(2))
		}
		return 123456789.125
	}
	return g.r.NormFloat64() * math.Pow(10, float64(g.n(40)-20))
}

// value returns a random value of type t.
func (g *gen) value(t reflect.Type, depth int) reflect.Value {
	v := reflect.New(t).Elem()
	g.fill(v, depth)
	return v
}

func (g *gen) fill(v reflect.Value, depth int) {
	t := v.Type()
	switch t {
	case reflect.TypeFor[json.Number]():
		v.SetString([]string{"0", "-1.5e3", "12", "abc", "", "1e400", "0x10"}[g.n(7)])
		return
	case reflect.TypeFor[json.RawMessage]():
		switch g.n(6) {
		case 0:
			return
		case 1:
			v.SetBytes([]byte(`{bad`))
		default:
			v.SetBytes(g.doc(2))
		}
		return
	case reflect.TypeFor[time.Time]():
		ts := []time.Time{{}, time.Unix(1700000000, 123).UTC(), time.Date(2024, 2, 29, 1, 2, 3, 4, time.FixedZone("x", 3600)), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}
		v.Set(reflect.ValueOf(ts[g.n(len(ts))]))
		return
	}
	switch t.Kind() {
	case reflect.Bool:
		v.SetBool(g.chance(2))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n := g.r.Int63() >> uint(g.n(64))
		if g.chance(2) {
			n = -n
		}
		if g.chance(3) {
			n = int64(g.n(16))
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		n := g.r.Uint64() >> uint(g.n(64))
		if g.chance(3) {
			n = uint64(g.n(16))
		}
		v.SetUint(n)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(g.float())
	case reflect.String:
		v.SetString(g.str())
	case reflect.Slice:
		if g.chance(5) {
			return
		}
		n := g.n(4)
		if depth <= 0 {
			n = 0
		}
		if t.Elem().Kind() == reflect.Uint8 {
			n = g.n(20)
		}
		s := reflect.MakeSlice(t, n, n)
		for i := 0; i < n; i++ {
			g.fill(s.Index(i), depth-1)
		}
		v.Set(s)
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			g.fill(v.Index(i), depth-1)
		}
	case reflect.Map:
		if g.chance(5) {
			return
		}
		m := reflect.MakeMap(t)
		n := g.n(4)
		if depth <= 0 {
			n = 0
		}
		for i := 0; i < n; i++ {
			k := g.value(t.Key(), 0)
			if t.Key().Kind() == reflect.Float64 || t.Key().Kind() == reflect.Bool {
				continue
			}
			m.SetMapIndex(k, g.value(t.Elem(), depth-1))
		}
		v.Set(m)
	case reflect.Pointer:
		if g.chance(4) || depth <= 0 && g.chance(2) {
			return
		}
		p := reflect.New(t.Elem())
		g.fill(p.Elem(), depth-1)
		v.Set(p)
	case reflect.Interface:
		if g.chance(4) {
			return
		}
		var x reflect.Value
		switch {
		case t == stringerType:
			x = reflect.ValueOf(StrVal(g.str()))
		case t.NumMethod() > 0:
			c := reflect.TypeOf(catalog[g.n(len(catalog))])
			switch {
			case c.Implements(t) && g.chance(2):
				x = reflect.New(c).Elem()
				g.fill(x, depth-1)
			case reflect.PointerTo(c).Implements(t):
				x = reflect.New(c)
				g.fill(x.Elem(), depth-1)
			default:
				return
			}
		case g.chance(3) && depth > 0:
			x = g.value(g.typ(1), depth-1)
		default:
			var a any
			json.Unmarshal(g.doc(depth), &a)
			if a == nil {
				return
			}
			x = reflect.ValueOf(a)
		}
		v.Set(x)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if f := v.Field(i); f.CanSet() {
				g.fill(f, depth-1)
			}
		}
	}
}

// doc returns a random JSON document nested at most depth levels.
func (g *gen) doc(depth int) []byte {
	return g.appendDoc(nil, depth)
}

func (g *gen) ws(b []byte) []byte {
	for g.chance(5) {
		b = append(b, " \t\r\n"[g.n(4)])
	}
	return b
}

func (g *gen) appendDoc(b []byte, depth int) []byte {
	b = g.ws(b)
	k := g.n(10)
	if depth <= 0 && k < 3 {
		k = 3 + g.n(7)
	}
	switch k {
	case 0, 1:
		b = append(b, '{')
		n := g.n(5)
		for i := 0; i < n; i++ {
			if i > 0 {
				b = append(b, ',')
			}
			b = g.ws(b)
			b = strconv.AppendQuote(b, fieldNames[g.n(len(fieldNames))])
			b = g.ws(b)
			b = append(b, ':')
			b = g.appendDoc(b, depth-1)
		}
		b = append(g.ws(b), '}')
	case 2:
		b = append(b, '[')
		n := g.n(5)
		for i := 0; i < n; i++ {
			if i > 0 {
				b = append(b, ',')
			}
			b = g.appendDoc(b, depth-1)
		}
		b = append(g.ws(b), ']')
	case 3, 4:
		s := g.str()
		if g.chance(2) {
			b = strconv.AppendQuote(b, s)
		} else {
			enc, _ := json.Marshal(s)
			b = append(b, enc...)
		}
	case 5, 6:
		b = append(b, []string{"0", "-0", "1", "-1", "255", "256", "-129", "1.5", "1e3", "1E+2", "-2.5e-3", "1e400", "-1e400", "12345678901234567890", "0.1", "3.4028235e38", "4e38", "9007199254740993"}[g.n(18)]...)
	case 7:
		b = append(b, "true"...)
	case 8:
		b = append(b, "false"...)
	default:
		b = append(b, "null"...)
	}
	return g.ws(b)
}

// mutate changes a document: scalars into other kinds, keys into other
// case, members added or dropped, or bytes broken.
func (g *gen) mutate(doc []byte) []byte {
	b := append([]byte(nil), doc...)
	for n := 1 + g.n(3); n > 0 && len(b) > 0; n-- {
		i := g.n(len(b))
		switch g.n(9) {
		case 0:
			const set = `{}[]:,"\ 0a-e.tfnu`
			b[i] = set[g.n(len(set))]
		case 1:
			b = append(b[:i], b[i+1:]...)
		case 2:
			b = append(b[:i:i], append([]byte{byte(g.n(256))}, b[i:]...)...)
		case 3:
			b = b[:i]
		case 4:
			if c := b[i]; c >= 'a' && c <= 'z' {
				b[i] = c - 32
			} else if c >= 'A' && c <= 'Z' {
				b[i] = c + 32
			}
		case 5:
			if j := strings.IndexByte(string(b), '{'); j >= 0 {
				ins := `"zz":` + string(g.doc(1)) + `,`
				if g.chance(2) {
					ins = `"` + fieldNames[g.n(len(fieldNames))] + `":` + string(g.doc(1)) + `,`
				}
				if j+1 < len(b) && b[j+1] == '}' {
					ins = ins[:len(ins)-1]
				}
				b = append(b[:j+1:j+1], append([]byte(ins), b[j+1:]...)...)
			}
		case 6:
			// A value of another kind in place of a scalar.
			for j := i; j < len(b); j++ {
				if c := b[j]; c == '-' || c >= '0' && c <= '9' || c == 't' || c == 'f' || c == 'n' {
					k := j
					for k < len(b) && (b[k] >= '0' && b[k] <= '9' || strings.IndexByte("-+.eEtruefalsn", b[k]) >= 0) {
						k++
					}
					rep := []string{`"x"`, `{}`, `[]`, `"12"`, `"true"`, `"null"`, `null`, `1.5`, `"1"`, `" 1"`}[g.n(10)]
					b = append(b[:j:j], append([]byte(rep), b[k:]...)...)
					break
				}
			}
		case 7:
			b = append([]byte{'['}, append(b, ']')...)
		default:
			b = append(b, g.ws(nil)...)
		}
	}
	return b
}

// docOf returns a document shaped like type t, with the wrong kind of
// value, unknown and duplicate members, keys in other case and quoted
// values here and there.
func (g *gen) docOf(t reflect.Type, depth int) []byte {
	return g.ws(g.appendOf(nil, t, depth, false))
}

var (
	timeType      = reflect.TypeFor[time.Time]()
	numberType    = reflect.TypeFor[json.Number]()
	rawType       = reflect.TypeFor[json.RawMessage]()
	unmarshalType = reflect.TypeFor[json.Unmarshaler]()
)

func (g *gen) appendOf(b []byte, t reflect.Type, depth int, quoted bool) []byte {
	b = g.ws(b)
	if depth < 0 || g.chance(14) {
		return g.appendDoc(b, 1)
	}
	if quoted {
		return g.appendQuoted(b, t)
	}
	if g.chance(12) {
		return append(b, "null"...)
	}
	switch {
	case t == timeType:
		return strconv.AppendQuote(b, []string{"2024-01-02T03:04:05Z", "2024-01-02T03:04:05.123+08:00", "bad", "", "2024-13-01T00:00:00Z"}[g.n(5)])
	case t == numberType:
		return append(b, []string{"12", "-1.5e3", `"7"`, `"x"`, `""`, "1e400", `"1e2"`}[g.n(7)]...)
	case t == rawType:
		return g.appendDoc(b, 2)
	case reflect.PointerTo(t).Implements(unmarshalType):
		return g.appendDoc(b, 2)
	case reflect.PointerTo(t).Implements(textUnmarshalerType) && t.Kind() != reflect.Pointer:
		return strconv.AppendQuote(b, []string{"a|b", "k3", "t5", "13", "x", "", "t", "|"}[g.n(8)])
	}
	switch t.Kind() {
	case reflect.Bool:
		return append(b, []string{"true", "false"}[g.n(2)]...)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return append(b, []string{"0", "1", "-1", "127", "128", "-129", "255", "256", "65536", "-2147483649", "4294967296", "9223372036854775807", "9223372036854775808", "18446744073709551616", "1.0", "1e2", "-0", "12345"}[g.n(18)]...)
	case reflect.Float32, reflect.Float64:
		return append(b, []string{"0", "-0", "1.5", "1e38", "3.5e38", "1e308", "1e309", "-2.5e-300", "1e-400", "123456789012345678901234567890", "0.1"}[g.n(11)]...)
	case reflect.String:
		if g.chance(8) {
			return append(b, "12"...)
		}
		return strconv.AppendQuote(b, g.str())
	case reflect.Pointer:
		return g.appendOf(b, t.Elem(), depth, false)
	case reflect.Interface:
		return g.appendDoc(b, 2)
	case reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 && !reflect.PointerTo(t.Elem()).Implements(unmarshalType) && !reflect.PointerTo(t.Elem()).Implements(textUnmarshalerType) {
			return strconv.AppendQuote(b, []string{"aGVsbG8=", "", "!!", "aGVsbG8", "YQ==", "YQ=\n="}[g.n(6)])
		}
		b = append(b, '[')
		n := g.n(4)
		if t.Kind() == reflect.Array && g.chance(2) {
			n = t.Len()
		}
		for i := 0; i < n; i++ {
			if i > 0 {
				b = append(b, ',')
			}
			b = g.appendOf(b, t.Elem(), depth-1, false)
		}
		return append(g.ws(b), ']')
	case reflect.Map:
		b = append(b, '{')
		for i, n := 0, g.n(4); i < n; i++ {
			if i > 0 {
				b = append(b, ',')
			}
			b = strconv.AppendQuote(b, g.keyFor(t.Key()))
			b = append(g.ws(b), ':')
			b = g.appendOf(b, t.Elem(), depth-1, false)
		}
		return append(g.ws(b), '}')
	case reflect.Struct:
		fs := jsonFields(t, nil, 0)
		b = append(b, '{')
		for i, n := 0, g.n(len(fs)+3); i < n; i++ {
			if i > 0 {
				b = append(b, ',')
			}
			if len(fs) == 0 || g.chance(6) {
				b = strconv.AppendQuote(b, fieldNames[g.n(len(fieldNames))])
				b = append(b, ':')
				b = g.appendDoc(b, 1)
				continue
			}
			f := fs[g.n(len(fs))]
			name := f.name
			switch g.n(8) {
			case 0:
				name = strings.ToUpper(name)
			case 1:
				name = strings.ToLower(name)
			case 2:
				name = strings.ReplaceAll(strings.ReplaceAll(name, "k", "\u212a"), "s", "\u017f")
			}
			b = append(b, '"')
			b = append(b, name...)
			b = append(g.ws(append(b, '"')), ':')
			b = g.appendOf(b, f.typ, depth-1, f.quoted)
		}
		return append(g.ws(b), '}')
	}
	return g.appendDoc(b, 1)
}

var textUnmarshalerType = reflect.TypeFor[interface{ UnmarshalText([]byte) error }]()

// appendQuoted returns a value for a field with the ",string" option.
func (g *gen) appendQuoted(b []byte, t reflect.Type) []byte {
	for t.Kind() == reflect.Pointer && g.chance(2) {
		t = t.Elem()
	}
	var in []string
	switch t.Kind() {
	case reflect.Bool:
		in = []string{"true", "false", "null", "1", "tru", " true", "true "}
	case reflect.String:
		in = []string{`\"s\"`, `\"\u00e9<\"`, "s", `\"`, "null", `\"a\" `, `\"\\\"`}
	case reflect.Float32, reflect.Float64:
		in = []string{"1.5", "-0", "1e400", "x", "null", "1 ", " 1", "1e", "\u0031"}
	default:
		in = []string{"12", "-3", "300", "1.5", "x", "null", "", "1 ", "07", "\u0031"}
	}
	switch g.n(10) {
	case 0:
		return append(b, "null"...)
	case 1:
		return append(b, "12"...)
	case 2:
		return append(b, "true"...)
	}
	b = append(b, '"')
	b = append(b, in[g.n(len(in))]...)
	return append(b, '"')
}

// keyFor returns an object key for maps with key type t.
func (g *gen) keyFor(t reflect.Type) string {
	if reflect.PointerTo(t).Implements(textUnmarshalerType) && g.chance(2) {
		return []string{"a|b", "k3", "t5", "k-1", "x", ""}[g.n(6)]
	}
	switch t.Kind() {
	case reflect.String:
		return g.str()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return []string{"0", "12", "-1", "300", "65536", "1e2", "x", "", " 1", "18446744073709551616", "-9223372036854775808"}[g.n(11)]
	}
	return []string{"a", "1", "true", "x"}[g.n(4)]
}

type jsonField struct {
	name   string
	typ    reflect.Type
	quoted bool
}

// jsonFields lists the member names of struct t roughly as encoding/json
// sees them; the decoders settle conflicts themselves.
func jsonFields(t reflect.Type, out []jsonField, depth int) []jsonField {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		ft := f.Type
		if f.Anonymous && name == "" {
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct && depth < 4 {
				out = jsonFields(ft, out, depth+1)
				continue
			}
		}
		if !f.IsExported() && !f.Anonymous {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out = append(out, jsonField{name, f.Type, strings.Contains(opts, "string")})
	}
	return out
}
