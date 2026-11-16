# Lossless image encoder benchmark

A separate module (its own `go.mod`, with
`replace github.com/MoeForge/moego => ../..`) that measures lossless image
encoders on one public corpus: output size, encode speed, and decode speed
with each format's Go and C reference decoders. Every encoded file is
decoded with both reference decoders and must reproduce the source pixels
exactly before it is timed. The results are in
[PNG results](../../docs/png-benchmark-results-linux-amd64.md) and
[WebP results](../../docs/webp-benchmark-results-linux-amd64.md).

## Pieces

- `codecs/`: the encoders and decoders under test, each wrapped as
  `func(*image.NRGBA) ([]byte, error)`. `libwebp.go`, `libpng.go`,
  `libdeflate.go` (a small PNG writer on libdeflate) and `fpnge.go` are cgo
  wrappers (built only with cgo; fpnge also needs `-tags fpnge` and amd64);
  the rest is pure Go, so `CGO_ENABLED=0 go build ./...` still works.
- `cmd/imgbench`: the driver. It decodes each corpus PNG once with
  `image/png` into `*image.NRGBA`, then for every encoder encodes,
  verifies, and times encode and decode. Raw data goes to CSV files.
- `cmd/imgsum`: turns the CSV files into `summary.md` and `summary.csv`.
- `cmd/deflatebench`: the zlib engines alone (moego's `internal/deflate`,
  compress/zlib, and with cgo zlib and libdeflate) on the bytes a PNG
  encoder hands to zlib: image/png's filtered rows of each corpus image.
- `cmd/imgalloc`: Go heap allocations of one encode per image for
  moego/png, image/png, moego/webp and deepteams/webp, cold (two GCs
  before each encode) and warm (second encode with an `EncoderBufferPool`;
  deepteams has none).
- `cmd/imgchart`: draws the charts in `docs/images` (compression ratio and
  encode speed per level, `-format png` or `webp`, `-lang en` or `zh`) from
  a run's `summary.csv` and `per-image.csv`.
- `vp8lcheck/`: tests that decode moego's VP8L streams with
  `golang.org/x/image/webp` and, with cgo, libwebp.
- `corpus.txt`: the image subset with sha256 sums; `fetch_corpus.sh`
  downloads and verifies it, `select_corpus.py` regenerates the list.
- `holdout.txt`: 51 other images of the same suite, not in `corpus.txt`,
  for checking that results hold beyond the corpus;
  `LIST=holdout.txt ./fetch_corpus.sh DIR` fetches them.
- `sysroot.sh`: fetches the libwebp/libpng/zlib/libdeflate headers as
  Debian `-dev` packages without root, links the system's shared libraries,
  and builds fpnge at a pinned commit into a static library.
- `run.sh`: the whole benchmark. It writes `summary.md` (tables),
  `summary.csv`, `per-image.csv`, `failures.csv`, and the raw imgbench
  output of every pass (`check*/`, `timing*/`: `results-*.csv` per encoder
  and image, `runs-*.csv` with every timed run, `images-*.csv`,
  `meta-*.json`, logs).

## Corpus

97 PNG images from the QOI benchmark suite
(<https://qoiformat.org/benchmark/>, `qoi_benchmark_suite.tar`, sha256
`00166f555ca760e23647025e46d9b046e03cd2e22869d4ecb496edc527cd0d7d`):
all 24 Kodak photos plus every k-th file by name of the other categories
(photos, game and web screenshots, icons, textures, cut-outs with alpha),
capped at 4.2 megapixels (7 for web screenshots); 69.8 megapixels in all,
29 images with alpha, 11 with at most 256 colors. The images are not in
the repository; `fetch_corpus.sh DIR` downloads the archive and extracts and
verifies them.

## Running

On Debian or Ubuntu amd64 with Go 1.26, gcc and g++, git, curl,
`apt-get` and `dpkg-deb` (for the C headers), `taskset` and at least 8
CPUs:

```sh
bench/image/run.sh /tmp/image-run
```

It fetches the C headers and the corpus next to the output directory if
needed, builds `imgbench` with cgo, runs the passes described in the script
header, and writes `summary.md`. The check pass runs one process on each
CPU but the last four; the timing passes run one process on each of the
last four (`CHECK_CPUS` and `TIMING_CPUS` choose others). A run takes about
an hour with 12 CPUs for the check pass. Set `NOTES=file.md` to put a notes
file into the summary.

`imgbench -listenc` lists the encoders; `-encoders 'libwebp/*,png-std/default'`
selects some, `-images kodak` filters the corpus, `-check` skips timing.

## Measurement rules

- Every encoder gets the same `*image.NRGBA`. 16-bit sources are reduced
  to their high byte, which defines the source pixels.
- An encoder output counts only if `image/png` + libpng (PNG) or
  `golang.org/x/image/webp` + libwebp (WebP) decode it back to the exact
  source pixels. If only the RGB of fully transparent pixels differs, the
  result is marked `alpha0` and listed, never silently passed.
- Timing: one process per CPU (`taskset`), `GOMAXPROCS=1`, libwebp
  `thread_level=0`, calamus `Workers=1`. The checked encode is a warm-up;
  then each cell runs at least 5 times and for at least 100 ms, up to 100
  runs. When the warm-up takes over 100 ms, 3 runs; when it takes 2 s or
  more (`-slow`), the warm-up is the only timed run. The median is
  reported; all individual runs are in `runs-*.csv`.
- Go heap allocation per encode comes from `runtime.MemStats` around the
  first timed run; C and WASM-internal allocations are not visible there.
  For moego/png the column depends on whether its internal buffer pool
  survived the GC before that run; `cmd/imgalloc` measures it without
  that noise.
