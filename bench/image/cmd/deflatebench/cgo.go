//go:build cgo

package main

/*
#cgo LDFLAGS: -ldeflate -lz
#include <stdint.h>
#include <stdlib.h>
#include <libdeflate.h>
#include <zlib.h>

static struct libdeflate_compressor *bench_ld[13];

// bench_ld_compress compresses with a cached libdeflate compressor; 0 means
// dst was too small.
static size_t bench_ld_compress(int level, const uint8_t *src, size_t n, uint8_t *dst, size_t cap) {
	if (!bench_ld[level]) bench_ld[level] = libdeflate_alloc_compressor(level);
	if (!bench_ld[level]) return 0;
	return libdeflate_zlib_compress(bench_ld[level], src, n, dst, cap);
}

static size_t bench_ld_bound(int level, size_t n) {
	if (!bench_ld[level]) bench_ld[level] = libdeflate_alloc_compressor(level);
	return libdeflate_zlib_compress_bound(bench_ld[level], n);
}

static size_t bench_z_compress(int level, const uint8_t *src, size_t n, uint8_t *dst, size_t cap) {
	uLongf out = cap;
	if (compress2(dst, &out, src, n, level) != Z_OK) return 0;
	return out;
}
*/
import "C"

import (
	"fmt"
	"log"
	"unsafe"
)

func cBuf(dst []byte, n int) []byte {
	if cap(dst) < n {
		dst = make([]byte, n)
	}
	return dst[:n]
}

func init() {
	ldLib := "libdeflate " + C.LIBDEFLATE_VERSION_STRING + " libdeflate_zlib_compress (cgo)"
	for _, level := range []int{1, 6, 9, 12} {
		addEngine(&engine{name: fmt.Sprintf("libdeflate/l%d", level), lib: ldLib,
			compress: func(dst, src []byte, bpp, stride int) []byte {
				dst = cBuf(dst, int(C.bench_ld_bound(C.int(level), C.size_t(len(src)))))
				n := C.bench_ld_compress(C.int(level), (*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(src))), C.size_t(len(src)),
					(*C.uint8_t)(unsafe.Pointer(&dst[0])), C.size_t(len(dst)))
				if n == 0 {
					log.Fatal("libdeflate_zlib_compress failed")
				}
				return dst[:n]
			}})
	}
	zLib := "zlib " + C.ZLIB_VERSION + " compress2 (cgo)"
	for _, level := range []int{1, 6, 9} {
		addEngine(&engine{name: fmt.Sprintf("zlib-c/l%d", level), lib: zLib,
			compress: func(dst, src []byte, bpp, stride int) []byte {
				dst = cBuf(dst, int(C.compressBound(C.uLong(len(src)))))
				n := C.bench_z_compress(C.int(level), (*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(src))), C.size_t(len(src)),
					(*C.uint8_t)(unsafe.Pointer(&dst[0])), C.size_t(len(dst)))
				if n == 0 {
					log.Fatal("compress2 failed")
				}
				return dst[:n]
			}})
	}
}
