import contextlib
import csv
import io
from pathlib import Path
import re
import tempfile
import unittest
import xml.etree.ElementTree as ElementTree

from plot import SCENARIOS, TEXT, main


SVG_TEXT = "{http://www.w3.org/2000/svg}text"


class ChartTests(unittest.TestCase):
    # 1 MiB medians in ns; CloudwegoFallback is the ARM64 name of Cloudwego.
    medians = {"Moego": 100000, "Stdlib": 200000, "Emmansun": 125000, "Segmentio": 150000,
               "CloudwegoFallback": 140000, "Cristalhq": 400000}

    def render(self, scenarios, lang="en"):
        with tempfile.TemporaryDirectory() as name:
            run = Path(name)
            with (run / "summary.csv").open("w", newline="") as target:
                writer = csv.writer(target)
                writer.writerow(("case", "codec", "samples", "median_ns", "minimum_ns", "maximum_ns"))
                for key in scenarios:
                    for codec, median in self.medians.items():
                        writer.writerow((f"{key}/1MiB", codec, 6, median, median, median))
            with contextlib.redirect_stdout(io.StringIO()):
                main([str(run), "--output", str(run / "out"), "--lang", lang])
            return {path.name: [element.text for element in ElementTree.parse(path).iter(SVG_TEXT)]
                    for path in (run / "out").glob("*.svg")}

    def test_multipliers_use_the_fastest_implementation_shown(self):
        for lang in TEXT:
            charts = self.render(SCENARIOS, lang)
            self.assertEqual(charts[f"bench-1MiB-vs-stdlib.{lang}.svg"].count("2.00×"), len(SCENARIOS))
            competitors = charts[f"bench-1MiB-vs-competitors.{lang}.svg"]
            self.assertEqual(competitors.count("1.25×"), len(SCENARIOS))
            self.assertEqual(competitors.count("CloudwegoFallback"), len(SCENARIOS))

    def test_missing_scenario_cannot_render(self):
        with self.assertRaisesRegex(SystemExit, "missing BenchmarkValidation/Reuse/1MiB"):
            self.render(SCENARIOS[:-1])

    def test_footer_names_tools_not_input_directory(self):
        footers = {"zh": "数据由 bench/rounds.py 测得，图由 bench/plot.py 生成",
                   "en": "Measured with bench/rounds.py, drawn by bench/plot.py"}
        for lang, footer in footers.items():
            for texts in self.render(SCENARIOS, lang).values():
                self.assertIn(footer, texts)
                self.assertFalse(any("summary.csv" in text for text in texts))

    def test_languages_share_every_number(self):
        zh, en = self.render(SCENARIOS, "zh"), self.render(SCENARIOS, "en")
        number = re.compile(r"[\d.]+×?")
        for chart in ("bench-1MiB-vs-stdlib", "bench-1MiB-vs-competitors"):
            self.assertEqual([text for text in zh[f"{chart}.zh.svg"] if number.fullmatch(text)],
                             [text for text in en[f"{chart}.en.svg"] if number.fullmatch(text)])
            self.assertFalse(any(re.search("[\u3000-\u9fff\uff00-\uffef]", text) for text in en[f"{chart}.en.svg"]))


if __name__ == "__main__":
    unittest.main()
