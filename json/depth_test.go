package json

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

type deepNode struct {
	Next *deepNode `json:"n,omitempty"`
}

type (
	anyBox    struct{ V any }
	sliceNode struct{ K []sliceNode }
	mapNode   map[string]mapNode
	textMap   map[depthKey]any
	arrayNode [1]*arrayNode
)

// depthKey is a map key with a MarshalText method.
type depthKey struct{ a, b string }

func (k depthKey) MarshalText() ([]byte, error) { return []byte(k.a + "-" + k.b), nil }

// checkptrEnabled reports whether the test binary was built with checkptr
// instrumentation, which the race detector also turns on. Test binaries
// of some toolchains, such as Go 1.23, do not record their build settings,
// so a child process tells: it converts an odd address in the heap to a
// pointer to a pointer, which checkptr stops with a fatal error.
var checkptrEnabled = sync.OnceValue(func() bool {
	cmd := exec.Command(os.Args[0], "-test.run=^TestCheckptrProbe$")
	cmd.Env = append(os.Environ(), "MOEGO_JSON_CHECKPTR_CHILD=1")
	out, err := cmd.CombinedOutput()
	return err != nil && bytes.Contains(out, []byte("checkptr"))
})

// checkptrSink and checkptrPtr keep the probe's allocation in the heap
// and its conversion in the code.
var (
	checkptrSink []byte
	checkptrPtr  *unsafe.Pointer
)

// TestCheckptrProbe is the child process of checkptrEnabled. In the test
// process, it checks checkptrEnabled against the build settings, when the
// test binary records them, and against the race detector.
func TestCheckptrProbe(t *testing.T) {
	if os.Getenv("MOEGO_JSON_CHECKPTR_CHILD") != "" {
		checkptrSink = make([]byte, 64)
		checkptrPtr = (*unsafe.Pointer)(unsafe.Pointer(&checkptrSink[1]))
		return
	}
	got := checkptrEnabled()
	if raceEnabled && !got {
		t.Fatal("checkptrEnabled is false under the race detector")
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok || len(bi.Settings) == 0 {
		return
	}
	want := raceEnabled
	for _, s := range bi.Settings {
		if s.Key == "-gcflags" && strings.Contains(s.Value, "checkptr") {
			want = true
		}
	}
	if got != want {
		t.Fatalf("checkptrEnabled is %v, the build settings say %v", got, want)
	}
}

// TestMarshalDepthLimit checks that values nested deeper than
// maxEncodeDepth fail with an UnsupportedValueError before they exhaust
// the default stack limit. It runs in a child process, which uses up to
// about 1 GB of stack. On encoding/json/v2, which writes at most
// maxNestingDepth levels of objects and arrays, they fail with a
// SyntaxError.
func TestMarshalDepthLimit(t *testing.T) {
	if os.Getenv("MOEGO_JSON_DEPTH_CHILD") == "" {
		if testing.Short() {
			t.Skip("deep values take a large stack")
		}
		if raceEnabled || checkptrEnabled() {
			t.Skip("the race detector and checkptr make frames larger")
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestMarshalDepthLimit$", "-test.v")
		cmd.Env = append(os.Environ(), "MOEGO_JSON_DEPTH_CHILD=1")
		out, err := cmd.CombinedOutput()
		if ee := (*exec.ExitError)(nil); err != nil && !errors.As(err, &ee) {
			// As under user-space emulation, which runs only the test.
			t.Skipf("the test binary cannot start itself: %v", err)
		}
		if err != nil || !bytes.Contains(out, []byte("--- PASS: TestMarshalDepthLimit")) {
			if len(out) > 4000 {
				out = out[:4000]
			}
			t.Fatalf("child: %v\n%s", err, out)
		}
		return
	}
	tooDeep := func(name string, v any) {
		t.Helper()
		_, err := Marshal(v)
		if jsonv2 {
			var se *SyntaxError
			if !errors.As(err, &se) || se.Error() != "exceeded max depth" {
				t.Errorf("%s: Marshal error %v, want a SyntaxError for the depth", name, err)
			}
			return
		}
		var uv *UnsupportedValueError
		if !errors.As(err, &uv) || !strings.Contains(uv.Str, "nested more than 1048576 levels deep") {
			t.Errorf("%s: Marshal error %v, want an UnsupportedValueError for the depth", name, err)
		}
	}
	const n = maxEncodeDepth + 1

	var a any = []any{}
	for i := 0; i < n; i++ {
		a = []any{a}
	}
	tooDeep("[]any", a)

	var m any = map[string]any{}
	for i := 0; i < n; i++ {
		m = map[string]any{"k": m}
	}
	tooDeep("map[string]any", m)

	var node *deepNode
	for i := 0; i < n; i++ {
		node = &deepNode{Next: node}
	}
	tooDeep("*deepNode", node)

	var box any
	for i := 0; i < n; i++ {
		box = anyBox{V: box}
	}
	tooDeep("anyBox", box)

	sn := sliceNode{}
	for i := 0; i < n; i++ {
		sn = sliceNode{K: []sliceNode{sn}}
	}
	tooDeep("sliceNode", sn)

	mn := mapNode{}
	for i := 0; i < n; i++ {
		mn = mapNode{"k": mn}
	}
	tooDeep("mapNode", mn)

	var tm any = textMap{}
	for i := 0; i < n; i++ {
		tm = textMap{{"a", "b"}: tm}
	}
	tooDeep("textMap", tm)

	var an *arrayNode
	for i := 0; i < n; i++ {
		an = &arrayNode{an}
	}
	tooDeep("arrayNode", an)

	// Just under the limit encodes.
	limit := maxEncodeDepth
	if jsonv2 {
		limit = maxNestingDepth
	}
	var ok any = []any{}
	for i := 0; i < limit-1; i++ {
		ok = []any{ok}
	}
	out, err := Marshal(ok)
	if err != nil || len(out) != 2*limit {
		t.Errorf("%d levels: %d bytes, %v", limit, len(out), err)
	}
}
