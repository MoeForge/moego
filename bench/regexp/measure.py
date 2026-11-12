#!/usr/bin/env python3
"""Run isolated regex measurements in rotating implementation order."""

import argparse
import json
import math
import os
from pathlib import Path
import subprocess
import sys


IMPLEMENTATIONS = ["Stdlib", "Grafana", "Coregex", "GoRE2", "Moego"]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=["memory", "speed"])
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--repetitions", type=int, default=5)
    parser.add_argument("--classes", default="ABCDEFGH")
    parser.add_argument("--config", type=Path, default=Path(__file__).with_name("memory.json"))
    parser.add_argument("--benchtime", default="500ms")
    args = parser.parse_args()
    if args.repetitions < 1 or not args.classes or any(c not in "ABCDEFGH" for c in args.classes):
        parser.error("use positive repetitions and classes A through H")
    binary = args.binary.resolve(strict=True)
    args.output.mkdir(parents=True, exist_ok=False)
    config = {}
    if args.mode == "memory":
        config = json.loads(args.config.read_text())
        for cl in args.classes:
            rate, count = config[cl]["rate"], config[cl]["iterations"]
            if not math.isfinite(rate) or rate <= 0 or not isinstance(count, int) or count < 1:
                parser.error(f"invalid rate or iteration count for {cl}")
        (args.output / "config.json").write_text(json.dumps(config, indent=2) + "\n")
    env = dict(os.environ, GOMAXPROCS="1", GOGC="100", GOMEMLIMIT="off",
               GODEBUG=os.environ.get("GODEBUG", ""))
    for key in ("RXPEAK", "RXPEAKLEGACY", "RXPEAKCALIBRATE", "RXPEAKRATE", "RXPEAKN", "RXFOOT"):
        env.pop(key, None)
    manifest = {"mode": args.mode, "repetitions": args.repetitions, "classes": args.classes,
                "implementations": IMPLEMENTATIONS, "benchtime": args.benchtime,
                "environment": {key: env[key] for key in ("GOMAXPROCS", "GOGC", "GOMEMLIMIT", "GODEBUG")}}
    thp = Path("/sys/kernel/mm/transparent_hugepage/enabled")
    if thp.exists():
        manifest["transparent_hugepage"] = thp.read_text().strip()
    (args.output / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    failures = []
    for repetition in range(args.repetitions):
        offset = repetition % len(IMPLEMENTATIONS)
        order = IMPLEMENTATIONS[offset:] + IMPLEMENTATIONS[:offset]
        groups = args.classes if args.mode == "memory" else ("matching", "compile")
        for group in groups:
            for implementation in order:
                name = f"{repetition + 1:02d}-{group}-{implementation}"
                command = [str(binary), "-test.count=1", "-test.timeout=90m"]
                child_env = env.copy()
                if args.mode == "memory":
                    child_env.update(RXPEAK=f"{implementation}/{group}",
                                     RXPEAKRATE=str(config[group]["rate"]),
                                     RXPEAKN=str(config[group]["iterations"]))
                    command += ["-test.run=^TestPeak$", "-test.v"]
                else:
                    pattern = (f"^BenchmarkRegexp$/^[{args.classes}]$/.*/^{implementation}$"
                               if group == "matching" else f"^BenchmarkCompile$/.*/^{implementation}$")
                    command += ["-test.run=^$", f"-test.bench={pattern}", "-test.benchmem",
                                f"-test.benchtime={args.benchtime}", "-test.cpu=1"]
                print(name, flush=True)
                log = args.output / f"{name}.txt"
                with log.open("w") as stream:
                    result = subprocess.run(command, env=child_env, stdout=stream, stderr=subprocess.STDOUT)
                (args.output / f"{name}.exit").write_text(str(result.returncode) + "\n")
                if result.returncode:
                    failures.append(name)
                if args.mode == "memory":
                    records = [line[11:] for line in log.read_text().splitlines() if line.startswith("PEAK_EQUAL ")]
                    if len(records) != 1:
                        failures.append(name + ": missing or duplicate record")
                        continue
                    data = json.loads(records[0])
                    data.update(repetition=repetition + 1, exit=result.returncode)
                    (args.output / f"{name}.json").write_text(json.dumps(data, indent=2) + "\n")
    (args.output / "complete.json").write_text(json.dumps({"failures": failures}, indent=2) + "\n")
    return bool(failures)


if __name__ == "__main__":
    sys.exit(main())
