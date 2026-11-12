# Regular expression measurements

This separate Go 1.27 module compares `moego/regexp`, the standard library,
Grafana regexp, coregex and go-re2. Dependency versions are pinned in
`go.mod` and `go.sum`; the production module has no external dependencies.

The 44 cases use deterministic synthetic inputs. They cover short request
fields, text replacement, redaction and keyword lists, structured text
extraction, streaming frames, source search, mixed text and binary payloads,
and general patterns. `TestCompetitors` compares every result with the
standard library. An implementation with a different result must be excluded
from the comparison for that case.

```sh
go test -count=1 ./...
go test -c -o regexp.test
python3 measure.py speed --binary ./regexp.test --output measurements --repetitions 5
```

Save verbose correctness output with `go test -count=1 -v -run
'^TestCompetitors$' . > correctness.txt`, then summarize with:

```sh
python3 report.py measurements --correctness correctness.txt --output summary
```

The report contains JSON and CSV with medians, ranges, sample standard
deviations and variances. Speed ratios compare implementations within each
repetition before taking the median. Class summaries are geometric means
of those case ratios. Missing or failed measurements are rejected; no
outliers are removed.

Install `requirements-plot.txt` in a Python environment to export charts:

```sh
python3 plot.py summary.json speed.svg --metric speed
python3 plot.py memory-summary.json memory.svg --metric rss
python3 plot.py summary.json speed.zh.svg --metric speed --language zh
```

Memory charts also accept `--metric heap` and `--metric bytes_per_op`;
`--metric cases` shows the speed of each matching case. `--caption` adds
the machine and toolchain description. Charts use selectable text and
English or Chinese labels, with light and dark palettes.
Their whiskers show observed ranges, not confidence intervals. A logarithmic
axis is marked when the implementations differ widely.

Each implementation runs in a separate process, with its execution order
rotated between repetitions. Calls use concrete implementation types. A
benchmark operation processes every input belonging to that case; for
example, the header case checks sixteen headers. Matching measures compiled
patterns after a warmup call. Cold compilation disables moego's compile
cache. All timed measurements have garbage collection enabled.

`measure.py` sets `GOMAXPROCS=1`, `GOGC=100` and `GOMEMLIMIT=off`. Pin the
driver to one CPU when comparing results. It records complete output and
exit status for every process, and refuses to overwrite an existing output
directory. Hosted CI measurements describe the runner that produced them;
measure the intended deployment machine for performance estimates.

## Peak memory at equal load

```sh
python3 measure.py memory --binary ./regexp.test --output memory --repetitions 5
```

Every implementation performs the same number of class iterations at the
same rate, as specified in `memory.json`. One class iteration executes every
case and input in that class. A fresh process compiles the patterns once,
then follows the fixed schedule. Input fixtures use preallocated storage;
their retained memory is included. The test requires at least three natural
garbage collections and checks that the requested rate is sustained. It
does not force collection. To calibrate a rate on another machine, use
`RXPEAK=Stdlib/C RXPEAKCALIBRATE=1 ./regexp.test -test.run '^TestPeak$'`,
repeat for every implementation, and choose a rate that all correct
implementations can sustain.

The JSON records contain peak Go heap in use, peak process RSS, allocation
bytes per class iteration and per API call, collection counts and timing
lag. Heap in use includes objects and unused space within allocated heap
spans. RSS includes code, input fixtures, runtime storage and native or Wasm
memory; it is read from `/proc/self/statm` on Linux. Memory is sampled every
250 microseconds and after compilation and each case. Absolute peaks and
the baseline and increase are recorded separately. Compare go-re2 by RSS:
its matching state is not represented by Go heap usage alone. Report the
median, range and sample variance across repetitions.

`RXPEAKLEGACY=1` selects the fixed-time method in `peak_test.go` instead.
That method gives faster implementations more work in the same time, so its
results are not at equal load.

## Retained compiled state

`RXFOOT=1 ./regexp.test -test.run '^TestFootprint$'` reports the average
retained Go heap of 64 handles compiled from the same pattern, after
warming them and collecting idle caches. The default compile cache stays
enabled, so moego handles can share compiled state. This is not a
measurement of 64 independently compiled patterns. Unlike
the peak-memory measurement, this separate test explicitly forces garbage
collections. Its output describes retained compiled state, not an active
process's peak memory.
