//go:build cgo

package codecs

/*
#cgo LDFLAGS: -lwebp -lsharpyuv -lm
#include <stdlib.h>
#include <webp/encode.h>
#include <webp/decode.h>

// bench_webp_encode encodes RGBA losslessly at a cwebp -z level.
// It returns 0 and a WebPMalloc'ed buffer, or a nonzero error code.
static int bench_webp_encode(const uint8_t *rgba, int w, int h, int stride,
                             int level, int exact, uint8_t **out, size_t *outlen) {
	WebPConfig cfg;
	if (!WebPConfigInit(&cfg)) return -1;
	if (!WebPConfigLosslessPreset(&cfg, level)) return -2;
	cfg.exact = exact;
	cfg.thread_level = 0;
	if (!WebPValidateConfig(&cfg)) return -3;
	WebPPicture pic;
	if (!WebPPictureInit(&pic)) return -4;
	pic.use_argb = 1;
	pic.width = w;
	pic.height = h;
	if (!WebPPictureImportRGBA(&pic, rgba, stride)) {
		WebPPictureFree(&pic);
		return -5;
	}
	WebPMemoryWriter wr;
	WebPMemoryWriterInit(&wr);
	pic.writer = WebPMemoryWrite;
	pic.custom_ptr = &wr;
	int ok = WebPEncode(&cfg, &pic);
	int code = pic.error_code;
	WebPPictureFree(&pic);
	if (!ok) {
		WebPMemoryWriterClear(&wr);
		return 100 + code;
	}
	*out = wr.mem;
	*outlen = wr.size;
	return 0;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"image"
	"unsafe"
)

func libwebpVersion() string {
	v := int(C.WebPGetEncoderVersion())
	return fmt.Sprintf("libwebp %d.%d.%d (cgo, system shared library)", v>>16, v>>8&0xff, v&0xff)
}

func libwebpEncode(m *image.NRGBA, level int, exact bool) ([]byte, error) {
	w, h := m.Rect.Dx(), m.Rect.Dy()
	var out *C.uint8_t
	var n C.size_t
	ex := C.int(0)
	if exact {
		ex = 1
	}
	rc := C.bench_webp_encode((*C.uint8_t)(unsafe.Pointer(&m.Pix[0])), C.int(w), C.int(h), C.int(m.Stride), C.int(level), ex, &out, &n)
	if rc != 0 {
		return nil, fmt.Errorf("libwebp encode error %d", int(rc))
	}
	b := C.GoBytes(unsafe.Pointer(out), C.int(n))
	C.WebPFree(unsafe.Pointer(out))
	return b, nil
}

func libwebpDecode(data []byte) (image.Image, error) {
	if len(data) == 0 {
		return nil, errors.New("libwebp: empty input")
	}
	var w, h C.int
	p := (*C.uint8_t)(unsafe.Pointer(&data[0]))
	if C.WebPGetInfo(p, C.size_t(len(data)), &w, &h) == 0 {
		return nil, errors.New("libwebp: WebPGetInfo failed")
	}
	m := image.NewNRGBA(image.Rect(0, 0, int(w), int(h)))
	if C.WebPDecodeRGBAInto(p, C.size_t(len(data)), (*C.uint8_t)(unsafe.Pointer(&m.Pix[0])), C.size_t(len(m.Pix)), C.int(m.Stride)) == nil {
		return nil, errors.New("libwebp: WebPDecodeRGBAInto failed")
	}
	return m, nil
}

func init() {
	lib := libwebpVersion()
	for _, z := range []int{0, 3, 6, 9} {
		addEncoder(&Encoder{Name: "libwebp", Setting: zname(z), Format: "webp", Lib: lib,
			Note:   "WebPConfigLosslessPreset, exact=1, thread_level=0",
			Encode: func(m *image.NRGBA) ([]byte, error) { return libwebpEncode(m, z, true) }})
	}
	// libwebp's own default drops RGB under alpha 0; kept for reference only.
	addEncoder(&Encoder{Name: "libwebp-inexact", Setting: zname(6), Format: "webp", Lib: lib,
		Note:   "WebPConfigLosslessPreset, exact=0 (libwebp default), thread_level=0",
		Encode: func(m *image.NRGBA) ([]byte, error) { return libwebpEncode(m, 6, false) }})
	addDecoder(&Decoder{Name: "c", Format: "webp", Lib: lib, Decode: libwebpDecode})
}
