package ojson

import "unsafe"

// bytesView returns b as a string without copying. The caller must not
// modify b while the string is in use.
func bytesView(b []byte) string { return unsafe.String(unsafe.SliceData(b), len(b)) }

// stringBytes returns s as a byte slice without copying. It must not be
// modified.
func stringBytes(s string) []byte { return unsafe.Slice(unsafe.StringData(s), len(s)) }
