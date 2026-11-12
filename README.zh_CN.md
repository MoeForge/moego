<div align="center">

<h1><img src="docs/images/moego-logo.svg" width="200" alt="moego"></h1>

为 Agent 网关打造的 Go 高速 Base64、JSON 与正则表达式库，API 和结果与 `encoding/base64`、`encoding/json`、`regexp`、`gjson` 和 `sjson` 相同。

<p align="center">
  <strong>简体中文</strong> |
  <a href="./README.md">English</a>
</p>

</div>

moego 为 Agent 网关这类产品而写。这类网关要读取、改写、过滤和转发大量请求体：JSON 里常常嵌着 Base64 编码的图片、音频和文件。每个包的 API 和结果都和它对应的包相同，换用 moego 通常只需要改一个 import。[new-api](https://github.com/QuantumNous/new-api) 是 moego 的最佳实践。

| 包 | API 和结果同 | 特点 |
|---|---|---|
| [`base64`](#base64) | [`encoding/base64`](https://pkg.go.dev/encoding/base64) | 编解码 1 MiB 数据的速度约为 `encoding/base64` 的 13 倍；生成 data URL；不解码的校验 |
| [`json`](#json) | [`encoding/json`](https://pkg.go.dev/encoding/json) | 全部 41 个场景里都是测过的 JSON 库中最快的，相对 `encoding/json` 的倍数中位数是 4.51 倍；不拷贝的解析 |
| [`ojson`](#ojson) | [`gjson`](https://github.com/tidwall/gjson) v1.19.0 和 [`sjson`](https://github.com/tidwall/sjson) v1.2.5，在合法 JSON 上 | 修改保留成员顺序和所有没改动的字节；在 1 MiB 请求上做 20 次覆盖，是最快的保序方案的 2.78 倍 |
| [`regexp`](#regexp) | [`regexp`](https://pkg.go.dev/regexp) | 线性时间匹配，40 个场景的几何平均是标准库的 43.4 倍、其他库里最快的一个的 7.25 倍；编译缓存；表达式集合 |

四个包都需要 Go 1.23 或更高版本，只依赖标准库：

```sh
go get github.com/MoeForge/moego
```

## base64

`github.com/MoeForge/moego/base64` 用 SIMD 编解码标准字母表和 URL 字母表的数据，输出和错误都和 `encoding/base64` 相同，包括错误位置和输入损坏时已经解出的部分。

<p align="center">
  <img src="docs/images/linux-amd64-1MiB-vs-competitors.zh.svg" alt="base64 与其他 Go Base64 实现在 1 MiB 数据上的吞吐量对比">
</p>

| 1 MiB，GB/s（按二进制数据计） | base64 | encoding/base64 | emmansun/base64 | 相比 encoding/base64 | 相比 emmansun/base64 |
|---|--:|--:|--:|--:|--:|
| 编码到预先分配的缓冲区 | 12.31 | 0.94 | 10.56 | 13.0× | 1.17× |
| 解码到预先分配的缓冲区 | 12.86 | 0.98 | 9.85 | 13.1× | 1.31× |
| `EncodeToString` | 8.31 | 0.78 | 5.52 | 10.7× | 1.51× |
| `DecodeString` | 9.10 | 0.90 | 5.86 | 10.1× | 1.55× |

`emmansun/base64` 是测过的其他四个实现里最快的。测量用 Intel Xeon E5-2667 v4、Go 1.26.6、`GOMAXPROCS=1`；各个大小、实现和场景的结果见 [base64 的性能测试结果](docs/benchmark-results-linux-amd64.zh_CN.md)。

`encoding/base64` 之外：

- `EncodeDataURL("image/png", data)` 用一次分配返回 `data:image/png;base64,...`，`AppendDataURL` 把它追加到你复用的缓冲区里。
- `ValidateString` 返回 `DecodeString` 会返回的长度和错误，但不保留解码结果：1 MiB 上 31.27 GB/s。

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

	// 检查输入，并得到解码后的长度。
	n, err := base64.StdEncoding.ValidateString(s)
	fmt.Println(n, err) // 12 <nil>

	fmt.Println(base64.EncodeDataURL("text/plain", data))
	// data:text/plain;base64,aGVsbG8sIG1vZWdv
}
```

`NewEncoder` 和 `NewDecoder` 返回标准库的流式编码器和解码器，不使用 SIMD；`Decode` 不支持原地解码。

## json

`github.com/MoeForge/moego/json` 的 API 和 `encoding/json` 相同，返回的字节、值和错误和构建时所用的 `encoding/json` 相同：经典版本，或基于 `encoding/json/v2` 实现的版本（Go 1.27 起默认如此）。`RawMessage`、`Number` 和各个错误类型都是 `encoding/json` 中对应类型的别名，所以值和 `errors.As` 在两个包之间通用。

<p align="center">
  <img src="docs/images/json-linux-amd64-summary.zh.svg" alt="json 与其他 JSON 库在六个场景上每次调用的耗时和分配字节数">
</p>

| 每次调用的中位耗时 | json | encoding/json | 其他库里最快的 | 相比 encoding/json | 相比其他库里最快的 |
|---|--:|--:|--:|--:|--:|
| 解析 1 MiB 带图聊天请求 | 195 µs | 1.46 ms | 216 µs，sonic Default | 7.52× | 1.10× |
| 解析 100K token 的 Agent 对话 | 399 µs | 2.55 ms | 472 µs，sonic Default | 6.40× | 1.18× |
| 序列化 100K token 的 Agent 对话 | 331 µs | 2.34 ms | 439 µs，sonic Default | 7.04× | 1.31× |
| 经 `map[string]RawMessage` 解析 64 KiB 请求、删掉两个成员再序列化 | 77.1 µs | 541 µs | 116 µs，goccy/go-json | 7.02× | 1.50× |
| 用 `Decoder` 读取文件里的 64 MiB 请求 | 57.2 ms | 220 ms | 142 ms，encoding/json/v2 | 3.85× | 2.49× |

在全部 41 个场景的每一轮里，`json` 都是最快的，相对 `encoding/json` 的倍数中位数是 4.51 倍，相对 `encoding/json/v2` 是 4.09 倍，分配次数都不多于任何其他库。领先其他库里最快的一个最少的场景是 1.04 到 1.09 倍：embedding 向量、小的流式分块、序列化 8 MiB 带图请求，以及解析 1 KiB Responses 请求和 64 KiB 带图请求。测量用 Intel Xeon E5-2699A v4、Go 1.27.3、`GOMAXPROCS=1`，对比 `encoding/json/v2`、[`sonic`](https://github.com/bytedance/sonic) v1.15.5 和 [`goccy/go-json`](https://github.com/goccy/go-json) v0.12.0；全部场景、内存结果和测量方法见 [json 的性能测试结果](docs/json-benchmark-results-linux-amd64.zh_CN.md)。

`encoding/json` 之外：

- `UnmarshalNoCopy` 和 `Unmarshal` 相同，只是字符串、map 键、`Number` 和 `RawMessage` 直接引用输入，不保存拷贝。解析 1 MiB 聊天请求用时 45 µs。使用解析出的值期间，不要修改输入。
- reader 支持 seek 时（例如 `*os.File`），`Decoder` 只保留解析出的值和一个 256 KiB 的窗口，不保留输入：读取 64 MiB 请求时它的堆峰值是 64.5 MiB，其他库是 160 MiB 到 245 MiB。

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

	// 和 encoding/json 一样。
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

	// 不拷贝。view 的字符串指向 body。
	var view Request
	if err := json.UnmarshalNoCopy(body, &view); err != nil {
		panic(err)
	}
	fmt.Println(view.Messages[0].Content) // hi
}
```

有几处不影响结果的差别，例如 `Marshal` 的递归深度上限，以及 `Decoder` 读文件时多出的读取，见[包文档](https://pkg.go.dev/github.com/MoeForge/moego/json)。在 Go 1.23 上，`json` 也处理 Go 1.24 加入的 `omitzero` 选项。

## ojson

`github.com/MoeForge/moego/ojson` 读取和修改 JSON，API 和路径语法都和 `gjson`、`sjson` 相同。修改时把新文本拼进输入，成员顺序、空白和所有没改动的字节都保持原样，Agent 网关在大请求里覆盖几个参数，不需要重新序列化整个请求体。在非法输入上，任何调用都不会 panic、栈溢出或无限占用内存。

<p align="center">
  <img src="docs/images/ojson-linux-amd64-1MiB.zh.svg" alt="ojson 与 gjson、sjson 和 sonic 在 1 MiB 聊天请求上的对比">
</p>

| 1 MiB 聊天请求，中位耗时 | ojson | gjson / sjson | sonic | 相比最快的保序方案 |
|---|--:|--:|--:|--:|
| 读取 `model` 和 `group` | 465 ns | 807 ns | 527 ns | 1.13× |
| 读取六个字段，大多不存在 | 44.9 µs | 1.72 ms | 743 µs | 16.5× |
| 查询 `messages.#(role=="user")…detail` | 96.5 µs | 4.64 ms | — | 48.1× |
| 用 `Doc` 做 20 次覆盖，输出新请求体 | 310 µs | 11.3 ms | 966 µs | 2.78× |
| 用 `Doc` 做 20 次覆盖，写进复用的缓冲区 | 132 µs | 11.3 ms | 966 µs | 8.26× |

在大文档和不存在的键上领先最多；在只读到请求体开头就结束的读取和小文档上领先最少（1.07 到 1.41 倍）。返回新切片的单次修改受拷贝请求体的成本限制：在 1 MiB 和 8 MiB 上，它的耗时和对请求体做一次 `bytes.Clone` 相同，相对 `sjson` 的速度（1.02× 和 0.92×）在拷贝这么多内存的波动范围以内。测量用 Intel Xeon E5-2667 v4、Go 1.26.6、`GOMAXPROCS=1`，对比 `gjson`、`sjson` 和 [`sonic`](https://github.com/bytedance/sonic) v1.15.5（`sonic.Get` 和 `sonic/ast`）；各个大小、路径和 modifier 的结果见 [ojson 的性能测试结果](docs/ojson-benchmark-results-linux-amd64.zh_CN.md)。

`gjson` 和 `sjson` 之外：

- `Doc` 累积多次修改，最后一次写出结果：`Bytes` 返回新切片，`AppendTo` 追加到你复用的缓冲区，`WriteTo` 写给 `io.Writer`，不拷贝请求体。
- `GetBytesNoCopy` 和 `GetManyBytesNoCopy` 返回指向输入的结果；传入一个 `[]Result` 复用时，读取不分配内存。
- `ValidReader` 通过固定大小的缓冲区从 `io.Reader` 校验 JSON。`GetManyReaderAt` 从文件读取路径；路径由成员名和数组下标组成时，所有路径都找到后就停止。

```go
package main

import (
	"fmt"
	"os"

	"github.com/MoeForge/moego/ojson"
)

func main() {
	body := []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	// 和 gjson 一样读取。结果不和 body 共用内存。
	fmt.Println(ojson.GetBytes(body, "model").String())           // gpt-4o
	fmt.Println(ojson.GetBytes(body, "messages.#.role").String()) // ["user"]

	// 和 sjson 一样做一次修改。body 不会被改动。
	out, err := ojson.SetBytes(body, "temperature", 0.2)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(out))
	// {"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}],"temperature":0.2}

	// 多次修改，最后一次写出。
	doc := ojson.NewDoc(body)
	doc.Set("model", "gpt-4o-mini")
	doc.Set("messages.0.content", "hello")
	doc.Delete("stream")
	doc.WriteTo(os.Stdout)
	// {"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}
}
```

`Get`、`GetMany` 和各个 `NoCopy` 函数的结果，以及 `Doc`，都指向输入：使用期间不要修改输入。`GetBytes` 和 `GetManyBytes` 返回拷贝，修改函数都不改动输入，只有同时设置 `Optimistic` 和 `ReplaceInPlace` 的 `SetBytesOptions`、`SetRawBytesOptions` 例外。在 `gjson`、`sjson` 内存耗尽、栈溢出或写出非法 JSON 的少数情况下，`ojson` 返回错误或保持文档合法；完整的情况列表见[包文档](https://pkg.go.dev/github.com/MoeForge/moego/ojson)。

## regexp

`github.com/MoeForge/moego/regexp` 匹配正则表达式，语法、API 和结果都和标准库的 `regexp` 相同，耗时和输入长度成线性关系。它为 Agent 网关在请求和模型输出上运行的模式而写：字段检查、脱敏、关键词过滤，以及提取工具调用和代码块。

<p align="center">
  <img src="docs/images/regexp-linux-amd64-speed.zh.svg" alt="regexp 相对其他库里最快的一个和标准库的各类别倍数">
</p>

| 类别（用例数） | 相比其他库里最快的 | 相比标准库 |
|---|--:|--:|
| A · 短请求字段（8） | 6.43× | 12.82× |
| B · 文本替换（3） | 8.08× | 69.20× |
| C · 脱敏与关键词（12） | 4.90× | 165.38× |
| D · 结构化文本提取（5） | 49.52× | 81.43× |
| E · 流式分片（3） | 4.77× | 11.72× |
| F · 源码搜索（6） | 3.89× | 13.26× |
| G · 文本与二进制载荷（3） | 9.24× | 46.17× |
| H · 通用模式（4） | 5.46× | 179.69× |
| A–G（40） | 7.25× | 43.39× |
| 冷编译（8） | 1.65× | 1.66× |

每个数值是该类各用例倍数的几何平均。在全部 44 个匹配用例的每一次重复里，`regexp` 都是最快的。测量用 Intel Xeon E5-2699A v4、Go 1.27.3、`GOMAXPROCS=1`，对比标准库、[Grafana regexp](https://github.com/grafana/regexp)、[`coregex`](https://github.com/coregx/coregex) v0.12.30 和 [`go-re2`](https://github.com/wasilibs/go-re2) v1.13.0；全部用例和分配情况见 [regexp 的性能测试结果](docs/regexp-benchmark-results-linux-amd64.zh_CN.md)。

标准库 `regexp` 之外：

- 迭代器：`All`、`AllString`、`AllSubmatch`、`AllStringSubmatch`、各个 `Index` 版本和 `SplitSeq` 逐个给出匹配和片段。
- `Compile` 最多保留 512 个编译好的表达式，每个请求都编译同一个模式的代码可以省掉这部分工作。`SetCacheSize` 修改上限，`SetCacheSize(0)` 关闭缓存。
- `Set` 一遍匹配一组表达式，按 leftmost-first 顺序，并报告每个匹配来自哪个表达式。

```go
package main

import (
	"fmt"

	"github.com/MoeForge/moego/regexp"
)

var action = regexp.MustCompile(`(?m)^Action:\s*(\S+)`)

func main() {
	out := "Thought: look it up\nAction: search\nThought: done\nAction: finish"

	// 和标准库一样。
	fmt.Println(action.FindStringSubmatch(out)[1]) // search

	// 逐个取出匹配。
	for m := range action.AllStringSubmatch(out) {
		fmt.Println(m[1]) // 先是 search，然后是 finish
	}

	// 一遍匹配多个表达式。
	secrets := regexp.MustCompileSet([]string{
		`sk-[A-Za-z0-9_-]{20,}`,
		`AKIA[0-9A-Z]{16}`,
	})
	text := "key sk-abcdefghijklmnopqrstuvwx and AKIAABCDEFGHIJKLMNOP"
	fmt.Println(secrets.ReplaceAllLiteralString(text, "[redacted]"))
	// key [redacted] and [redacted]
}
```

`*regexp.Regexp` 是本包的类型，接收标准库 `*regexp.Regexp` 的代码需要标准库的类型。匹配状态按需构建，不用时会被释放，所以编译之后或空闲一段时间之后的头几次搜索会慢一些。

## 架构

| | amd64（AVX2） | arm64（NEON） |
|---|---|---|
| `base64` | 编码、解码和校验 | 同 amd64 |
| `json` | 扫描、校验和字符串转义 | 检查长字符串里要转义的字符，以及 `Valid` 和 `Indent` 里的校验 |
| `ojson` | 扫描和校验 | 同 amd64 |
| `regexp` | 字面量和字符类的搜索 | 同 amd64 |

其余情况不用 SIMD，结果相同：不支持 AVX2 的 amd64 CPU、其他架构、用 `-tags purego` 编译的程序，以及 arm64 上 `json` 的其他工作。这时 `base64` 调用 `encoding/base64`（自定义字母表和很短的输入也是如此），其他包运行可移植的 Go 代码。速度只在 linux/amd64 上测过；arm64 和可移植代码的路径有正确性测试。

## 结果缓冲区不预先清零

在 amd64 和 arm64 上，下面这些结果分配时不先清零，因为返回之前每个字节都会写入：

- `base64`：`EncodeToString`、`DecodeString` 和 `EncodeDataURL`。
- `json`：`Marshal` 的输出，以及 `Unmarshal` 和 `Decoder` 存下的字符串、`RawMessage` 拷贝等值。
- `ojson`：`GetBytes`、`GetManyBytes` 拷出的字节，以及 `SetBytes`、`Doc.Bytes` 等返回新切片的修改。

它们通过 `//go:linkname` 调用 Go 运行时的内部函数 `runtime.mallocgc`，`json` 还用 `runtime.typedslicecopy`、`runtime.memclrHasPointers`、`runtime.procPin` 和 `runtime.procUnpin`。被广泛使用的包依赖这些函数，所以 Go 会保留它们（[go.dev/issue/67401](https://go.dev/issue/67401)），但编译器不检查它们的签名，升级 Go 之后请跑一遍你的测试。用 `-tags moego_zeroalloc` 编译会改用普通的清零分配，不再使用 `//go:linkname`，这些函数会变慢，其他部分不受影响。

## 测试

`go test ./...` 运行四个包的单元测试，以及 `base64`、`json` 和 `regexp` 与标准库的差分测试。与标准库之外的包对比的测试和基准测试放在单独的 module 里，所以那些包不会成为 moego 的依赖：

| Module | Go | 内容 |
|---|---|---|
| `bench/` | 1.26 | `base64` 与其他 Base64 包对比的基准测试 |
| `bench/json/` | 1.26 | `json` 和 `ojson` 的差分测试、模糊测试和基准测试 |
| `bench/regexp/` | 1.27 | `regexp` 的对比测试和基准测试 |

```sh
# 模糊测试：base64 与 encoding/base64 对比，regexp 与标准库对比。
go test -run '^$' -fuzz FuzzCodecDifferential ./base64
go test -run '^$' -fuzz FuzzDiff ./regexp

# 在 bench/json 里：json 与 encoding/json 对比，ojson 与 gjson、sjson 对比。
go test -run '^$' -fuzz FuzzUnmarshal ./jdiff   # 还有 FuzzDecoder、FuzzFormat、FuzzMarshal
go test -run '^$' -fuzz FuzzGet2 ./diff         # 还有 FuzzSet2
```

公布的数字怎么复现，见各个性能测试结果页的“复现”一节：[base64](docs/benchmark-results-linux-amd64.zh_CN.md#复现)、[json](docs/json-benchmark-results-linux-amd64.zh_CN.md#复现)、[ojson](docs/ojson-benchmark-results-linux-amd64.zh_CN.md#复现) 和 [regexp](docs/regexp-benchmark-results-linux-amd64.zh_CN.md#复现)。

## 许可证

moego 使用 [MIT 许可证](LICENSE)。
