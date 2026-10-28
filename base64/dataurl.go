package base64

import "slices"

// EncodeDataURL returns data:<mediaType>;base64,<payload> in one allocation.
// mediaType must be a valid MIME type supplied by the caller, such as image/png.
func EncodeDataURL(mediaType string, src []byte) string {
	const prefix, separator = "data:", ";base64,"
	buf := makeNoZero(len(prefix) + len(mediaType) + len(separator) + StdEncoding.EncodedLen(len(src)))
	n := copy(buf, prefix)
	n += copy(buf[n:], mediaType)
	n += copy(buf[n:], separator)
	StdEncoding.Encode(buf[n:], src)
	return ownedString(buf)
}

// AppendDataURL appends a standard padded base64 data URL to dst. mediaType
// must be a valid MIME type supplied by the caller.
func AppendDataURL(dst []byte, mediaType string, src []byte) []byte {
	const prefix, separator = "data:", ";base64,"
	n := len(prefix) + len(mediaType) + len(separator) + StdEncoding.EncodedLen(len(src))
	dst = slices.Grow(dst, n)
	dst = append(dst, prefix...)
	dst = append(dst, mediaType...)
	dst = append(dst, separator...)
	return StdEncoding.AppendEncode(dst, src)
}
