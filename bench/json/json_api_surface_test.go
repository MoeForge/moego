package jsonbench

import (
	stdjson "encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/MoeForge/moego/json"
)

// jsonNorm writes a type with both packages under one name, so that
// signatures from encoding/json and moego/json compare as text.
func jsonNorm(t reflect.Type) string {
	return strings.NewReplacer("encoding/json.", "P.", "github.com/MoeForge/moego/json.", "P.", "json.", "P.").Replace(t.String())
}

// TestJSONSurface checks that moego/json declares every exported
// identifier of encoding/json in Go 1.26 with the same signature, that the
// shared types are the same types, and that it adds only UnmarshalNoCopy.
// The reference lists come from `go doc -short encoding/json`; methods
// are enumerated by reflection.
func TestJSONSurface(t *testing.T) {
	funcs := []struct {
		name     string
		ref, got any
	}{
		{"Compact", stdjson.Compact, json.Compact},
		{"HTMLEscape", stdjson.HTMLEscape, json.HTMLEscape},
		{"Indent", stdjson.Indent, json.Indent},
		{"Marshal", stdjson.Marshal, json.Marshal},
		{"MarshalIndent", stdjson.MarshalIndent, json.MarshalIndent},
		{"Unmarshal", stdjson.Unmarshal, json.Unmarshal},
		{"Valid", stdjson.Valid, json.Valid},
		{"NewDecoder", stdjson.NewDecoder, json.NewDecoder},
		{"NewEncoder", stdjson.NewEncoder, json.NewEncoder},
	}
	for _, f := range funcs {
		if a, b := jsonNorm(reflect.TypeOf(f.ref)), jsonNorm(reflect.TypeOf(f.got)); a != b {
			t.Errorf("%s: moego/json has %s, want %s", f.name, b, a)
		}
	}

	// Types that moego/json shares with encoding/json.
	same := []struct {
		name     string
		ref, got reflect.Type
	}{
		{"Delim", reflect.TypeFor[stdjson.Delim](), reflect.TypeFor[json.Delim]()},
		{"InvalidUTF8Error", reflect.TypeFor[stdjson.InvalidUTF8Error](), reflect.TypeFor[json.InvalidUTF8Error]()},
		{"InvalidUnmarshalError", reflect.TypeFor[stdjson.InvalidUnmarshalError](), reflect.TypeFor[json.InvalidUnmarshalError]()},
		{"Marshaler", reflect.TypeFor[stdjson.Marshaler](), reflect.TypeFor[json.Marshaler]()},
		{"MarshalerError", reflect.TypeFor[stdjson.MarshalerError](), reflect.TypeFor[json.MarshalerError]()},
		{"Number", reflect.TypeFor[stdjson.Number](), reflect.TypeFor[json.Number]()},
		{"RawMessage", reflect.TypeFor[stdjson.RawMessage](), reflect.TypeFor[json.RawMessage]()},
		{"SyntaxError", reflect.TypeFor[stdjson.SyntaxError](), reflect.TypeFor[json.SyntaxError]()},
		{"Token", reflect.TypeFor[stdjson.Token](), reflect.TypeFor[json.Token]()},
		{"UnmarshalFieldError", reflect.TypeFor[stdjson.UnmarshalFieldError](), reflect.TypeFor[json.UnmarshalFieldError]()},
		{"UnmarshalTypeError", reflect.TypeFor[stdjson.UnmarshalTypeError](), reflect.TypeFor[json.UnmarshalTypeError]()},
		{"Unmarshaler", reflect.TypeFor[stdjson.Unmarshaler](), reflect.TypeFor[json.Unmarshaler]()},
		{"UnsupportedTypeError", reflect.TypeFor[stdjson.UnsupportedTypeError](), reflect.TypeFor[json.UnsupportedTypeError]()},
		{"UnsupportedValueError", reflect.TypeFor[stdjson.UnsupportedValueError](), reflect.TypeFor[json.UnsupportedValueError]()},
	}
	for _, s := range same {
		if s.ref != s.got {
			t.Errorf("%s: moego/json has its own type %v", s.name, s.got)
		}
	}

	// Types of moego/json's own, with the same methods.
	own := []struct {
		name     string
		ref, got reflect.Type
	}{
		{"*Decoder", reflect.TypeFor[*stdjson.Decoder](), reflect.TypeFor[*json.Decoder]()},
		{"*Encoder", reflect.TypeFor[*stdjson.Encoder](), reflect.TypeFor[*json.Encoder]()},
	}
	for _, o := range own {
		if a, b := o.ref.NumMethod(), o.got.NumMethod(); a != b {
			t.Errorf("%s: %d methods, want %d", o.name, b, a)
		}
		for i := 0; i < o.ref.NumMethod(); i++ {
			m := o.ref.Method(i)
			g, ok := o.got.MethodByName(m.Name)
			if !ok {
				t.Errorf("%s.%s: missing", o.name, m.Name)
				continue
			}
			if a, b := jsonNorm(m.Type), jsonNorm(g.Type); a != b {
				t.Errorf("%s.%s: moego/json has %s, want %s", o.name, m.Name, b, a)
			}
		}
		if o.got.Elem().Kind() != reflect.Struct {
			t.Errorf("%s: kind %v, want struct", o.name, o.got.Elem().Kind())
		}
		for i := 0; i < o.got.Elem().NumField(); i++ {
			if f := o.got.Elem().Field(i); f.IsExported() {
				t.Errorf("%s: exported field %s", o.name, f.Name)
			}
		}
	}

	// The package declares nothing else that is exported.
	want := map[string]bool{"UnmarshalNoCopy": true}
	for _, f := range funcs {
		want[f.name] = true
	}
	for _, s := range same {
		want[s.name] = true
	}
	want["Decoder"], want["Encoder"] = true, true
	for _, m := range []string{"Decoder.Buffered", "Decoder.Decode", "Decoder.DisallowUnknownFields", "Decoder.InputOffset",
		"Decoder.More", "Decoder.Token", "Decoder.UseNumber", "Encoder.Encode", "Encoder.SetEscapeHTML", "Encoder.SetIndent"} {
		want[m] = true
	}
	var extra []string
	for _, name := range exported(t, "../../json") {
		if !want[name] {
			extra = append(extra, name)
		}
	}
	if len(extra) > 0 {
		t.Errorf("github.com/MoeForge/moego/json exports more than encoding/json: %v", extra)
	}
}

// exported lists the exported top-level identifiers and methods declared
// by the non-test files in dir, methods as Type.Method.
func exported(t *testing.T, dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	seen := map[string]bool{}
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if !d.Name.IsExported() {
					continue
				}
				if d.Recv == nil {
					seen[d.Name.Name] = true
					continue
				}
				rt := d.Recv.List[0].Type
				if s, ok := rt.(*ast.StarExpr); ok {
					rt = s.X
				}
				if id, ok := rt.(*ast.Ident); ok && id.IsExported() {
					seen[id.Name+"."+d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							seen[s.Name.Name] = true
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.IsExported() {
								seen[n.Name] = true
							}
						}
					}
				}
			}
		}
	}
	var out []string
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
