// Package vp8l is moego's entropy-coding back end for WebP lossless
// (VP8L, RFC 9649 section 3), written for moego/webp. It writes the
// bitstream header and raw transform bits for its caller, and codes the
// entropy-coded images: the transform sub-images (predictor and color
// transform images, the color table) and the main image.
//
// An image is coded as backward references (LZ77 with the 120 neighbourhood
// distance codes), color cache hits and literal pixels, under prefix codes
// that are length-limited canonical Huffman codes built by package-merge.
// The main image may use several prefix code groups chosen per block (the
// entropy image) by clustering block histograms.
//
// The package is written from RFC 9649; it does not derive from libwebp or
// any other WebP implementation.
package vp8l
