package codec

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// sameJSON reports whether a and b encode the same value.
func sameJSON(a, b []byte) bool {
	var x, y any
	da := json.NewDecoder(bytes.NewReader(a))
	da.UseNumber()
	db := json.NewDecoder(bytes.NewReader(b))
	db.UseNumber()
	if da.Decode(&x) != nil || db.Decode(&y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// checkOutput compares an engine's output with encoding/json's: byte for
// byte for engines that promise it, else as values.
func checkOutput(t *testing.T, e Engine, got, want []byte) {
	t.Helper()
	if e.Std && !bytes.Equal(got, want) || !e.Std && !sameJSON(got, want) {
		t.Errorf("%s output differs from encoding/json\n got %.300s\nwant %.300s", e.Name, got, want)
	}
}

func decodeWith[T any](t *testing.T, e Engine, body []byte) T {
	t.Helper()
	Use = e
	var v T
	if err := e.Unmarshal(body, &v); err != nil {
		t.Fatalf("%s: %v", e.Name, err)
	}
	return v
}

func encodeWith(t *testing.T, e Engine, v any) []byte {
	t.Helper()
	Use = e
	out, err := e.Marshal(v)
	if err != nil {
		t.Fatalf("%s: %v", e.Name, err)
	}
	return out
}

// TestOutputs checks that every engine decodes each workload to the value
// encoding/json decodes, and encodes it to the same JSON.
func TestOutputs(t *testing.T) {
	std := Engines[1]
	for _, sz := range Sizes {
		body := RequestBody(sz.N)
		want := decodeWith[GeneralOpenAIRequest](t, std, body)
		wantOut := encodeWith(t, std, want)
		for _, e := range DecodeEngines {
			got := decodeWith[GeneralOpenAIRequest](t, e, body)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("W1 %s %s: decoded value differs", sz.Name, e.Name)
			}
			checkOutput(t, e, encodeWith(t, e, want), wantOut)
		}
	}
	for _, c := range Chunks {
		want := decodeWith[ChatCompletionsStreamResponse](t, std, c.Body)
		wantOut := encodeWith(t, std, want)
		for _, e := range Engines {
			got := decodeWith[ChatCompletionsStreamResponse](t, e, c.Body)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("W3 %s %s: decoded value differs", c.Name, e.Name)
			}
			checkOutput(t, e, encodeWith(t, e, want), wantOut)
		}
	}
	for _, sz := range MapSizes {
		body := MapBody(sz.N)
		wantAny, err := AnyRoundTrip(std, body)
		if err != nil {
			t.Fatal(err)
		}
		wantAWS, err := AWSFlow(std, body)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range Engines {
			got, err := AnyRoundTrip(e, body)
			if err != nil {
				t.Fatalf("W4 any %s %s: %v", sz.Name, e.Name, err)
			}
			checkOutput(t, e, got, wantAny)
			got, err = AWSFlow(e, body)
			if err != nil {
				t.Fatalf("W4 aws %s %s: %v", sz.Name, e.Name, err)
			}
			checkOutput(t, e, got, wantAWS)
		}
	}
}

func BenchmarkW1(b *testing.B) {
	for _, sz := range Sizes {
		body := RequestBody(sz.N)
		for _, e := range DecodeEngines {
			b.Run(sz.Name+"/"+e.Name, func(b *testing.B) {
				Use = e
				b.SetBytes(int64(len(body)))
				run(b, func() error {
					var r GeneralOpenAIRequest
					return e.Unmarshal(body, &r)
				})
			})
		}
	}
}

func BenchmarkW3(b *testing.B) {
	for _, c := range Chunks {
		var v ChatCompletionsStreamResponse
		if err := json.Unmarshal(c.Body, &v); err != nil {
			b.Fatal(err)
		}
		for _, e := range Engines {
			b.Run("unmarshal/"+c.Name+"/"+e.Name, func(b *testing.B) {
				Use = e
				run(b, func() error {
					var r ChatCompletionsStreamResponse
					return e.Unmarshal(c.Body, &r)
				})
			})
			b.Run("marshal/"+c.Name+"/"+e.Name, func(b *testing.B) {
				Use = e
				run(b, func() error {
					_, err := e.Marshal(&v)
					return err
				})
			})
		}
	}
}

// BenchmarkW3m encodes the image requests of W1 and the long-context
// requests of W1t.
func BenchmarkW3m(b *testing.B) {
	bodies := []body{}
	for _, sz := range Sizes {
		bodies = append(bodies, body{sz.Name, RequestBody(sz.N)})
	}
	for _, body := range append(bodies, longChats...) {
		var r GeneralOpenAIRequest
		Use = Engines[1]
		if err := json.Unmarshal(body.Body, &r); err != nil {
			b.Fatal(err)
		}
		for _, e := range Engines {
			b.Run(body.Name+"/"+e.Name, func(b *testing.B) {
				Use = e
				run(b, func() error {
					_, err := e.Marshal(&r)
					return err
				})
			})
		}
	}
}

func BenchmarkW4(b *testing.B) {
	for _, sz := range MapSizes {
		body := MapBody(sz.N)
		for _, e := range Engines {
			b.Run("any/"+sz.Name+"/"+e.Name, func(b *testing.B) {
				b.SetBytes(int64(len(body)))
				run(b, func() error {
					_, err := AnyRoundTrip(e, body)
					return err
				})
			})
			b.Run("aws/"+sz.Name+"/"+e.Name, func(b *testing.B) {
				b.SetBytes(int64(len(body)))
				run(b, func() error {
					_, err := AWSFlow(e, body)
					return err
				})
			})
		}
	}
}
