package vp8lcheck

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MoeForge/moego/bench/image/codecs"
	"github.com/MoeForge/moego/internal/vp8l"
	"github.com/MoeForge/moego/internal/vp8l/vp8ltest"
)

var long = os.Getenv("MOEGO_WEBP_LONG") == "1"

func decoders(t *testing.T) []*codecs.Decoder {
	ds := codecs.Decoders("webp")
	names := ""
	for _, d := range ds {
		names += " " + d.Name
	}
	t.Logf("decoders:%s", names)
	if len(ds) < 2 && os.Getenv("VP8LCHECK_REQUIRE_C") == "1" {
		t.Fatal("libwebp decoder missing (build with cgo)")
	}
	return ds
}

// check decodes the payload with every decoder and compares with argb.
func check(t *testing.T, ds []*codecs.Decoder, name string, payload []byte, w, h int, argb []uint32) {
	t.Helper()
	file := vp8ltest.WebP(payload)
	for _, d := range ds {
		m, err := d.Decode(file)
		if err != nil {
			t.Fatalf("%s: %s decoder: %v", name, d.Name, err)
		}
		if b := m.Bounds(); b.Dx() != w || b.Dy() != h {
			t.Fatalf("%s: %s decoder: %v, want %dx%d", name, d.Name, b, w, h)
		}
		n, ok := m.(*image.NRGBA)
		if !ok {
			n = image.NewNRGBA(m.Bounds())
			draw.Draw(n, n.Rect, m, m.Bounds().Min, draw.Src)
		}
		for y := 0; y < h; y++ {
			row := n.Pix[(y)*n.Stride:]
			for x := 0; x < w; x++ {
				q := row[4*x : 4*x+4]
				got := uint32(q[3])<<24 | uint32(q[0])<<16 | uint32(q[1])<<8 | uint32(q[2])
				if want := argb[y*w+x]; got != want {
					t.Fatalf("%s: %s decoder: pixel (%d,%d) is %08x, want %08x", name, d.Name, x, y, got, want)
				}
			}
		}
	}
	// The test decoder must agree too.
	if _, _, px, err := vp8ltest.DecodeWebP(file); err != nil {
		t.Fatalf("%s: vp8ltest: %v", name, err)
	} else {
		for i := range px {
			if px[i] != argb[i] {
				t.Fatalf("%s: vp8ltest: pixel %d is %08x, want %08x", name, i, px[i], argb[i])
			}
		}
	}
}

// TestCases checks the round-trip cases of vp8ltest at every level.
func TestCases(t *testing.T) {
	ds := decoders(t)
	var e vp8l.Encoder
	for i, c := range vp8ltest.Cases(1, long) {
		for l := 0; l <= 9; l++ {
			if !long && (l+i)%3 != 0 {
				continue
			}
			out := vp8ltest.Encode(&e, l, c.ARGB, c.W, c.H, c.TS)
			check(t, ds, c.Name, out, c.W, c.H, c.ARGB)
		}
	}
}

// TestRandomSizes checks random images of random sizes at random levels.
func TestRandomSizes(t *testing.T) {
	ds := decoders(t)
	rng := rand.New(rand.NewSource(4))
	var e vp8l.Encoder
	for iter := 0; iter < 300; iter++ {
		w, h := 1+rng.Intn(200), 1+rng.Intn(60)
		var px []uint32
		switch iter % 4 {
		case 0:
			px = vp8ltest.Random(rng, w, h)
		case 1:
			px = vp8ltest.Smooth(rng, w, h)
		case 2:
			px = vp8ltest.FewColors(rng, w, h, 1+rng.Intn(20))
		default:
			px = vp8ltest.Runs(rng, w, h)
		}
		l := rng.Intn(10)
		out := vp8ltest.Encode(&e, l, px, w, h, nil)
		check(t, ds, "random", out, w, h, px)
	}
}

// TestSaved codes the main images of the WebP files in the directories
// that VP8LCHECK_FILES lists (separated by the path list separator, for
// example imgbench -save output) again at VP8LCHECK_LEVELS (default
// 1,6,9) and checks the streams with every decoder. It runs only when
// VP8LCHECK_FILES is set.
func TestSaved(t *testing.T) {
	dirs := os.Getenv("VP8LCHECK_FILES")
	if dirs == "" {
		t.Skip("VP8LCHECK_FILES not set")
	}
	levels := []int{1, 6, 9}
	if s := os.Getenv("VP8LCHECK_LEVELS"); s != "" {
		levels = levels[:0]
		for _, f := range strings.Split(s, ",") {
			l, err := strconv.Atoi(f)
			if err != nil {
				t.Fatal(err)
			}
			levels = append(levels, l)
		}
	}
	ds := decoders(t)
	var e vp8l.Encoder
	images, streams := 0, 0
	for _, dir := range filepath.SplitList(dirs) {
		names, err := filepath.Glob(filepath.Join(dir, "*.webp"))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			if len(data) < 20 {
				continue
			}
			n := int(binary.LittleEndian.Uint32(data[16:]))
			w, h, px, _, err := vp8ltest.DecodeMain(data[20 : 20+n])
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			images++
			for _, l := range levels {
				out := vp8ltest.Encode(&e, l, px, w, h, nil)
				check(t, ds, fmt.Sprintf("%s level %d", filepath.Base(name), l), out, w, h, px)
				streams++
			}
		}
	}
	t.Logf("%d main images, %d streams checked", images, streams)
}
