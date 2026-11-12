#!/usr/bin/env python3
"""Draw the moego/json benchmark charts.

The first input is `go test -bench -benchmem` output of the codec matrix,
run as the results page shows: each row of each engine in a process of its
own, one sample per process, rounds one after another. The n-th sample of
a row's engines comes from the n-th round, so each round gives one paired
time ratio per competitor. The time multiplier of a panel is the closest
competitor ÷ moego/json, the reciprocal of the median of the per-round
ratios moego/json ÷ competitor; the memory multiplier is the lowest median
of the competitors ÷ moego/json's. Bars are medians.

--w7 TIME PEAK draws the large-body chart from the lines that the commands
in cmd/w7 print: TIME from the pass that measures time, PEAK from the pass
that measures memory. Its values are the medians of each engine's runs,
and its multipliers compare medians.

--host names a file with goos, goarch and cpu lines, for output that has
none, such as the matrix, which keeps only the result lines.
"""

import argparse
import collections
import html
import re
import statistics
from pathlib import Path

FONT = 'system-ui, -apple-system, "Segoe UI", "Noto Sans CJK SC", "PingFang SC", sans-serif'
# Display names in each language and the bar class: moego/json rows are
# blue, competitors gray.
ENGINES = {
    "moego": ("moego/json", "moego/json", "own"),
    "moego-nocopy": ("moego/json NoCopy", "moego/json NoCopy", "own"),
    "encjson": ("encoding/json", "encoding/json", "peer"),
    "jsonv2": ("encoding/json/v2", "encoding/json/v2", "peer"),
    "sonic-std": ("sonic Std", "sonic Std", "peer"),
    "sonic-default": ("sonic Default", "sonic Default", "peer"),
    "goccy": ("goccy/go-json", "goccy/go-json", "peer"),
}
OWN = ("moego", "moego-nocopy")
RIVALS = ("encjson", "jsonv2", "sonic-std", "sonic-default", "goccy")
STYLE = f"""<style>
  svg {{ font-family: {FONT}; }}
  .surface {{ fill: #fcfcfb; }}
  .ink {{ fill: #0b0b0b; }}
  .ink2 {{ fill: #52514e; }}
  .muted {{ fill: #6f6d68; }}
  .axis {{ stroke: #c3c2b7; stroke-width: 1; }}
  .own {{ fill: #2a78d6; }}
  .peer {{ fill: #898781; }}
  .num {{ font-variant-numeric: tabular-nums; }}
  @media (prefers-color-scheme: dark) {{
    .surface {{ fill: #1a1a19; }}
    .ink {{ fill: #ffffff; }}
    .ink2 {{ fill: #c3c2b7; }}
    .muted {{ fill: #9a9891; }}
    .axis {{ stroke: #383835; }}
    .own {{ fill: #3987e5; }}
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
W7LINE = re.compile(r"W7/(\w+)/(\d+)MiB/(\S+) ms=([\d.]+) peakMiB=([\d.]+) hwm=(\d+)kB")


def load(path):
    """{(bench, workload, engine): [(ns, bytes, allocs), ...]} in run order."""
    data = collections.defaultdict(list)
    for line in Path(path).read_text().splitlines():
        m = LINE.match(line)
        if m:
            bench, name, ns, b, allocs = m.groups()
            workload, _, engine = name.rpartition("/")
            data[(bench, workload, engine)].append((float(ns), int(b), int(allocs)))
    return data


def load_w7(path):
    """{(scenario, MiB, engine): [(ns, heap bytes, peak RSS bytes), ...]}."""
    data = collections.defaultdict(list)
    for line in Path(path).read_text().splitlines():
        m = W7LINE.search(line)
        if m:
            sc, mb, engine, ms, peak, hwm = m.groups()
            data[(sc, int(mb), engine)].append((float(ms) * 1e6, float(peak) * (1 << 20), int(hwm) * 1024))
    return data


def header(path):
    """goos, goarch and cpu."""
    info = {}
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
        if len(parts) >= 2 and parts[0] in ("github.com/bytedance/sonic", "github.com/goccy/go-json"):
            found[parts[0].removeprefix("github.com/")] = parts[1]
    return " · ".join(f"{name} {found[name]}" for name in ("bytedance/sonic", "goccy/go-json") if name in found)


def median(samples, metric):
    return statistics.median(s[metric] for s in samples) if samples else None


def speedup(data, key, rivals):
    """The closest rival ÷ the row key: the reciprocal of the largest of the
    medians of the per-round ratios key ÷ rival."""
    own = data[key]
    worst = None
    for r in rivals:
        theirs = data[(key[0], key[1], r)]
        if len(theirs) == len(own):
            m = statistics.median(o[0] / t[0] for o, t in zip(own, theirs))
        else:
            m = median(own, 0) / median(theirs, 0)
        worst = m if worst is None else max(worst, m)
    return 1 / worst


def smaller(best, v):
    """best ÷ v, or a key of TEXT where one of them is zero."""
    if v == 0:
        return "same" if best == 0 else "none"
    if best == 0:
        return "rival0"
    return best / v


def rows(data, bench, workload, metric, paired=True):
    """One panel's rows: (engine, median, multiplier) for the engines that
    have the row, moego/json first. Only moego/json rows carry multipliers."""
    have = [e for e in ENGINES if (bench, workload, e) in data]
    if "moego" not in have:
        raise SystemExit(f"missing Benchmark{bench}/{workload}/moego in the input")
    rivals = [e for e in have if e in RIVALS]
    out = []
    for e in have:
        key = (bench, workload, e)
        v = median(data[key], metric)
        r = None
        if e in OWN and rivals:
            if metric == 0:
                r = speedup(data, key, rivals) if paired else min(median(data[(bench, workload, x)], 0) for x in rivals) / v
            else:
                r = smaller(min(median(data[(bench, workload, x)], metric) for x in rivals), v)
        out.append((e, v, r))
    return out


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


def scaled(v, units):
    """v in the largest unit it reaches, with three significant digits; a
    value that rounds up to 1000 moves to the next unit."""
    for i, (unit, scale) in enumerate(units):
        if v >= scale or i == len(units) - 1:
            s = f"{v / scale:.3g}"
            if float(s) >= 1000 and i > 0:
                unit, scale = units[i - 1]
                s = f"{v / scale:.3g}"
            return f"{s} {unit}"


def human_time(ns):
    return scaled(ns, (("s", 1e9), ("ms", 1e6), ("µs", 1e3), ("ns", 1)))


def human_bytes(b):
    return scaled(b, (("GiB", 1 << 30), ("MiB", 1 << 20), ("KiB", 1 << 10), ("B", 1)))


def multiple(r, words):
    if isinstance(r, str):
        return words[r]
    return f"{r:.0f}×" if r >= 100 else f"{r:.1f}×" if r >= 10 else f"{r:.2f}×"


def panel(body, x, y, title, rows, metric, words):
    """One row of the matrix: title, moego/json's multiplier, one bar per engine."""
    fmt = human_time if metric == 0 else human_bytes
    body.append(text(x, y + 14, title, "ink", 13, weight=600))
    if rows[0][2] is not None:
        body.append(text(x + PANEL, y + 14, multiple(rows[0][2], words), "ink", 13, "end", 600, " num"))
    top = y + 24
    base = x + NAME_COLUMN
    plot = PANEL - NAME_COLUMN - VALUE_ROOM
    scale = max((v for _, v, _ in rows if v), default=1)
    body.append(f'<line class="axis" x1="{base}" y1="{top}" x2="{base}" y2="{top + ROW * len(rows)}"/>')
    for i, (engine, v, r) in enumerate(rows):
        at = top + i * ROW
        shown, cls = ENGINES[engine][words["lang"]], ENGINES[engine][2]
        ink, weight = ("ink", 600) if engine in OWN else ("ink2", None)
        body.append(text(base - 8, at + 13, shown, ink, 12, "end", weight))
        width = max(v / scale * plot, 1) if v > 0 else 0
        if width:
            body.append(bar(base, at + 3, width, cls, f"{shown} {fmt(v)}"))
        value = fmt(v)
        if i and r is not None:
            value += " · " + multiple(r, words)
        body.append(text(base + width + 6, at + 13, value, ink, 12, weight=weight, extra=" num"))
    return top + ROW * len(rows)


def chart(title, subtitle, notes, sections, footers):
    """sections: [(heading, metric, panels, words)] with panels [(title,
    rows)]; metric 0 is ns, 1 and 2 are bytes. Panels sit two to a line."""
    width = MARGIN * 2 + PANEL * 2 + GUTTER
    body = [text(MARGIN, 36, title, "ink", 18, weight=600), text(MARGIN, 58, subtitle, "ink2", 13)]
    y = 58
    for note in notes:
        y += 20
        body.append(text(MARGIN, y, note, "muted", 12))
    y += 22
    for heading, metric, panels, words in sections:
        body.append(text(MARGIN, y + 12, heading, "ink2", 12))
        y += 26
        for start in range(0, len(panels), 2):
            bottom = y
            for column, (name, rs) in enumerate(panels[start:start + 2]):
                bottom = max(bottom, panel(body, MARGIN + column * (PANEL + GUTTER), y, name, rs, metric, words))
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
        "sep": "; ",
        "scale": "each panel has its own scale",
        "time": "Time per call, shorter is faster",
        "memory": "Bytes allocated per call, shorter is less",
        "multiplier": "top-right: the closest competitor ÷ moego/json",
        "summary": ("moego/json, encoding/json, sonic and goccy/go-json on new-api request types",
                    "Decode and encode with the encoding/json API; {}"),
        "summary_panels": (("W1", "1MiB", "Decode a 1 MiB chat request with an image"),
                           ("W1t", "100Ktok", "Decode a 100K-token conversation"),
                           ("W3m", "100Ktok", "Encode a 100K-token conversation"),
                           ("W4", "aws/64KiB", "Pass a 64 KiB request through RawMessage"),
                           ("W6", "unmarshal/16x1536", "Decode 16 embeddings of 1536 floats"),
                           ("W8", "indent/1MiB", "Indent 1 MiB")),
        "decode": ("Decoding: {}", "Unmarshal into new-api's request and response types; {}"),
        "encode": ("Encoding: {}", "Marshal of new-api's types, and a decode and encode of the same body; {}"),
        "other": ("map[string]any, RawMessage and formatting: {}",
                  "Generic maps, the AWS pass-through, Valid, Compact and Indent; {}"),
        "groups": {
            "W1": "Chat request with an image (GeneralOpenAIRequest)",
            "W1t": "Text chat and coding-agent conversations (GeneralOpenAIRequest)",
            "W2claude": "Claude Messages request with an image",
            "W2responses": "OpenAI Responses request with an image",
            "W3unmarshal": "Decode one stream chunk",
            "W6unmarshal": "Decode an embedding response",
            "W3marshal": "Encode one stream chunk",
            "W3m": "Encode the requests of W1 and W1t",
            "W6marshal": "Encode an embedding response",
            "W5": "Decode and encode a request held in RawMessage members",
            "W4any": "map[string]any: decode and encode",
            "W4aws": "AWS pass-through: map[string]RawMessage, drop two keys, encode",
            "W8valid": "Valid",
            "W8compact": "Compact",
            "W8indent": "Indent with two spaces",
        },
        "chunks": {"content": "content delta", "tool": "tool call delta", "usage": "final usage chunk"},
        "tokens": "{}K tokens",
        "vectors": lambda n: f"{n} vector{'s' if n != '1' else ''} × 1536",
        "w7": ("{} MiB request bodies from a file: time and memory",
               "One process per run, which links one engine; {}"),
        "w7_scenarios": {"decoder": "Decoder on the file", "bytes": "Read the file, Unmarshal",
                         "marshal": "Marshal the request", "marshalp": "Marshal the messages",
                         "valid": "Read the file, Valid"},
        "w7_sections": ("Time, shorter is faster", "Peak of the live heap above the start, shorter is less",
                        "Peak resident set of the process, shorter is less"),
        "host": "{cpu} · {platform} · Go {go} · GOMAXPROCS=1 on one pinned CPU · one process per row and engine, "
                "{rounds} rotated rounds",
        "w7_host": "{cpu} · {platform} · Go {go} · median of {runs} runs per engine and pass",
        "shared": "KVM virtual machine; results apply to this machine only",
        "notes": [
            "The time multiplier is the reciprocal of the median of the per-round ratios moego/json ÷ competitor, "
            "for the closest competitor.",
            "Outputs were checked before timing: values equal encoding/json's; bytes too, except sonic Default and "
            "encoding/json/v2, which do not promise encoding/json's bytes.",
        ],
        "w7_notes": [
            "Time runs on one pinned CPU. Memory is a second pass on two CPUs, so that the heap sampler runs while "
            "the engine works.",
            "The live heap is sampled every 200 µs. The peak resident set is VmHWM, which includes the program "
            "and the runtime.",
        ],
        "source": "Measured with bench/json/codec, drawn by bench/json/codec/plot.py",
        "w7_source": "Measured with bench/json/cmd/w7, drawn by bench/json/codec/plot.py",
        "versions": "Versions: ",
    },
    "zh": {
        "lang": 1,
        "same": "持平",
        "none": "不分配",
        "rival0": "竞品 0 B",
        "sep": "；",
        "scale": "每个面板横轴比例各自不同",
        "time": "每次耗时，越短越快",
        "memory": "每次分配的字节，越短越少",
        "multiplier": "右上角倍数为最接近的竞品 ÷ moego/json",
        "summary": ("moego/json 与 encoding/json、sonic、goccy/go-json 对比：new-api 请求类型",
                    "用 encoding/json 的 API 解析和序列化；{}"),
        "summary_panels": (("W1", "1MiB", "解析 1 MiB 带图聊天请求"),
                           ("W1t", "100Ktok", "解析 100K token 的对话"),
                           ("W3m", "100Ktok", "序列化 100K token 的对话"),
                           ("W4", "aws/64KiB", "用 RawMessage 转发 64 KiB 请求"),
                           ("W6", "unmarshal/16x1536", "解析 16 个 1536 维向量"),
                           ("W8", "indent/1MiB", "缩进 1 MiB")),
        "decode": ("解析：{}", "Unmarshal 到 new-api 的请求和响应类型；{}"),
        "encode": ("序列化：{}", "new-api 类型的 Marshal，以及同一请求体的解析加序列化；{}"),
        "other": ("map[string]any、RawMessage 与格式化：{}", "通用 map、AWS 透传、Valid、Compact 和 Indent；{}"),
        "groups": {
            "W1": "带图聊天请求（GeneralOpenAIRequest）",
            "W1t": "文字聊天与编程 Agent 对话（GeneralOpenAIRequest）",
            "W2claude": "带图的 Claude Messages 请求",
            "W2responses": "带图的 OpenAI Responses 请求",
            "W3unmarshal": "解析一个流式分块",
            "W6unmarshal": "解析 embedding 响应",
            "W3marshal": "序列化一个流式分块",
            "W3m": "序列化 W1 和 W1t 的请求",
            "W6marshal": "序列化 embedding 响应",
            "W5": "解析并序列化成员为 RawMessage 的请求",
            "W4any": "map[string]any：解析并序列化",
            "W4aws": "AWS 透传：map[string]RawMessage，删两个键，再序列化",
            "W8valid": "Valid",
            "W8compact": "Compact",
            "W8indent": "Indent，两个空格",
        },
        "chunks": {"content": "内容增量", "tool": "工具调用增量", "usage": "最后的用量分块"},
        "tokens": "{}K token",
        "vectors": lambda n: f"{n} 个 1536 维向量",
        "w7": ("{} MiB 请求体文件：耗时与内存", "每次运行一个进程，进程只链接一个实现；{}"),
        "w7_scenarios": {"decoder": "用 Decoder 读文件", "bytes": "读入文件后 Unmarshal",
                         "marshal": "Marshal 整个请求", "marshalp": "Marshal 消息列表",
                         "valid": "读入文件后 Valid"},
        "w7_sections": ("耗时，越短越快", "活跃堆相对起点的峰值，越短越少", "进程常驻内存峰值，越短越少"),
        "host": "{cpu} · {platform} · Go {go} · 固定一个 CPU，GOMAXPROCS=1 · 每行每个实现一个进程，轮换顺序测 {rounds} 轮",
        "w7_host": "{cpu} · {platform} · Go {go} · 每个实现每遍 {runs} 次运行的中位数",
        "shared": "KVM 虚拟机，结果只代表这台机器",
        "notes": [
            "耗时倍数取最接近的竞品：逐轮计算 moego/json ÷ 竞品，取中位数后再取倒数。",
            "计时前已核对输出：解析出的值都与 encoding/json 相同；输出字节也相同，sonic Default 和 encoding/json/v2 除外，"
            "它们不保证与 encoding/json 逐字节相同。",
        ],
        "w7_notes": [
            "耗时固定在一个 CPU 上测；内存另测一遍，用两个 CPU，让堆采样在实现运行时同时进行。",
            "活跃堆每 200 µs 采样一次。常驻内存峰值是 VmHWM，包含程序本身和运行时。",
        ],
        "source": "数据由 bench/json/codec 测得，图由 bench/json/codec/plot.py 生成",
        "w7_source": "数据由 bench/json/cmd/w7 测得，图由 bench/json/codec/plot.py 生成",
        "versions": "竞品版本：",
    },
}
SIZES = ("1KiB", "64KiB", "1MiB", "8MiB")
W7_SCENARIOS = ("decoder", "bytes", "marshal", "marshalp", "valid")


def size_name(s, words):
    if s.endswith("Ktok"):
        return words["tokens"].format(s.removesuffix("Ktok"))
    if "x1536" in s:
        return words["vectors"](s.removesuffix("x1536"))
    return s.replace("KiB", " KiB").replace("MiB", " MiB")


def matrix_charts(data, words, lang, go, info, prefix):
    rounds = len(data[("W1", "1MiB", "moego")])
    cpu = re.sub(r"\(R\)|\(TM\)| CPU", "", info.get("cpu", "")).strip()
    platform = f"{info.get('goos', '')}/{info.get('goarch', '')}"
    notes = [words["host"].format(cpu=cpu, platform=platform, go=go.removeprefix("go"), rounds=rounds), words["shared"]]
    footers = [*words["notes"], words["source"], words["versions"] + versions()]
    g = words["groups"]

    def sect(name, bench, workloads, metric=0):
        return (name, metric, [(size_name(w.rpartition("/")[2], words) if bench != "W3" else words["chunks"][w.rpartition("/")[2]],
                                rows(data, bench, w, metric)) for w in workloads], words)

    charts = {}
    sections = []
    for metric, head in ((0, "time"), (1, "memory")):
        sections.append((words[head] + words["sep"] + words["scale"], metric,
                         [(t, rows(data, b, w, metric)) for b, w, t in words["summary_panels"]], words))
    title, sub = words["summary"]
    charts[f"{prefix}-summary.{lang}.svg"] = chart(title, sub.format(words["multiplier"]), notes, sections, footers)

    unit = words["time"]
    sub_tail = words["multiplier"] + words["sep"] + words["scale"]
    groups = {
        "decode": [
            sect(g["W1"], "W1", SIZES),
            sect(g["W1t"], "W1t", ("16KiB", "100Ktok", "256Ktok")),
            sect(g["W2claude"], "W2", ("claude/1KiB", "claude/1MiB")),
            sect(g["W2responses"], "W2", ("responses/1KiB", "responses/1MiB")),
            sect(g["W3unmarshal"], "W3", ("unmarshal/content", "unmarshal/tool", "unmarshal/usage")),
            sect(g["W6unmarshal"], "W6", ("unmarshal/1x1536", "unmarshal/16x1536")),
        ],
        "encode": [
            sect(g["W3marshal"], "W3", ("marshal/content", "marshal/tool", "marshal/usage")),
            sect(g["W3m"], "W3m", (*SIZES, "100Ktok", "256Ktok")),
            sect(g["W6marshal"], "W6", ("marshal/1x1536", "marshal/16x1536")),
            sect(g["W5"], "W5", ("4KiB", "16KiB")),
        ],
        "other": [
            sect(g["W4any"], "W4", ("any/1KiB", "any/64KiB", "any/1MiB")),
            sect(g["W4aws"], "W4", ("aws/1KiB", "aws/64KiB", "aws/1MiB")),
            sect(g["W8valid"], "W8", ("valid/1KiB", "valid/1MiB")),
            sect(g["W8compact"], "W8", ("compact/1KiB", "compact/1MiB")),
            sect(g["W8indent"], "W8", ("indent/1KiB", "indent/1MiB")),
        ],
    }
    for name, sections in groups.items():
        title, sub = words[name]
        charts[f"{prefix}-{name}-time.{lang}.svg"] = chart(title.format(unit), sub.format(sub_tail), notes,
                                                          sections, footers)
    return charts


def w7_chart(t, p, size, words, lang, go, info, prefix):
    cpu = re.sub(r"\(R\)|\(TM\)| CPU", "", info.get("cpu", "")).strip()
    platform = f"{info.get('goos', '')}/{info.get('goarch', '')}"
    runs = sorted({len(v) for v in (*t.values(), *p.values())})
    notes = [words["w7_host"].format(cpu=cpu, platform=platform, go=go.removeprefix("go"),
                                     runs="/".join(map(str, runs))), words["shared"]]
    data = {}
    for (sc, mb, e), v in t.items():
        if mb == size and e in ENGINES and sc in W7_SCENARIOS:
            data[("time", sc, e)] = [(x[0], 0, 0) for x in v]
    for (sc, mb, e), v in p.items():
        if mb == size and e in ENGINES and sc in W7_SCENARIOS:
            data[("heap", sc, e)] = [(0, x[1], 0) for x in v]
            data[("rss", sc, e)] = [(0, x[2], 0) for x in v]
    sections = []
    for (kind, metric), head in zip((("time", 0), ("heap", 1), ("rss", 1)), words["w7_sections"]):
        sections.append((head + words["sep"] + words["scale"], metric,
                         [(words["w7_scenarios"][sc], rows(data, kind, sc, metric, paired=False))
                          for sc in W7_SCENARIOS], words))
    title, sub = words["w7"]
    multiplier = words["multiplier"].replace("closest", "best") if lang == "en" else words["multiplier"].replace("最接近", "最好")
    return {f"{prefix}-w7-{size}MiB.{lang}.svg": chart(
        title.format(size), sub.format(multiplier), notes, sections,
        [*words["w7_notes"], words["w7_source"], words["versions"] + versions()])}


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("bench", help="go test -bench -benchmem output of the codec matrix")
    ap.add_argument("--w7", nargs=2, metavar=("TIME", "PEAK"), help="output of the cmd/w7 time and memory passes")
    ap.add_argument("--w7-size", type=int, default=64, help="body size in MiB for the large-body chart")
    ap.add_argument("--host", help="file with goos, goarch and cpu lines, when the benchmark output has none")
    ap.add_argument("--output", type=Path, default=Path("."), help="directory for the SVG files")
    ap.add_argument("--lang", choices=TEXT, default="en", help="chart text language and file name suffix")
    ap.add_argument("--go", required=True, help="Go version the benchmarks ran with, e.g. go1.27.3")
    args = ap.parse_args(argv)
    data = load(args.bench)
    info = header(args.host or args.bench)
    words = TEXT[args.lang]
    prefix = "-".join(["json", info.get("goos", "bench"), info.get("goarch", "")]).rstrip("-")
    charts = matrix_charts(data, words, args.lang, args.go, info, prefix)
    if args.w7:
        charts.update(w7_chart(load_w7(args.w7[0]), load_w7(args.w7[1]), args.w7_size, words, args.lang, args.go,
                               info, prefix))
    args.output.mkdir(parents=True, exist_ok=True)
    for name, svg in charts.items():
        (args.output / name).write_text(svg)
        print(args.output / name)


if __name__ == "__main__":
    main()
