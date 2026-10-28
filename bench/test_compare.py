import unittest

from compare import assess


class ComparisonTests(unittest.TestCase):
    def test_uses_fastest_competitor(self):
        status, detail = assess({"Moego": [90] * 5, "Emmansun": [100] * 5, "Segmentio": [80] * 5}, 5, 0.05)
        self.assertEqual(status, "FAIL")
        self.assertIn("best Segmentio", detail)

    def test_missing_and_insufficient_measurements_are_incomplete(self):
        self.assertEqual(assess({"Moego": [1] * 5}, 5, 0.1)[0], "INCOMPLETE")
        self.assertEqual(assess({"Moego": [1] * 5, "Emmansun": [2], "Segmentio": [3] * 5}, 5, 0.1)[0], "INCOMPLETE")

    def test_overlapping_range_fails_even_with_good_median(self):
        self.assertEqual(assess({"Moego": [50, 50, 50, 50, 101], "Emmansun": [100] * 5, "Segmentio": [110] * 5}, 5, 0.1)[0], "FAIL")

    def test_consistent_improvement_passes(self):
        self.assertEqual(assess({"Moego": [70, 71, 72, 73, 74], "Emmansun": [100] * 5, "Segmentio": [110] * 5}, 5, 0.1)[0], "PASS")

    def test_exact_margin_is_not_rejected_by_float_roundoff(self):
        self.assertEqual(assess({"Moego": [90] * 5, "Emmansun": [100] * 5, "Segmentio": [110] * 5}, 5, 0.1)[0], "PASS")


if __name__ == "__main__":
    unittest.main()
