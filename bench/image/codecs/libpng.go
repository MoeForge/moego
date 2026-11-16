//go:build cgo

package codecs

/*
#cgo LDFLAGS: -lpng16 -lz
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <setjmp.h>
#include <png.h>
#include <zlib.h>

typedef struct {
	uint8_t *buf;
	size_t len, cap;
	int oom;
} bench_membuf;

static void bench_png_write(png_structp png, png_bytep data, png_size_t n) {
	bench_membuf *m = (bench_membuf *)png_get_io_ptr(png);
	if (m->oom) return;
	if (m->len + n > m->cap) {
		size_t c = m->cap ? m->cap : 65536;
		while (c < m->len + n) c *= 2;
		uint8_t *p = realloc(m->buf, c);
		if (!p) { m->oom = 1; return; }
		m->buf = p;
		m->cap = c;
	}
	memcpy(m->buf + m->len, data, n);
	m->len += n;
}

static void bench_png_flush(png_structp png) { (void)png; }

// bench_png_encode writes 8-bit RGB (opaque) or RGBA with libpng's default
// adaptive filtering (filters < 0) or the given filter mask, at a zlib level,
// with libpng's default zlib strategy (strategy < 0: Z_FILTERED for
// filtered rows) or the given one.
static int bench_png_encode(const uint8_t *rgba, int w, int h, int stride, int opaque,
                            int level, int filters, int strategy, uint8_t **out, size_t *outlen) {
	bench_membuf m = {0};
	png_bytep *rows = malloc(sizeof(png_bytep) * (size_t)h);
	if (!rows) return -1;
	for (int y = 0; y < h; y++) rows[y] = (png_bytep)(rgba + (size_t)y * stride);
	png_structp png = png_create_write_struct(PNG_LIBPNG_VER_STRING, NULL, NULL, NULL);
	if (!png) { free(rows); return -2; }
	png_infop info = png_create_info_struct(png);
	if (!info) { png_destroy_write_struct(&png, NULL); free(rows); return -3; }
	if (setjmp(png_jmpbuf(png))) {
		png_destroy_write_struct(&png, &info);
		free(rows);
		free(m.buf);
		return -4;
	}
	png_set_write_fn(png, &m, bench_png_write, bench_png_flush);
	png_set_compression_level(png, level);
	if (filters >= 0) png_set_filter(png, PNG_FILTER_TYPE_BASE, filters);
	if (strategy >= 0) png_set_compression_strategy(png, strategy);
	png_set_IHDR(png, info, w, h, 8, opaque ? PNG_COLOR_TYPE_RGB : PNG_COLOR_TYPE_RGB_ALPHA,
	             PNG_INTERLACE_NONE, PNG_COMPRESSION_TYPE_BASE, PNG_FILTER_TYPE_BASE);
	png_write_info(png, info);
	if (opaque) png_set_filler(png, 0, PNG_FILLER_AFTER);
	png_write_image(png, rows);
	png_write_end(png, info);
	png_destroy_write_struct(&png, &info);
	free(rows);
	if (m.oom) { free(m.buf); return -5; }
	*out = m.buf;
	*outlen = m.len;
	return 0;
}

// bench_png_decode decodes to 8-bit non-premultiplied RGBA with the
// simplified API; out must hold w*h*4 bytes.
static int bench_png_info(const void *data, size_t n, int *w, int *h) {
	png_image img;
	memset(&img, 0, sizeof img);
	img.version = PNG_IMAGE_VERSION;
	if (!png_image_begin_read_from_memory(&img, data, n)) return -1;
	*w = img.width;
	*h = img.height;
	png_image_free(&img);
	return 0;
}

static int bench_png_decode(const void *data, size_t n, uint8_t *out, int stride) {
	png_image img;
	memset(&img, 0, sizeof img);
	img.version = PNG_IMAGE_VERSION;
	if (!png_image_begin_read_from_memory(&img, data, n)) return -1;
	img.format = PNG_FORMAT_RGBA;
	if (!png_image_finish_read(&img, NULL, out, stride, NULL)) {
		png_image_free(&img);
		return -2;
	}
	return 0;
}

static const char *bench_zlib_version(void) { return zlibVersion(); }
*/
import "C"

import (
	"errors"
	"fmt"
	"image"
	"unsafe"
)

func libpngEncode(m *image.NRGBA, level, filters, strategy int) ([]byte, error) {
	w, h := m.Rect.Dx(), m.Rect.Dy()
	op := C.int(0)
	if Opaque(m) {
		op = 1
	}
	var out *C.uint8_t
	var n C.size_t
	rc := C.bench_png_encode((*C.uint8_t)(unsafe.Pointer(&m.Pix[0])), C.int(w), C.int(h), C.int(m.Stride), op, C.int(level), C.int(filters), C.int(strategy), &out, &n)
	if rc != 0 {
		return nil, fmt.Errorf("libpng encode error %d", int(rc))
	}
	b := C.GoBytes(unsafe.Pointer(out), C.int(n))
	C.free(unsafe.Pointer(out))
	return b, nil
}

func libpngDecode(data []byte) (image.Image, error) {
	if len(data) == 0 {
		return nil, errors.New("libpng: empty input")
	}
	p := unsafe.Pointer(&data[0])
	var w, h C.int
	if C.bench_png_info(p, C.size_t(len(data)), &w, &h) != 0 {
		return nil, errors.New("libpng: png_image_begin_read_from_memory failed")
	}
	m := image.NewNRGBA(image.Rect(0, 0, int(w), int(h)))
	if C.bench_png_decode(p, C.size_t(len(data)), (*C.uint8_t)(unsafe.Pointer(&m.Pix[0])), C.int(m.Stride)) != 0 {
		return nil, errors.New("libpng: png_image_finish_read failed")
	}
	return m, nil
}

func init() {
	lib := fmt.Sprintf("libpng %s + zlib %s (cgo, system shared libraries)", C.GoString(C.png_get_libpng_ver(nil)), C.GoString(C.bench_zlib_version()))
	for _, l := range []struct {
		s     string
		level int
	}{{"z1", 1}, {"z6-default", 6}, {"z9", 9}} {
		addEncoder(&Encoder{Name: "libpng", Setting: l.s, Format: "png", Lib: lib,
			Note:   "libpng default adaptive filters, RGB if opaque else RGBA",
			Encode: func(m *image.NRGBA) ([]byte, error) { return libpngEncode(m, l.level, -1, -1) }})
	}
	addEncoder(&Encoder{Name: "libpng", Setting: "z9-defstrategy", Format: "png", Lib: lib,
		Note:   "libpng default adaptive filters, zlib Z_DEFAULT_STRATEGY instead of libpng's Z_FILTERED",
		Encode: func(m *image.NRGBA) ([]byte, error) { return libpngEncode(m, 9, -1, C.Z_DEFAULT_STRATEGY) }})
	addDecoder(&Decoder{Name: "c", Format: "png", Lib: lib, Decode: libpngDecode})
}
