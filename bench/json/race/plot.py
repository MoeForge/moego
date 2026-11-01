#!/usr/bin/env python3
"""Draw the ojson benchmark charts from `go test -bench -benchmem` output.

Pass the output of bench/json/race and bench/json/complex. When several
files hold the same benchmark, the samples of the later file replace the
earlier ones, so a second run of a few rows can be given after the main run.

The charts follow the layout of bench/plot.py. Each panel is one workload
on its own linear scale; bars are sample medians. The multiplier at the top
right compares the first ojson row with the fastest, or for memory the
smallest, competitor that keeps member order; other ojson rows carry their
own multiplier after the value. When those rows ran in rounds, one process
per row and round with --count samples each, in run order, the time
multiplier is the median of the per-round ratios; otherwise it is the ratio
of the medians.
"""

import argparse
import collections
import html
import math
import re
import statistics
from pathlib import Path

FONT = 'system-ui, -apple-system, "Segoe UI", "Noto Sans CJK SC", "PingFang SC", sans-serif'
# Display names in each language and the bar class: ojson rows are blue,
# competitors gray, and the copy floors light gray.
ENGINES = {
    "ojson": ("ojson", "ojson", "own"),
    "ojson-nocopy": ("ojson NoCopy", "ojson NoCopy", "own"),
    "ojson-doc": ("ojson Doc", "ojson Doc", "own"),
    "ojson-doc-append": ("ojson Doc.AppendTo", "ojson Doc.AppendTo", "own"),
    "ojson-doc-writeto": ("ojson Doc.WriteTo", "ojson Doc.WriteTo", "own"),
    "ojson-setbytes": ("ojson SetBytes", "ojson SetBytes", "own"),
    "ojson-inplace": ("ojson in place", "ojson 原地替换", "own"),
    "gjson": ("gjson", "gjson", "peer"),
    "sjson": ("sjson", "sjson", "peer"),
    "sjson-inplace": ("sjson in place", "sjson 原地替换", "peer"),
    "sonic": ("sonic.Get", "sonic.Get", "peer"),
    "sonic-ast": ("sonic/ast", "sonic/ast", "peer"),
    "encjson-map": ("encoding/json map (unordered)", "encoding/json map（无序）", "peer"),
    "sonic-map": ("sonic map (unordered)", "sonic map（无序）", "peer"),
    "floor-clone": ("floor: copy to a new slice", "下限：拷成新切片", "floor"),
    "floor-copy": ("floor: copy to a reused buffer", "下限：拷进复用缓冲", "floor"),
}
OJSON = ("ojson", "ojson-nocopy", "ojson-doc", "ojson-doc-append", "ojson-doc-writeto", "ojson-setbytes", "ojson-inplace")
# Competitors that keep member order; ojson is compared with the best of them.
ORDERED = ("gjson", "sjson", "sonic", "sonic-ast")
# SetBytes makes one call per edit, like sjson; an edit that overwrites its
# input is compared only with sjson's edit that does the same.
RIVALS = {"ojson-setbytes": ("sjson",), "ojson-inplace": ("sjson-inplace",)}
STYLE = f"""<style>
  svg {{ font-family: {FONT}; }}
  .surface {{ fill: #fcfcfb; }}
  .ink {{ fill: #0b0b0b; }}
  .ink2 {{ fill: #52514e; }}
  .muted {{ fill: #6f6d68; }}
  .axis {{ stroke: #c3c2b7; stroke-width: 1; }}
  .own {{ fill: #2a78d6; }}
  .peer {{ fill: #898781; }}
  .floor {{ fill: #d4d2ca; }}
  .num {{ font-variant-numeric: tabular-nums; }}
  @media (prefers-color-scheme: dark) {{
    .surface {{ fill: #1a1a19; }}
    .ink {{ fill: #ffffff; }}
    .ink2 {{ fill: #c3c2b7; }}
    .muted {{ fill: #9a9891; }}
    .axis {{ stroke: #383835; }}
    .own {{ fill: #3987e5; }}
    .floor {{ fill: #4a4945; }}
  }}
</style>"""
PANEL = 560
GUTTER = 32
MARGIN = 24
NAME_COLUMN = 196
VALUE_ROOM = 124
ROW = 18
BAR = 12
LINE = re.compile(r"^Benchmark(\w+)/(\S+?)(?:-\d+)?\s+\d+\s+([\d.]+) ns/op(?:\s+[\d.]+ MB/s)?\s+(\d+) B/op\s+(\d+) allocs/op")


def load(paths):
    """Return {(bench, workload, engine): [(ns, bytes, allocs), ...]}, the
    samples of each benchmark in run order, taken from the last file that
    holds it."""
    data = {}
    for path in paths:
        rows = collections.defaultdict(list)
        for line in Path(path).read_text().splitlines():
            m = LINE.match(line)
            if not m:
                continue
            bench, name, ns, b, allocs = m.groups()
            workload, _, engine = name.rpartition("/")
            rows[(bench, workload, engine)].append((float(ns), int(b), int(allocs)))
        data.update(rows)
    return data


def header(paths):
    """goos, goarch and cpu from the first file that reports them."""
    info = {}
    for path in paths:
        for line in Path(path).read_text().splitlines():
            key, _, value = line.partition(": ")
            if key in ("goos", "goarch", "cpu") and value:
                info.setdefault(key, value.strip())
    return info


def versions():
    """Pinned competitor versions from bench/json/go.mod."""
    found = {}
    for line in (Path(__file__).resolve().parent.parent / "go.mod").read_text().splitlines():
        parts = line.split()
        if len(parts) >= 2 and parts[0] in ("github.com/tidwall/gjson", "github.com/tidwall/sjson", "github.com/bytedance/sonic"):
            found[parts[0].removeprefix("github.com/")] = parts[1]
    return " · ".join(f"{name} {found[name]}" for name in ("tidwall/gjson", "tidwall/sjson", "bytedance/sonic")
                      if name in found)


def median(samples, metric):
    return statistics.median(s[metric] for s in samples) if samples else None


def speedup(data, key, rivals, count):
    """Time ratio of the fastest rival to the ojson row key. Rows that ran in
    rounds of count samples give the median of the per-round ratios, other
    rows the ratio of the medians. None without rival samples."""
    s = data.get(key)
    rs = [data.get((key[0], key[1], r)) for r in rivals]
    rs = [r for r in rs if r]
    if not s or not rs:
        return None
    if len(s) > count and len(s) % count == 0 and all(len(r) == len(s) for r in rs):
        per = []
        for i in range(0, len(s), count):
            o = statistics.median(x[0] for x in s[i:i + count])
            per.append(min(statistics.median(x[0] for x in r[i:i + count]) for r in rs) / o)
        return statistics.median(per)
    return min(median(r, 0) for r in rs) / median(s, 0)


def row(data, key, engines, metric, count):
    """One chart row: (engine, median, multiplier). The multiplier of an
    ojson row is the best rival over ojson, a key of TEXT for the zero
    cases, or None without rivals; other rows have None."""
    e = key[2]
    v = median(data.get(key), metric)
    rivals = [r for r in RIVALS.get(e, ORDERED) if r in engines]
    if e not in OJSON or not rivals or v is None:
        return (e, v, None)
    if metric == 0:
        return (e, v, speedup(data, key, rivals, count))
    best = min(median(data.get((key[0], key[1], r)), 1) for r in rivals)
    if v == 0:
        return (e, v, "same" if best == 0 else "none")
    if best == 0:
        return (e, v, "rival0")
    return (e, v, best / v)


def text(x, y, body, cls="ink2", size=13, anchor="start", weight=None, extra=""):
    w = f' font-weight="{weight}"' if weight else ""
    return (f'<text x="{x:.1f}" y="{y:.1f}" class="{cls}{extra}" font-size="{size}" '
            f'text-anchor="{anchor}"{w}>{html.escape(body)}</text>')


def bar(x, y, width, cls, tip):
    """Square at the baseline, 4px rounded data end."""
    r = min(4, width / 2, BAR / 2)
    path = (f"M{x:.1f},{y:.1f}H{x + width - r:.1f}A{r},{r} 0 0 1 {x + width:.1f},{y + r:.1f}"
            f"V{y + BAR - r:.1f}A{r},{r} 0 0 1 {x + width - r:.1f},{y + BAR:.1f}H{x:.1f}Z")
    return f'<path class="{cls}" d="{path}"><title>{html.escape(tip)}</title></path>'


def human_time(ns):
    for unit, scale in (("s", 1e9), ("ms", 1e6), ("µs", 1e3)):
        if ns >= scale:
            return f"{ns / scale:.3g} {unit}"
    return f"{ns:.3g} ns"


def human_bytes(b):
    for unit, scale in (("GB", 1e9), ("MB", 1e6), ("KB", 1e3)):
        if b >= scale:
            return f"{b / scale:.3g} {unit}"
    return f"{b:.0f} B"


def multiple(r, words):
    if isinstance(r, str):
        return words[r]
    return f"{r:.1f}×" if r >= 10 else f"{r:.2f}×"


def panel(body, x, y, title, rows, metric, words):
    """One workload: title, the first ojson row's multiplier, one bar per engine."""
    fmt = human_time if metric == 0 else human_bytes
    first = next((i for i, (e, _, _) in enumerate(rows) if e in OJSON), None)
    body.append(text(x, y + 14, title, "ink", 13, weight=600))
    if first is not None and rows[first][2] is not None:
        body.append(text(x + PANEL, y + 14, multiple(rows[first][2], words), "ink", 13, "end", 600, " num"))
    top = y + 24
    base = x + NAME_COLUMN
    plot = PANEL - NAME_COLUMN - VALUE_ROOM
    scale = max((v for _, v, _ in rows if v), default=1)
    body.append(f'<line class="axis" x1="{base}" y1="{top}" x2="{base}" y2="{top + ROW * len(rows)}"/>')
    for i, (engine, v, r) in enumerate(rows):
        at = top + i * ROW
        shown, cls = ENGINES[engine][words["lang"]], ENGINES[engine][2]
        ink, weight = ("ink", 600) if engine in OJSON else ("ink2", None)
        body.append(text(base - 8, at + 13, shown, ink, 12, "end", weight))
        if v is None:
            body.append(text(base + 6, at + 13, "n/a", "muted", 12))
            continue
        width = max(v / scale * plot, 1) if v > 0 else 0
        if width:
            body.append(bar(base, at + 3, width, cls, f"{shown} {fmt(v)}"))
        value = fmt(v)
        if i != first and r is not None:
            value += " · " + multiple(r, words)
        body.append(text(base + width + 6, at + 13, value, ink, 12, weight=weight, extra=" num"))
    return top + ROW * len(rows)


def chart(title, subtitle, notes, sections, footers, words):
    """sections: [(heading, metric, panels)] with panels [(title, rows)];
    metric 0 is ns, 1 is bytes. Panels sit two to a line."""
    width = MARGIN * 2 + PANEL * 2 + GUTTER
    body = [text(MARGIN, 36, title, "ink", 18, weight=600), text(MARGIN, 58, subtitle, "ink2", 13)]
    y = 58
    for note in notes:
        y += 20
        body.append(text(MARGIN, y, note, "muted", 12))
    y += 22
    for heading, metric, panels in sections:
        body.append(text(MARGIN, y + 12, heading, "ink2", 12))
        y += 26
        for start in range(0, len(panels), 2):
            bottom = y
            for column, (name, rows) in enumerate(panels[start:start + 2]):
                bottom = max(bottom, panel(body, MARGIN + column * (PANEL + GUTTER), y, name, rows, metric, words))
            y = bottom + 18
        y += 4
    for f in footers:
        y += 16
        body.append(text(MARGIN, y, f, "muted", 12))
    height = y + 20
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" '
            f'viewBox="0 0 {width} {height}" role="img" aria-labelledby="t">\n'
            f'<title id="t">{html.escape(title)}</title>\n{STYLE}\n'
            f'<rect class="surface" width="{width}" height="{height}" rx="8"/>\n'
            + "\n".join(body) + "\n</svg>\n")


TEXT = {
    "en": {
        "lang": 0,
        "same": "same",
        "none": "no allocation",
        "rival0": "rival: 0 B",
        "scale": "each panel has its own scale",
        "time": "Time per call, shorter is faster",
        "memory": "Bytes allocated per call, shorter is less",
        "multiplier": "top-right: the best order-preserving competitor ÷ ojson",
        "reads": {"model+group": "Read model and group", "six-field": "Read six fields, most absent",
                  "deep": "Read one deep path"},
        "sse": ("SSE frame", {"three-field": "three fields", "one-field": "one field"}),
        "edits": lambda k: f"{k} override{'s' if k != '1' else ''}",
        "summary": ("ojson, gjson, sjson and sonic on a 1 MiB chat request",
                    "Chat request with an embedded image; reads take several paths, overrides return a new body; {}"),
        "summary_groups": ("Read model and group", "Read six fields, most absent", "1 override", "20 overrides",
                           "Query: messages.#(role==\"user\")…detail", "Set through a query"),
        "read": ("Reading request bodies: {}",
                 "Chat requests with an embedded image, 1 KiB to 8 MiB, and a small SSE frame; {}"),
        "override": ("Parameter overrides: {}",
                     "k sets and deletes on the original body, returning the new body; {}"),
        "host": "{cpu} · {platform} · Go {go} · GOMAXPROCS=1 on one pinned CPU · median of {samples} samples each",
        "samples_join": " or ",
        "shared": "Shared KVM virtual machine with other users' load; results apply to this machine only",
        "notes": [
            "Other ojson rows carry their own multiplier after the value; SetBytes is compared with sjson only.",
            "Rows that ran in rounds, one process per row and round, use the median of the per-round time ratios; "
            "bars are the medians of all samples.",
            "Outputs were checked before timing: ojson matches gjson and sjson byte for byte, sonic/ast after compacting.",
        ],
        "read_notes": [
            "ojson and gjson use GetManyBytes for several paths and GetBytes for one; NoCopy uses GetManyBytesNoCopy "
            "and GetBytesNoCopy; sonic.Get runs once per path.",
        ],
        "override_notes": [
            "sjson calls SetBytes once per edit. Doc.AppendTo appends to a reused buffer. Doc.WriteTo writes to "
            "io.Discard, so sending the body is not counted.",
            "In place = Optimistic + ReplaceInPlace, a same-length edit that overwrites the input, compared with "
            "sjson's same options.",
            "The floors copy the body without editing it. No edit that returns a new slice can beat copying into "
            "a new slice.",
        ],
        "source": "Measured with bench/json/race and bench/json/complex, drawn by bench/json/race/plot.py",
        "versions": "Versions: ",
    },
    "zh": {
        "lang": 1,
        "same": "持平",
        "none": "不分配",
        "rival0": "竞品 0 B",
        "scale": "每个面板横轴比例各自不同",
        "time": "每次耗时，越短越快",
        "memory": "每次分配的字节，越短越少",
        "multiplier": "右上角倍数为最好的保序竞品 ÷ ojson",
        "reads": {"model+group": "读取 model 和 group", "six-field": "读取六个字段，大多不存在", "deep": "读取一个深层路径"},
        "sse": ("SSE 帧", {"three-field": "三个字段", "one-field": "一个字段"}),
        "edits": lambda k: f"{k} 次覆盖",
        "summary": ("ojson 与 gjson、sjson、sonic 对比：1 MiB 聊天请求",
                    "带图聊天请求；读取一次取多个路径，覆盖从原始请求体开始、输出新的请求体；{}"),
        "summary_groups": ("读取 model 和 group", "读取六个字段，大多不存在", "1 次覆盖", "20 次覆盖",
                           "查询：messages.#(role==\"user\")…detail", "按查询路径 Set"),
        "read": ("读取请求体：{}", "带图聊天请求 1 KiB–8 MiB，以及一个 SSE 小帧；{}"),
        "override": ("参数覆盖：{}", "每次调用从原始请求体开始做 k 次 Set 或 Delete，输出新的请求体；{}"),
        "host": "{cpu} · {platform} · Go {go} · 固定一个 CPU，GOMAXPROCS=1 · 每项 {samples} 个样本的中位数",
        "samples_join": " 或 ",
        "shared": "多人共用的 KVM 虚拟机，测量时有其他用户的负载，结果只代表这台机器",
        "notes": [
            "其他 ojson 行的倍数写在数值后面；SetBytes 只对比 sjson。",
            "分轮测量的行每轮每项一个进程，耗时倍数是逐轮倍数的中位数；条形是全部样本的中位数。",
            "计时前已核对输出：ojson 与 gjson、sjson 逐字节相同，sonic/ast 紧凑后相同。",
        ],
        "read_notes": [
            "ojson 和 gjson 取多个路径用 GetManyBytes，取一个用 GetBytes；NoCopy 用 GetManyBytesNoCopy 和 GetBytesNoCopy；"
            "sonic.Get 每个路径调用一次。",
        ],
        "override_notes": [
            "sjson 每次修改调用一次 SetBytes。Doc.AppendTo 追加到反复使用的缓冲区；Doc.WriteTo 写给 io.Discard，"
            "发送请求体的成本不计入。",
            "原地替换 = Optimistic + ReplaceInPlace，同长度替换并改写输入，只对比 sjson 的同一组选项。",
            "两行下限只拷贝请求体、不做修改。返回新切片的修改不可能快过「拷成新切片」。",
        ],
        "source": "数据由 bench/json/race 和 bench/json/complex 测得，图由 bench/json/race/plot.py 生成",
        "versions": "竞品版本：",
    },
}
SIZES = ("1KiB", "64KiB", "1MiB", "8MiB")
READERS = ("ojson", "ojson-nocopy", "gjson", "sonic", "encjson-map", "sonic-map")
EDITORS = ("ojson-doc", "ojson-doc-append", "ojson-doc-writeto", "ojson-setbytes", "ojson-inplace", "sjson",
           "sjson-inplace", "sonic-ast", "encjson-map", "sonic-map", "floor-clone", "floor-copy")
# Measured for one edit only.
ONE_EDIT = ("ojson-inplace", "sjson-inplace", "floor-clone", "floor-copy")
# The summary chart: (bench, workload, engines) at 1 MiB. The first ojson
# engine of each workload is the one the README table reports.
SUMMARY = (
    ("Read", "model+group/1MiB", ("ojson", "ojson-nocopy", "gjson", "sonic")),
    ("Read", "six-field/1MiB", ("ojson", "ojson-nocopy", "gjson", "sonic")),
    ("Override", "1/1MiB", ("ojson-doc", "ojson-setbytes", "sjson", "sonic-ast", "floor-clone")),
    ("Override", "20/1MiB", ("ojson-doc", "ojson-doc-append", "ojson-setbytes", "sjson", "sonic-ast")),
    ("Get", "req1MiB/query", ("ojson", "gjson")),
    ("Set", "req1MiB/query", ("ojson", "sjson")),
)


def size_name(s):
    return s.replace("KiB", " KiB").replace("MiB", " MiB")


def describe(words, info, go, groups):
    """The host line and the shared-machine caveat printed under the title."""
    cpu = re.sub(r"\(R\)|\(TM\)| CPU", "", info.get("cpu", "")).strip()
    counts = sorted({len(samples) for samples in groups})
    host = words["host"].format(cpu=cpu, platform=f"{info.get('goos', '')}/{info.get('goarch', '')}",
                                go=go.removeprefix("go"), samples=words["samples_join"].join(map(str, counts)))
    return [host, words["shared"]]


def footers(words, notes=()):
    return [*words["notes"], *notes, words["source"], words["versions"] + versions()]


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("bench", nargs="+", help="go test -bench -benchmem output files; later files replace earlier rows")
    ap.add_argument("--output", type=Path, default=Path("."), help="directory for the SVG files")
    ap.add_argument("--lang", choices=TEXT, default="en", help="chart text language and file name suffix")
    ap.add_argument("--go", required=True, help="Go version the benchmarks ran with, e.g. go1.26.6")
    ap.add_argument("--count", type=int, default=5, help="samples per process, for rows that ran in rounds")
    args = ap.parse_args(argv)
    data = load(args.bench)
    info = header(args.bench)
    words = TEXT[args.lang]
    prefix = "-".join(["ojson", info.get("goos", "bench"), info.get("goarch", "")]).rstrip("-")
    scale = words["scale"]

    def need(key):
        if key not in data:
            raise SystemExit(f"missing Benchmark{key[0]}/{key[1]}/{key[2]} in the input")
        return data[key]

    def rows(bench, workload, engines, metric):
        for e in engines:
            need((bench, workload, e))
        return [row(data, (bench, workload, e), engines, metric, args.count) for e in engines]

    charts = {}
    used = [need((b, w, e)) for b, w, es in SUMMARY for e in es]
    names = words["summary_groups"]
    sections = [(f"{words[h]}；{scale}" if args.lang == "zh" else f"{words[h]}; {scale}", m,
                 [(names[i], rows(b, w, es, m)) for i, (b, w, es) in enumerate(SUMMARY)])
                for m, h in ((0, "time"), (1, "memory"))]
    title, sub = words["summary"]
    charts[f"{prefix}-1MiB.{args.lang}.svg"] = chart(
        title, sub.format(words["multiplier"]), describe(words, info, args.go, used), sections,
        footers(words), words)

    sep = "；" if args.lang == "zh" else "; "
    for metric, suffix, unit in ((0, "time", words["time"]), (1, "memory", words["memory"])):
        sections = []
        for w, name in words["reads"].items():
            sections.append((name, metric, [(size_name(s), rows("Read", f"{w}/{s}", READERS, metric)) for s in SIZES]))
        frame, kinds = words["sse"]
        sections.append((frame, metric, [(name, rows("SSE", w, READERS, metric)) for w, name in kinds.items()]))
        used = [need(("Read", f"{w}/{s}", e)) for w in words["reads"] for s in SIZES for e in READERS]
        used += [need(("SSE", w, e)) for w in kinds for e in READERS]
        title, sub = words["read"]
        charts[f"{prefix}-read-{suffix}.{args.lang}.svg"] = chart(
            title.format(unit), sub.format(words["multiplier"] + sep + scale), describe(words, info, args.go, used),
            sections, footers(words, words["read_notes"]), words)

        sections, used = [], []
        for k in ("1", "5", "20", "50"):
            editors = [e for e in EDITORS if k == "1" or e not in ONE_EDIT]
            sections.append((words["edits"](k), metric,
                             [(size_name(s), rows("Override", f"{k}/{s}", editors, metric)) for s in SIZES]))
            used += [need(("Override", f"{k}/{s}", e)) for s in SIZES for e in editors]
        title, sub = words["override"]
        charts[f"{prefix}-override-{suffix}.{args.lang}.svg"] = chart(
            title.format(unit), sub.format(words["multiplier"] + sep + scale), describe(words, info, args.go, used),
            sections, footers(words, words["override_notes"]), words)

    args.output.mkdir(parents=True, exist_ok=True)
    for name, svg in charts.items():
        (args.output / name).write_text(svg)
        print(args.output / name)


if __name__ == "__main__":
    main()
