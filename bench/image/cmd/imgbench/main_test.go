package main

import (
	"encoding/binary"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestShardOf(t *testing.T) {
	dir := t.TempDir()
	*corpusDir = dir
	sizes := []int{100, 10, 400, 30, 200, 5}
	var entries []corpusEntry
	for i, px := range sizes {
		name := filepath.Join(dir, string(rune('a'+i))+".png")
		var b [24]byte
		binary.BigEndian.PutUint32(b[16:], uint32(px))
		binary.BigEndian.PutUint32(b[20:], 1)
		if err := os.WriteFile(name, b[:], 0o644); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, corpusEntry{rel: filepath.Base(name), index: i})
	}
	all := func(corpusEntry) bool { return true }
	seen := map[int]int{}
	loads := []int{}
	for k := 0; k < 3; k++ {
		load := 0
		for _, e := range shardOf(entries, k, 3, all) {
			seen[e.index]++
			load += sizes[e.index]
		}
		loads = append(loads, load)
	}
	for i := range sizes {
		if seen[i] != 1 {
			t.Fatalf("image %d in %d shards", i, seen[i])
		}
	}
	// largest first: 400 | 200 | 100, then 30, 10, 5 onto the lightest.
	if loads[0] != 400 || loads[1] != 200 || loads[2] != 145 {
		t.Fatalf("loads = %v", loads)
	}
	none := shardOf(entries, 0, 2, func(e corpusEntry) bool { return e.index != 2 })
	for _, e := range none {
		if e.index == 2 {
			t.Fatal("image without work was assigned")
		}
	}
}

func TestStats(t *testing.T) {
	m := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	copy(m.Pix, []byte{1, 2, 3, 0, 1, 2, 3, 0, 9, 9, 9, 255})
	colors, alpha0 := stats(m)
	if colors != 2 || alpha0 != 2 {
		t.Fatalf("stats = %d, %d", colors, alpha0)
	}
}

func TestSeries(t *testing.T) {
	s := series{3 * time.Millisecond, 1 * time.Millisecond, 2 * time.Millisecond, 10 * time.Millisecond}
	if s.median() != int64(2500*time.Microsecond) || s.min() != int64(time.Millisecond) {
		t.Fatalf("median %d min %d", s.median(), s.min())
	}
	if (series{}).median() != 0 {
		t.Fatal("empty median")
	}
}

func TestSrcType(t *testing.T) {
	b := make([]byte, 26)
	b[24], b[25] = 8, 6
	if got := srcType(b); got != "rgba8" {
		t.Fatalf("srcType = %q", got)
	}
}
