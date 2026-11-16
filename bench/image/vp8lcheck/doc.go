// Package vp8lcheck holds tests only: the streams of moego/internal/vp8l,
// wrapped in a WebP file, must decode to the same pixels with
// golang.org/x/image/webp and, in cgo builds, with libwebp.
package vp8lcheck
