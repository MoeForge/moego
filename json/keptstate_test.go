package json

import (
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"
)

type keptRaw struct {
	Model string     `json:"model"`
	Meta  RawMessage `json:"metadata"`
	Text  RawMessage `json:"text"`
	Tools RawMessage `json:"tools"`
	Input RawMessage `json:"input"`
}

// TestDecodeStateSize checks that a decoder state, with the header the
// allocator puts before it, fits a block of 3456 bytes on 64-bit
// platforms, and does not take one of 4096.
func TestDecodeStateSize(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("the size is checked on 64-bit platforms")
	}
	if n := unsafe.Sizeof(decodeState{}); n > 3448 {
		t.Errorf("a decodeState takes %d bytes, more than 3448", n)
	}
}

// TestKeptStateHoldsNoInput checks that the decoder state kept for later
// calls does not keep the input of the last call alive, whichever
// validator read it.
func TestKeptStateHoldsNoInput(t *testing.T) {
	long := strings.Repeat("x", 300)
	cases := []struct {
		name string
		doc  string
		dec  func([]byte) error
	}{
		{"tables", `{"a":[1,2,{"b":"c"}],"d":"` + long + `"}`, func(b []byte) error {
			// A target that is not zero is validated with tables first.
			m := map[string]any{"z": 1}
			return Unmarshal(b, &m)
		}},
		{"rest", `{"model":"m","metadata":{"a":1},"text":{"b":2},"tools":[{"c":3}],"input":[{"d":"` + long + `"}]}`, func(b []byte) error {
			// RawMessage members validate the rest of the input at once.
			var v keptRaw
			return Unmarshal(b, &v)
		}},
	}
	for _, c := range cases {
		freed := make(chan struct{})
		func() {
			b := []byte(c.doc)
			runtime.SetFinalizer(&b[0], func(*byte) { close(freed) })
			for range 3 {
				if err := c.dec(b); err != nil {
					t.Fatal(err)
				}
			}
		}()
		ok := false
		for range 100 {
			runtime.GC()
			select {
			case <-freed:
				ok = true
			case <-time.After(10 * time.Millisecond):
			}
			if ok {
				break
			}
		}
		if !ok {
			t.Errorf("%s: the input is still alive after Unmarshal returned", c.name)
		}
	}
}
