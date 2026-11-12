<div align="center">

<h1><img src="docs/images/moego-logo.svg" width="200" alt="moego"></h1>

Fast Base64, JSON and regular expressions for Go, built for agent gateways,
with the APIs and results of `encoding/base64`, `encoding/json`, `regexp`,
`gjson` and `sjson`.

<p align="center">
  <a href="./README.zh_CN.md">简体中文</a> |
  <strong>English</strong>
</p>

</div>

moego is built for products such as agent gateways, which read, rewrite,
filter and forward large request bodies: JSON that often embeds images,
audio and files as Base64. Each package keeps the API and the results of
the package it follows, so moving to moego usually means changing one
import. [new-api](https://github.com/QuantumNous/new-api) is the
best-practice example of moego in use.

| Package | API and results of | Highlights |
|---|---|---|
| [`base64`](#base64) | [`encoding/base64`](https://pkg.go.dev/encoding/base64) | Encodes and decodes 1 MiB 13× as fast as `encoding/base64`; data URLs; validation without decoding |
| [`json`](#json) | [`encoding/json`](https://pkg.go.dev/encoding/json) | Fastest of the JSON packages measured in all 41 workloads, 4.51× `encoding/json` at the median; decoding without copies |
| [`ojson`](#ojson) | [`gjson`](https://github.com/tidwall/gjson) v1.19.0 and [`sjson`](https://github.com/tidwall/sjson) v1.2.5, on valid JSON | Edits keep member order and every untouched byte; 20 overrides of a 1 MiB request 2.78× as fast as the fastest order-preserving alternative |
| [`regexp`](#regexp) | [`regexp`](https://pkg.go.dev/regexp) | Linear-time matching, 43.4× the standard library and 7.25× the fastest other package over 40 workloads; compile cache; sets of expressions |

All four packages need Go 1.23 or later and depend only on the standard
library:

```sh
go get github.com/MoeForge/moego
```

## base64

`github.com/MoeForge/moego/base64` encodes and decodes the standard and URL
alphabets with SIMD and returns the output and errors of `encoding/base64`,
including the error offsets and partial output on corrupt input.

<p align="center">
  <img src="docs/images/linux-amd64-1MiB-vs-competitors.en.svg" alt="Throughput of base64 and other Go Base64 implementations on 1 MiB of data">
</p>

| 1 MiB, GB/s of binary data | base64 | encoding/base64 | emmansun/base64 | vs encoding/base64 | vs emmansun/base64 |
|---|--:|--:|--:|--:|--:|
| Encode into a preallocated buffer | 12.31 | 0.94 | 10.56 | 13.0× | 1.17× |
| Decode into a preallocated buffer | 12.86 | 0.98 | 9.85 | 13.1× | 1.31× |
| `EncodeToString` | 8.31 | 0.78 | 5.52 | 10.7× | 1.51× |
| `DecodeString` | 9.10 | 0.90 | 5.86 | 10.1× | 1.55× |

`emmansun/base64` was the fastest of the four other packages measured.
Measured on an Intel Xeon E5-2667 v4 with Go 1.26.6 and `GOMAXPROCS=1`;
every size, package and scenario is in the
[base64 benchmark results](docs/benchmark-results-linux-amd64.md).

Beyond `encoding/base64`:

- `EncodeDataURL("image/png", data)` returns `data:image/png;base64,...` in
  one allocation, and `AppendDataURL` appends it to a buffer you reuse.
- `ValidateString` returns the length and error that `DecodeString` would
  return, without keeping the decoded bytes: 31.27 GB/s on 1 MiB.

```go
package main

import (
	"fmt"

	"github.com/MoeForge/moego/base64"
)

func main() {
	data := []byte("hello, moego")

	s := base64.StdEncoding.EncodeToString(data)
	fmt.Println(s) // aGVsbG8sIG1vZWdv

	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(decoded)) // hello, moego

	// Check the input and get its decoded length.
	n, err := base64.StdEncoding.ValidateString(s)
	fmt.Println(n, err) // 12 <nil>

	fmt.Println(base64.EncodeDataURL("text/plain", data))
	// data:text/plain;base64,aGVsbG8sIG1vZWdv
}
```

`NewEncoder` and `NewDecoder` return the standard library's streaming
encoder and decoder, which do not use SIMD, and `Decode` does not decode in
place.

## json

`github.com/MoeForge/moego/json` has the API of `encoding/json` and returns
the bytes, values and errors of the `encoding/json` it is built with: the
classic one, or the one built on `encoding/json/v2`, as it is by default
from Go 1.27. `RawMessage`, `Number` and the error types are aliases of
those of `encoding/json`, so values and `errors.As` work across both.

<p align="center">
  <img src="docs/images/json-linux-amd64-summary.en.svg" alt="Time and bytes per call of json and other JSON packages on six workloads">
</p>

| Median time per call | json | encoding/json | fastest other package | vs encoding/json | vs fastest other |
|---|--:|--:|--:|--:|--:|
| Decode a 1 MiB chat request with an image | 195 µs | 1.46 ms | 216 µs, sonic Default | 7.52× | 1.10× |
| Decode a 100K-token agent conversation | 399 µs | 2.55 ms | 472 µs, sonic Default | 6.40× | 1.18× |
| Encode a 100K-token agent conversation | 331 µs | 2.34 ms | 439 µs, sonic Default | 7.04× | 1.31× |
| Decode, drop two members and encode a 64 KiB request through `map[string]RawMessage` | 77.1 µs | 541 µs | 116 µs, goccy/go-json | 7.02× | 1.50× |
| `Decoder` on a 64 MiB request in a file | 57.2 ms | 220 ms | 142 ms, encoding/json/v2 | 3.85× | 2.49× |

`json` was the fastest package in every round of all 41 workloads, with a
median speedup of 4.51× over `encoding/json` and 4.09× over
`encoding/json/v2`, and made no more allocations than any other package.
It leads the fastest other package by 1.04× to 1.09× on its closest
workloads: embedding vectors, small stream chunks, encoding the 8 MiB image
request, and decoding the 1 KiB Responses request and the 64 KiB image
request. Measured on an Intel Xeon E5-2699A v4 with Go 1.27.3 and
`GOMAXPROCS=1`, against `encoding/json/v2`,
[`sonic`](https://github.com/bytedance/sonic) v1.15.5 and
[`goccy/go-json`](https://github.com/goccy/go-json) v0.12.0; every
workload, the memory results and the method are in the
[json benchmark results](docs/json-benchmark-results-linux-amd64.md).

Beyond `encoding/json`:

- `UnmarshalNoCopy` is `Unmarshal` with strings, map keys, `Number`s and
  `RawMessage`s that view the input instead of holding copies. It decodes
  the 1 MiB chat request in 45 µs. Keep the input unchanged while you use
  the decoded values.
- A `Decoder` on a reader that can seek, such as an `*os.File`, holds the
  decoded value and a 256 KiB window instead of the input: its heap peak
  on the 64 MiB request was 64.5 MiB, against 160 MiB to 245 MiB for the
  other packages.

```go
package main

import (
	"fmt"

	"github.com/MoeForge/moego/json"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Request struct {
	Model    string    `json:"model"`
	Stream   bool      `json:"stream,omitempty"`
	Messages []Message `json:"messages"`
}

func main() {
	body := []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	// As with encoding/json.
	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		panic(err)
	}
	req.Model = "gpt-4o-mini"
	out, err := json.Marshal(req)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(out))
	// {"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"hi"}]}

	// Without copies. The strings of view point into body.
	var view Request
	if err := json.UnmarshalNoCopy(body, &view); err != nil {
		panic(err)
	}
	fmt.Println(view.Messages[0].Content) // hi
}
```

A few behaviors differ without changing results, such as how deep
`Marshal` recurses and the extra reads of a `Decoder` on a file; the
[package documentation](https://pkg.go.dev/github.com/MoeForge/moego/json)
lists them. With Go 1.23, `json` also honors the `omitzero` option that
Go 1.24 added.

## ojson

`github.com/MoeForge/moego/ojson` reads and edits JSON with the API and the
path syntax of `gjson` and `sjson`. Edits splice the new text into the
input, so member order, whitespace and every byte they do not touch stay
as they were, and a gateway can override a few parameters in a large
request without encoding it again. On malformed input no call panics,
overflows the stack or uses unbounded memory.

<p align="center">
  <img src="docs/images/ojson-linux-amd64-1MiB.en.svg" alt="ojson, gjson, sjson and sonic on a 1 MiB chat request">
</p>

| 1 MiB chat request, median time | ojson | gjson / sjson | sonic | vs the fastest order-preserving alternative |
|---|--:|--:|--:|--:|
| Read `model` and `group` | 465 ns | 807 ns | 527 ns | 1.13× |
| Read six fields, most absent | 44.9 µs | 1.72 ms | 743 µs | 16.5× |
| Query `messages.#(role=="user")…detail` | 96.5 µs | 4.64 ms | — | 48.1× |
| 20 overrides with `Doc`, new body | 310 µs | 11.3 ms | 966 µs | 2.78× |
| 20 overrides with `Doc`, into a reused buffer | 132 µs | 11.3 ms | 966 µs | 8.26× |

The margin is largest on big documents and absent keys, and smallest on
reads that stop near the top of the body and on small documents (1.07× to
1.41×). A single edit that returns a new slice is bound by copying the
body: at 1 MiB and 8 MiB it takes as long as `bytes.Clone` of the body, and
its speed relative to `sjson` (1.02× and 0.92×) is within the variation of
copying that much memory. Measured on an Intel Xeon E5-2667 v4 with
Go 1.26.6 and `GOMAXPROCS=1`, against `gjson`, `sjson` and
[`sonic`](https://github.com/bytedance/sonic) v1.15.5 (`sonic.Get` and
`sonic/ast`); every size, path and modifier is in the
[ojson benchmark results](docs/ojson-benchmark-results-linux-amd64.md).

Beyond `gjson` and `sjson`:

- A `Doc` collects several edits and writes the result once: `Bytes`
  returns a new slice, `AppendTo` appends to a buffer you reuse, and
  `WriteTo` writes to an `io.Writer` without copying the body.
- `GetBytesNoCopy` and `GetManyBytesNoCopy` return results that point into
  the input, and allocate nothing when you pass in a `[]Result` to reuse.
- `ValidReader` checks JSON from an `io.Reader` through a fixed buffer.
  `GetManyReaderAt` reads paths from a file and, for paths of member names
  and array indexes, stops once every path is found.

```go
package main

import (
	"fmt"
	"os"

	"github.com/MoeForge/moego/ojson"
)

func main() {
	body := []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	// Reads, as with gjson. The result does not share memory with body.
	fmt.Println(ojson.GetBytes(body, "model").String())           // gpt-4o
	fmt.Println(ojson.GetBytes(body, "messages.#.role").String()) // ["user"]

	// One edit, as with sjson. body is not modified.
	out, err := ojson.SetBytes(body, "temperature", 0.2)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(out))
	// {"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}],"temperature":0.2}

	// Several edits, written once.
	doc := ojson.NewDoc(body)
	doc.Set("model", "gpt-4o-mini")
	doc.Set("messages.0.content", "hello")
	doc.Delete("stream")
	doc.WriteTo(os.Stdout)
	// {"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}
}
```

Results of `Get`, `GetMany` and the `NoCopy` functions, and a `Doc`, point
into the input: keep it unchanged while you use them. `GetBytes` and
`GetManyBytes` return copies, and edits never modify their input, except
`SetBytesOptions` and `SetRawBytesOptions` with `Optimistic` and
`ReplaceInPlace`. In the few cases where `gjson` and `sjson` run out of
memory, overflow the stack or write invalid JSON, `ojson` returns an error
or keeps the document valid; the
[package documentation](https://pkg.go.dev/github.com/MoeForge/moego/ojson)
lists every case.

## regexp

`github.com/MoeForge/moego/regexp` matches regular expressions with the
syntax, API and results of the standard library's `regexp`, in time linear
in the input. It is built for the patterns an agent gateway runs on
requests and model output: field checks, redaction, keyword filters and
extraction of tool calls and code blocks.

<p align="center">
  <img src="docs/images/regexp-linux-amd64-speed.en.svg" alt="Speedup of regexp over the fastest other package and the standard library by class">
</p>

| Class (cases) | vs the fastest other package | vs the standard library |
|---|--:|--:|
| A · Short request fields (8) | 6.43× | 12.82× |
| B · Text replacement (3) | 8.08× | 69.20× |
| C · Redaction and keywords (12) | 4.90× | 165.38× |
| D · Structured extraction (5) | 49.52× | 81.43× |
| E · Streaming frames (3) | 4.77× | 11.72× |
| F · Source search (6) | 3.89× | 13.26× |
| G · Text and binary payloads (3) | 9.24× | 46.17× |
| H · General patterns (4) | 5.46× | 179.69× |
| A–G (40) | 7.25× | 43.39× |
| Cold compilation (8) | 1.65× | 1.66× |

Each value is the geometric mean over the cases of the class. `regexp` was
the fastest package in every repetition of all 44 matching cases. Measured
on an Intel Xeon E5-2699A v4 with Go 1.27.3 and `GOMAXPROCS=1`, against
the standard library, [Grafana regexp](https://github.com/grafana/regexp),
[`coregex`](https://github.com/coregx/coregex) v0.12.30 and
[`go-re2`](https://github.com/wasilibs/go-re2) v1.13.0; every case and the
allocations are in the
[regexp benchmark results](docs/regexp-benchmark-results-linux-amd64.md).

Beyond `regexp`:

- Iterators: `All`, `AllString`, `AllSubmatch`, `AllStringSubmatch`, the
  `Index` variants and `SplitSeq` yield matches and pieces one at a time.
- `Compile` keeps up to 512 compiled expressions, so code that compiles the
  same pattern on every request skips the work. `SetCacheSize` changes the
  limit, and `SetCacheSize(0)` turns the cache off.
- A `Set` matches a list of expressions in one pass, leftmost-first, and
  reports which expression each match came from.

```go
package main

import (
	"fmt"

	"github.com/MoeForge/moego/regexp"
)

var action = regexp.MustCompile(`(?m)^Action:\s*(\S+)`)

func main() {
	out := "Thought: look it up\nAction: search\nThought: done\nAction: finish"

	// As with the standard library.
	fmt.Println(action.FindStringSubmatch(out)[1]) // search

	// One match at a time.
	for m := range action.AllStringSubmatch(out) {
		fmt.Println(m[1]) // search, then finish
	}

	// Several expressions in one pass.
	secrets := regexp.MustCompileSet([]string{
		`sk-[A-Za-z0-9_-]{20,}`,
		`AKIA[0-9A-Z]{16}`,
	})
	text := "key sk-abcdefghijklmnopqrstuvwx and AKIAABCDEFGHIJKLMNOP"
	fmt.Println(secrets.ReplaceAllLiteralString(text, "[redacted]"))
	// key [redacted] and [redacted]
}
```

`*regexp.Regexp` is a type of this package, so code that takes the
standard library's type needs that type. Matching state is built on demand
and released when unused, so the first searches after compiling a pattern
or after an idle period take longer.

## Architectures

| | amd64 with AVX2 | arm64 with NEON |
|---|---|---|
| `base64` | Encoding, decoding and validation | Same as amd64 |
| `json` | Scanning, validation and string quoting | Checking long strings for characters to escape, and validation in `Valid` and `Indent` |
| `ojson` | Scanning and validation | Same as amd64 |
| `regexp` | Literal and character-class searches | Same as amd64 |

Everything else runs without SIMD and returns the same results: on amd64
CPUs without AVX2, on other architectures, in builds with `-tags purego`,
and for the rest of the work of `json` on arm64. There `base64` calls
`encoding/base64`, as it also does for custom alphabets and very short
inputs, and the other packages run portable Go code. Speed was measured on
linux/amd64 only; the arm64 and portable paths are tested for
correctness.

## Result buffers are not zeroed first

On amd64 and arm64, these results are allocated without zeroing, because
every byte is written before they are returned:

- `base64`: `EncodeToString`, `DecodeString` and `EncodeDataURL`.
- `json`: the output of `Marshal`, and the strings, `RawMessage` copies and
  other values that `Unmarshal` and `Decoder` store.
- `ojson`: the bytes that `GetBytes` and `GetManyBytes` copy, and edits
  that return a new slice, such as `SetBytes` and `Doc.Bytes`.

They use the Go runtime's internal `runtime.mallocgc` through
`//go:linkname`, and `json` also uses `runtime.typedslicecopy`,
`runtime.memclrHasPointers`, `runtime.procPin` and `runtime.procUnpin`. Go
keeps these functions available because widely used packages depend on
them ([go.dev/issue/67401](https://go.dev/issue/67401)), but the compiler
does not check their signatures, so run your tests after upgrading Go.
Build with `-tags moego_zeroalloc` to use ordinary zeroed allocation
without `//go:linkname`; these functions are then slower and nothing else
changes.

## Testing

`go test ./...` runs the unit tests of the four packages and the
differential tests of `base64`, `json` and `regexp` against the standard
library. The tests that compare with packages outside the standard
library, and the benchmarks, are in separate modules, so those packages
are never dependencies of moego:

| Module | Go | Contents |
|---|---|---|
| `bench/` | 1.26 | Benchmarks of `base64` against other Base64 packages |
| `bench/json/` | 1.26 | Differential tests, fuzzing and benchmarks of `json` and `ojson` |
| `bench/regexp/` | 1.27 | Comparisons and benchmarks of `regexp` |

```sh
# Fuzz against encoding/base64 and regexp.
go test -run '^$' -fuzz FuzzCodecDifferential ./base64
go test -run '^$' -fuzz FuzzDiff ./regexp

# In bench/json: fuzz json against encoding/json, and ojson against gjson and sjson.
go test -run '^$' -fuzz FuzzUnmarshal ./jdiff   # also FuzzDecoder, FuzzFormat, FuzzMarshal
go test -run '^$' -fuzz FuzzGet2 ./diff         # also FuzzSet2
```

How to reproduce the published numbers is in the reproducing section of
each benchmark results page:
[base64](docs/benchmark-results-linux-amd64.md#reproducing),
[json](docs/json-benchmark-results-linux-amd64.md#reproducing),
[ojson](docs/ojson-benchmark-results-linux-amd64.md#reproducing) and
[regexp](docs/regexp-benchmark-results-linux-amd64.md#reproducing).

## License

moego is licensed under the [MIT License](LICENSE).
