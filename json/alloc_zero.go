//go:build (!amd64 && !arm64) || purego || moego_zeroalloc

package json

import (
	"reflect"
	"unsafe"
)

// makeNoZero uses an ordinary zeroed allocation for builds without the
// runtime allocator linkname.
func makeNoZero(n int) []byte { return make([]byte, n) }

func newObject(t reflect.Type, rt unsafe.Pointer, size uintptr) unsafe.Pointer {
	return reflect.New(t).UnsafePointer()
}

func newArray(t reflect.Type, rt unsafe.Pointer, size uintptr, n int) unsafe.Pointer {
	return reflect.MakeSlice(reflect.SliceOf(t), n, n).UnsafePointer()
}

// copyElems copies n elements of the slice plan pl from src to dst.
func copyElems(pl *decPlan, dst, src unsafe.Pointer, n int) {
	reflect.Copy(sliceAt(pl, dst, n), sliceAt(pl, src, n))
}

// clearElems zeroes n elements of the slice plan pl at p.
func clearElems(pl *decPlan, p unsafe.Pointer, n int) {
	if n > 0 {
		sliceAt(pl, p, n).Clear()
	}
}

func sliceAt(pl *decPlan, p unsafe.Pointer, n int) reflect.Value {
	return reflect.NewAt(pl.typ, unsafe.Pointer(&sliceHeader{p, n, n})).Elem()
}
