# moego 基准测试

`bench/` 是独立的 Go module，用来把 moego 和其他 Go Base64 实现放在一起测量。其他实现只出现在本目录的 `go.mod` 和 `go.sum` 里，moego 通过 `replace github.com/MoeForge/moego => ..` 引入。运行基准需要 Go 1.26 或更高版本，因为 emmansun/base64 v0.11.0 要求 Go 1.26。

## 对比的实现

版本固定在本目录的 `go.mod` 中：

| 基准中的名称 | 包与版本 | AMD64 | ARM64 |
| --- | --- | --- | --- |
| Stdlib | 所用 Go 版本的 `encoding/base64` | Go 标准库 | Go 标准库 |
| Segmentio | github.com/segmentio/asm v1.2.1 | AVX2，按 CPU 能力分派 | NEON |
| Emmansun | github.com/emmansun/base64 v0.11.0 | SSE、AVX2、AVX-512 VBMI | NEON |
| Cristalhq | github.com/cristalhq/base64 v0.1.2 | Go 查表实现 | Go 查表实现 |
| Cloudwego / CloudwegoFallback | github.com/cloudwego/base64x v0.1.7 | SSE、AVX2 | 调用 `encoding/base64` |
| Moego | 本仓库 | AVX2 | NEON |

各实现的架构支持见 [segmentio/asm 的 ARM64 分派](https://github.com/segmentio/asm/blob/v1.2.1/base64/base64_arm64.go)、[AMD64 分派](https://github.com/segmentio/asm/blob/v1.2.1/base64/base64_amd64.go)、[emmansun/base64 的说明](https://github.com/emmansun/base64/tree/v0.11.0)、[cristalhq/base64 源码](https://github.com/cristalhq/base64/tree/v0.1.2)和 [cloudwego/base64x 的回退实现](https://github.com/cloudwego/base64x/blob/v0.1.7/base64x_fallback.go)。在 amd64 以外的架构上，cloudwego/base64x 在基准中名为 `CloudwegoFallback`，它的成绩就是标准库回退路径的成绩。

## 测试数据

数据是固定种子生成的伪随机字节，大小为 1 KiB、32 KiB、256 KiB、1 MiB 和 4 MiB。图片和音频在 Base64 这一层的处理方式相同，基准只按大小换用 `audio/wav` 或 `image/png` 作为 MIME 类型，数据本身不是有效的音频或图片文件。

`TestCompetitors` 把每个实现在基准中用到的操作和 `encoding/base64` 对比，覆盖各种尾部长度和 SIMD 块边界附近的输入。它不比较非法输入的处理：cristalhq/base64 处理错误和换行的方式与标准库不同，所以基准输入都是合法、不含换行的标准 Base64。

## 场景

所有实现使用相同的调用边界。吞吐量（MB/s）一律按编码前的原始数据量计算，编码和解码可以直接对照。基准不启动额外的 goroutine。

- `BenchmarkCodec/Encode`、`BenchmarkCodec/Decode`：输入和输出缓冲区预先分配并反复使用，准备工作和结果核对都不计时。解码目标比 `DecodedLen` 多留 8 字节，每个实现都一样，因为有的实现写入时会越过结果末尾。
- `BenchmarkNewAPI/EncodeToString`、`BenchmarkNewAPI/DecodeString`：各包的字符串 API，计入结果的分配和 GC。这一组模拟 [new-api](https://github.com/QuantumNous/new-api) 转发图片、音频时的调用。
- `BenchmarkNewAPI/DataURLConcat`：拼出 `"data:" + mime + ";base64," + EncodeToString(src)`。`BenchmarkNewAPI/DataURLDecode`：用 `strings.Cut` 在逗号处切开 data URL，再解码其中的 Base64。
- `BenchmarkDataURL/OneAllocation`：moego 调用 `EncodeDataURL`。其他实现用同样的写法：按最终长度分配一次，写入前缀，再把 `Encode` 的输出直接写进这块内存，不复制就作为字符串返回。这样各实现的分配次数相同，比较的是编码本身。
- `BenchmarkDataURL/AppendReuse`：moego 调用 `AppendDataURL`；其他实现把前缀和 `Encode` 的输出写进同一个复用的 byte slice。
- `BenchmarkValidation/Allocate`：只需要知道输入是否合法、能解出多少字节的场景。moego 调用 `ValidateString`，不写出解码结果；其他实现用 `DecodeString` 解码后返回长度。`BenchmarkValidation/Reuse` 改为让其他实现解码到预先分配的缓冲区，以区分省掉的分配和省掉的解码写入。两者比较的都不是解码速度，解码速度看 `BenchmarkCodec`。

## 快速运行

在本目录运行：

```sh
go test ./...
go test -run '^$' -bench . -benchmem -benchtime=500ms -count=10 -cpu=1 > bench.txt
python3 compare.py --suite all bench.txt
```

这样能很快看出大致情况，但同一个进程里各实现总按固定顺序执行。要发布的数字请用下面的完整轮换测量。测量期间请停掉其他基准、编译和 CPU 密集的任务。

## 完整轮换测量

[rounds.py](rounds.md) 用同一个预编译的测试二进制串行采样，每个场景的每种大小单独启动一个进程；跑 6 轮时，六个实现在每个执行顺位上各跑一次。它的默认值是 5 轮、`benchtime=300ms`，而且只测四项（32 KiB 和 1 MiB 的 `BenchmarkCodec/Encode`、`BenchmarkCodec/Decode`）。完整测量要用 `--rounds 6 --benchtime 200ms`，并用 50 个 `--case` 列出五种大小与以下十个场景的全部组合：

- `BenchmarkCodec/Encode`、`BenchmarkCodec/Decode`
- `BenchmarkNewAPI/EncodeToString`、`BenchmarkNewAPI/DecodeString`、`BenchmarkNewAPI/DataURLConcat`、`BenchmarkNewAPI/DataURLDecode`
- `BenchmarkDataURL/OneAllocation`、`BenchmarkDataURL/AppendReuse`
- `BenchmarkValidation/Allocate`、`BenchmarkValidation/Reuse`

```sh
go test ./...
go test -c -o /tmp/moego-bench.test
cases=()
for op in Codec/Encode Codec/Decode NewAPI/EncodeToString NewAPI/DecodeString \
          NewAPI/DataURLConcat NewAPI/DataURLDecode DataURL/OneAllocation \
          DataURL/AppendReuse Validation/Allocate Validation/Reuse; do
  for size in 1KiB 32KiB 256KiB 1MiB 4MiB; do
    cases+=(--case "Benchmark$op/$size")
  done
done
taskset -c 2 python3 rounds.py --binary /tmp/moego-bench.test \
  --output results/linux-amd64 --rounds 6 --benchtime 200ms "${cases[@]}"
python3 compare.py --suite all results/linux-amd64/combined.txt
```

脚本使用 bash 数组。`taskset` 把采样固定在一个 CPU 上（Linux），把 2 换成一个空闲 CPU 的编号。`--output` 的目录必须尚不存在。输出文件的说明见 [rounds.md](rounds.md)；[Linux AMD64 实测](../docs/benchmark-results-linux-amd64.md)用的就是这组参数。

## compare.py

`compare.py` 读取 `go test -bench` 的输出，逐项检查 moego 是否同时满足：

- 中位耗时比最快的其他实现至少少 10%，可用 `--margin` 调整，例如 `--margin 0.05`；
- moego 最慢的样本也快于每个其他实现最快的样本；
- 每个实现至少有 5 个样本，可用 `--min-samples` 调整。

`--suite` 选择检查的场景：`kernel`（默认，即 `BenchmarkCodec`）、`newapi`、`dataurl`、`validation` 或 `all`。缺少场景、缺少 Emmansun 或 Segmentio、样本不足或条件不满足时，脚本返回非零状态。样本范围检查只看观测到的最快和最慢值，不是统计置信区间。

## plot.py

`plot.py` 读取 rounds.py 输出目录中的 `summary.csv`，并从同一目录的 `combined.txt` 和 `environment.json` 取 CPU、平台、Go 版本和 `GOMAXPROCS`，生成两张 SVG 横条图：moego 对 `encoding/base64`，以及 moego 对全部实现。吞吐量按原始数据量计算，默认取 1 MiB；`--size` 选择其他大小，`--lang` 选择图中文字的语言（`en` 或 `zh`，默认 `en`），`--note` 在标题下加一行说明，用与 `--lang` 相同的语言书写。文件名取自平台、大小和语言，例如 `linux-amd64-1MiB-vs-stdlib.zh.svg`。README 中的图由完整轮换测量的输出生成，中文和英文各运行一次：

```sh
python3 plot.py results/linux-amd64 --output ../docs/images --lang zh --note "多人共用的 KVM 虚拟机，结果只代表这台机器"
python3 plot.py results/linux-amd64 --output ../docs/images --lang en --note "Shared KVM virtual machine; results apply to this machine only"
```

## 脚本测试

脚本测试使用静态数据，不运行基准：

```sh
python3 -m unittest test_compare.py test_rounds.py test_plot.py
```

## 记录结果

发布测量结果时，请写明 Go 版本、操作系统、CPU 和被测的提交。不同系统、不同 CPU 的结果不能互相代替，Linux AMD64 和 Linux ARM64 需要分别测量；交叉编译只能说明代码能够构建。Linux AMD64 的结果见 [docs/benchmark-results-linux-amd64.md](../docs/benchmark-results-linux-amd64.md)，当前版本的 ARM64 还没有测过速度。
