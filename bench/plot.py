#!/usr/bin/env python3
"""Draw SVG throughput charts from a rounds.py summary.csv."""

import argparse
import collections
import csv
import html
import json
from pathlib import Path
import re


SIZES = {"1KiB": 1 << 10, "32KiB": 32 << 10, "256KiB": 256 << 10, "1MiB": 1 << 20, "4MiB": 4 << 20}
# Order follows the README tables; validation compares ValidateString with full decodes.
SCENARIOS = (
    "BenchmarkCodec/Encode",
    "BenchmarkCodec/Decode",
    "BenchmarkNewAPI/EncodeToString",
    "BenchmarkNewAPI/DecodeString",
    "BenchmarkNewAPI/DataURLConcat",
    "BenchmarkNewAPI/DataURLDecode",
    "BenchmarkDataURL/OneAllocation",
    "BenchmarkDataURL/AppendReuse",
    "BenchmarkValidation/Allocate",
    "BenchmarkValidation/Reuse",
)
# Chart text for --lang. Names follow SCENARIOS; the first section holds the first eight scenarios.
TEXT = {
    "zh": {
        "names": ("预分配编码", "预分配解码", "EncodeToString", "DecodeString", "data URL 拼接", "data URL 解码",
                  "单次分配 data URL", "复用缓冲区 data URL", "校验计长 vs 分配后解码", "校验计长 vs 复用输出"),
        "sections": ("编码与解码：这 8 个场景横轴比例相同",
                     "校验计长，横轴比例与上方不同：moego 调用 ValidateString，不写出解码结果；其他实现完整解码后返回长度"),
        "samples": "每项 {} 个样本的中位数",
        "stdlib": ("moego 与 encoding/base64 吞吐量对比",
                   "{} 原始数据，单位 GB/s，越长越快；右上角倍数为 moego 吞吐量 ÷ encoding/base64 吞吐量"),
        "competitors": ("moego 与 Go base64 实现吞吐量横向对比",
                        "{} 原始数据，单位 GB/s，越长越快；右上角倍数为 moego 吞吐量 ÷ 最快的其他实现"),
        "source": "数据由 bench/rounds.py 测得，图由 bench/plot.py 生成",
        "versions": "竞品版本：",
    },
    "en": {
        "names": ("Encode into a preallocated buffer", "Decode into a preallocated buffer", "EncodeToString",
                  "DecodeString", "data URL by concatenation", "Decode a data URL", "data URL in one allocation",
                  "data URL into a reused buffer", "ValidateString vs DecodeString",
                  "ValidateString vs Decode, reused buffer"),
        "sections": ("Encoding and decoding: these 8 scenarios share one scale",
                     "Validation, on a different scale: moego's ValidateString writes no decoded bytes; "
                     "the others decode in full and return the length"),
        "samples": "median of {} samples each",
        "stdlib": ("Throughput of moego and encoding/base64",
                   "{} of raw data in GB/s, longer is faster; "
                   "top-right multiplier: moego throughput ÷ encoding/base64 throughput"),
        "competitors": ("Throughput of moego and other Go Base64 implementations",
                        "{} of raw data in GB/s, longer is faster; "
                        "top-right multiplier: moego throughput ÷ the fastest other implementation"),
        "source": "Measured with bench/rounds.py, drawn by bench/plot.py",
        "versions": "Versions: ",
    },
}
COMPETITORS = ("Emmansun", "Segmentio", "Cloudwego", "CloudwegoFallback", "Cristalhq")
LABELS = {"Moego": "moego", "Stdlib": "encoding/base64"}
FONT = 'system-ui, -apple-system, "Segoe UI", "Noto Sans CJK SC", "PingFang SC", sans-serif'
STYLE = f"""<style>
  svg {{ font-family: {FONT}; }}
  .surface {{ fill: #fcfcfb; }}
  .ink {{ fill: #0b0b0b; }}
  .ink2 {{ fill: #52514e; }}
  .muted {{ fill: #6f6d68; }}
  .axis {{ stroke: #c3c2b7; stroke-width: 1; }}
  .moego {{ fill: #2a78d6; }}
  .peer {{ fill: #898781; }}
  .num {{ font-variant-numeric: tabular-nums; }}
  @media (prefers-color-scheme: dark) {{
    .surface {{ fill: #1a1a19; }}
    .ink {{ fill: #ffffff; }}
    .ink2 {{ fill: #c3c2b7; }}
    .muted {{ fill: #9a9891; }}
    .axis {{ stroke: #383835; }}
    .moego {{ fill: #3987e5; }}
  }}
</style>"""


def load(path, size):
    medians = collections.defaultdict(dict)
    samples = set()
    with open(path, newline="") as source:
        for row in csv.DictReader(source):
            medians[row["case"]][row["codec"]] = float(row["median_ns"])
            if row["case"].endswith("/" + size):
                samples.add(int(row["samples"]))
    throughput = {}
    for key in SCENARIOS:
        case = f"{key}/{size}"
        if case not in medians:
            raise SystemExit(f"missing {case} in {path}")
        # Bytes per nanosecond equals GB/s; every case reports raw payload bytes.
        throughput[key] = {codec: SIZES[size] / ns for codec, ns in medians[case].items()}
    low, high = min(samples), max(samples)
    return throughput, str(low) if low == high else f"{low}–{high}"


def bar(x, y, width, height, cls, tip):
    """Square at the baseline, 4px rounded data end."""
    r = min(4, width / 2, height / 2)
    path = (f"M{x:.1f},{y:.1f}H{x + width - r:.1f}A{r},{r} 0 0 1 {x + width:.1f},{y + r:.1f}"
            f"V{y + height - r:.1f}A{r},{r} 0 0 1 {x + width - r:.1f},{y + height:.1f}H{x:.1f}Z")
    return f'<path class="{cls}" d="{path}"><title>{html.escape(tip)}</title></path>'


def text(x, y, body, cls="ink2", size=13, anchor="start", weight=None, extra=""):
    attrs = f' font-weight="{weight}"' if weight else ""
    return (f'<text x="{x:.1f}" y="{y:.1f}" class="{cls}{extra}" font-size="{size}" '
            f'text-anchor="{anchor}"{attrs}>{html.escape(body)}</text>')


def document(width, height, title, body):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" '
            f'viewBox="0 0 {width} {height}" role="img" aria-labelledby="t">\n'
            f'<title id="t">{html.escape(title)}</title>\n{STYLE}\n'
            f'<rect class="surface" width="{width}" height="{height}" rx="8"/>\n'
            + "\n".join(body) + "\n</svg>\n")


SECTIONS = (SCENARIOS[:8], SCENARIOS[8:])
MODULES = ("emmansun/base64", "segmentio/asm", "cloudwego/base64x", "cristalhq/base64")
PANEL = 450
GUTTER = 32
MARGIN = 24
NAME_COLUMN = 118
VALUE_ROOM = 44
ROW = 18
BAR = 12


def describe_host(run, samples, words):
    """Host line from the Go benchmark headers and rounds.py metadata."""
    info = {}
    combined = run / "combined.txt"
    if combined.exists():
        for line in combined.read_text().splitlines():
            field, _, value = line.partition(": ")
            if field in ("goos", "goarch", "cpu") and value:
                info.setdefault(field, value.strip())
    environment = run / "environment.json"
    if environment.exists():
        meta = json.loads(environment.read_text())
        version = re.search(r"\bgo1\.[\d.]+", meta.get("build_info", {}).get("output", ""))
        if version:
            info["go"] = version.group(0)
        procs = meta.get("effective_environment", {}).get("GOMAXPROCS")
        if procs:
            info["procs"] = f"GOMAXPROCS={procs}"
    platform = "/".join(info[key] for key in ("goos", "goarch") if key in info)
    parts = (info.get("cpu"), platform, info.get("go"), info.get("procs"), words["samples"].format(samples))
    return " · ".join(part for part in parts if part), info


def competitor_versions():
    versions = {}
    for line in Path(__file__).with_name("go.mod").read_text().splitlines():
        match = re.match(r"^\s*(?:require\s+)?github\.com/(\S+)\s+(v\S+)", line)
        if match:
            versions[match.group(1)] = match.group(2)
    return " · ".join(f"{module} {versions[module]}" for module in MODULES if module in versions)


def multiple(value):
    return f"{value:.1f}×" if value >= 10 else f"{value:.2f}×"


def panel(body, x, y, title, ratio, rows, scale):
    """One scenario: title, multiplier, and one bar per implementation."""
    body.append(text(x, y + 14, title, "ink", 13, weight=600))
    body.append(text(x + PANEL, y + 14, ratio, "ink", 13, "end", 600, " num"))
    top = y + 24
    base = x + NAME_COLUMN
    plot = PANEL - NAME_COLUMN - VALUE_ROOM
    body.append(f'<line class="axis" x1="{base}" y1="{top}" x2="{base}" y2="{top + ROW * len(rows)}"/>')
    for index, (codec, value) in enumerate(rows):
        row = top + index * ROW
        name = LABELS.get(codec, codec)
        ink, weight = ("ink", 600) if codec == "Moego" else ("ink2", None)
        width = max(value / scale * plot, 1)
        body.append(text(base - 8, row + 13, name, ink, 12, "end", weight))
        body.append(bar(base, row + 3, width, BAR, "moego" if codec == "Moego" else "peer", f"{name} {value:.2f} GB/s"))
        body.append(text(base + width + 6, row + 13, f"{value:.2f}", ink, 12, weight=weight))
    return top + ROW * len(rows)


def chart(data, codecs, words, title, subtitle, notes, footers):
    width = MARGIN * 2 + PANEL * 2 + GUTTER
    names = dict(zip(SCENARIOS, words["names"]))
    body = [text(MARGIN, 36, title, "ink", 18, weight=600), text(MARGIN, 58, subtitle, "ink2", 13)]
    y = 58
    for note in notes:
        y += 20
        body.append(text(MARGIN, y, note, "muted", 12))
    y += 22
    for heading, keys in zip(words["sections"], SECTIONS):
        body.append(text(MARGIN, y + 12, heading, "ink2", 12))
        y += 26
        # Bars share one scale within a section; validation runs far faster than full decodes.
        scale = max(data[key][codec] for key in keys for codec in codecs)
        for start in range(0, len(keys), 2):
            bottom = y
            for column, key in enumerate(keys[start:start + 2]):
                x = MARGIN + column * (PANEL + GUTTER)
                rows = [(codec, data[key][codec]) for codec in codecs]
                # The multiplier compares Moego with the fastest implementation drawn in this chart.
                best = max(value for codec, value in rows if codec != "Moego")
                bottom = max(bottom, panel(body, x, y, names[key], multiple(data[key]["Moego"] / best), rows, scale))
            y = bottom + 18
        y += 4
    for footer in footers:
        y += 16
        body.append(text(MARGIN, y, footer, "muted", 12))
    return document(width, y + 20, title, body)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("run", type=Path, help="rounds.py output directory with summary.csv")
    parser.add_argument("--output", type=Path, default=Path("."), help="directory for the SVG files")
    parser.add_argument("--size", choices=SIZES, default="1MiB")
    parser.add_argument("--note", default="", help="host caveat printed under the title")
    parser.add_argument("--lang", choices=TEXT, default="en", help="chart text language and file name suffix")
    args = parser.parse_args(argv)
    data, samples = load(args.run / "summary.csv", args.size)
    present = set.intersection(*(set(values) for values in data.values()))
    others = present - {"Moego", "Stdlib"}
    peers = [codec for codec in COMPETITORS if codec in others] + sorted(others - set(COMPETITORS))
    if not {"Moego", "Stdlib"} <= present or not peers:
        raise SystemExit("every case needs Moego, Stdlib and at least one competitor")
    words = TEXT[args.lang]
    host, info = describe_host(args.run, samples, words)
    notes = [host] + ([args.note] if args.note else [])
    size = args.size.replace("KiB", " KiB").replace("MiB", " MiB")
    # Name the tools, not the input directory: measurement output is not part of the repository.
    source = words["source"]
    prefix = "-".join(info[key] for key in ("goos", "goarch") if key in info) or "bench"
    stdlib_title, stdlib_subtitle = words["stdlib"]
    peers_title, peers_subtitle = words["competitors"]
    charts = {
        f"{prefix}-{args.size}-vs-stdlib.{args.lang}.svg": chart(
            data, ("Moego", "Stdlib"), words, stdlib_title, stdlib_subtitle.format(size), notes, [source]),
        f"{prefix}-{args.size}-vs-competitors.{args.lang}.svg": chart(
            data, ("Moego", *peers, "Stdlib"), words, peers_title, peers_subtitle.format(size),
            notes, [source, words["versions"] + competitor_versions()]),
    }
    args.output.mkdir(parents=True, exist_ok=True)
    for name, svg in charts.items():
        (args.output / name).write_text(svg, encoding="utf-8")
        print(args.output / name)


if __name__ == "__main__":
    main()
