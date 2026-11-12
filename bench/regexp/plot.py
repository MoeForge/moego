#!/usr/bin/env python3
"""Render standalone SVG charts from report.py JSON output."""

import argparse
import json
from pathlib import Path
import re

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt


LABELS = {"Moego": "moego", "Stdlib": "stdlib", "Grafana": "Grafana", "Coregex": "coregex", "GoRE2": "go-re2"}
COLORS = {name: "#2a78d6" if name == "Moego" else "#898781" for name in LABELS}
ORDER = list(LABELS)
CLASSES = {"A": "Short request fields", "B": "Text replacement", "C": "Redaction and keywords",
           "D": "Structured extraction", "E": "Streaming frames", "F": "Source search",
           "G": "Text and binary payloads", "H": "General patterns"}
CLASSES_ZH = {"A": "短请求字段", "B": "文本替换", "C": "脱敏与关键词",
              "D": "结构化文本提取", "E": "流式分片", "F": "源码搜索",
              "G": "文本与二进制载荷", "H": "通用模式"}


def wording(en, zh, language):
    return zh if language == "zh" else en


def class_label(group, language):
    return f"{group} · {(CLASSES_ZH if language == 'zh' else CLASSES)[group]}"


def frame(ax):
    ax.grid(axis="x", color="#c3c2b7", alpha=0.45)
    ax.set_axisbelow(True)
    for side in ("top", "right", "left"):
        ax.spines[side].set_visible(False)


def environment(fig, caption):
    if caption:
        fig.text(0.04, 0.012, caption, fontsize=9, color="#6f6d68")


def memory_chart(data, metric, language):
    groups = sorted({r["case"] for r in data["records"] if r["kind"] == "memory"})
    fig, axes = plt.subplots((len(groups) + 1) // 2, 2, figsize=(12, 2.7 * ((len(groups) + 1) // 2)), squeeze=False)
    titles = {"heap": wording("Peak Go heap in use", "Go 堆使用峰值", language),
              "rss": wording("Peak process RSS", "进程 RSS 峰值", language),
              "bytes_per_op": wording("Allocated bytes per API call", "每次 API 调用的分配字节数", language)}
    for ax, group in zip(axes.flat, groups):
        records = {r["implementation"]: r for r in data["records"] if r["kind"] == "memory" and r["case"] == group}
        names = [name for name in ORDER if name in records and not records[name]["excluded"] and not (metric != "rss" and name == "GoRE2")]
        if not names:
            raise ValueError(f"no comparable samples for {group}")
        high = max(records[name]["metrics"][metric]["max"] for name in names)
        unit, divisor = ("MiB", 1 << 20) if high >= 1 << 20 else (("KiB", 1 << 10) if high >= 1 << 10 else ("B", 1))
        low = min(records[name]["metrics"][metric]["min"] for name in names)
        logarithmic = low > 0 and high / low > 8
        if logarithmic:
            ax.set_xscale("log")
            ax.set_xlim(low / divisor / 1.5, high / divisor * 1.5)
        else:
            ax.set_xlim(0, high / divisor * 1.16)
        for y, name in enumerate(names):
            sample = records[name]["metrics"][metric]
            value, minimum, maximum = (sample[key] / divisor for key in ("median", "min", "max"))
            ax.errorbar(value, y, xerr=[[value - minimum], [maximum - value]],
                        fmt="o", color=COLORS[name], markersize=6, capsize=3, linewidth=1.5)
            ax.annotate(f"{value:.3f}", (value, y), xytext=(6, 7), textcoords="offset points", fontsize=8)
        ax.set_yticks(range(len(names)), [LABELS[name] for name in names])
        ax.invert_yaxis()
        ax.set_ylim(len(names) - 0.5, -0.65)
        ax.set_title(class_label(group, language), loc="left", fontsize=11, fontweight="bold")
        ax.set_xlabel(unit + (wording(" / API call", " / API 调用", language) if metric == "bytes_per_op" else "")
                      + (wording(" · log scale", " · 对数刻度", language) if logarithmic else ""))
        frame(ax)
    for ax in list(axes.flat)[len(groups):]:
        ax.set_visible(False)
    count = data["manifest"]["repetitions"]
    note = wording(f"Median of {count} rotated fresh-process samples; whiskers: minimum–maximum. Equal work and rate within each class.",
                   f"{count} 次轮换、独立进程测量的中位数；横线：最小值至最大值。每类的工作量和速率相同。", language)
    if metric != "rss":
        note += wording("\ngo-re2 is compared by RSS; its Go heap omits part of the matching state.",
                        "\ngo-re2 只比较 RSS；它的 Go 堆没有包含全部匹配状态。", language)
    fig.suptitle(titles[metric] + wording(" · lower is better", " · 越低越好", language), x=0.04, ha="left", fontsize=16, fontweight="bold")
    fig.text(0.04, 0.04, note, fontsize=9, color="#52514e")
    fig.tight_layout(rect=(0.02, 0.1, 0.99, 0.94), h_pad=2.5)
    return fig


def speed_chart(data, language):
    groups = [cl for cl in CLASSES if cl in data["classes"]]
    if "compile" in data["classes"]:
        groups.append("compile")
    fig, ax = plt.subplots(figsize=(11, 0.65 * len(groups) + 2))
    for key, offset, color, label in [("fastest", -0.13, "#2a78d6", wording("Fastest correct competitor", "最快且结果正确的对照", language)),
                                      ("stdlib", 0.13, "#898781", wording("Standard library", "标准库", language))]:
        values = [data["classes"][group][key] for group in groups]
        positions = [i + offset for i in range(len(groups))]
        ax.scatter(values, positions, color=color, s=35, label=label)
        for value, y in zip(values, positions):
            ax.annotate(f"{value:.2f}×", (value, y), xytext=(7, -3), textcoords="offset points", fontsize=9)
    ax.set_xscale("log", base=2)
    high = max(data["classes"][group][key] for group in groups for key in ("fastest", "stdlib"))
    low = min(data["classes"][group][key] for group in groups for key in ("fastest", "stdlib"))
    ax.set_xlim(min(0.8, low / 1.3), high * 1.8)
    ax.axvline(1, color="#c3c2b7", linewidth=1)
    ax.set_yticks(range(len(groups)), [class_label(group, language) if group != "compile" else wording("Cold compilation", "冷编译", language) for group in groups])
    ax.invert_yaxis()
    ax.set_xlabel(wording("Competitor time / moego time · geometric mean of paired case ratios · log scale",
                         "对照耗时 / moego 耗时 · 配对用例比值的几何平均 · 对数刻度", language))
    ax.set_title(wording("Regular expression speed · higher is better", "正则表达式速度 · 越高越好", language), loc="left", fontsize=15, fontweight="bold", pad=20)
    ax.legend(loc="lower right", frameon=False)
    frame(ax)
    fig.tight_layout(rect=(0, 0.04, 1, 1))
    return fig


def cases_chart(data, language):
    cases = sorted(key for key in data["ratios"] if key.startswith("matching/"))
    half = (len(cases) + 1) // 2
    fig, axes = plt.subplots(1, 2, figsize=(14, half * 0.33 + 1.8))
    for ax, selected in zip(axes, (cases[:half], cases[half:])):
        values = [data["ratios"][case]["fastest"] for case in selected]
        ax.set_xscale("log", base=2)
        ax.set_xlim(min(0.8, min(row["min"] for row in values) / 1.2), max(row["max"] for row in values) * 1.8)
        for y, row in enumerate(values):
            value = row["median"]
            ax.errorbar(value, y, xerr=[[value - row["min"]], [row["max"] - value]],
                        fmt="o", color=COLORS["Moego"], markersize=5, capsize=2, linewidth=1)
            ax.annotate(f"{value:.2f}×", (value, y), xytext=(6, 5), textcoords="offset points", fontsize=8)
        ax.set_yticks(range(len(selected)), [key.removeprefix("matching/") for key in selected], fontsize=9)
        ax.invert_yaxis()
        ax.axvline(1, color="#c3c2b7", linewidth=1)
        ax.set_xlabel(wording("Fastest correct competitor / moego · log scale", "最快正确对照 / moego · 对数刻度", language), fontsize=9)
        frame(ax)
    fig.suptitle(wording("Speed by case · higher is better", "各用例速度 · 越高越好", language), x=0.03, ha="left", fontsize=16, fontweight="bold")
    count = data["manifest"]["repetitions"]
    fig.text(0.04, 0.045, wording(f"Median of {count} paired rotations; whiskers show minimum–maximum ratios.",
                                f"{count} 次配对轮换的中位数；横线为比值的最小值至最大值。", language), fontsize=9, color="#52514e")
    fig.tight_layout(rect=(0, 0.08, 1, 0.95), w_pad=2)
    return fig


def export_svg(fig, path, title):
    fig.savefig(path, format="svg", metadata={"Date": None, "Creator": "moego regexp measurements", "Title": title})
    svg = path.read_text()
    # Keep text selectable and use the same palette and system fonts as the
    # other documentation figures, including the viewer's dark preference.
    style = '''<style type="text/css">
text { font-family: system-ui, -apple-system, "Segoe UI", "Noto Sans CJK SC", "PingFang SC", sans-serif !important; }
@media (prefers-color-scheme: dark) {
  [style*="fill: #fcfcfb"] { fill: #1a1a19 !important; }
  [style*="fill: #0b0b0b"] { fill: #ffffff !important; }
  [style*="fill: #52514e"] { fill: #c3c2b7 !important; }
  [style*="fill: #6f6d68"] { fill: #9a9891 !important; }
  [style*="stroke: #c3c2b7"] { stroke: #383835 !important; }
}
</style>'''
    svg = re.sub(r"(<svg\b[^>]*>)", r'\1\n' + style, svg, count=1)
    path.write_text("\n".join(line.rstrip() for line in svg.splitlines()) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("summary", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--metric", choices=["heap", "rss", "bytes_per_op", "speed", "cases"], required=True)
    parser.add_argument("--language", choices=["en", "zh"], default="en")
    parser.add_argument("--caption", default="", help="machine and toolchain description")
    args = parser.parse_args()
    data = json.loads(args.summary.read_text())
    plt.rcParams.update({"font.family": ["Noto Sans CJK JP", "DejaVu Sans"],
                         "svg.fonttype": "none", "svg.hashsalt": "moego-regexp",
                         "figure.facecolor": "#fcfcfb", "axes.facecolor": "#fcfcfb",
                         "text.color": "#0b0b0b", "axes.labelcolor": "#52514e",
                         "xtick.color": "#52514e", "ytick.color": "#52514e", "axes.edgecolor": "#c3c2b7"})
    if args.metric == "speed":
        fig = speed_chart(data, args.language)
    elif args.metric == "cases":
        fig = cases_chart(data, args.language)
    else:
        fig = memory_chart(data, args.metric, args.language)
    environment(fig, args.caption)
    export_svg(fig, args.output, f"regexp · {args.metric} · {args.language}")
    plt.close(fig)


if __name__ == "__main__":
    main()
