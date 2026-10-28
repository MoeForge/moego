import unittest

from rounds import benchmark_pattern, parse_case_output, rotated


class RoundIntegrityTests(unittest.TestCase):
    case = "BenchmarkCodec/Encode/1MiB"
    codecs = ["Stdlib", "Segmentio", "Emmansun", "Cristalhq", "CloudwegoFallback", "Moego"]

    def output(self, names):
        lines = ["goos: darwin", "goarch: arm64", "pkg: github.com/MoeForge/moego/bench", "cpu: fixture"]
        lines += [f"{self.case}/{name} 100 100.0 ns/op 0 B/op 0 allocs/op" for name in names]
        return "\n".join(lines) + "\nPASS\n"

    def test_missing_codec_cannot_pass(self):
        with self.assertRaisesRegex(ValueError, "missing=.*Emmansun"):
            parse_case_output(self.output([name for name in self.codecs if name != "Emmansun"]), self.case, self.codecs)

    def test_missing_case_cannot_pass(self):
        with self.assertRaisesRegex(ValueError, "unexpected benchmark"):
            parse_case_output(self.output(self.codecs), "BenchmarkCodec/Decode/1MiB", self.codecs)

    def test_duplicate_or_unrotated_results_cannot_pass(self):
        with self.assertRaisesRegex(ValueError, "duplicate=.*Moego"):
            parse_case_output(self.output(self.codecs + ["Moego"]), self.case, self.codecs)
        with self.assertRaisesRegex(ValueError, "expected order"):
            parse_case_output(self.output(self.codecs), self.case, rotated(self.codecs, 1))

    def test_cpu_or_failure_cannot_pass(self):
        with self.assertRaisesRegex(ValueError, "cpu=1"):
            parse_case_output(self.output([name + "-8" for name in self.codecs]), self.case, self.codecs)
        with self.assertRaisesRegex(ValueError, "did not report PASS"):
            parse_case_output(self.output(self.codecs).replace("PASS", "FAIL"), self.case, self.codecs)
        with self.assertRaisesRegex(ValueError, "reported a failure"):
            parse_case_output(self.output(self.codecs) + self.case + "/Moego\t--- FAIL: bad output\nPASS\n", self.case, self.codecs)

    def test_complete_rotated_case_passes(self):
        order = rotated(self.codecs, 4)
        records, metadata = parse_case_output(self.output(order), self.case, order)
        self.assertEqual([record["codec"] for record in records], order)
        self.assertEqual(metadata["goarch"], "arm64")
        self.assertEqual(rotated(self.codecs, 6), self.codecs)
        self.assertEqual(benchmark_pattern(self.case, ["Stdlib", "Moego"]), "^BenchmarkCodec$/^Encode$/^1MiB$/^(Stdlib|Moego)$")


if __name__ == "__main__":
    unittest.main()
