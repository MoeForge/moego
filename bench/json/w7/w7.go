// Package w7 measures one disk scenario in a process of its own, and prints
// its time, the peak of the live heap above the start, and the peak resident
// set.
//
// Each engine has a command of its own in cmd/w7, which links that engine
// and no other, as a program that uses it would:
//
//	go build -o w7-moego ./cmd/w7/moego
//	w7-moego -scenario gen -size 64 -file /tmp/body.json
//	w7-moego -scenario decoder -size 64 -file /tmp/body.json
//
// Scenarios:
//
//	gen        writes the request body of size MiB to the file
//	decoder    decodes the file with the engine's Decoder
//	bytes      reads the file into memory and calls the engine's Unmarshal
//	marshal    marshals the request read from the file (the time and heap
//	           counted start after the decode)
//	marshalp   the same for the messages alone, which have no MarshalJSON
//	           method of their own around the whole output
//	valid      reads the file into memory and calls the engine's Valid
//
// and the engine's own scenarios (Engine.Own): cmd/w7/ojson has validr,
// getmany and getmanyr.
package w7

//go:generate sh -c "{ echo '// Code generated from ../codec/dto.go by go generate. DO NOT EDIT.'; echo; sed 's/^package codec$/package w7/' ../codec/dto.go; } > dto.go"

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MoeForge/moego/bench/json/race"
)

// A Decoder decodes one value from a stream.
type Decoder interface{ Decode(v any) error }

// An Engine is the one JSON implementation that a command links. Its
// functions are nil for the scenarios it does not run.
type Engine struct {
	Name       string
	Marshal    func(v any) ([]byte, error)
	Unmarshal  func(data []byte, v any) error
	Valid      func(data []byte) bool
	NewDecoder func(r io.Reader) Decoder
	// Own are scenarios of the engine's own API, by name. Each works on the
	// file and returns what it decoded, which stays alive until the
	// measurement ends.
	Own map[string]func(file string) any
}

// Use is the engine that the types' own MarshalJSON and UnmarshalJSON
// methods call.
var Use Engine

// sampler follows the live heap. It allocates nothing once started: what
// it allocated per sample would be counted in the peak it measures.
type sampler struct {
	stop, done chan struct{}
	peak       atomic.Uint64
}

const (
	heapObjects = "/memory/classes/heap/objects:bytes"
	sampleEvery = 200 * time.Microsecond
)

func heapNow(s []metrics.Sample) uint64 {
	metrics.Read(s)
	return s[0].Value.Uint64()
}

func startSampler() *sampler {
	sm := &sampler{stop: make(chan struct{}), done: make(chan struct{})}
	s := []metrics.Sample{{Name: heapObjects}}
	t := time.NewTimer(sampleEvery)
	go func() {
		defer close(sm.done)
		defer t.Stop()
		for {
			if h := heapNow(s); h > sm.peak.Load() {
				sm.peak.Store(h)
			}
			select {
			case <-sm.stop:
				return
			case <-t.C:
				t.Reset(sampleEvery)
			}
		}
	}()
	return sm
}

func vmHWM() string {
	b, _ := os.ReadFile("/proc/self/status")
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "VmHWM:") {
			return strings.Join(strings.Fields(l)[1:], "")
		}
	}
	return "?"
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Main runs the scenario the flags name with e and prints its line.
func Main(e Engine) {
	scenario := flag.String("scenario", "decoder", "the scenario")
	size := flag.Int("size", 64, "the size of the body in MiB")
	file := flag.String("file", "", "the body file")
	flag.Parse()

	if *scenario == "gen" {
		f, err := os.Create(*file)
		must(err)
		w := bufio.NewWriter(f)
		_, err = w.Write(race.Body(*size << 20))
		must(err)
		must(w.Flush())
		must(f.Close())
		return
	}

	Use = e
	var run func()
	var req GeneralOpenAIRequest
	var keep any
	switch {
	case *scenario == "decoder" && e.NewDecoder != nil:
		run = func() {
			f, err := os.Open(*file)
			must(err)
			defer f.Close()
			must(e.NewDecoder(f).Decode(&req))
		}
	case *scenario == "bytes" && e.Unmarshal != nil:
		run = func() {
			b, err := os.ReadFile(*file)
			must(err)
			must(e.Unmarshal(b, &req))
		}
	case *scenario == "marshal" && e.Marshal != nil:
		b, err := os.ReadFile(*file)
		must(err)
		must(e.Unmarshal(b, &req))
		b = nil
		run = func() {
			out, err := e.Marshal(&req)
			must(err)
			keep = out
		}
	case *scenario == "marshalp" && e.Marshal != nil:
		b, err := os.ReadFile(*file)
		must(err)
		must(e.Unmarshal(b, &req))
		b = nil
		run = func() {
			out, err := e.Marshal(req.Messages)
			must(err)
			keep = out
		}
	case *scenario == "valid" && e.Valid != nil:
		run = func() {
			b, err := os.ReadFile(*file)
			must(err)
			if !e.Valid(b) {
				must(fmt.Errorf("not valid"))
			}
		}
	case e.Own[*scenario] != nil:
		own := e.Own[*scenario]
		run = func() { keep = own(*file) }
	default:
		must(fmt.Errorf("%s has no scenario %q", e.Name, *scenario))
	}

	runtime.GC()
	debug.FreeOSMemory()
	s := []metrics.Sample{{Name: heapObjects}}
	base := heapNow(s)
	sm := startSampler()
	start := time.Now()
	run()
	elapsed := time.Since(start)
	close(sm.stop)
	<-sm.done
	runtime.KeepAlive(&req)
	runtime.KeepAlive(keep)
	peak := sm.peak.Load()
	if peak < base {
		peak = base
	}
	fmt.Printf("W7/%s/%dMiB/%s ms=%.1f peakMiB=%.1f hwm=%s\n", *scenario, *size, e.Name,
		float64(elapsed.Microseconds())/1000, float64(peak-base)/(1<<20), vmHWM())
}
