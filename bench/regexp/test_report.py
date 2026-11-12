import json
from pathlib import Path
import tempfile
import unittest

import report


ENGINES = ["Stdlib", "Grafana", "Coregex", "GoRE2", "Moego"]


class ReportTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        (self.root / "complete.json").write_text('{"failures": []}')

    def manifest(self, mode):
        (self.root / "manifest.json").write_text(json.dumps({
            "mode": mode, "classes": "A", "repetitions": 2, "implementations": ENGINES,
        }))

    def test_paired_ratios_and_incomplete_input(self):
        self.manifest("speed")
        times = {"Moego": [2, 4], "Stdlib": [8, 8], "Grafana": [10, 10],
                 "Coregex": [4, 12], "GoRE2": [6, 6]}
        for engine, values in times.items():
            for repetition, ns in enumerate(values, 1):
                path = self.root / f"{repetition:02d}-matching-{engine}.txt"
                path.write_text(f"BenchmarkRegexp/A/example/{engine} 100 {ns} ns/op 10 MB/s 16 B/op 1 allocs/op\n")
                path.with_suffix(".exit").write_text("0\n")
        result = report.summarize(self.root, {})
        ratio = result["ratios"]["matching/A/example"]
        self.assertEqual(ratio["fastest"]["median"], 1.75)
        self.assertEqual(ratio["stdlib"]["median"], 3)
        self.assertEqual(ratio["fastest"]["variance"], 0.125)
        self.assertAlmostEqual(result["classes"]["A"]["fastest"], 1.75)
        (self.root / "02-matching-Grafana.txt").unlink()
        with self.assertRaisesRegex(ValueError, "missing samples"):
            report.summarize(self.root, {})

    def test_memory_rss_only_peer_and_equal_work(self):
        self.manifest("memory")
        for engine in ENGINES:
            for repetition in (1, 2):
                row = {key: 0 for key in ("heap_base", "rss_base", "heap_delta", "rss_delta",
                                          "seconds", "work_seconds", "late_iterations", "max_lag_seconds")}
                row.update(exit=0, mode="equal-load", gcs=3, forced_gcs=0, rate=10,
                           iterations=100, operations_per_iteration=2, **{"class": "A"},
                           implementation=engine, repetition=repetition,
                           heap=100 + 20 * (repetition - 1), rss=100 + 20 * (repetition - 1),
                           bytes_per_op=10 if engine == "Moego" else 20,
                           bytes_per_iteration=20 if engine == "Moego" else 40)
                if engine == "GoRE2":
                    row.update(heap=1, rss=80 + 20 * (repetition - 1), bytes_per_op=1)
                (self.root / f"{repetition:02d}-A-{engine}.json").write_text(json.dumps(row))
        result = report.summarize(self.root, {})
        self.assertEqual(result["ratios"]["A"]["heap"], 1)
        self.assertAlmostEqual(result["ratios"]["A"]["rss"], 11 / 9)
        self.assertEqual(result["ratios"]["A"]["bytes_per_op"], 0.5)
        path = self.root / "02-A-Moego.json"
        row = json.loads(path.read_text())
        row["iterations"] += 1
        path.write_text(json.dumps(row))
        with self.assertRaisesRegex(ValueError, "unequal work or rate"):
            report.summarize(self.root, {})
        row["iterations"] -= 1
        row["gcs"] = 2
        path.write_text(json.dumps(row))
        with self.assertRaisesRegex(ValueError, "invalid memory sample"):
            report.summarize(self.root, {})


if __name__ == "__main__":
    unittest.main()
