package webp

import (
	"bytes"
	"os"
	"os/exec"
	"sync"
	"testing"
	"unsafe"
)

// checkptrEnabled reports whether the test binary was built with checkptr
// instrumentation, under which converted pointers escape and the
// allocation counts change. A child process converts an odd heap address
// to a pointer to a pointer, which checkptr stops with a fatal error.
var checkptrEnabled = sync.OnceValue(func() bool {
	cmd := exec.Command(os.Args[0], "-test.run=^TestCheckptrProbe$")
	cmd.Env = append(os.Environ(), "MOEGO_WEBP_CHECKPTR_CHILD=1")
	out, err := cmd.CombinedOutput()
	return err != nil && bytes.Contains(out, []byte("checkptr"))
})

var (
	checkptrSink []byte
	checkptrPtr  *unsafe.Pointer
)

// TestCheckptrProbe is the child process of checkptrEnabled; in the test
// process it checks the probe against the race detector, which turns
// checkptr on.
func TestCheckptrProbe(t *testing.T) {
	if os.Getenv("MOEGO_WEBP_CHECKPTR_CHILD") != "" {
		checkptrSink = make([]byte, 64)
		checkptrPtr = (*unsafe.Pointer)(unsafe.Pointer(&checkptrSink[1]))
		return
	}
	if raceEnabled && !checkptrEnabled() {
		t.Fatal("checkptrEnabled is false under the race detector")
	}
}
