package race

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/tidwall/sjson"
)

// TestOutputs checks every engine against gjson/sjson on every workload, so
// the benchmarks below only time engines that produce the same result.
func TestOutputs(t *testing.T) {
	for _, sz := range Sizes {
		body := Body(sz.N)
		if !json.Valid(body) {
			t.Fatalf("%s: generated body is not valid JSON", sz.Name)
		}
		for _, w := range ReadPaths {
			want := readAll(t, "gjson", w.Paths, body)
			for _, r := range readers {
				got := readAll(t, r.name, w.Paths, body)
				if !sameReads(r.name, got, want) {
					t.Errorf("%s/%s %s: got %q, want %q", sz.Name, w.Name, r.name, clip(got), clip(want))
				}
			}
		}
		for _, k := range Counts {
			ops := Overrides(k)
			want, err := flat(sjson.SetBytes, sjson.DeleteBytes)(body, ops)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range editors {
				got, err := e.run(bytes.Clone(body), ops)
				if err != nil {
					t.Errorf("%s/%d %s: %v", sz.Name, k, e.name, err)
					continue
				}
				if msg := compare(e.kind, got, want); msg != "" {
					t.Errorf("%s/%d %s: %s", sz.Name, k, e.name, msg)
				}
			}
		}
	}
	frame := SSEFrame()
	for _, paths := range [][]string{SSEPaths, {SSEPath1}} {
		want := readAll(t, "gjson", paths, frame)
		for _, r := range readers {
			if got := readAll(t, r.name, paths, frame); !sameReads(r.name, got, want) {
				t.Errorf("SSE %v %s: got %q, want %q", paths, r.name, got, want)
			}
		}
	}
}

func readAll(t *testing.T, name string, paths []string, body []byte) []string {
	for _, r := range readers {
		if r.name == name {
			run, out := r.make(paths)
			run(body)
			return out()
		}
	}
	t.Fatalf("no reader %s", name)
	return nil
}

// sameReads compares raw texts. Map readers re-encode values, so they are
// compared as JSON values instead of bytes.
func sameReads(name string, got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] == want[i] {
			continue
		}
		if name != "encjson-map" && name != "sonic-map" || got[i] == absent || want[i] == absent {
			return false
		}
		var a, b any
		if json.Unmarshal([]byte(got[i]), &a) != nil || json.Unmarshal([]byte(want[i]), &b) != nil || !reflect.DeepEqual(a, b) {
			return false
		}
	}
	return true
}

func compare(kind int, got, want []byte) string {
	switch kind {
	case exact:
		if !bytes.Equal(got, want) {
			return fmt.Sprintf("bytes differ: got %q, want %q", clip1(got), clip1(want))
		}
	case ordered:
		var a, b bytes.Buffer
		if json.Compact(&a, got) != nil || json.Compact(&b, want) != nil {
			return "invalid JSON"
		}
		if !bytes.Equal(a.Bytes(), b.Bytes()) {
			return fmt.Sprintf("compact form differs: got %q, want %q", clip1(a.Bytes()), clip1(b.Bytes()))
		}
	default:
		var a, b any
		if json.Unmarshal(got, &a) != nil || json.Unmarshal(want, &b) != nil {
			return "invalid JSON"
		}
		if !reflect.DeepEqual(a, b) {
			return "values differ"
		}
	}
	return ""
}

func clip(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = string(clip1([]byte(v)))
	}
	return out
}

func clip1(b []byte) []byte {
	if len(b) > 300 {
		return append(append(b[:150:150], "..."...), b[len(b)-150:]...)
	}
	return b
}

var sink []byte

func BenchmarkRead(b *testing.B) {
	for _, sz := range Sizes {
		body := Body(sz.N)
		for _, w := range ReadPaths {
			for _, r := range readers {
				run, _ := r.make(w.Paths)
				b.Run(fmt.Sprintf("%s/%s/%s", w.Name, sz.Name, r.name), func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(body)))
					for i := 0; i < b.N; i++ {
						run(body)
					}
				})
			}
		}
	}
}

func BenchmarkOverride(b *testing.B) {
	for _, sz := range Sizes {
		body := Body(sz.N)
		for _, k := range Counts {
			ops := Overrides(k)
			for _, e := range editors {
				in := body
				if e.inPlace {
					if k != 1 {
						continue
					}
					in = bytes.Clone(body)
				}
				b.Run(fmt.Sprintf("%d/%s/%s", k, sz.Name, e.name), func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(in)))
					for i := 0; i < b.N; i++ {
						if e.bench != nil {
							e.bench(in, ops)
							continue
						}
						out, _ := e.run(in, ops)
						sink = out
					}
				})
			}
			if k == 1 {
				floor(b, sz.Name, body)
			}
		}
	}
}

// floor measures, next to the one-edit rows, the copy every compatible
// one-shot edit makes: a new slice holding the body (floor-clone), and the
// same bytes copied into a buffer reused across calls (floor-copy).
func floor(b *testing.B, size string, body []byte) {
	b.Run("1/"+size+"/floor-clone", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for i := 0; i < b.N; i++ {
			sink = bytes.Clone(body)
		}
	})
	b.Run("1/"+size+"/floor-copy", func(b *testing.B) {
		buf := make([]byte, len(body))
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			copy(buf, body)
			sink = buf
		}
	})
}

func BenchmarkSSE(b *testing.B) {
	frame := SSEFrame()
	for _, c := range []struct {
		name  string
		paths []string
	}{{"three-field", SSEPaths}, {"one-field", []string{SSEPath1}}} {
		for _, r := range readers {
			run, _ := r.make(c.paths)
			b.Run(fmt.Sprintf("%s/%s", c.name, r.name), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(frame)))
				for i := 0; i < b.N; i++ {
					run(frame)
				}
			})
		}
	}
}
