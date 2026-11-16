// Command imgsum turns imgbench output into summary.md, summary.csv and
// failures.csv. Sizes and correctness come from a -check run over the
// whole corpus (-check DIR); speeds come from timing runs (-timing DIR,...),
// which may cover only part of the corpus for the slowest encoders.
//
// Each encoder is compared with the references on the images where it and
// both references round-trip exactly; an encoder that fails some images is
// marked and its totals cover only the images it passed.
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var (
	dir      = flag.String("out", "", "output directory for summary.md, summary.csv, failures.csv")
	checkDir = flag.String("check", "", "comma-separated imgbench -check output directories (sizes, correctness)")
	timeDirs = flag.String("timing", "", "comma-separated imgbench timing output directories")
	refPNG   = flag.String("refpng", "png-std/default", "PNG reference encoder")
	refWebP  = flag.String("refwebp", "libwebp/z6-default", "WebP reference encoder")
	machine  = flag.String("machine", "", "machine description line")
	commands = flag.String("commands", "", "file whose contents go in the Commands section")
	notes    = flag.String("notes", "", "markdown file inserted after the summary table")
)

type img struct {
	name, cat  string
	px         int
	alpha      bool
	colors     int
	srcType    string
	w, h, size int
}

type cell struct {
	status, detail     string
	bytes              int
	enc, decGo, decC   int64 // median ns
	encRuns            int
	allocB, allocCount int64
}

type encInfo struct {
	id, format, lib, note string
	pure                  bool
	order                 int
}

func main() {
	flag.Parse()
	if *dir == "" || *checkDir == "" || *timeDirs == "" {
		log.Fatal("need -out, -check and -timing")
	}
	checkDirs := strings.Split(*checkDir, ",")
	dirs := append(append([]string{}, checkDirs...), strings.Split(*timeDirs, ",")...)
	imgs := map[string]*img{}
	var imgOrder []string
	for _, f := range glob(checkDirs[0], "images-*.csv") {
		for _, r := range readCSV(f) {
			if _, ok := imgs[r["image"]]; ok {
				continue
			}
			imgs[r["image"]] = &img{name: r["image"], cat: r["category"], px: atoi(r["pixels"]), alpha: r["has_alpha"] == "true",
				colors: atoi(r["colors"]), srcType: r["src_type"], w: atoi(r["width"]), h: atoi(r["height"]), size: atoi(r["file_bytes"])}
			imgOrder = append(imgOrder, r["image"])
		}
	}
	sort.Strings(imgOrder)

	encs := map[string]*encInfo{}
	var metas []string
	for _, d := range dirs {
		metas = append(metas, glob(d, "meta-*.json")...)
	}
	for _, f := range metas {
		var m struct {
			Encoders []struct {
				ID, Format, Lib, Note string
				Pure                  bool
				Index                 int
			}
		}
		b, err := os.ReadFile(f)
		if err != nil {
			log.Fatal(err)
		}
		if err := json.Unmarshal(b, &m); err != nil {
			log.Fatalf("%s: %v", f, err)
		}
		for _, e := range m.Encoders {
			if _, ok := encs[e.ID]; !ok {
				encs[e.ID] = &encInfo{e.ID, e.Format, e.Lib, e.Note, e.Pure, e.Index}
			}
		}
	}

	timingFailures := map[string]map[string][]*cell{}
	cells := map[string]map[string]*cell{} // encoder -> image -> cell
	for _, d := range checkDirs {
		for id, m := range loadCells(d) {
			if cells[id] != nil {
				log.Fatalf("encoder %s in more than one -check directory", id)
			}
			cells[id] = m
		}
	}
	timed := map[string]map[string]*cell{}
	for _, d := range strings.Split(*timeDirs, ",") {
		for id, m := range loadCells(d) {
			if timed[id] == nil {
				timed[id] = map[string]*cell{}
			}
			for im, c := range m {
				// Timing passes verify their own output again; gowebper
				// level 0 is not deterministic and can fail there alone.
				if c.enc > 0 && (c.status == "ok" || c.status == "alpha0") {
					timed[id][im] = c
				} else if c.status != "ok" && c.status != "alpha0" {
					if timingFailures[id] == nil {
						timingFailures[id] = map[string][]*cell{}
					}
					timingFailures[id][im] = append(timingFailures[id][im], c)
				}
			}
		}
	}
	var ids []string
	for id := range cells {
		if encs[id] == nil {
			log.Fatalf("encoder %s has results but no meta", id)
		}
		ids = append(ids, id)
	}
	// Passes run by different builds number the encoders differently, so
	// keep each encoder's settings together: encoders by their first
	// setting's index (then name), settings by index.
	name := func(id string) string { return strings.SplitN(id, "/", 2)[0] }
	first := map[string]int{}
	for _, id := range ids {
		if o, ok := first[name(id)]; !ok || encs[id].order < o {
			first[name(id)] = encs[id].order
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := encs[ids[i]], encs[ids[j]]
		if a.format != b.format {
			return a.format == "png"
		}
		if a.pure != b.pure {
			return a.pure
		}
		na, nb := name(ids[i]), name(ids[j])
		if first[na] != first[nb] {
			return first[na] < first[nb]
		}
		if na != nb {
			return na < nb
		}
		return a.order < b.order
	})
	for _, r := range []string{*refPNG, *refWebP} {
		if cells[r] == nil {
			log.Fatalf("reference %s missing", r)
		}
	}

	good := func(c *cell) bool { return c != nil && (c.status == "ok" || c.status == "alpha0") }
	usable := func(id, name string) bool {
		return good(cells[id][name]) && good(cells[*refPNG][name]) && good(cells[*refWebP][name])
	}

	mps := func(px int64, ns int64) string {
		if ns <= 0 {
			return "—"
		}
		return fmt.Sprintf("%.2f", float64(px)/float64(ns)*1e3)
	}
	type agg struct {
		n, px                      int
		bytes, refP, refW          int64
		logP, logW                 float64
		tn                         int // images with timing
		encPx, encNs               int64
		decGoNs, decCNs            int64
		decGoPx, decCPx, alloc, ac int64
	}
	sum := func(id string, filter func(*img) bool) agg {
		var a agg
		for _, name := range imgOrder {
			im := imgs[name]
			if !filter(im) || !usable(id, name) {
				continue
			}
			c, p, w := cells[id][name], cells[*refPNG][name], cells[*refWebP][name]
			a.n++
			a.px += im.px
			a.bytes += int64(c.bytes)
			a.refP += int64(p.bytes)
			a.refW += int64(w.bytes)
			a.logP += math.Log(float64(c.bytes) / float64(p.bytes))
			a.logW += math.Log(float64(c.bytes) / float64(w.bytes))
			t := timed[id][name]
			if t == nil {
				continue
			}
			a.tn++
			a.encPx += int64(im.px)
			a.encNs += t.enc
			if t.decGo > 0 {
				a.decGoNs += t.decGo
				a.decGoPx += int64(im.px)
			}
			if t.decC > 0 {
				a.decCNs += t.decC
				a.decCPx += int64(im.px)
			}
			a.alloc += t.allocB
			a.ac += t.allocCount
		}
		return a
	}
	// timeRatio is the encoder's total encode time over ref's, on the
	// images both were timed on (and both round-tripped).
	timeRatio := func(id, ref string, filter func(*img) bool) (float64, int) {
		var a, b int64
		n := 0
		for _, name := range imgOrder {
			if !filter(imgs[name]) || !good(cells[id][name]) || !good(cells[ref][name]) {
				continue
			}
			t, r := timed[id][name], timed[ref][name]
			if t == nil || r == nil {
				continue
			}
			a += t.enc
			b += r.enc
			n++
		}
		if b == 0 {
			return 0, 0
		}
		return float64(a) / float64(b), n
	}
	ratioText := func(r float64, n int) string {
		if n == 0 {
			return "—"
		}
		if r >= 10 {
			return fmt.Sprintf("%.0f", r)
		}
		return fmt.Sprintf("%.2f", r)
	}
	encMPS := func(a agg) string {
		s := mps(a.encPx, a.encNs)
		if a.tn < a.n && a.tn > 0 {
			s += fmt.Sprintf(" (%d img)", a.tn)
		}
		return s
	}
	all := func(*img) bool { return true }

	var md strings.Builder
	var totalPx int
	cats := map[string]int{}
	var catOrder []string
	for _, name := range imgOrder {
		im := imgs[name]
		totalPx += im.px
		if cats[im.cat] == 0 {
			catOrder = append(catOrder, im.cat)
		}
		cats[im.cat]++
	}
	sort.Strings(catOrder)
	fmt.Fprintf(&md, "# Lossless image encoders\n\n")
	if *machine != "" {
		fmt.Fprintf(&md, "Machine: %s\n\n", *machine)
	}
	fmt.Fprintf(&md, "Corpus: %d images, %.1f megapixels (", len(imgOrder), float64(totalPx)/1e6)
	for i, c := range catOrder {
		if i > 0 {
			md.WriteString(", ")
		}
		fmt.Fprintf(&md, "%s %d", c, cats[c])
	}
	md.WriteString("); list and checksums in `bench/image/corpus.txt`.\n\n")
	fmt.Fprintf(&md, "Sizes are file bytes. \"vs png\" and \"vs webp\" are total bytes divided by the total of %s and %s on the same images; "+
		"\"geo\" columns are geometric means of per-image ratios. MP/s is total megapixels over the sum of per-image median times, one thread. "+
		"\"enc time vs\" is the encoder's total encode time over the reference's on the images both were timed on (lower is faster). "+
		"Decode columns time the format's Go decoder (image/png, x/image/webp) and C decoder (libpng, libwebp) on that encoder's output. "+
		"Allocation columns are Go heap bytes and allocations for one encode, summed over the images (C allocations are not counted).\n\n", *refPNG, *refWebP)

	md.WriteString("## Summary\n\n")
	md.WriteString("| encoder | setting | fmt | cgo | images ok | total bytes | bits/px | vs png | vs webp | geo vs png | geo vs webp | encode MP/s | enc time vs png | enc time vs webp | decode Go MP/s | decode C MP/s | Go alloc MB | Go allocs |\n")
	md.WriteString("|---|---|---|---|---|--:|--:|--:|--:|--:|--:|--:|--:|--:|--:|--:|--:|--:|\n")
	csvRows := [][]string{{"encoder", "setting", "format", "cgo", "images_ok", "images_run", "total_bytes", "bits_per_px", "ratio_vs_png", "ratio_vs_webp",
		"geo_vs_png", "geo_vs_webp", "encode_mps", "enc_time_vs_png", "enc_time_vs_webp", "decode_go_mps", "decode_c_mps", "images_timed", "go_alloc_bytes", "go_allocs", "failures"}}
	type failure struct{ id, image, status, detail string }
	var failures []failure
	for _, id := range ids {
		e := encs[id]
		a := sum(id, all)
		name, setting, _ := strings.Cut(id, "/")
		nfail, nalpha0, ran := 0, 0, 0
		for _, im := range imgOrder {
			c := cells[id][im]
			if c != nil {
				ran++
			}
			switch {
			case c == nil:
			case c.status == "alpha0":
				nalpha0++
			case c.status != "ok":
				nfail++
				failures = append(failures, failure{id, im, c.status, c.detail})
			}
			// A cell that passed the check but failed when timed.
			if c != nil && good(c) {
				for _, t := range timingFailures[id][im] {
					failures = append(failures, failure{id, im, "timing pass: " + t.status, t.detail})
				}
			}
		}
		okText := fmt.Sprintf("%d/%d", a.n, ran)
		if ran < len(imgOrder) {
			okText += fmt.Sprintf(" (run on %d of %d)", ran, len(imgOrder))
		}
		if nalpha0 > 0 {
			okText += fmt.Sprintf(" (%d alpha-0 RGB changed)", nalpha0)
		}
		cgo := ""
		if !e.pure {
			cgo = "yes"
		}
		if a.n == 0 {
			fmt.Fprintf(&md, "| %s | %s | %s | %s | %s |%s\n", name, setting, e.format, cgo, okText, strings.Repeat(" — |", 13))
			continue
		}
		tp, tpn := timeRatio(id, *refPNG, all)
		tw, twn := timeRatio(id, *refWebP, all)
		bpp := float64(a.bytes*8) / float64(a.px)
		rp, rw := float64(a.bytes)/float64(a.refP), float64(a.bytes)/float64(a.refW)
		gp, gw := math.Exp(a.logP/float64(a.n)), math.Exp(a.logW/float64(a.n))
		fmt.Fprintf(&md, "| %s | %s | %s | %s | %s | %d | %.3f | %.3f | %.3f | %.3f | %.3f | %s | %s | %s | %s | %s | %.0f | %d |\n",
			name, setting, e.format, cgo, okText, a.bytes, bpp, rp, rw, gp, gw,
			encMPS(a), ratioText(tp, tpn), ratioText(tw, twn), mps(a.decGoPx, a.decGoNs), mps(a.decCPx, a.decCNs), float64(a.alloc)/1e6, a.ac)
		csvRows = append(csvRows, []string{name, setting, e.format, strconv.FormatBool(!e.pure), strconv.Itoa(a.n), strconv.Itoa(ran),
			strconv.FormatInt(a.bytes, 10), f4(bpp), f4(rp), f4(rw), f4(gp), f4(gw),
			mps(a.encPx, a.encNs), f4(tp), f4(tw), mps(a.decGoPx, a.decGoNs), mps(a.decCPx, a.decCNs), strconv.Itoa(a.tn),
			strconv.FormatInt(a.alloc, 10), strconv.FormatInt(a.ac, 10), strconv.Itoa(nfail)})
	}
	{
		var srcBytes, refP, refW int64
		var px int
		for _, name := range imgOrder {
			srcBytes += int64(imgs[name].size)
			px += imgs[name].px
			if good(cells[*refPNG][name]) && good(cells[*refWebP][name]) {
				refP += int64(cells[*refPNG][name].bytes)
				refW += int64(cells[*refWebP][name].bytes)
			}
		}
		fmt.Fprintf(&md, "| *source files* | as distributed | png | | %d | %d | %.3f | %.3f | %.3f | | | | | | | | | |\n",
			len(imgOrder), srcBytes, float64(srcBytes*8)/float64(px), float64(srcBytes)/float64(refP), float64(srcBytes)/float64(refW))
	}
	md.WriteString("\nImages ok counts exact round trips out of the images the encoder was run on. An encoder with fewer images ok is compared on the images it passed only; " +
		"its totals are not comparable with the full-corpus rows. An encode MP/s value marked (n img) was timed on n of those images only " +
		"(the slowest settings are timed on every 4th image of corpus.txt).\n")

	if *notes != "" {
		b, err := os.ReadFile(*notes)
		if err != nil {
			log.Fatal(err)
		}
		md.WriteString("\n")
		md.Write(b)
	}

	md.WriteString("\n## Size by category (total bytes vs " + *refPNG + ")\n\n| encoder | setting |")
	for _, c := range catOrder {
		fmt.Fprintf(&md, " %s (%d) |", c, cats[c])
	}
	md.WriteString("\n|---|---|" + strings.Repeat("--:|", len(catOrder)) + "\n")
	catTable := func(val func(a agg) string) {
		for _, id := range ids {
			name, setting, _ := strings.Cut(id, "/")
			fmt.Fprintf(&md, "| %s | %s |", name, setting)
			for _, c := range catOrder {
				a := sum(id, func(im *img) bool { return im.cat == c })
				if a.n == 0 {
					md.WriteString(" — |")
					continue
				}
				s := val(a)
				if a.n < cats[c] {
					s += fmt.Sprintf(" (%d)", a.n)
				}
				fmt.Fprintf(&md, " %s |", s)
			}
			md.WriteString("\n")
		}
	}
	catTable(func(a agg) string { return fmt.Sprintf("%.3f", float64(a.bytes)/float64(a.refP)) })

	md.WriteString("\n## Bits per pixel by category\n\n| encoder | setting |")
	for _, c := range catOrder {
		fmt.Fprintf(&md, " %s |", c)
	}
	md.WriteString("\n|---|---|" + strings.Repeat("--:|", len(catOrder)) + "\n")
	catTable(func(a agg) string { return fmt.Sprintf("%.3f", float64(a.bytes*8)/float64(a.px)) })

	md.WriteString("\n## Encode MP/s by category\n\n| encoder | setting |")
	for _, c := range catOrder {
		fmt.Fprintf(&md, " %s |", c)
	}
	md.WriteString("\n|---|---|" + strings.Repeat("--:|", len(catOrder)) + "\n")
	catTable(func(a agg) string { return encMPS(a) })

	md.WriteString("\n## Failures\n\n")
	if len(failures) == 0 {
		md.WriteString("None.\n")
	} else {
		byEnc := map[string][]failure{}
		var order []string
		for _, f := range failures {
			if byEnc[f.id] == nil {
				order = append(order, f.id)
			}
			byEnc[f.id] = append(byEnc[f.id], f)
		}
		md.WriteString("| encoder | images | first failures |\n|---|--:|---|\n")
		for _, id := range order {
			fs := byEnc[id]
			var ex []string
			for i, f := range fs {
				if i == 3 {
					break
				}
				d := f.detail
				if len(d) > 90 {
					d = d[:90] + "…"
				}
				ex = append(ex, fmt.Sprintf("`%s` %s: %s", f.image, f.status, strings.ReplaceAll(d, "|", "/")))
			}
			fmt.Fprintf(&md, "| %s | %d | %s |\n", id, len(fs), strings.Join(ex, "<br>"))
		}
		md.WriteString("\nEvery failure is listed in `failures.csv`.\n")
		fw := [][]string{{"encoder", "image", "status", "detail"}}
		for _, f := range failures {
			fw = append(fw, []string{f.id, f.image, f.status, f.detail})
		}
		writeCSV("failures.csv", fw)
	}

	md.WriteString("\n## Versions\n\n")
	for _, id := range ids {
		e := encs[id]
		note := ""
		if e.note != "" {
			note = "; " + e.note
		}
		fmt.Fprintf(&md, "- `%s`: %s%s\n", id, e.lib, note)
	}
	for _, f := range metas {
		var m struct{ Decoders map[string]string }
		b, _ := os.ReadFile(f)
		json.Unmarshal(b, &m)
		var ks []string
		for k := range m.Decoders {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			fmt.Fprintf(&md, "- decoder `%s`: %s\n", k, m.Decoders[k])
		}
		break
	}
	if *commands != "" {
		b, err := os.ReadFile(*commands)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(&md, "\n## Commands\n\n```sh\n%s```\n", b)
	}
	// Per image: every encoder's bytes, ratio to the references, and encode
	// time where it was timed.
	per := [][]string{{"image", "category", "width", "height", "has_alpha", "colors", "src_type", "encoder", "setting", "status",
		"bytes", "ratio_vs_png", "ratio_vs_webp", "enc_median_ns", "dec_go_median_ns", "dec_c_median_ns"}}
	for _, name := range imgOrder {
		im := imgs[name]
		p, w := cells[*refPNG][name], cells[*refWebP][name]
		for _, id := range ids {
			c := cells[id][name]
			if c == nil {
				continue
			}
			ratio := func(ref *cell) string {
				if !good(c) || !good(ref) {
					return ""
				}
				return f4(float64(c.bytes) / float64(ref.bytes))
			}
			var enc, dg, dc string
			if t := timed[id][name]; t != nil {
				enc, dg, dc = strconv.FormatInt(t.enc, 10), strconv.FormatInt(t.decGo, 10), strconv.FormatInt(t.decC, 10)
			}
			en, set, _ := strings.Cut(id, "/")
			per = append(per, []string{name, im.cat, strconv.Itoa(im.w), strconv.Itoa(im.h), strconv.FormatBool(im.alpha),
				strconv.Itoa(im.colors), im.srcType, en, set, c.status, strconv.Itoa(c.bytes), ratio(p), ratio(w), enc, dg, dc})
		}
	}
	writeCSV("per-image.csv", per)

	if err := os.WriteFile(filepath.Join(*dir, "summary.md"), []byte(md.String()), 0o644); err != nil {
		log.Fatal(err)
	}
	writeCSV("summary.csv", csvRows)
}

func f4(f float64) string { return strconv.FormatFloat(f, 'f', 4, 64) }

func loadCells(dir string) map[string]map[string]*cell {
	cells := map[string]map[string]*cell{}
	for _, f := range glob(dir, "results-*.csv") {
		for _, r := range readCSV(f) {
			id := r["encoder"] + "/" + r["setting"]
			if cells[id] == nil {
				cells[id] = map[string]*cell{}
			}
			cells[id][r["image"]] = &cell{status: r["status"], detail: r["detail"], bytes: atoi(r["bytes"]),
				enc: atoi64(r["enc_median_ns"]), decGo: atoi64(r["dec_go_median_ns"]), decC: atoi64(r["dec_c_median_ns"]),
				encRuns: atoi(r["enc_runs"]), allocB: atoi64(r["enc_alloc_bytes"]), allocCount: atoi64(r["enc_allocs"])}
		}
	}
	return cells
}

func glob(dir, pat string) []string {
	m, err := filepath.Glob(filepath.Join(dir, pat))
	if err != nil {
		log.Fatal(err)
	}
	sort.Strings(m)
	return m
}

func readCSV(path string) []map[string]string {
	f, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		log.Fatalf("%s: %v", path, err)
	}
	if len(recs) == 0 {
		return nil
	}
	var out []map[string]string
	for _, r := range recs[1:] {
		m := map[string]string{}
		for i, h := range recs[0] {
			if i < len(r) {
				m[h] = r[i]
			}
		}
		out = append(out, m)
	}
	return out
}

func writeCSV(name string, rows [][]string) {
	f, err := os.Create(filepath.Join(*dir, name))
	if err != nil {
		log.Fatal(err)
	}
	w := csv.NewWriter(f)
	w.WriteAll(rows)
	if err := w.Error(); err != nil {
		log.Fatal(err)
	}
	f.Close()
}

func atoi(s string) int     { n, _ := strconv.Atoi(s); return n }
func atoi64(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }
