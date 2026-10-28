#!/usr/bin/env python3
"""Check repeated, same-host Go benchmark measurements against competitors."""

import argparse
import collections
import math
import re
import statistics
import sys
from pathlib import Path


LINE = re.compile(r"^(Benchmark\S+)\s+\d+\s+([\d.eE+-]+)\s+ns/op(?:\s|$)")
SIZES = ("1KiB", "32KiB", "256KiB", "1MiB", "4MiB")
SUITES = {
    "kernel": ("BenchmarkCodec", ("Encode", "Decode")),
    "newapi": ("BenchmarkNewAPI", ("EncodeToString", "DecodeString", "DataURLConcat", "DataURLDecode")),
    "dataurl": ("BenchmarkDataURL", ("OneAllocation", "AppendReuse")),
    "validation": ("BenchmarkValidation", ("Allocate", "Reuse")),
}


def read_results(paths):
    results = collections.defaultdict(lambda: collections.defaultdict(list))
    metadata = collections.defaultdict(set)
    for path in paths:
        for line in Path(path).read_text().splitlines():
            if line.strip().startswith("FAIL") or "--- FAIL" in line:
                raise ValueError(f"benchmark reported a failure in {path}: {line.strip()}")
            for field in ("goos", "goarch", "cpu"):
                if line.startswith(field + ": "):
                    metadata[field].add(line.split(": ", 1)[1])
            match = LINE.match(line)
            if not match:
                continue
            name, value = match.groups()
            parts = name.split("/")
            if len(parts) != 4:
                continue
            ns = float(value)
            if not math.isfinite(ns) or ns <= 0:
                raise ValueError(f"invalid timing: {line}")
            implementation = re.sub(r"-\d+$", "", parts[-1])
            results["/".join(parts[:-1])][implementation].append(ns)
    for field, values in metadata.items():
        if len(values) > 1:
            raise ValueError(f"mixed {field}: {sorted(values)}; compare one host per report")
    return results, metadata


def assess(samples, min_samples, margin):
    required = ("Moego", "Emmansun", "Segmentio")
    missing = [name for name in required if name not in samples]
    if missing:
        return "INCOMPLETE", "missing " + ", ".join(missing)
    short = [f"{name}={len(values)}" for name, values in samples.items() if len(values) < min_samples]
    if short:
        return "INCOMPLETE", f"need {min_samples} samples: " + ", ".join(short)
    ours = samples["Moego"]
    peers = {name: values for name, values in samples.items() if name != "Moego"}
    best = min(peers, key=lambda name: statistics.median(peers[name]))
    theirs = peers[best]
    ratio = statistics.median(theirs) / statistics.median(ours)
    # Margin is a reduction in time/op, e.g. 0.10 means 10% less time.
    saved = 1 - statistics.median(ours) / statistics.median(theirs)
    # This is an observed range check, not a statistical confidence interval.
    separated = max(ours) < min(min(values) for values in peers.values())
    status = "PASS" if saved + 1e-12 >= margin and separated else "FAIL"
    detail = (
        f"Moego {statistics.median(ours):.1f} ns/op, best {best} "
        f"{statistics.median(theirs):.1f} ns/op, {ratio:.3f}x throughput, "
        f"{saved:+.1%} less time, ranges {'separate' if separated else 'overlap/reverse'}"
    )
    return status, detail


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("files", nargs="+", help="go test -bench output from the same host and Go version")
    parser.add_argument("--suite", choices=(*SUITES, "all"), default="kernel")
    parser.add_argument("--min-samples", type=int, default=5)
    parser.add_argument("--margin", type=float, default=0.10, help="minimum reduction in time/op (default: 0.10)")
    args = parser.parse_args(argv)
    if args.min_samples < 2 or not 0 <= args.margin < 1:
        parser.error("min-samples must be >=2 and margin must be in [0,1)")
    try:
        results, metadata = read_results(args.files)
    except (ValueError, OSError) as exc:
        parser.error(str(exc))
    for field, values in sorted(metadata.items()):
        print(f"{field}: {next(iter(values))}")
    print(f"Gate: >= {args.margin:.0%} less median time, all observed ranges separated, >= {args.min_samples} samples")
    failures = 0
    selected = SUITES.values() if args.suite == "all" else (SUITES[args.suite],)
    for suite, operations in selected:
        for operation in operations:
            for size in SIZES:
                name = f"{suite}/{operation}/{size}"
                status, detail = assess(results.get(name, {}), args.min_samples, args.margin)
                print(f"{status:10} {name}: {detail}")
                failures += status != "PASS"
    print(f"{'FAIL' if failures else 'PASS'}: {failures} cases failed or incomplete; this report applies to this host only.")
    return int(bool(failures))


if __name__ == "__main__":
    sys.exit(main())
