# moego Linux AMD64 性能

moego 与 `encoding/base64` 及其他 Go Base64 实现在一台 Linux AMD64 机器上的对比，包括 1 KiB 到 4 MiB 的全部结果，以及如何复现这些数字。

## 测试环境

以下数字于 2026-10-26 测得。测试机是多人共用的 KVM 虚拟机：Intel Xeon E5-2667 v4 @ 3.20GHz（Broadwell-EP），支持 AVX2，没有 AVX-512；linux/amd64，Go 1.26.6（默认的 `GOAMD64=v1`），`GOMAXPROCS=1`。测量时机器上还有其他用户的负载。

对比的实现是 Go 1.26.6 的 `encoding/base64`、[emmansun/base64](https://github.com/emmansun/base64) v0.11.0、[segmentio/asm](https://github.com/segmentio/asm) v1.2.1、[cloudwego/base64x](https://github.com/cloudwego/base64x) v0.1.7 和 [cristalhq/base64](https://github.com/cristalhq/base64) v0.1.2。这台 CPU 没有 AVX-512，emmansun/base64 的 AVX-512 VBMI 路径不会启用；emmansun/base64、segmentio/asm 和 cloudwego/base64x 都走各自的 AVX2 路径。

## 方法

- 用 `bench/` 中的基准测试编译出一个测试二进制，所有样本都来自这个二进制。
- [bench/rounds.py](../bench/rounds.md) 串行采样：每个场景的每种大小单独启动一个进程，参数为 `-benchtime=200ms -count=1 -cpu=1`。采样进程用 `taskset` 固定在一个 vCPU 上。
- 共 6 轮，每轮把六个实现的执行顺序左旋一位，每个实现在每个顺位上各跑一次。每项结果是 6 个样本的中位数。
- 数据是固定种子生成的伪随机二进制数据，有 1 KiB、32 KiB、256 KiB、1 MiB、4 MiB 五种大小。吞吐量都按编码前的原始字节数计算，编码和解码可以直接对照。

## 各实现的调用方式

每个实现都通过相同的调用边界测量：

- **预分配编码、解码**：输入和输出缓冲区预先分配、反复使用，只测编解码本身。解码目标比 `DecodedLen` 多留 8 字节，每个实现都一样，因为有的实现写入时会越过结果末尾。
- **`EncodeToString`、`DecodeString`**：各包自己的字符串 API，包括结果的分配和 GC。
- **data URL 拼接**：`"data:" + mime + ";base64," + EncodeToString(src)`。**data URL 解码**：用 `strings.Cut` 在逗号处切开，再调用 `DecodeString`。
- **单次分配 data URL**：moego 调用 `EncodeDataURL`。其他实现用同样的做法：按最终长度分配一次，写入前缀，再用各自的 `Encode` 编码到同一块内存里，不复制就作为字符串返回。这样比较的是编码速度，而不是省掉的分配。
- **复用缓冲区 data URL**：moego 调用 `AppendDataURL`；其他实现把前缀和 `Encode` 的输出写进同一个复用的 byte slice。
- **校验计长**：moego 调用 `ValidateString`。其他实现没有对应的 API，分别用 `DecodeString`（分配后解码）或 `Decode` 到预先分配的缓冲区（复用输出），再返回长度。

每项测量结束后都会核对结果。`TestCompetitors` 另外把每个实现的输出和 `encoding/base64` 对比，覆盖各种尾部长度和 SIMD 块边界。所有输入都是合法、不含换行的标准 Base64。

## 结果

下表和 README 中的图来自同一次完整测量。吞吐量单位为 GB/s，按原始数据量计算；倍数为对方的中位耗时除以 moego 的中位耗时；“最快的其他实现”是该行中除 moego 以外最快的实现。

| 场景 | 大小 | moego 耗时 (μs) | moego (GB/s) | encoding/base64 (GB/s) | 最快的其他实现 (GB/s) | 相比 encoding/base64 | 相比最快的其他实现 |
|---|---|--:|--:|--:|---|--:|--:|
| 预分配编码 | 1 KiB | 0.07712 | 13.28 | 0.93 | emmansun/base64 9.94 | 14.2× | 1.34× |
| 预分配编码 | 32 KiB | 2.112 | 15.51 | 0.94 | emmansun/base64 11.57 | 16.5× | 1.34× |
| 预分配编码 | 256 KiB | 21.38 | 12.26 | 0.94 | emmansun/base64 10.50 | 13.0× | 1.17× |
| 预分配编码 | 1 MiB | 85.18 | 12.31 | 0.94 | emmansun/base64 10.56 | 13.0× | 1.17× |
| 预分配编码 | 4 MiB | 341.6 | 12.28 | 0.94 | emmansun/base64 10.55 | 13.0× | 1.16× |
| 预分配解码 | 1 KiB | 0.09566 | 10.71 | 0.94 | emmansun/base64 6.64 | 11.4× | 1.61× |
| 预分配解码 | 32 KiB | 2.479 | 13.22 | 0.98 | emmansun/base64 9.96 | 13.5× | 1.33× |
| 预分配解码 | 256 KiB | 20.54 | 12.76 | 0.98 | emmansun/base64 9.78 | 13.1× | 1.31× |
| 预分配解码 | 1 MiB | 81.52 | 12.86 | 0.98 | emmansun/base64 9.85 | 13.1× | 1.31× |
| 预分配解码 | 4 MiB | 326.4 | 12.85 | 0.98 | emmansun/base64 9.82 | 13.1× | 1.31× |
| `EncodeToString` | 1 KiB | 0.2445 | 4.19 | 0.67 | emmansun/base64 3.37 | 6.21× | 1.24× |
| `EncodeToString` | 32 KiB | 6.017 | 5.45 | 0.71 | emmansun/base64 3.88 | 7.67× | 1.40× |
| `EncodeToString` | 256 KiB | 43.57 | 6.02 | 0.72 | emmansun/base64 4.39 | 8.34× | 1.37× |
| `EncodeToString` | 1 MiB | 126.2 | 8.31 | 0.78 | emmansun/base64 5.52 | 10.7× | 1.51× |
| `EncodeToString` | 4 MiB | 606.1 | 6.92 | 0.79 | cloudwego/base64x 4.38 | 8.72× | 1.58× |
| `DecodeString` | 1 KiB | 0.2472 | 4.14 | 0.80 | emmansun/base64 3.01 | 5.17× | 1.38× |
| `DecodeString` | 32 KiB | 5.263 | 6.23 | 0.85 | emmansun/base64 4.16 | 7.36× | 1.50× |
| `DecodeString` | 256 KiB | 37.92 | 6.91 | 0.88 | emmansun/base64 4.86 | 7.88× | 1.42× |
| `DecodeString` | 1 MiB | 115.3 | 9.10 | 0.90 | emmansun/base64 5.86 | 10.1× | 1.55× |
| `DecodeString` | 4 MiB | 602.4 | 6.96 | 0.84 | emmansun/base64 4.07 | 8.32× | 1.71× |
| data URL 拼接 | 1 KiB | 0.4958 | 2.07 | 0.57 | emmansun/base64 1.83 | 3.60× | 1.13× |
| data URL 拼接 | 32 KiB | 12.33 | 2.66 | 0.62 | emmansun/base64 2.22 | 4.27× | 1.20× |
| data URL 拼接 | 256 KiB | 92.69 | 2.83 | 0.64 | cloudwego/base64x 2.50 | 4.44× | 1.13× |
| data URL 拼接 | 1 MiB | 268.9 | 3.90 | 0.63 | emmansun/base64 3.22 | 6.22× | 1.21× |
| data URL 拼接 | 4 MiB | 854.8 | 4.91 | 0.62 | emmansun/base64 3.80 | 7.98× | 1.29× |
| data URL 解码 | 1 KiB | 0.2732 | 3.75 | 0.78 | emmansun/base64 2.82 | 4.79× | 1.33× |
| data URL 解码 | 32 KiB | 5.567 | 5.89 | 0.84 | emmansun/base64 3.86 | 7.04× | 1.52× |
| data URL 解码 | 256 KiB | 38.81 | 6.75 | 0.87 | emmansun/base64 4.46 | 7.72× | 1.52× |
| data URL 解码 | 1 MiB | 121.4 | 8.64 | 0.90 | emmansun/base64 5.31 | 9.56× | 1.63× |
| data URL 解码 | 4 MiB | 545.9 | 7.68 | 0.83 | segmentio/asm 4.14 | 9.25× | 1.86× |
| 单次分配 data URL | 1 KiB | 0.2571 | 3.98 | 0.76 | emmansun/base64 3.04 | 5.21× | 1.31× |
| 单次分配 data URL | 32 KiB | 6.159 | 5.32 | 0.81 | emmansun/base64 3.84 | 6.54× | 1.39× |
| 单次分配 data URL | 256 KiB | 41.28 | 6.35 | 0.84 | emmansun/base64 4.57 | 7.56× | 1.39× |
| 单次分配 data URL | 1 MiB | 139.2 | 7.53 | 0.86 | emmansun/base64 5.15 | 8.77× | 1.46× |
| 单次分配 data URL | 4 MiB | 465.4 | 9.01 | 0.80 | segmentio/asm 5.88 | 11.2× | 1.53× |
| 复用缓冲区 data URL | 1 KiB | 0.08859 | 11.56 | 0.92 | emmansun/base64 8.61 | 12.5× | 1.34× |
| 复用缓冲区 data URL | 32 KiB | 2.201 | 14.88 | 0.94 | emmansun/base64 11.04 | 15.8× | 1.35× |
| 复用缓冲区 data URL | 256 KiB | 21.68 | 12.09 | 0.94 | emmansun/base64 10.25 | 12.8× | 1.18× |
| 复用缓冲区 data URL | 1 MiB | 86.45 | 12.13 | 0.94 | emmansun/base64 10.29 | 12.9× | 1.18× |
| 复用缓冲区 data URL | 4 MiB | 346.0 | 12.12 | 0.94 | emmansun/base64 10.29 | 12.9× | 1.18× |
| 校验计长 vs 分配后解码 | 1 KiB | 0.05545 | 18.47 | 0.80 | emmansun/base64 3.02 | 23.2× | 6.12× |
| 校验计长 vs 分配后解码 | 32 KiB | 0.9931 | 32.99 | 0.85 | emmansun/base64 4.18 | 38.9× | 7.89× |
| 校验计长 vs 分配后解码 | 256 KiB | 8.442 | 31.05 | 0.89 | emmansun/base64 5.16 | 34.9× | 6.02× |
| 校验计长 vs 分配后解码 | 1 MiB | 33.40 | 31.40 | 0.86 | emmansun/base64 4.95 | 36.7× | 6.34× |
| 校验计长 vs 分配后解码 | 4 MiB | 133.3 | 31.45 | 0.87 | emmansun/base64 6.08 | 36.0× | 5.18× |
| 校验计长 vs 复用输出 | 1 KiB | 0.05508 | 18.59 | 0.93 | emmansun/base64 6.57 | 19.9× | 2.83× |
| 校验计长 vs 复用输出 | 32 KiB | 0.9889 | 33.14 | 0.97 | emmansun/base64 9.96 | 34.0× | 3.33× |
| 校验计长 vs 复用输出 | 256 KiB | 8.478 | 30.92 | 0.98 | emmansun/base64 9.70 | 31.6× | 3.19× |
| 校验计长 vs 复用输出 | 1 MiB | 33.54 | 31.27 | 0.98 | emmansun/base64 9.73 | 31.9× | 3.21× |
| 校验计长 vs 复用输出 | 4 MiB | 133.1 | 31.52 | 0.98 | emmansun/base64 9.77 | 32.2× | 3.22× |

校验计长的两组里，moego 只检查输入并算出解码后的长度，不写出解码结果；其他实现要完整解码。两边做的工作不同，这两组的倍数不代表解码速度，解码速度看“预分配解码”和 `DecodeString`。

## 重复测量

同一份代码做了三次完整测量，每一项 moego 的中位耗时都低于所有其他实现；差距最小的是 data URL 拼接，中位耗时最少只比最快的其他实现少 5%。

## 不清零分配

在 amd64 和 arm64 上，`EncodeToString`、`DecodeString` 和 `EncodeDataURL` 默认通过 `//go:linkname` 调用 `runtime.mallocgc` 分配结果，不先清零；用 `-tags moego_zeroalloc` 编译会改用普通的 `make`（见 [README](../README.zh_CN.md)）。在同一台机器上，用 `moego_zeroalloc` 构建的二进制单独测了 `BenchmarkNewAPI` 和 `BenchmarkDataURL` 的 30 项，方法同上。

其中分配结果的 25 项都变慢了，moego 的中位耗时增加 8%–83%。受影响最大的是 `EncodeToString`：相比最快的其他实现，默认构建为 1.24–1.58 倍，`moego_zeroalloc` 为 0.95–1.08 倍。除 4 MiB 的 `EncodeToString` 外，moego 的中位耗时仍低于所有其他实现。

| 大小 | 默认构建 moego 耗时 (μs) | 相比最快的其他实现 | `moego_zeroalloc` moego 耗时 (μs) | 相比最快的其他实现 |
|---|--:|--:|--:|--:|
| 1 KiB | 0.2445 | 1.24× | 0.2813 | 1.08× |
| 32 KiB | 6.017 | 1.40× | 8.039 | 1.05× |
| 256 KiB | 43.57 | 1.37× | 55.19 | 1.08× |
| 1 MiB | 126.2 | 1.51× | 176.2 | 1.08× |
| 4 MiB | 606.1 | 1.58× | 1,111 | 0.95× |

默认构建和 `moego_zeroalloc` 不是在同一次运行中测的，差值里也包含运行之间的波动。复用缓冲区 data URL、预分配编解码和校验计长不分配结果，不受这个选项影响。

## 局限

- 只测了一台多人共用的 KVM 虚拟机，结果只代表这台机器。其他用户的负载会改变可用的 L3 缓存和内存带宽；分配结果的场景还受 GC 和堆状态影响。
- 换成别的 CPU，结果可能不同。比如在支持 AVX-512 VBMI 的 CPU 上，emmansun/base64 会走 AVX-512 路径，这里没有测到。
- 当前版本的 arm64 NEON 路径还没有测过速度。
- 预分配编码在 256 KiB 及以上约为最快的其他实现的 1.17 倍，低于 1 KiB 和 32 KiB 时的 1.34 倍。

## 复现

在 `bench/` 目录运行，需要 Go 1.26、Python 3 和 bash；Linux 上用 `taskset` 固定 CPU：

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
python3 plot.py results/linux-amd64 --output ../docs/images --lang zh --note "多人共用的 KVM 虚拟机，结果只代表这台机器"
python3 plot.py results/linux-amd64 --output ../docs/images --lang en --note "Shared KVM virtual machine; results apply to this machine only"
```

把 `taskset -c 2` 中的 2 换成一个空闲 CPU 的编号；`--output` 的目录必须尚不存在。`compare.py` 逐项列出 moego 与最快的其他实现的中位耗时和吞吐量倍数；`plot.py` 生成 README 中的中文图（`.zh.svg`）和英文图（`.en.svg`）。`moego_zeroalloc` 的对照用 `go test -c -tags moego_zeroalloc` 构建二进制，只选 `BenchmarkNewAPI` 和 `BenchmarkDataURL` 的 30 项，再用 `compare.py --suite newapi` 和 `--suite dataurl` 对比。参数和输出文件的说明见 [bench/README.md](../bench/README.md) 和 [bench/rounds.md](../bench/rounds.md)。
