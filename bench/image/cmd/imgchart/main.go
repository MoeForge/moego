// Command imgchart draws the chart of a results directory for one format.
// For each compression level it lists the moego encoder and the other
// encoders' matching levels with the compression ratio (output bytes over
// the raw pixel bytes) as a dot plot and the single-thread encode speed as
// bars scaled within the level. The WebP chart ends each level with
// moego/png's matching level, so both formats share the ratio axis.
//
//	go run ./cmd/imgchart -lang en -summary RUN/summary.csv \
//		-images RUN/per-image.csv -o ../../docs/images/png-ratio-speed.en.svg
//	go run ./cmd/imgchart -lang en -format webp -summary RUN/summary.csv \
//		-images RUN/per-image.csv -timing Z9 -o ../../docs/images/webp-ratio-speed.en.svg
//
// RUN is the output directory of run.sh. -timing names a directory of
// imgbench results that timed some cells on more images than the main run
// (libwebp z9 on all of them); those cells take their speed from it.
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"html"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	summary = flag.String("summary", "", "summary.csv written by imgsum")
	images  = flag.String("images", "", "per-image.csv written by imgsum (image sizes for the raw byte count)")
	outFile = flag.String("o", "", "output SVG")
	format  = flag.String("format", "png", "chart to draw: png or webp")
	timing  = flag.String("timing", "", "imgbench output directory whose results-*.csv give the speed of the cells it timed")
	lang    = flag.String("lang", "zh", "language of the chart: en or zh")
)

type kind int

const (
	moego kind = iota
	other
)

// A row is one encoder setting.
type row struct {
	enc, setting, label string
	k                   kind
	c                   bool // a C library through cgo
	bytes, mps          float64
	timed               int  // images timed
	noBar               bool // another format's reference: a dot and the speed figure, no bar
}

type section struct {
	title string
	rows  []row
}

// A chart is the text and rows of one format's chart.
type chart struct {
	title, subtitle    string
	legend0, legend1   string
	ratioMin, ratioMax float64 // percent; a dot plot needs no zero baseline
	tick               float64
	ref                string // the speed of moego's rows is also given as a multiple of this encoder's
	refLabel           string
	fine               bool // two decimals for speeds below 10 and multiples below 1.1
	speedNote          string
	caption            []string
	sections           []section
	text               labels
}

// labels are the words of a chart that do not depend on the format.
type labels struct {
	ratioHead, ratioNote string // ratioNote takes the axis start in percent
	speedHead            string
	cMark                string // after the name of a C library
	tooltip              string // label, ratio, MB, MP/s
	times                func(ref string, digits int, m float64) string
	timed                string // images timed
}

var zhLabels = labels{
	ratioHead: "压缩率 ← 越靠左文件越小",
	ratioNote: "压缩后大小占原始像素数据的比例；横轴从 %.0f%% 开始",
	speedHead: "编码速度 → 越长越快",
	cMark:     "（C）",
	tooltip:   "%s：压缩率 %.2f%%（%.1f MB），%.2f MP/s",
	times:     func(ref string, d int, m float64) string { return fmt.Sprintf("，%s 的 %.*f 倍", ref, d, m) },
	timed:     "（%d 张）",
}

var enLabels = labels{
	ratioHead: "Compression ratio ← smaller files to the left",
	ratioNote: "Compressed size over the raw pixel data; the axis starts at %.0f%%",
	speedHead: "Encode speed → longer is faster",
	cMark:     " (C)",
	tooltip:   "%s: ratio %.2f%% (%.1f MB), %.2f MP/s",
	times:     func(ref string, d int, m float64) string { return fmt.Sprintf(", %.*f× %s", d, m, ref) },
	timed:     " (%d images)",
}

var pngChart = chart{
	title:    "PNG 编码：压缩率和编码速度",
	subtitle: "97 张公开图片（照片、截图、图标、纹理、透明抠图，69.8 MP），单线程。每组是同一压缩级别下的各个编码器。",
	legend0:  "moego/png（纯 Go）",
	legend1:  "其他编码器；标 C 的是经 cgo 调用的 C 库",
	ratioMin: 30, ratioMax: 40, tick: 2,
	ref: "png-std", refLabel: "image/png", text: zhLabels,
	speedNote: "每秒编码的百万像素（MP/s）；每组按组内最快者缩放",
	caption: []string{
		"原始像素数据按每像素 3 字节（带透明的 4 字节）计，共 %.0f MB。moego/png 为 KeepColorType，颜色类型与 image/png 相同；默认的颜色缩减还会再小约 0.4%%。",
		"Intel Xeon E5-2699A v4（AVX2，KVM 虚拟机），linux/amd64，Go 1.26.6，GOMAXPROCS=1，每个进程绑定一个 CPU。fpnge 不做 LZ77 匹配查找，只列在 BestSpeed。用 bench/image 测量。",
	},
	sections: []section{
		{"BestSpeed（最快）", []row{
			{enc: "moego-png-keep", setting: "speed", label: "moego/png", k: moego},
			{enc: "png-std", setting: "speed", label: "image/png", k: other},
			{enc: "woozypng", setting: "speed", label: "woozymasta/png", k: other},
			{enc: "libdeflate-png", setting: "l1", label: "libdeflate 1", k: other, c: true},
			{enc: "libpng", setting: "z1", label: "libpng 1", k: other, c: true},
			{enc: "fpnge", setting: "default", label: "fpnge", k: other, c: true},
		}},
		{"默认级别", []row{
			{enc: "moego-png-keep", setting: "default", label: "moego/png", k: moego},
			{enc: "png-std", setting: "default", label: "image/png", k: other},
			{enc: "woozypng", setting: "default", label: "woozymasta/png", k: other},
			{enc: "libdeflate-png", setting: "l6", label: "libdeflate 6", k: other, c: true},
			{enc: "libpng", setting: "z6-default", label: "libpng 6", k: other, c: true},
		}},
		{"BestCompression（最小）", []row{
			{enc: "moego-png-keep", setting: "best", label: "moego/png", k: moego},
			{enc: "png-std", setting: "best", label: "image/png", k: other},
			{enc: "woozypng", setting: "best", label: "woozymasta/png", k: other},
			{enc: "libdeflate-png", setting: "l12", label: "libdeflate 12", k: other, c: true},
			{enc: "libpng", setting: "z9", label: "libpng 9", k: other, c: true},
		}},
	},
}

var webpChart = chart{
	title:    "WebP 无损编码：压缩率和编码速度",
	subtitle: "97 张公开图片（照片、截图、图标、纹理、透明抠图，69.8 MP），单线程。每组是同一压缩级别下的各个编码器，最后一行是同级别的 moego/png，作为 PNG 的对照。",
	legend0:  "moego/webp（纯 Go）",
	legend1:  "其他编码器；标 C 的是经 cgo 调用的 C 库",
	ratioMin: 20, ratioMax: 40, tick: 4,
	ref: "libwebp", refLabel: "libwebp", fine: true, text: zhLabels,
	speedNote: "每秒编码的百万像素（MP/s）；按组内最快的 WebP 缩放，PNG 只标数字",
	caption: []string{
		"原始像素数据按每像素 3 字节（带透明的 4 字节）计，共 %.0f MB。所有输出都保留透明像素下的 RGB（libwebp 为 exact=1）。SeriousBug 最高 effort 6；deepteams z0 压缩率 49.4%%，不在图内。",
		"BestCompression 的 moego/webp 和 libwebp z9 是在全部 97 张图上单独计时的速度。",
		"Intel Xeon E5-2699A v4（AVX2，KVM 虚拟机），linux/amd64，Go 1.26.6，libwebp 1.5.0，GOMAXPROCS=1，每个进程绑定一个 CPU。用 bench/image 测量。",
	},
	sections: []section{
		{"BestSpeed（最快）", []row{
			{enc: "moego-webp", setting: "speed", label: "moego/webp", k: moego},
			{enc: "libwebp", setting: "z0", label: "libwebp z0", k: other, c: true},
			{enc: "seriousbug", setting: "effort0", label: "SeriousBug 0", k: other},
			{enc: "nativewebp", setting: "speed", label: "nativewebp", k: other},
			{enc: "moego-png-keep", setting: "speed", label: "moego/png（PNG）", k: other, noBar: true},
		}},
		{"默认级别", []row{
			{enc: "moego-webp", setting: "default", label: "moego/webp", k: moego},
			{enc: "libwebp", setting: "z6-default", label: "libwebp z6", k: other, c: true},
			{enc: "deepteams", setting: "z6-default", label: "deepteams z6", k: other},
			{enc: "seriousbug", setting: "effort6-default", label: "SeriousBug 6", k: other},
			{enc: "nativewebp", setting: "default", label: "nativewebp", k: other},
			{enc: "moego-png-keep", setting: "default", label: "moego/png（PNG）", k: other, noBar: true},
		}},
		{"BestCompression（最小）", []row{
			{enc: "moego-webp", setting: "best", label: "moego/webp", k: moego},
			{enc: "libwebp", setting: "z9", label: "libwebp z9", k: other, c: true},
			{enc: "deepteams", setting: "z9", label: "deepteams z9", k: other},
			{enc: "seriousbug", setting: "effort6-default", label: "SeriousBug 6", k: other},
			{enc: "nativewebp", setting: "best", label: "nativewebp", k: other},
			{enc: "moego-png-keep", setting: "best", label: "moego/png（PNG）", k: other, noBar: true},
		}},
	},
}

// The English charts have the rows of the Chinese ones.
var pngChartEn = translate(pngChart, chart{
	title:     "PNG encoding: compression ratio and encode speed",
	subtitle:  "97 public images (photos, screenshots, icons, textures, cut-outs with alpha; 69.8 MP), single thread. Each group is one compression level.",
	legend0:   "moego/png (pure Go)",
	legend1:   "Other encoders; (C) marks C libraries called through cgo",
	speedNote: "Megapixels encoded per second (MP/s); bars scaled to the fastest in each group",
	caption: []string{
		"Raw pixel data counts 3 bytes per pixel (4 with alpha), %.0f MB in all. moego/png with KeepColorType writes image/png's color types; its default color reduction is about 0.4%% smaller still.",
		"Intel Xeon E5-2699A v4 (AVX2, KVM guest), linux/amd64, Go 1.26.6, GOMAXPROCS=1, one pinned CPU per process. fpnge (no LZ77 search) is under BestSpeed only. Measured with bench/image.",
	},
	sections: []section{{title: "BestSpeed (fastest)"}, {title: "Default"}, {title: "BestCompression (smallest)"}},
	text:     enLabels,
})

var webpChartEn = translate(webpChart, chart{
	title:     "Lossless WebP encoding: compression ratio and encode speed",
	subtitle:  "97 public images (photos, screenshots, icons, textures, cut-outs with alpha; 69.8 MP), single thread. Each group is one level; its last row is moego/png.",
	legend0:   "moego/webp (pure Go)",
	legend1:   "Other encoders; (C) marks C libraries called through cgo",
	speedNote: "Megapixels per second (MP/s); bars scaled to the fastest WebP encoder",
	caption: []string{
		"Raw pixel data counts 3 bytes per pixel (4 with alpha), %.0f MB in all. Every output keeps the RGB under transparent pixels (libwebp with exact=1).",
		"SeriousBug's highest effort is 6; deepteams z0 (49.4%) is off the axis. At BestCompression, moego/webp and libwebp z9 were timed on all 97 images.",
		"Intel Xeon E5-2699A v4 (AVX2, KVM guest), linux/amd64, Go 1.26.6, libwebp 1.5.0, GOMAXPROCS=1, each process pinned to one CPU. Measured with bench/image.",
	},
	sections: []section{{title: "BestSpeed (fastest)"}, {title: "Default"}, {title: "BestCompression (smallest)"}},
	text:     enLabels,
})

// translate returns base with the text of t: titles, notes, captions,
// section titles and labels. Row labels ending in "（PNG）" get " (PNG)".
func translate(base, t chart) chart {
	c := base
	c.title, c.subtitle, c.legend0, c.legend1 = t.title, t.subtitle, t.legend0, t.legend1
	c.speedNote, c.caption, c.text = t.speedNote, t.caption, t.text
	c.sections = make([]section, len(base.sections))
	for i, s := range base.sections {
		c.sections[i] = section{title: t.sections[i].title, rows: append([]row(nil), s.rows...)}
		for j := range c.sections[i].rows {
			r := &c.sections[i].rows[j]
			r.label = strings.Replace(r.label, "（PNG）", " (PNG)", 1)
		}
	}
	return c
}

// Layout, left to right: names, the ratio dot plot, the speed bars.
const (
	width   = 1100.0
	nameX   = 178.0 // names end here
	ratioX  = 210.0 // ratio axis
	ratioW  = 340.0
	speedX  = 640.0
	speedW  = 240.0
	rowH    = 26.0
	barH    = 14.0
	top     = 186.0
	sectGap = 18.0
)

func main() {
	flag.Parse()
	if *summary == "" || *images == "" || *outFile == "" {
		log.Fatal("need -summary, -images and -o")
	}
	charts := map[string]chart{"png/zh": pngChart, "webp/zh": webpChart, "png/en": pngChartEn, "webp/en": webpChartEn}
	c, ok := charts[*format+"/"+*lang]
	if !ok {
		log.Fatalf("-format %q -lang %q: want png or webp, and en or zh", *format, *lang)
	}
	sections := c.sections
	raw := rawBytes(*images)
	recs := readCSV(*summary)
	col := map[string]int{}
	for i, h := range recs[0] {
		col[h] = i
	}
	for si := range sections {
		for ri := range sections[si].rows {
			r := &sections[si].rows[ri]
			found := false
			for _, rec := range recs[1:] {
				if rec[col["encoder"]] != r.enc || rec[col["setting"]] != r.setting {
					continue
				}
				if rec[col["images_ok"]] != "97" || rec[col["images_run"]] != "97" {
					log.Fatalf("%s/%s: not all 97 images round-trip", r.enc, r.setting)
				}
				r.bytes, _ = strconv.ParseFloat(rec[col["total_bytes"]], 64)
				r.mps, _ = strconv.ParseFloat(rec[col["encode_mps"]], 64)
				r.timed, _ = strconv.Atoi(rec[col["images_timed"]])
				found = r.bytes > 0 && r.mps > 0
			}
			if !found {
				log.Fatalf("%s/%s: no size and speed in %s", r.enc, r.setting, *summary)
			}
			if p := 100 * r.bytes / raw; p < c.ratioMin || p > c.ratioMax {
				log.Fatalf("%s/%s: ratio %.1f%% outside the axis", r.enc, r.setting, p)
			}
		}
	}

	// summary.csv rounds MP/s to two decimals; take the speeds from the
	// per-image times so the multiples are not computed from rounded
	// figures.
	precise := timedSpeeds([]string{*images})
	for si := range sections {
		for ri := range sections[si].rows {
			r := &sections[si].rows[ri]
			if s, ok := precise[r.enc+"/"+r.setting]; ok && s.n == r.timed {
				r.mps = s.mps
			}
		}
	}
	if *timing != "" {
		files, err := filepath.Glob(filepath.Join(*timing, "results-*.csv"))
		if err != nil || len(files) == 0 {
			log.Fatalf("%s: no results-*.csv", *timing)
		}
		speeds := timedSpeeds(files)
		for si := range sections {
			for ri := range sections[si].rows {
				r := &sections[si].rows[ri]
				if s, ok := speeds[r.enc+"/"+r.setting]; ok {
					r.mps, r.timed = s.mps, s.n
				}
			}
		}
	}

	rx := func(p float64) float64 { return ratioX + (p-c.ratioMin)/(c.ratioMax-c.ratioMin)*ratioW }
	height := top
	for _, s := range sections {
		height += 28 + float64(len(s.rows))*rowH + sectGap
	}
	bodyBot := height - sectGap + 4
	height += 24 + 20*float64(len(c.caption))

	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w(`<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f" role="img" aria-labelledby="title">`+"\n", width, height, width, height)
	w(`<title id="title">%s</title>
`, html.EscapeString(c.title))
	w(`<style>
text{font-family:"Noto Sans CJK SC","PingFang SC","Microsoft YaHei",system-ui,sans-serif}
.bg{fill:#fcfcfb}.t1{font-size:21px;font-weight:700;fill:#0b0b0b}.t2{font-size:13px;fill:#52514e}
.st{font-size:14.5px;font-weight:700;fill:#0b0b0b}.ch{font-size:14px;font-weight:700;fill:#0b0b0b}.cs{font-size:12px;fill:#52514e}
.nm{font-size:13px;fill:#52514e}.nmm{font-size:13.5px;font-weight:700;fill:#0b0b0b}
.v{font-size:12.5px;fill:#52514e}.vm{font-size:13px;font-weight:700;fill:#0b0b0b}
.cap{font-size:11.5px;fill:#6f6d68}.ax{stroke:#8f8d86;stroke-width:1}.grid{stroke:#e4e3df;stroke-width:1}
.b0{fill:#2a78d6}.b1{fill:#a3a29c}.d0{fill:#2a78d6;stroke:#fcfcfb;stroke-width:2}.d1{fill:#a3a29c;stroke:#fcfcfb;stroke-width:2}
.band{fill:#2a78d6;fill-opacity:.07}
@media (prefers-color-scheme: dark){
.bg{fill:#1a1a19}.t1,.st,.ch,.nmm,.vm{fill:#f0efec}.t2,.cs,.nm,.v{fill:#c3c2b7}.cap{fill:#9a9891}
.grid{stroke:#383835}.b0{fill:#3987e5}.b1{fill:#6f6d68}.d0{fill:#3987e5;stroke:#1a1a19}.d1{fill:#6f6d68;stroke:#1a1a19}
.band{fill:#3987e5;fill-opacity:.12}}
</style>
<rect class="bg" width="100%%" height="100%%"/>
`)
	w(`<text class="t1" x="24" y="40">%s</text>`+"\n", html.EscapeString(c.title))
	w(`<text class="t2" x="24" y="64">%s</text>`+"\n", html.EscapeString(c.subtitle))
	w(`<rect class="b0" x="24" y="80" width="14" height="10" rx="2"/><text class="cs" x="44" y="90">%s</text>`, html.EscapeString(c.legend0))
	w(`<rect class="b1" x="188" y="80" width="14" height="10" rx="2"/><text class="cs" x="208" y="90">%s</text>`+"\n", html.EscapeString(c.legend1))

	w(`<text class="ch" x="%.0f" y="132">%s</text>`, ratioX, html.EscapeString(c.text.ratioHead))
	w(`<text class="cs" x="%.0f" y="150">%s</text>`+"\n", ratioX, html.EscapeString(fmt.Sprintf(c.text.ratioNote, c.ratioMin)))
	w(`<text class="ch" x="%.0f" y="132">%s</text>`, speedX, html.EscapeString(c.text.speedHead))
	w(`<text class="cs" x="%.0f" y="150">%s</text>`+"\n", speedX, html.EscapeString(c.speedNote))

	for p := c.ratioMin; p <= c.ratioMax+1e-9; p += c.tick {
		x := rx(p)
		w(`<line class="grid" x1="%.1f" x2="%.1f" y1="%.0f" y2="%.0f"/>`, x, x, top-16, bodyBot)
		w(`<text class="cs" x="%.1f" y="%.0f" text-anchor="middle">%.0f%%</text>`+"\n", x, bodyBot+16, p)
	}
	w(`<line class="ax" x1="%.0f" x2="%.0f" y1="%.0f" y2="%.0f"/>`+"\n", speedX, speedX, top-16, bodyBot)

	y := top
	for _, s := range sections {
		w(`<text class="st" x="24" y="%.1f">%s</text>`+"\n", y+6, html.EscapeString(s.title))
		y += 28
		var fastest, refMPS float64
		for _, r := range s.rows {
			if !r.noBar {
				fastest = math.Max(fastest, r.mps)
			}
			if r.enc == c.ref {
				refMPS = r.mps
			}
		}
		for _, r := range s.rows {
			name, val := "nm", "v"
			if r.k == moego {
				name, val = "nmm", "vm"
				w(`<rect class="band" x="16" y="%.1f" width="%.0f" height="%.0f" rx="4"/>`, y+1, width-32, rowH-2)
			}
			label := r.label
			if r.c {
				label += c.text.cMark
			}
			cy := y + rowH/2
			ratio := 100 * r.bytes / raw
			w(`<g><title>%s</title>`, html.EscapeString(fmt.Sprintf(c.text.tooltip, label, ratio, r.bytes/1e6, r.mps)))
			w(`<text class="%s" x="%.0f" y="%.1f" text-anchor="end">%s</text>`, name, nameX, cy+4.5, html.EscapeString(label))
			x := rx(ratio)
			rad := 5.5
			if r.k == moego {
				rad = 7
			}
			w(`<circle class="d%d" cx="%.1f" cy="%.1f" r="%.1f"/>`, r.k, x, cy, rad)
			w(`<text class="%s" x="%.1f" y="%.1f">%.1f%%</text>`, val, x+rad+5, cy+4.5, ratio)
			l := r.mps / fastest * speedW
			if r.noBar {
				l = 0
			} else {
				hbar(w, r.k, speedX, cy-barH/2, l, false)
			}
			v := fmt.Sprintf("%.1f", r.mps)
			if c.fine && r.mps < 10 {
				v = fmt.Sprintf("%.2f", r.mps)
			}
			if r.mps >= 100 {
				v = fmt.Sprintf("%.0f", r.mps)
			}
			if r.k == moego && refMPS > 0 {
				m, d := r.mps/refMPS, 1
				if c.fine && m < 2 {
					d = 2 // one decimal would round 1.04 to "1.0"
				}
				v += c.text.times(c.refLabel, d, m)
			}
			if r.timed > 0 && r.timed < 97 {
				v += fmt.Sprintf(c.text.timed, r.timed)
			}

			w(`<text class="%s" x="%.1f" y="%.1f">%s</text></g>`+"\n", val, speedX+l+6, cy+4.5, v)
			y += rowH
		}
		y += sectGap
	}

	for i, line := range c.caption {
		if strings.Contains(line, "%") && strings.Contains(line, "%.0f") {
			line = fmt.Sprintf(line, raw/1e6)
		}
		w(`<text class="cap" x="24" y="%.0f">%s</text>`+"\n", height-14-20*float64(len(c.caption)-1-i), html.EscapeString(line))
	}
	w("</svg>\n")
	if err := os.WriteFile(*outFile, []byte(b.String()), 0o644); err != nil {
		log.Fatal(err)
	}
}

// hbar draws a horizontal bar of the given length from x, square at the
// zero line and rounded at its data end, which is on the left when left
// is set.
func hbar(w func(string, ...any), k kind, x, y, length float64, left bool) {
	const r = 4.0
	if length < 2*r {
		w(`<rect class="b%d" x="%.1f" y="%.1f" width="%.1f" height="%.0f"/>`, k, x, y, math.Max(length, 1.5), barH)
		return
	}
	if left {
		w(`<path class="b%d" d="M%.1f %.1f h-%.1f a%.0f %.0f 0 0 0 -%.0f %.0f v%.0f a%.0f %.0f 0 0 0 %.0f %.0f h%.1f z"/>`,
			k, x+length, y, length-r, r, r, r, r, barH-2*r, r, r, r, r, length-r)
		return
	}
	w(`<path class="b%d" d="M%.1f %.1f h%.1f a%.0f %.0f 0 0 1 %.0f %.0f v%.0f a%.0f %.0f 0 0 1 -%.0f %.0f h-%.1f z"/>`,
		k, x, y, length-r, r, r, r, r, barH-2*r, r, r, r, r, length-r)
}

type speed struct {
	mps float64
	n   int
}

// timedSpeeds reads per-image results (imgbench's results-*.csv or
// imgsum's per-image.csv) and returns each cell's speed over the images
// it passed and was timed on.
func timedSpeeds(files []string) map[string]speed {
	px := map[string]float64{}
	ns := map[string]float64{}
	n := map[string]int{}
	for _, f := range files {
		recs := readCSV(f)
		col := map[string]int{}
		for i, h := range recs[0] {
			col[h] = i
		}
		for _, r := range recs[1:] {
			if r[col["status"]] != "ok" {
				continue
			}
			t, _ := strconv.ParseFloat(r[col["enc_median_ns"]], 64)
			if t <= 0 {
				continue
			}
			w, _ := strconv.ParseFloat(r[col["width"]], 64)
			h, _ := strconv.ParseFloat(r[col["height"]], 64)
			k := r[col["encoder"]] + "/" + r[col["setting"]]
			px[k] += w * h
			ns[k] += t
			n[k]++
		}
	}
	out := map[string]speed{}
	for k := range px {
		out[k] = speed{mps: px[k] / ns[k] * 1e3, n: n[k]}
	}
	return out
}

func readCSV(path string) [][]string {
	f, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		log.Fatal(err)
	}
	return recs
}

// rawBytes sums the uncompressed pixel bytes of the corpus: 3 bytes per
// pixel for opaque images, 4 for images with alpha.
func rawBytes(path string) float64 {
	recs := readCSV(path)
	col := map[string]int{}
	for i, h := range recs[0] {
		col[h] = i
	}
	seen := map[string]bool{}
	var total float64
	for _, r := range recs[1:] {
		name := r[col["image"]]
		if seen[name] {
			continue
		}
		seen[name] = true
		w, _ := strconv.ParseFloat(r[col["width"]], 64)
		h, _ := strconv.ParseFloat(r[col["height"]], 64)
		bpp := 3.0
		if r[col["has_alpha"]] == "true" {
			bpp = 4
		}
		total += w * h * bpp
	}
	if len(seen) != 97 {
		log.Fatalf("%s: %d images, want 97", path, len(seen))
	}
	return total
}
