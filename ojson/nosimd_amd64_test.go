//go:build amd64 && !purego

package ojson

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// kernelDoc returns a document whose strings, containers and whitespace
// span many 64-byte blocks: long strings with escapes, HTML characters and
// bytes of 0x80 and above, deep nesting, long arrays and runs of spaces,
// with the paths that lead into it. Documents stay below about 8 KiB.
func kernelDoc(r *rand.Rand) (string, []string) {
	var b strings.Builder
	var paths []string
	str := func() {
		b.WriteByte('"')
		for n := []int{0, 10, 63, 64, 65, 200, 1000}[r.Intn(7)]; n > 0; n-- {
			switch r.Intn(40) {
			case 0:
				b.WriteString(`\"`)
			case 1:
				b.WriteString(`\\`)
			case 2:
				b.WriteString(`\u00e9`)
			case 3:
				b.WriteString("é中")
			case 4:
				b.WriteString("<&>")
			case 5:
				b.WriteString(`\n`)
			case 6:
				b.WriteString("\u2028")
			default:
				b.WriteByte("abcdefghijklmnopqrstuvwxyz .,:{}[]"[r.Intn(34)])
			}
		}
		b.WriteByte('"')
	}
	ws := func() {
		if r.Intn(4) == 0 {
			c := r.Intn(4)
			b.WriteString(strings.Repeat(" \n\t "[c:c+1], r.Intn(100)))
		}
	}
	var value func(path string, depth int)
	value = func(path string, depth int) {
		if path != "" && len(paths) < 30 && r.Intn(2) == 0 {
			paths = append(paths, path, path+".#", path+"|@ugly")
		}
		join := func(c string) string {
			if path == "" {
				return c
			}
			return path + "." + c
		}
		ws()
		switch k := r.Intn(10); {
		case depth > 5 || k < 4 || b.Len() > 6000:
			switch r.Intn(4) {
			case 0:
				str()
			case 1:
				b.WriteString(strconv.Itoa(r.Intn(100000) - 50000))
			case 2:
				b.WriteString("-12.5e-3")
			default:
				b.WriteString([]string{"true", "false", "null"}[r.Intn(3)])
			}
		case k < 7:
			b.WriteByte('{')
			for i := range r.Intn(6) {
				if i > 0 {
					b.WriteByte(',')
				}
				ws()
				key := []string{"model", "messages", "content", "role", "a", "b", "long_key_name_for_paths", `k\"q`, "é"}[r.Intn(9)]
				b.WriteString(`"` + key + `"`)
				ws()
				b.WriteByte(':')
				value(join(escapeComp(strings.ReplaceAll(key, `\"`, `"`))), depth+1)
			}
			ws()
			b.WriteByte('}')
		default:
			b.WriteByte('[')
			n := r.Intn(6)
			if depth < 2 && r.Intn(4) == 0 {
				n = 60
			}
			for i := range n {
				if i > 0 {
					b.WriteByte(',')
				}
				value(join(strconv.Itoa(i)), depth+1)
			}
			ws()
			b.WriteByte(']')
		}
		ws()
	}
	value("", 0)
	return b.String(), paths
}

// kernelCorpus returns documents and paths: random ones of the other
// tests, kernelDoc ones, request bodies, deep nesting, and changed and cut
// copies of them.
func kernelCorpus(r *rand.Rand) (docs []string, paths [][]string) {
	add := func(doc string, p []string) {
		docs = append(docs, doc)
		paths = append(paths, p)
	}
	common := append([]string{"@ugly", "@pretty", "@reverse", "@flatten", "@keys", "@values", "@valid", "@this",
		"@tostr", "@join", "#", "#.a", "..0", "*", "a*", "model", "messages.1.content.0.image_url.url",
		"messages.#.role", "tools.0.function.name", "metadata.trace.tags.1", "long_key_name_for_paths.0.model",
		"content|@pretty", "a.b.c.d.e.f.g.h", "choices.0.delta.content.text.value"}, getPaths...)
	for range randomIterations(40) {
		doc, p := randomDoc(r)
		add(doc, p)
		doc, p = kernelDoc(r)
		add(doc, p)
		var b strings.Builder
		randomJSON(r, &b, 4)
		add(b.String(), randomPaths(r))
	}
	for _, n := range []int{100, 1000, 5000, 20000} {
		add(string(benchBody(n)), nil)
	}
	add(strings.Repeat("[", 100)+`{"a":"`+strings.Repeat("x", 100)+`"}`+strings.Repeat("]", 100), []string{strings.Repeat("0.", 100) + "a"})
	add(strings.Repeat(`{"a":`, 70)+"1"+strings.Repeat("}", 70), []string{strings.Repeat("a.", 69) + "a"})
	add(`[`+strings.Repeat(`"`+strings.Repeat(`\\`, 33)+`",`, 20)+`0]`, []string{"19", "20", "#"})
	n := len(docs)
	for i := range n {
		add(mutate(r, docs[i]), paths[i])
		add(docs[i][:r.Intn(len(docs[i])+1)], paths[i])
	}
	for i := range paths {
		paths[i] = append(paths[i], common...)
	}
	return docs, paths
}

// kernelLog records what the functions give on the corpus.
type kernelLog struct{ lines []string }

func (l *kernelLog) add(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *kernelLog) result(r Result) {
	l.add("%d %q %q %x %d %v", r.Type, r.Raw, r.Str, math.Float64bits(r.Num), r.Index, r.Indexes)
}

// kernelRun runs the reads, checks, edits and modifiers of the package on
// doc with paths.
func kernelRun(l *kernelLog, doc string, paths []string) {
	l.add("valid %v %v", Valid(doc), ValidBytes([]byte(doc)))
	ok, err := ValidReader(strings.NewReader(doc))
	l.add("reader %v %v", ok, err)
	for _, p := range paths {
		l.result(Get(doc, p))
		l.result(GetBytes([]byte(doc), p))
	}
	for _, r := range GetMany(doc, paths...) {
		l.result(r)
	}
	if rs, err := GetManyReaderAt(bytes.NewReader([]byte(doc)), int64(len(doc)), paths...); err == nil {
		for _, r := range rs {
			l.result(r)
		}
	} else {
		l.add("readerAt %v", err)
	}
	Parse(doc).ForEach(func(k, v Result) bool {
		l.result(k)
		l.result(v)
		return true
	})
	ForEachLine(doc+"\n"+doc, func(r Result) bool {
		l.result(r)
		return true
	})
	values := []any{strings.Repeat(`a"b\c<d>&é`, 20), "short", 12.5, true, nil, []string{"x", strings.Repeat("y", 100)}}
	for i, p := range paths {
		if i > 6 {
			break
		}
		out, err := Set(doc, p, values[i%len(values)])
		l.add("set %q %q %v", p, out, err)
		out, err = SetRaw(doc, p, `{"k": [1, "`+strings.Repeat("z", 70)+`"]}`)
		l.add("setraw %q %q %v", p, out, err)
		out, err = Delete(doc, p)
		l.add("delete %q %q %v", p, out, err)
	}
	l.add("%q", AppendJSONString(nil, doc))
	l.add("%q", Escape(doc))
}

// TestKernelsOff compares what the package gives with the AVX2 kernels and
// without them, as on CPUs without AVX2 or AVX, on documents and paths
// that reach the block kernels.
func TestKernelsOff(t *testing.T) {
	if !jsonsimd.HaveAVX2 {
		t.Skip("no AVX2")
	}
	docs, paths := kernelCorpus(rand.New(rand.NewSource(20261113)))
	for i, doc := range docs {
		var want kernelLog
		kernelRun(&want, doc, paths[i])
		for _, drop := range []string{"avx2", "avx"} {
			var got kernelLog
			func() {
				defer jsonsimd.LimitCPU(drop)()
				kernelRun(&got, doc, paths[i])
			}()
			for k := range max(len(got.lines), len(want.lines)) {
				var g, w string
				if k < len(got.lines) {
					g = got.lines[k]
				}
				if k < len(want.lines) {
					w = want.lines[k]
				}
				if g != w {
					t.Fatalf("document %.200q without %s, entry %d:\n got %.300s\nwant %.300s", doc, drop, k, g, w)
				}
			}
		}
	}
}
