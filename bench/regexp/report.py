#!/usr/bin/env python3
"""Summarize isolated regex measurements without discarding outliers."""

import argparse
from collections import defaultdict
import csv
import json
import math
from pathlib import Path
import re
import statistics


def stats(values):
    values = list(values)
    return {"samples": len(values), "median": statistics.median(values),
            "min": min(values), "max": max(values), "mean": statistics.mean(values),
            "stdev": statistics.stdev(values) if len(values) > 1 else 0,
            "variance": statistics.variance(values) if len(values) > 1 else 0}


def geomean(values):
    values = list(values)
    return math.exp(sum(map(math.log, values)) / len(values))


def exclusions(path):
    result = defaultdict(set)
    for line in path.read_text().splitlines():
        match = re.search(r"([A-H]/\S+): (\w+) differs from the standard library", line)
        if match:
            result[match[1]].add(match[2])
    if any("Moego" in engines for engines in result.values()):
        raise ValueError("moego correctness mismatch")
    if not re.search(r"^PASS$", path.read_text(), re.MULTILINE):
        raise ValueError("correctness log must contain a passing test run")
    return result


def load(root):
    manifest = json.loads((root / "manifest.json").read_text())
    completion = json.loads((root / "complete.json").read_text())
    if completion["failures"]:
        raise ValueError(f"failed measurements: {completion['failures']}")
    samples = defaultdict(dict)
    if manifest["mode"] == "memory":
        settings = {}
        for path in sorted(root.glob("[0-9]*-*.json")):
            row = json.loads(path.read_text())
            if row["exit"] or row["mode"] != "equal-load" or row["gcs"] < 3 or row["forced_gcs"]:
                raise ValueError(f"invalid memory sample: {path.name}")
            cl = row["class"]
            setting = (row["rate"], row["iterations"], row["operations_per_iteration"])
            if settings.setdefault(cl, setting) != setting:
                raise ValueError(f"unequal work or rate in class {cl}")
            key = ("memory", cl, row["implementation"])
            rep = row["repetition"]
            if rep in samples[key]:
                raise ValueError(f"duplicate sample: {path.name}")
            samples[key][rep] = row
    else:
        settings = {}
        pattern = re.compile(r"^(Benchmark(?:Regexp|Compile)/\S+)\s+\d+\s+([\d.eE+-]+) ns/op"
                             r"(?:\s+[\d.eE+-]+ MB/s)?\s+(\d+) B/op\s+(\d+) allocs/op$")
        for path in sorted(root.glob("[0-9]*-*.txt")):
            if int(path.with_suffix(".exit").read_text()) != 0:
                raise ValueError(f"failed benchmark: {path.name}")
            rep = int(path.name.split("-", 1)[0])
            for line in path.read_text().splitlines():
                match = pattern.match(line)
                if not match:
                    continue
                parts = match[1].split("/")
                kind = "matching" if parts[0] == "BenchmarkRegexp" else "compile"
                case, implementation = "/".join(parts[1:-1]), re.sub(r"-\d+$", "", parts[-1])
                key = (kind, case, implementation)
                if rep in samples[key]:
                    raise ValueError(f"duplicate sample: {path.name}: {match[1]}")
                samples[key][rep] = {"ns": float(match[2]), "bytes": int(match[3]), "allocs": int(match[4])}
    if not samples:
        raise ValueError("no samples")
    wanted = set(range(1, manifest["repetitions"] + 1))
    cases = {(kind, case) for kind, case, _ in samples}
    for kind, case in cases:
        for implementation in manifest["implementations"]:
            if set(samples[kind, case, implementation]) != wanted:
                raise ValueError(f"missing samples: {kind}/{case}/{implementation}")
    if manifest["mode"] == "memory" and {case for _, case in cases} != set(manifest["classes"]):
        raise ValueError("missing memory class")
    return manifest, samples, settings


def summarize(root, excluded):
    manifest, samples, settings = load(root)
    records = []
    by_case = defaultdict(dict)
    for (kind, case, implementation), repeated in sorted(samples.items()):
        invalid = implementation in excluded.get(case, ())
        if kind == "memory":
            invalid = any(implementation in names for key, names in excluded.items() if key.startswith(case + "/"))
        fields = ("heap", "rss", "bytes_per_iteration", "bytes_per_op", "gcs", "heap_base", "rss_base",
                  "heap_delta", "rss_delta", "seconds", "work_seconds", "late_iterations", "max_lag_seconds") if kind == "memory" else ("ns", "bytes", "allocs")
        metrics = {field: stats(row[field] for row in repeated.values()) for field in fields}
        records.append({"kind": kind, "case": case, "implementation": implementation,
                        "excluded": invalid, "metrics": metrics})
        by_case[kind, case][implementation] = (repeated, metrics, invalid)
    ratios = {}
    for (kind, case), engines in sorted(by_case.items()):
        moego, metrics, _ = engines["Moego"]
        peers = {name: data for name, data in engines.items() if name != "Moego" and not data[2]}
        if not peers:
            raise ValueError(f"no correct peers: {case}")
        if kind == "memory":
            ratios[case] = {field: metrics[field]["median"] / min(data[1][field]["median"] for name, data in peers.items()
                                                                                if field == "rss" or name != "GoRE2")
                            for field in ("heap", "rss", "bytes_per_op")}
        else:
            ratios[kind + "/" + case] = {
                "fastest": stats(min(data[0][rep]["ns"] for data in peers.values()) / row["ns"] for rep, row in moego.items()),
                "stdlib": stats(engines["Stdlib"][0][rep]["ns"] / row["ns"] for rep, row in moego.items()),
            }
    classes = {}
    if manifest["mode"] == "speed":
        for cl in manifest["classes"]:
            selected = [data for key, data in ratios.items() if key.startswith("matching/" + cl + "/")]
            if not selected:
                raise ValueError(f"missing matching class {cl}")
            classes[cl] = {name: geomean(data[name]["median"] for data in selected) for name in ("fastest", "stdlib")}
        selected = [data for key, data in ratios.items() if key.startswith("matching/") and key.split("/")[1] in "ABCDEFG"]
        if selected:
            classes["A-G"] = {name: geomean(data[name]["median"] for data in selected) for name in ("fastest", "stdlib")}
        selected = [data for key, data in ratios.items() if key.startswith("compile/")]
        if selected:
            classes["compile"] = {name: geomean(data[name]["median"] for data in selected) for name in ("fastest", "stdlib")}
    return {"manifest": manifest, "settings": settings, "records": records, "ratios": ratios, "classes": classes}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    parser.add_argument("--correctness", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    report = summarize(args.directory, exclusions(args.correctness))
    args.output.with_suffix(".json").write_text(json.dumps(report, indent=2) + "\n")
    with args.output.with_suffix(".csv").open("w", newline="") as stream:
        writer = csv.writer(stream, lineterminator="\n")
        fields = ("samples", "median", "min", "max", "stdev", "variance")
        writer.writerow(("kind", "case", "implementation", "excluded", "metric", *fields))
        for record in report["records"]:
            for metric, values in record["metrics"].items():
                writer.writerow((record["kind"], record["case"], record["implementation"], record["excluded"], metric,
                                 *(values[key] for key in fields)))


if __name__ == "__main__":
    main()
