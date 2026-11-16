//go:build cgo

package codecs

/*
#cgo LDFLAGS: -ldeflate
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <libdeflate.h>

static void bench_put32(uint8_t *p, uint32_t v) {
	p[0] = v >> 24; p[1] = v >> 16; p[2] = v >> 8; p[3] = v;
}

static uint32_t bench_sumabs(const uint8_t *p, size_t n) {
	uint32_t s = 0;
	for (size_t i = 0; i < n; i++) s += p[i] < 128 ? p[i] : 256 - p[i];
	return s;
}

static uint8_t bench_paeth(int a, int b, int c) {
	int p = a + b - c;
	int pa = abs(p - a), pb = abs(p - b), pc = abs(p - c);
	if (pa <= pb && pa <= pc) return a;
	if (pb <= pc) return b;
	return c;
}

// bench_ldpng_encode writes an 8-bit RGB (opaque) or RGBA PNG: each row
// gets the filter with the least sum of absolute values of its bytes as
// signed numbers (libpng's and image/png's heuristic, ties to the lower
// filter type), and the filtered rows go through libdeflate_zlib_compress
// into a single IDAT chunk.
static int bench_ldpng_encode(const uint8_t *rgba, int w, int h, int stride, int opaque,
                              int level, uint8_t **out, size_t *outlen) {
	const size_t bpp = opaque ? 3 : 4, rowlen = (size_t)w * bpp, n = (rowlen + 1) * (size_t)h;
	uint8_t *raw = malloc(n), *rows = malloc(7 * rowlen);
	if (!raw || !rows) { free(raw); free(rows); return -1; }
	uint8_t *prev = rows, *cur = rows + rowlen, *cand = rows + 2 * rowlen;
	memset(prev, 0, rowlen);
	for (int y = 0; y < h; y++) {
		const uint8_t *src = rgba + (size_t)y * stride;
		if (opaque) {
			for (int x = 0; x < w; x++) memcpy(cur + 3 * x, src + 4 * x, 3);
		} else {
			memcpy(cur, src, rowlen);
		}
		uint8_t *f[5] = {cur, cand, cand + rowlen, cand + 2 * rowlen, cand + 3 * rowlen};
		for (size_t i = 0; i < rowlen; i++) {
			int a = i >= bpp ? cur[i - bpp] : 0, b = prev[i], c = i >= bpp ? prev[i - bpp] : 0;
			f[1][i] = cur[i] - a;
			f[2][i] = cur[i] - b;
			f[3][i] = cur[i] - ((a + b) >> 1);
			f[4][i] = cur[i] - bench_paeth(a, b, c);
		}
		int best = 0;
		uint32_t min = bench_sumabs(f[0], rowlen);
		for (int t = 1; t < 5; t++) {
			uint32_t s = bench_sumabs(f[t], rowlen);
			if (s < min) { min = s; best = t; }
		}
		uint8_t *dst = raw + (size_t)y * (rowlen + 1);
		dst[0] = best;
		memcpy(dst + 1, f[best], rowlen);
		uint8_t *t = prev; prev = cur; cur = t;
	}
	free(rows);

	struct libdeflate_compressor *c = libdeflate_alloc_compressor(level);
	if (!c) { free(raw); return -2; }
	size_t bound = libdeflate_zlib_compress_bound(c, n);
	uint8_t *png = malloc(8 + 25 + 12 + bound + 12);
	if (!png) { libdeflate_free_compressor(c); free(raw); return -3; }
	static const uint8_t sig[8] = {0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'};
	uint8_t *p = png;
	memcpy(p, sig, 8); p += 8;
	bench_put32(p, 13); memcpy(p + 4, "IHDR", 4);
	bench_put32(p + 8, w); bench_put32(p + 12, h);
	p[16] = 8; p[17] = opaque ? 2 : 6; p[18] = 0; p[19] = 0; p[20] = 0;
	bench_put32(p + 21, libdeflate_crc32(0, p + 4, 17)); p += 25;
	size_t z = libdeflate_zlib_compress(c, raw, n, p + 8, bound);
	libdeflate_free_compressor(c);
	free(raw);
	if (z == 0) { free(png); return -4; }
	bench_put32(p, z); memcpy(p + 4, "IDAT", 4);
	bench_put32(p + 8 + z, libdeflate_crc32(0, p + 4, 4 + z)); p += 12 + z;
	bench_put32(p, 0); memcpy(p + 4, "IEND", 4); bench_put32(p + 8, 0xae426082); p += 12;
	*out = png;
	*outlen = p - png;
	return 0;
}
*/
import "C"

import (
	"fmt"
	"image"
	"unsafe"
)

func libdeflatePNGEncode(m *image.NRGBA, level int) ([]byte, error) {
	op := C.int(0)
	if Opaque(m) {
		op = 1
	}
	var out *C.uint8_t
	var n C.size_t
	rc := C.bench_ldpng_encode((*C.uint8_t)(unsafe.Pointer(&m.Pix[0])), C.int(m.Rect.Dx()), C.int(m.Rect.Dy()), C.int(m.Stride), op, C.int(level), &out, &n)
	if rc != 0 {
		return nil, fmt.Errorf("libdeflate png encode error %d", int(rc))
	}
	b := C.GoBytes(unsafe.Pointer(out), C.int(n))
	C.free(unsafe.Pointer(out))
	return b, nil
}

func init() {
	lib := "libdeflate " + C.LIBDEFLATE_VERSION_STRING + " (cgo, system shared library) with a PNG writer in C in bench/image"
	for _, level := range []int{1, 6, 9, 12} {
		addEncoder(&Encoder{Name: "libdeflate-png", Setting: fmt.Sprintf("l%d", level), Format: "png", Lib: lib,
			Note:   "per-row filter by least sum of absolute differences (as libpng), one IDAT, RGB if opaque else RGBA",
			Encode: func(m *image.NRGBA) ([]byte, error) { return libdeflatePNGEncode(m, level) }})
	}
}
