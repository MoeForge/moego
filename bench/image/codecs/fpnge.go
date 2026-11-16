//go:build cgo && amd64 && fpnge

package codecs

/*
#cgo LDFLAGS: -lfpnge -lstdc++ -lm
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <fpnge.h>

// bench_fpnge_encode encodes 8-bit RGB (opaque, alpha dropped here) or RGBA.
// level follows FPNGEFillOptions in fpnge.h (an inline function, so its
// settings are spelled out here): 1 = fixed Up predictor, 4 = default
// (best predictor per row, sampled Huffman statistics).
static int bench_fpnge_encode(const uint8_t *rgba, int w, int h, int stride, int opaque,
                              int level, uint8_t **out, size_t *outlen) {
	struct FPNGEOptions o;
	memset(&o, 0, sizeof o);
	o.huffman_sample = 1;
	o.cicp_colorspace = FPNGE_CICP_NONE;
	o.channel_order = FPNGE_ORDER_RGB;
	o.predictor = level == 1 ? FPNGE_PREDICTOR_FIXED_TOP : FPNGE_PREDICTOR_BEST;
	const size_t ch = opaque ? 3 : 4;
	const uint8_t *data = rgba;
	uint8_t *rgb = NULL;
	size_t rs = stride;
	if (opaque) {
		rs = (size_t)w * 3;
		rgb = malloc(rs * (size_t)h);
		if (!rgb) return -1;
		for (int y = 0; y < h; y++) {
			const uint8_t *s = rgba + (size_t)y * stride;
			uint8_t *d = rgb + (size_t)y * rs;
			for (int x = 0; x < w; x++) memcpy(d + 3 * x, s + 4 * x, 3);
		}
		data = rgb;
	}
	// FPNGEOutputAllocSize, also inline in fpnge.h.
	size_t cap = 1024 + (2 * ch * (size_t)w + 1) * (size_t)h;
	uint8_t *buf = malloc(cap);
	if (!buf) { free(rgb); return -2; }
	size_t n = FPNGEEncode(1, ch, data, w, rs, h, buf, &o);
	free(rgb);
	if (n == 0) { free(buf); return -3; }
	*out = buf;
	*outlen = n;
	return 0;
}
*/
import "C"

import (
	"fmt"
	"image"
	"unsafe"
)

// fpngeCommit is the github.com/veluca93/fpnge commit sysroot.sh builds.
const fpngeCommit = "097ebccf3d3db443d9a8f238b1deed010a82f961"

func fpngeEncode(m *image.NRGBA, level int) ([]byte, error) {
	op := C.int(0)
	if Opaque(m) {
		op = 1
	}
	var out *C.uint8_t
	var n C.size_t
	rc := C.bench_fpnge_encode((*C.uint8_t)(unsafe.Pointer(&m.Pix[0])), C.int(m.Rect.Dx()), C.int(m.Rect.Dy()), C.int(m.Stride), op, C.int(level), &out, &n)
	if rc != 0 {
		return nil, fmt.Errorf("fpnge encode error %d", int(rc))
	}
	b := C.GoBytes(unsafe.Pointer(out), C.int(n))
	C.free(unsafe.Pointer(out))
	return b, nil
}

func init() {
	lib := "fpnge " + fpngeCommit[:12] + " (C++, g++ 14 -O2 -mavx2 -mbmi2 -mpclmul, static, cgo)"
	for _, l := range []struct {
		s     string
		level int
	}{{"fast", 1}, {"default", 4}} {
		addEncoder(&Encoder{Name: "fpnge", Setting: l.s, Format: "png", Lib: lib,
			Note:   fmt.Sprintf("FPNGEFillOptions level %d, RGB if opaque else RGBA", l.level),
			Encode: func(m *image.NRGBA) ([]byte, error) { return fpngeEncode(m, l.level) }})
	}
}
