#!/usr/bin/env python3
"""Run rotated benchmark rounds from one existing Go test binary."""

import argparse
import collections
import csv
import datetime
import fcntl
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import re
import shutil
import statistics
import subprocess
import sys
import tempfile


DEFAULT_CASES = (
    "BenchmarkCodec/Encode/32KiB",
    "BenchmarkCodec/Decode/32KiB",
    "BenchmarkCodec/Encode/1MiB",
    "BenchmarkCodec/Decode/1MiB",
)
RESULT = re.compile(r"^(Benchmark\S+)\s+([0-9]+)\s+([\d.eE+-]+)\s+ns/op(?:\s|$)")
CASE_NAME = re.compile(r"^Benchmark[A-Za-z0-9_]+/[A-Za-z0-9_]+/[A-Za-z0-9_]+$")
METADATA = ("goos", "goarch", "pkg", "cpu")


def utc_now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def file_digest(path):
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def rotated(items, offset):
    offset %= len(items)
    return list(items[offset:]) + list(items[:offset])


def benchmark_pattern(case, codecs):
    parts = ["^" + re.escape(part) + "$" for part in case.split("/")]
    parts.append("^(" + "|".join(re.escape(codec) for codec in codecs) + ")$")
    return "/".join(parts)


def parse_case_output(output, case, expected_order):
    """Reject incomplete, duplicated, reordered, or mismatched measurements."""
    records = []
    metadata = {}
    if not any(line.strip() == "PASS" for line in output.splitlines()):
        raise ValueError(f"{case}: test binary did not report PASS")
    for line in output.splitlines():
        if line.strip().startswith("FAIL") or "--- FAIL" in line:
            raise ValueError(f"{case}: test binary reported a failure")
        for field in METADATA:
            if line.startswith(field + ": "):
                value = line.split(": ", 1)[1]
                if field in metadata and metadata[field] != value:
                    raise ValueError(f"{case}: conflicting {field} metadata")
                metadata[field] = value
        match = RESULT.match(line)
        if match is None:
            continue
        name, iterations, raw_ns = match.groups()
        parts = name.split("/")
        if len(parts) != 4 or "/".join(parts[:3]) != case:
            raise ValueError(f"{case}: unexpected benchmark result {name}")
        suffix = re.search(r"-([0-9]+)$", parts[3])
        if suffix is not None and int(suffix.group(1)) != 1:
            raise ValueError(f"{case}: expected cpu=1, found {name}")
        codec = re.sub(r"-[0-9]+$", "", parts[3])
        ns = float(raw_ns)
        if int(iterations) <= 0 or not math.isfinite(ns) or ns <= 0:
            raise ValueError(f"{case}: invalid timing {line}")
        records.append({"case": case, "codec": codec, "iterations": int(iterations), "ns_per_op": ns})
    actual_order = [record["codec"] for record in records]
    if actual_order != list(expected_order):
        missing = sorted(set(expected_order) - set(actual_order))
        extra = sorted(set(actual_order) - set(expected_order))
        duplicates = sorted(codec for codec, count in collections.Counter(actual_order).items() if count > 1)
        raise ValueError(
            f"{case}: expected order {list(expected_order)}, got {actual_order}; "
            f"missing={missing}, unexpected={extra}, duplicate={duplicates}"
        )
    if not metadata.get("goos") or not metadata.get("goarch"):
        raise ValueError(f"{case}: missing goos/goarch metadata")
    return records, metadata


def build_info(binary):
    go = shutil.which("go")
    if go is None:
        return {"available": False, "reason": "go command not found"}
    result = subprocess.run([go, "version", "-m", str(binary)], text=True, capture_output=True, check=False)
    return {"available": result.returncode == 0, "output": result.stdout, "stderr": result.stderr}


def write_json(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")


def write_reports(output_dir, records):
    with (output_dir / "samples.csv").open("w", newline="") as target:
        writer = csv.DictWriter(target, fieldnames=("round", "rotation", "case", "codec", "iterations", "ns_per_op"))
        writer.writeheader()
        writer.writerows(records)
    grouped = collections.defaultdict(list)
    paired = collections.defaultdict(dict)
    for record in records:
        grouped[record["case"], record["codec"]].append(record["ns_per_op"])
        paired[record["round"], record["case"]][record["codec"]] = record["ns_per_op"]
    with (output_dir / "summary.csv").open("w", newline="") as target:
        writer = csv.writer(target)
        writer.writerow(("case", "codec", "samples", "median_ns", "minimum_ns", "maximum_ns"))
        for (case, codec), samples in sorted(grouped.items()):
            writer.writerow((case, codec, len(samples), statistics.median(samples), min(samples), max(samples)))
    with (output_dir / "paired.csv").open("w", newline="") as target:
        writer = csv.writer(target)
        writer.writerow(("round", "case", "competitor", "competitor_ns", "moego_ns", "competitor_over_moego", "moego_time_saved_fraction"))
        for (round_number, case), samples in sorted(paired.items()):
            ours = samples["Moego"]
            for codec, theirs in sorted(samples.items()):
                if codec != "Moego":
                    writer.writerow((round_number, case, codec, theirs, ours, theirs / ours, 1 - ours / theirs))


def run_rounds(args):
    binary = args.binary.resolve(strict=True)
    if not binary.is_file() or not os.access(binary, os.X_OK):
        raise ValueError(f"not an executable test binary: {binary}")
    script_dir = Path(__file__).resolve().parent
    lock_name = hashlib.sha256(str(script_dir).encode()).hexdigest()[:16]
    lock_path = Path(tempfile.gettempdir()) / f"moego-benchmark-rounds-{lock_name}.lock"
    with lock_path.open("a+") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            raise ValueError("another rounds.py process is already measuring this project") from exc
        output_dir = args.output.resolve()
        output_dir.mkdir(parents=True, exist_ok=False)
        initial_digest = file_digest(binary)
        environment = {
            "started_at": utc_now(),
            "host": platform.uname()._asdict(),
            "python": sys.version,
            "binary": str(binary),
            "binary_sha256": initial_digest,
            "binary_bytes": binary.stat().st_size,
            "build_info": build_info(binary),
            "runner_sha256": file_digest(Path(__file__).resolve()),
            "settings": {
                "rounds": args.rounds,
                "benchtime": args.benchtime,
                "count": 1,
                "cpu": 1,
                "cases": args.cases,
                "codec_baseline_order": args.codecs,
            },
            "inherited_environment": {key: os.environ[key] for key in ("GODEBUG", "GOGC", "GOMEMLIMIT", "GOMAXPROCS", "GOAMD64", "GOARM64") if key in os.environ},
            "effective_environment": {"GOMAXPROCS": "1", "MOEGO_BENCH_ROTATE": "set separately per round"},
        }
        write_json(output_dir / "environment.json", environment)
        manifest = {"status": "running", "rounds": []}
        manifest_path = output_dir / "manifest.json"
        write_json(manifest_path, manifest)
        all_records = []
        canonical_metadata = None
        try:
            for index in range(args.rounds):
                if file_digest(binary) != initial_digest:
                    raise ValueError("test binary changed between rounds")
                rotation = index % len(args.codecs)
                expected_order = rotated(args.codecs, rotation)
                round_info = {
                    "round": index + 1,
                    "rotation": rotation,
                    "codec_order": expected_order,
                    "started_at": utc_now(),
                    "status": "running",
                    "commands": [],
                }
                manifest["rounds"].append(round_info)
                write_json(manifest_path, manifest)
                print(f"round {index + 1}/{args.rounds}: {', '.join(expected_order)}", flush=True)
                env = os.environ.copy()
                env["MOEGO_BENCH_ROTATE"] = str(rotation)
                env["GOMAXPROCS"] = "1"
                with (output_dir / f"round-{index + 1:03d}.txt").open("w") as raw_output:
                    for case in args.cases:
                        command = [
                            str(binary), "-test.run=^$",
                            "-test.bench=" + benchmark_pattern(case, args.codecs),
                            "-test.benchmem", "-test.benchtime=" + args.benchtime,
                            "-test.count=1", "-test.cpu=1", "-test.timeout=" + args.timeout,
                        ]
                        entry = {"case": case, "argv": command, "started_at": utc_now()}
                        round_info["commands"].append(entry)
                        write_json(manifest_path, manifest)
                        completed = subprocess.run(command, cwd=script_dir, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False)
                        raw_output.write(completed.stdout)
                        if not completed.stdout.endswith("\n"):
                            raw_output.write("\n")
                        raw_output.flush()
                        entry.update({"finished_at": utc_now(), "returncode": completed.returncode})
                        if completed.returncode:
                            raise ValueError(f"round {index + 1}, {case}: binary exited {completed.returncode}; see raw output")
                        records, metadata = parse_case_output(completed.stdout, case, expected_order)
                        if canonical_metadata is None:
                            canonical_metadata = metadata
                        elif metadata != canonical_metadata:
                            raise ValueError(f"benchmark environment changed: {canonical_metadata} -> {metadata}")
                        for record in records:
                            record.update({"round": index + 1, "rotation": rotation})
                        all_records.extend(records)
                if file_digest(binary) != initial_digest:
                    raise ValueError("test binary changed during the round")
                round_info.update({"status": "complete", "finished_at": utc_now()})
                write_json(manifest_path, manifest)
            expected_count = args.rounds * len(args.cases) * len(args.codecs)
            if len(all_records) != expected_count:
                raise ValueError(f"incomplete measurement set: {len(all_records)}/{expected_count}")
            with (output_dir / "combined.txt").open("w") as combined:
                for index in range(args.rounds):
                    combined.write((output_dir / f"round-{index + 1:03d}.txt").read_text())
            write_reports(output_dir, all_records)
            manifest.update({"status": "complete", "finished_at": utc_now(), "samples": len(all_records), "benchmark_metadata": canonical_metadata})
            write_json(manifest_path, manifest)
            print(f"complete: {len(all_records)} samples in {output_dir}; completeness is not a speed gate")
        except BaseException as exc:
            if manifest["rounds"] and manifest["rounds"][-1]["status"] == "running":
                manifest["rounds"][-1]["status"] = "failed"
            manifest.update({"status": "failed", "finished_at": utc_now(), "error": str(exc)})
            write_json(manifest_path, manifest)
            raise


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True, help="already compiled Go test binary; this script never builds it")
    parser.add_argument("--output", type=Path, required=True, help="new directory for raw rounds, environment, and reports")
    parser.add_argument("--case", dest="cases", action="append", help="exact Suite/Operation/Size; repeat to select several cases")
    parser.add_argument("--rounds", type=int, default=5)
    parser.add_argument("--benchtime", default="300ms")
    parser.add_argument("--timeout", default="10m", help="Go test timeout for each case subprocess")
    fallback = "Cloudwego" if platform.machine().lower() in ("amd64", "x86_64") else "CloudwegoFallback"
    parser.add_argument("--codecs", default=f"Stdlib,Segmentio,Emmansun,Cristalhq,{fallback},Moego", help="complete baseline order of codecs(), comma separated")
    args = parser.parse_args(argv)
    args.cases = args.cases or list(DEFAULT_CASES)
    args.codecs = args.codecs.split(",")
    if args.rounds < 1:
        parser.error("rounds must be at least 1")
    if len(set(args.cases)) != len(args.cases) or any(not CASE_NAME.fullmatch(case) for case in args.cases):
        parser.error("cases must be unique exact Suite/Operation/Size names")
    if len(set(args.codecs)) != len(args.codecs) or len(args.codecs) < 2 or "Moego" not in args.codecs or any(not re.fullmatch(r"[A-Za-z0-9_]+", codec) for codec in args.codecs):
        parser.error("codecs must list distinct baseline codec names including Moego and its competitors")
    for field in ("benchtime", "timeout"):
        if not re.fullmatch(r"(?:[1-9][0-9]*(?:\.[0-9]+)?|0\.[0-9]*[1-9][0-9]*)(?:ms|s|m|h)", getattr(args, field)):
            parser.error(field + " must be a positive duration such as 300ms or 1s")
    try:
        run_rounds(args)
    except (OSError, ValueError) as exc:
        print(f"measurement failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
