package json

import (
	"reflect"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

// A field is one JSON member of a struct type, after the embedding and
// visibility rules: its name, its Go type and how to reach it.
type field struct {
	name  string
	index []int // the field index path through embedded structs
	typ   reflect.Type

	// offset is the field's offset in the outer struct when the path has
	// no embedded pointer; path is nil then.
	offset uintptr
	path   []embedStep

	tagged    bool // the name comes from a tag
	omitEmpty bool
	omitZero  bool
	quoted    bool // the ",string" option applies
}

// embedStep is one embedded pointer on a field path: add off, then follow
// the pointer, which has element type elem when it must be allocated.
type embedStep struct {
	off      uintptr
	elem     reflect.Type
	exported bool // the embedded pointer field is exported, so settable
}

// tagOptions is the part of a struct tag after the name.
type tagOptions string

func parseTag(tag string) (string, tagOptions) {
	name, opt, _ := strings.Cut(tag, ",")
	return name, tagOptions(opt)
}

func (o tagOptions) contains(name string) bool {
	s := string(o)
	for s != "" {
		var opt string
		opt, s, _ = strings.Cut(s, ",")
		if opt == name {
			return true
		}
	}
	return false
}

// isValidTag reports whether s may be a JSON member name in a struct tag:
// letters, digits and punctuation other than quotes and backslashes.
func isValidTag(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}
	return true
}

func isQuotableKind(k reflect.Kind) bool {
	switch k {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.String:
		return true
	}
	return false
}

// inlineV2 reports whether the "inline" option applies, as it does on
// encoding/json/v2 before Go 1.27.
const inlineV2 = jsonv2 && !go127

// structFields returns the JSON members of struct type t in field order.
// Embedded structs without a tag name have their fields promoted, level by
// level. Of the fields with one name, those at the shallowest level win;
// among them a single tagged one wins, and when that leaves more than one,
// none is used.
//
// With inlineV2, a field with the "inline" option and no name is promoted
// like an embedded struct when it is a struct, and is returned as inline
// when it is a map with string keys or a raw value: it holds the members
// that are not fields. Other inline fields, and every inline map or raw
// value when there is more than one, are left out.
func structFields(t reflect.Type) (fields []field, inline *field) {
	type embedded struct {
		typ   reflect.Type
		index []int
	}
	var catchAll []field
	// Fields are kept for the life of the program: room for the fields of
	// t itself is made at once, and the index paths of a level share one
	// block.
	fields = make([]field, 0, t.NumField())
	level := []embedded{}
	next := []embedded{{typ: t}}
	count := map[reflect.Type]int{}
	visited := map[reflect.Type]bool{}
	for len(next) > 0 {
		level, next = next, level[:0]
		nextCount := map[reflect.Type]int{}
		for _, e := range level {
			if visited[e.typ] {
				continue
			}
			visited[e.typ] = true
			depth := len(e.index)
			indexes := make([]int, (depth+1)*e.typ.NumField())
			for i := 0; i < e.typ.NumField(); i++ {
				sf := e.typ.Field(i)
				if sf.Anonymous {
					et := sf.Type
					if et.Kind() == reflect.Pointer {
						et = et.Elem()
					}
					if !sf.IsExported() && et.Kind() != reflect.Struct {
						continue
					}
				} else if !sf.IsExported() {
					continue
				}
				tag := sf.Tag.Get("json")
				if tag == "-" {
					continue
				}
				name, opts := parseTag(tag)
				if !isValidTag(name) {
					name = ""
				}
				index := indexes[i*(depth+1) : (i+1)*(depth+1) : (i+1)*(depth+1)]
				copy(index, e.index)
				index[depth] = i
				ft := sf.Type
				if ft.Name() == "" && ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				embed := name == "" && sf.Anonymous && ft.Kind() == reflect.Struct
				if inlineV2 && opts.contains("inline") {
					switch {
					case name == "" && ft.Kind() == reflect.Struct:
						embed = true
					case name == "" && isCatchAll(sf.Type):
						catchAll = append(catchAll, field{name: sf.Name, index: index, typ: sf.Type})
						if count[e.typ] > 1 {
							catchAll = append(catchAll, catchAll[len(catchAll)-1])
						}
						continue
					default:
						continue
					}
				}
				if !embed {
					f := field{
						name:      name,
						tagged:    name != "",
						index:     index,
						typ:       sf.Type,
						omitEmpty: opts.contains("omitempty"),
						omitZero:  opts.contains("omitzero"),
						quoted:    opts.contains("string") && isQuotableKind(ft.Kind()),
					}
					if f.name == "" {
						f.name = sf.Name
					}
					fields = append(fields, f)
					if count[e.typ] > 1 {
						// The struct is embedded more than once at this
						// level: its fields collide with themselves.
						fields = append(fields, fields[len(fields)-1])
					}
					continue
				}
				nextCount[ft]++
				if nextCount[ft] == 1 {
					next = append(next, embedded{typ: ft, index: index})
				}
			}
		}
		count = nextCount
	}

	slices.SortStableFunc(fields, func(a, b field) int {
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		if len(a.index) != len(b.index) {
			return len(a.index) - len(b.index)
		}
		if a.tagged != b.tagged {
			if a.tagged {
				return -1
			}
			return 1
		}
		return slices.Compare(a.index, b.index)
	})
	out := fields[:0]
	for i := 0; i < len(fields); {
		j := i + 1
		for j < len(fields) && fields[j].name == fields[i].name {
			j++
		}
		// Sorted by depth then tag: the first wins unless the second is as
		// deep and as tagged.
		if j-i == 1 || len(fields[i].index) != len(fields[i+1].index) || fields[i].tagged != fields[i+1].tagged {
			out = append(out, fields[i])
		}
		i = j
	}
	fields = out
	// No two fields have the same index path, so any sort gives this
	// order; the stable one is the sort of fields above.
	slices.SortStableFunc(fields, func(a, b field) int { return slices.Compare(a.index, b.index) })

	for k := range fields {
		fields[k].locate(t)
	}
	if len(catchAll) == 1 {
		inline = &catchAll[0]
		inline.locate(t)
	}
	return fields, inline
}

// locate sets the offset and path of field f of struct type t from its
// index path.
func (f *field) locate(t reflect.Type) {
	st := t
	var off uintptr
	for n, i := range f.index {
		sf := st.Field(i)
		off += sf.Offset
		if n == len(f.index)-1 {
			break
		}
		st = sf.Type
		if st.Kind() == reflect.Pointer {
			f.path = append(f.path, embedStep{off: off, elem: st.Elem(), exported: sf.IsExported()})
			off = 0
			st = st.Elem()
		}
	}
	f.offset = off
}

// appendContext appends to b the error context of field f of struct type
// t: the Go names of the embedded structs on its path, then its JSON name,
// joined by dots.
func (f *field) appendContext(b []byte, t reflect.Type) []byte {
	st := t
	for _, i := range f.index[:len(f.index)-1] {
		sf := st.Field(i)
		if len(b) > 0 {
			b = append(b, '.')
		}
		b = append(b, sf.Name...)
		st = sf.Type
		if st.Kind() == reflect.Pointer {
			st = st.Elem()
		}
	}
	if len(b) > 0 {
		b = append(b, '.')
	}
	return append(b, f.name...)
}

// isCatchAll reports whether an inline field of type t holds the members
// that are not fields: a map with string keys, or a RawMessage.
func isCatchAll(t reflect.Type) bool {
	return t.Kind() == reflect.Map && t.Key().Kind() == reflect.String || t == rawMessageType
}

// appendFold appends s with each letter mapped to the smallest rune of its
// case-folding orbit, so names that differ only in case fold to the same
// string.
func appendFold(out []byte, s string) []byte {
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if 'a' <= c && c <= 'z' {
				c -= 'a' - 'A'
			}
			out = append(out, c)
			i++
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		out = utf8.AppendRune(out, foldRune(r))
		i += n
	}
	return out
}

func foldRune(r rune) rune {
	for {
		r2 := unicode.SimpleFold(r)
		if r2 <= r {
			return r2
		}
		r = r2
	}
}

// fieldPtr returns the address of f in the struct at p for reading. It
// returns nil when an embedded pointer on the path is nil.
func (f *field) readPtr(p unsafe.Pointer) unsafe.Pointer {
	for _, s := range f.path {
		p = *(*unsafe.Pointer)(unsafe.Add(p, s.off))
		if p == nil {
			return nil
		}
	}
	return unsafe.Add(p, f.offset)
}
