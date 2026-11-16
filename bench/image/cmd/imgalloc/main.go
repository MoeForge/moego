// Command imgalloc measures the Go heap allocations of one encode per
// corpus image, for moego/png, image/png, moego/webp and deepteams/webp at
// each compression level, without the noise of imgbench's allocation
// column: two GCs before every encode empty the moego encoders' internal
// buffer pools (cold), and the output goes to io.Discard. A second
// measurement encodes twice with an EncoderBufferPool and counts the second
// encode (warm); deepteams/webp has no buffer pool, so it has no warm
// column.
//
//	go run ./cmd/imgalloc -corpus DIR/images -list corpus.txt
//
// DIR is a corpus directory written by fetch_corpus.sh.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/draw"
	stdpng "image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	moegopng "github.com/MoeForge/moego/png"
	moegowebp "github.com/MoeForge/moego/webp"
	deepteams "github.com/deepteams/webp"
)

var (
	corpusDir = flag.String("corpus", "", "directory the corpus list's paths are relative to")
	listFile  = flag.String("list", "corpus.txt", "corpus list: lines of `sha256  relpath`")
)

type pool struct{ b *moegopng.EncoderBuffer }

func (p *pool) Get() *moegopng.EncoderBuffer  { return p.b }
func (p *pool) Put(b *moegopng.EncoderBuffer) { p.b = b }

type webpPool struct{ b *moegowebp.EncoderBuffer }

func (p *webpPool) Get() *moegowebp.EncoderBuffer  { return p.b }
func (p *webpPool) Put(b *moegowebp.EncoderBuffer) { p.b = b }

type stdPool struct{ b *stdpng.EncoderBuffer }

func (p *stdPool) Get() *stdpng.EncoderBuffer  { return p.b }
func (p *stdPool) Put(b *stdpng.EncoderBuffer) { p.b = b }

// allocs returns the heap bytes and objects f allocates after two GCs.
func allocs(f func() error) (uint64, uint64) {
	runtime.GC()
	runtime.GC()
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	if err := f(); err != nil {
		log.Fatal(err)
	}
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc, b.Mallocs - a.Mallocs
}

type setting struct {
	name  string
	cold  func(m image.Image) error // a fresh encode with an empty pool
	empty func()                    // empties the warm encoder's pool
	warm  func(m image.Image) error // an encode with the warm encoder's pool; nil if there is none
}

func main() {
	flag.Parse()
	runtime.GOMAXPROCS(1)
	if *corpusDir == "" {
		log.Fatal("need -corpus")
	}
	f, err := os.Open(*listFile)
	if err != nil {
		log.Fatal(err)
	}
	var rels []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		_, rel, ok := strings.Cut(line, "  ")
		if !ok {
			log.Fatalf("bad list line %q", line)
		}
		rels = append(rels, rel)
	}
	f.Close()

	var settings []setting
	for _, l := range []struct {
		name   string
		moego  moegopng.CompressionLevel
		std    stdpng.CompressionLevel
		webp   moegowebp.CompressionLevel
		z      string // libwebp's matching lossless preset
		preset [2]int // its method and quality (WebPConfigLosslessPreset)
	}{{"speed", moegopng.BestSpeed, stdpng.BestSpeed, moegowebp.BestSpeed, "z0", [2]int{0, 0}},
		{"default", moegopng.DefaultCompression, stdpng.DefaultCompression, moegowebp.DefaultCompression, "z6-default", [2]int{4, 75}},
		{"best", moegopng.BestCompression, stdpng.BestCompression, moegowebp.BestCompression, "z9", [2]int{6, 100}}} {
		for _, keep := range []bool{true, false} {
			name := "moego-png-keep/" + l.name
			if !keep {
				name = "moego-png/" + l.name
			}
			cold := &moegopng.Encoder{CompressionLevel: l.moego, KeepColorType: keep}
			p := &pool{}
			warm := &moegopng.Encoder{CompressionLevel: l.moego, KeepColorType: keep, BufferPool: p}
			settings = append(settings, setting{name,
				func(m image.Image) error { return cold.Encode(io.Discard, m) },
				func() { p.b = nil },
				func(m image.Image) error { return warm.Encode(io.Discard, m) }})
		}
		cold := &stdpng.Encoder{CompressionLevel: l.std}
		p := &stdPool{}
		warm := &stdpng.Encoder{CompressionLevel: l.std, BufferPool: p}
		settings = append(settings, setting{"png-std/" + l.name,
			func(m image.Image) error { return cold.Encode(io.Discard, m) },
			func() { p.b = nil },
			func(m image.Image) error { return warm.Encode(io.Discard, m) }})
		wcold := &moegowebp.Encoder{CompressionLevel: l.webp}
		wp := &webpPool{}
		wwarm := &moegowebp.Encoder{CompressionLevel: l.webp, BufferPool: wp}
		settings = append(settings, setting{"moego-webp/" + l.name,
			func(m image.Image) error { return wcold.Encode(io.Discard, m) },
			func() { wp.b = nil },
			func(m image.Image) error { return wwarm.Encode(io.Discard, m) }})
		o := deepteams.DefaultOptions()
		o.Lossless, o.Exact = true, true
		o.Method, o.Quality = l.preset[0], float32(l.preset[1])
		settings = append(settings, setting{"deepteams/" + l.z,
			func(m image.Image) error { return deepteams.Encode(io.Discard, m, o) },
			nil, nil})
	}

	type total struct{ coldB, coldN, warmB, warmN, maxCold uint64 }
	totals := make([]total, len(settings))
	for _, rel := range rels {
		raw, err := os.ReadFile(filepath.Join(*corpusDir, rel))
		if err != nil {
			log.Fatal(err)
		}
		dec, err := stdpng.Decode(bytes.NewReader(raw))
		if err != nil {
			log.Fatalf("%s: %v", rel, err)
		}
		m := image.NewNRGBA(image.Rect(0, 0, dec.Bounds().Dx(), dec.Bounds().Dy()))
		draw.Draw(m, m.Rect, dec, dec.Bounds().Min, draw.Src)
		for i, s := range settings {
			b, n := allocs(func() error { return s.cold(m) })
			t := &totals[i]
			t.coldB += b
			t.coldN += n
			t.maxCold = max(t.maxCold, b)
			if s.warm == nil {
				continue
			}
			// The first warm encode fills the pool; count the second.
			s.empty()
			if err := s.warm(m); err != nil {
				log.Fatal(err)
			}
			b, n = allocs(func() error { return s.warm(m) })
			t.warmB += b
			t.warmN += n
		}
	}
	fmt.Printf("%d images, %s, GOMAXPROCS=1, output to io.Discard\n\n", len(rels), runtime.Version())
	fmt.Println("| encoder | cold MB | cold allocs | largest image, cold MB | warm (BufferPool) bytes | warm allocs |")
	fmt.Println("|---|--:|--:|--:|--:|--:|")
	for i, s := range settings {
		t := totals[i]
		warmB, warmN := strconv.FormatUint(t.warmB, 10), strconv.FormatUint(t.warmN, 10)
		if s.warm == nil {
			warmB, warmN = "–", "–"
		}
		fmt.Printf("| %s | %.1f | %d | %.2f | %s | %s |\n", s.name, float64(t.coldB)/1e6, t.coldN, float64(t.maxCold)/1e6, warmB, warmN)
	}
}
