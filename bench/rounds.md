`rounds.py` 每轮用同一个预编译的 Go test binary 执行选定 case。默认 5 轮，每次 `benchtime=300ms`、`count=1`、`cpu=1`。基准中的 `codecs()` 读取 `MOEGO_BENCH_ROTATE`，把 codec 列表左旋相应位数。脚本核对每个 case 的实际输出顺序，轮换未生效时会失败。

在 `bench/` 中先完成正确性验证、编译，再由一个进程串行采样：

```sh
go test ./...
go test -c -o /tmp/moego-bench.test
python3 rounds.py --binary /tmp/moego-bench.test --output results/rotated-run
```

脚本的进程锁放在系统临时目录。默认 case 是 Encode/Decode 的 32 KiB 和 1 MiB 输入。通过重复 `--case` 选择需要测量的 case：

```sh
python3 rounds.py \
  --binary /tmp/moego-bench.test \
  --output results/rotated-media \
  --rounds 6 \
  --case BenchmarkCodec/Encode/1MiB \
  --case BenchmarkCodec/Decode/1MiB \
  --case BenchmarkNewAPI/EncodeToString/1MiB \
  --case BenchmarkNewAPI/DecodeString/1MiB
```

六个 codec 使用 6 轮可各占据每个执行顺位一次。默认的 5 轮采样较短，不能覆盖全部顺位。完整的 50 项测量命令见 [README.md](README.md)。轮换减少固定执行顺序造成的偏差，测量期间仍需要停止其他基准、编译和 CPU 密集任务。脚本持有本项目的进程锁，防止另一份 `rounds.py` 同时采样。

每个 case 启动一个新进程，环境中固定 `GOMAXPROCS=1`。`--codecs` 指定 `codecs()` 完整的初始顺序，默认包含六个实现；ARM64 使用 `CloudwegoFallback`，AMD64 使用 `Cloudwego`。此选项用于匹配基准二进制的配置。结果中的缺项、重复项、意外 case、顺序变化、错误 CPU 数或缺少成功退出标记，都会让整次运行失败。

输出目录需要是新目录，以免混入旧数据：

- `environment.json` 保存主机信息、Go binary build info、二进制 SHA-256、runner SHA-256、影响 Go runtime 的环境配置和参数。
- `manifest.json` 保存每个子进程的 argv、时间、轮换顺序、返回值和运行状态。中断或校验失败时标记为 `failed`。
- `round-001.txt` 等文件保存每轮原始输出，包括失败时已经产生的数据。
- `combined.txt` 合并完整轮次，成功完成后生成，可供 Go benchmark 工具读取。
- `samples.csv` 保存带轮次的全部样本，`summary.csv` 列出每个实现的中位数和极值。
- `paired.csv` 将同一轮中每个竞品与 Moego 配对，列出吞吐比和耗时减少比例，供检查频率漂移对结论的影响。

成功退出只说明样本完整、来自同一个二进制。moego 是否更快，要另看 `summary.csv`、每轮的配对结果和重复测量。`compare.py` 按整套 suite 检查；只采样部分 case 后直接传给它，会因其余 case 缺失而返回失败。

脚本测试使用静态输出 fixture，不执行性能测试：

```sh
python3 -m unittest test_rounds.py
```
