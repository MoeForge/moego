//go:build arm64 && !purego

#include "textflag.h"

// Instructions missing from Go's arm64 assembler are written as WORD,
// each with the instruction in a comment.

// PAETH leaves the Paeth predictor of a (V1), b (V2) and c (V3) in V11:
//
//	UABD V5, V2, V3 (pa = |b-c|); UABD V6, V1, V3 (pb = |a-c|)
//	CMHS V8, V2, V3; CMHS V9, V1, V3; EOR: b-c and a-c differ in sign
//	UQADD V7, V5, V6; UABD V10, V5, V6; BIT: pc = |pa-pb| if they
//	differ, else pa+pb, saturated, which keeps it at or above pa and pb
//	so every comparison comes out as with exact values
//	CMHS V9, V6, V5 (pa <= pb); CMHS V10, V7, V5 (pa <= pc); AND
//	CMHS V10, V7, V6 (pb <= pc); select c, then b, then a
//
// Clobbers V5 to V10.
#define PAETH \
	WORD $0x6e237445; \
	WORD $0x6e237426; \
	WORD $0x6e233c48; \
	WORD $0x6e233c29; \
	VEOR V9.B16, V8.B16, V8.B16; \
	WORD $0x6e260ca7; \
	WORD $0x6e2674aa; \
	VBIT V8.B16, V10.B16, V7.B16; \
	WORD $0x6e253cc9; \
	WORD $0x6e253cea; \
	VAND V10.B16, V9.B16, V9.B16; \
	WORD $0x6e263cea; \
	VMOV V3.B16, V11.B16; \
	VBIT V10.B16, V2.B16, V11.B16; \
	VBIT V9.B16, V1.B16, V11.B16

// FLUSH widens the five halfword cost accumulators V16 to V20 into the
// doubleword accumulators V21 to V25: UADDLP V26.4S, Vk.8H, then
// UADALP V(k+5).2D, V26.4S.
#define FLUSH \
	WORD $0x6e602a1a; \
	WORD $0x6ea06b55; \
	WORD $0x6e602a3a; \
	WORD $0x6ea06b56; \
	WORD $0x6e602a5a; \
	WORD $0x6ea06b57; \
	WORD $0x6e602a7a; \
	WORD $0x6ea06b58; \
	WORD $0x6e602a9a; \
	WORD $0x6ea06b59

// func costsNEON(cur, prev *byte, n, bpp int, c *[5]uint64) int
//
// costsNEON adds to c the cost of each filter for the bytes from bpp on,
// in whole 16-byte blocks, and returns the end of the last block. The
// halfword accumulators take at most 64 blocks (64*2*255 per lane)
// before they are widened.
TEXT ·costsNEON(SB), NOSPLIT, $0-48
	MOVD cur+0(FP), R0
	MOVD prev+8(FP), R1
	MOVD n+16(FP), R2
	MOVD bpp+24(FP), R3
	MOVD c+32(FP), R4
	ADD  R3, R0, R5 // &cur[i]
	ADD  R3, R1, R6 // &prev[i]
	MOVD R0, R7     // &cur[i-bpp]
	MOVD R1, R8     // &prev[i-bpp]
	MOVD R3, R9     // i
	SUB  $16, R2, R2
	CMP  R2, R9
	BGT  costs_none
	VEOR V21.B16, V21.B16, V21.B16
	VEOR V22.B16, V22.B16, V22.B16
	VEOR V23.B16, V23.B16, V23.B16
	VEOR V24.B16, V24.B16, V24.B16
	VEOR V25.B16, V25.B16, V25.B16

costs_outer:
	VEOR V16.B16, V16.B16, V16.B16
	VEOR V17.B16, V17.B16, V17.B16
	VEOR V18.B16, V18.B16, V18.B16
	VEOR V19.B16, V19.B16, V19.B16
	VEOR V20.B16, V20.B16, V20.B16
	MOVD $64, R10

costs_inner:
	VLD1.P 16(R5), [V0.B16] // x
	VLD1.P 16(R7), [V1.B16] // a
	VLD1.P 16(R6), [V2.B16] // b
	VLD1.P 16(R8), [V3.B16] // c
	WORD $0x4e20b804 // ABS V4.16B, V0.16B
	WORD $0x6e206890 // UADALP V16.8H, V4.16B
	VSUB V1.B16, V0.B16, V4.B16
	WORD $0x4e20b884 // ABS V4.16B, V4.16B
	WORD $0x6e206891 // UADALP V17.8H, V4.16B
	VSUB V2.B16, V0.B16, V4.B16
	WORD $0x4e20b884 // ABS V4.16B, V4.16B
	WORD $0x6e206892 // UADALP V18.8H, V4.16B
	WORD $0x6e220424 // UHADD V4.16B, V1.16B, V2.16B
	VSUB V4.B16, V0.B16, V4.B16
	WORD $0x4e20b884 // ABS V4.16B, V4.16B
	WORD $0x6e206893 // UADALP V19.8H, V4.16B
	PAETH
	VSUB V11.B16, V0.B16, V4.B16
	WORD $0x4e20b884 // ABS V4.16B, V4.16B
	WORD $0x6e206894 // UADALP V20.8H, V4.16B
	ADD  $16, R9
	CMP  R2, R9
	BGT  costs_last
	SUB  $1, R10
	CBNZ R10, costs_inner
	FLUSH
	B    costs_outer

costs_last:
	FLUSH
	VMOV V21.D[0], R11
	VMOV V21.D[1], R12
	ADD  R12, R11
	MOVD 0(R4), R13
	ADD  R11, R13
	MOVD R13, 0(R4)
	VMOV V22.D[0], R11
	VMOV V22.D[1], R12
	ADD  R12, R11
	MOVD 8(R4), R13
	ADD  R11, R13
	MOVD R13, 8(R4)
	VMOV V23.D[0], R11
	VMOV V23.D[1], R12
	ADD  R12, R11
	MOVD 16(R4), R13
	ADD  R11, R13
	MOVD R13, 16(R4)
	VMOV V24.D[0], R11
	VMOV V24.D[1], R12
	ADD  R12, R11
	MOVD 24(R4), R13
	ADD  R11, R13
	MOVD R13, 24(R4)
	VMOV V25.D[0], R11
	VMOV V25.D[1], R12
	ADD  R12, R11
	MOVD 32(R4), R13
	ADD  R11, R13
	MOVD R13, 32(R4)

costs_none:
	MOVD R9, ret+40(FP)
	RET

// func filterNEON(dst, cur, prev *byte, n, bpp, f int) int
//
// filterNEON writes filter f's residuals for the bytes from bpp on, in
// whole 16-byte blocks, and returns the end of the last block. f is Sub,
// Up, Average or Paeth.
TEXT ·filterNEON(SB), NOSPLIT, $0-56
	MOVD dst+0(FP), R4
	MOVD cur+8(FP), R0
	MOVD prev+16(FP), R1
	MOVD n+24(FP), R2
	MOVD bpp+32(FP), R3
	MOVD f+40(FP), R10
	ADD  R3, R4, R4 // &dst[i]
	ADD  R3, R0, R5 // &cur[i]
	ADD  R3, R1, R6 // &prev[i]
	MOVD R0, R7     // &cur[i-bpp]
	MOVD R1, R8     // &prev[i-bpp]
	MOVD R3, R9     // i
	SUB  $16, R2, R2
	CMP  R2, R9
	BGT  filter_none
	CMP  $1, R10
	BEQ  filter_sub
	CMP  $2, R10
	BEQ  filter_up
	CMP  $3, R10
	BEQ  filter_avg

filter_paeth:
	VLD1.P 16(R5), [V0.B16]
	VLD1.P 16(R7), [V1.B16]
	VLD1.P 16(R6), [V2.B16]
	VLD1.P 16(R8), [V3.B16]
	PAETH
	VSUB V11.B16, V0.B16, V4.B16
	VST1.P [V4.B16], 16(R4)
	ADD  $16, R9
	CMP  R2, R9
	BLE  filter_paeth
	B    filter_none

filter_sub:
	VLD1.P 16(R5), [V0.B16]
	VLD1.P 16(R7), [V1.B16]
	VSUB V1.B16, V0.B16, V4.B16
	VST1.P [V4.B16], 16(R4)
	ADD  $16, R9
	CMP  R2, R9
	BLE  filter_sub
	B    filter_none

filter_up:
	VLD1.P 16(R5), [V0.B16]
	VLD1.P 16(R6), [V2.B16]
	VSUB V2.B16, V0.B16, V4.B16
	VST1.P [V4.B16], 16(R4)
	ADD  $16, R9
	CMP  R2, R9
	BLE  filter_up
	B    filter_none

filter_avg:
	VLD1.P 16(R5), [V0.B16]
	VLD1.P 16(R7), [V1.B16]
	VLD1.P 16(R6), [V2.B16]
	WORD $0x6e220424 // UHADD V4.16B, V1.16B, V2.16B
	VSUB V4.B16, V0.B16, V4.B16
	VST1.P [V4.B16], 16(R4)
	ADD  $16, R9
	CMP  R2, R9
	BLE  filter_avg

filter_none:
	MOVD R9, ret+48(FP)
	RET

// func rgbaFlagsNEON(p *byte, n int) (opaque, gray bool)
//
// rgbaFlagsNEON checks the RGBA pixels of p, n a positive multiple of 64.
TEXT ·rgbaFlagsNEON(SB), NOSPLIT, $0-18
	MOVD p+0(FP), R0
	MOVD n+8(FP), R1
	VCMEQ V4.B16, V4.B16, V4.B16 // alpha: all ones while opaque
	VCMEQ V5.B16, V5.B16, V5.B16 // all ones while gray

flags_loop:
	VLD4.P 64(R0), [V0.B16, V1.B16, V2.B16, V3.B16]
	VAND  V3.B16, V4.B16, V4.B16
	VCMEQ V1.B16, V0.B16, V6.B16
	VCMEQ V2.B16, V0.B16, V7.B16
	VAND  V6.B16, V5.B16, V5.B16
	VAND  V7.B16, V5.B16, V5.B16
	SUBS  $64, R1
	BNE   flags_loop

	VMOV  V4.D[0], R2
	VMOV  V4.D[1], R3
	AND   R3, R2
	CMN   $1, R2
	CSET  EQ, R2
	MOVB  R2, opaque+16(FP)
	VMOV  V5.D[0], R2
	VMOV  V5.D[1], R3
	AND   R3, R2
	CMN   $1, R2
	CSET  EQ, R2
	MOVB  R2, gray+17(FP)
	RET

// func toRGBNEON(dst, src *byte, n int)
//
// toRGBNEON writes the RGB of n RGBA pixels, n a positive multiple of 16.
TEXT ·toRGBNEON(SB), NOSPLIT, $0-24
	MOVD dst+0(FP), R1
	MOVD src+8(FP), R0
	MOVD n+16(FP), R2

torgbneon_loop:
	VLD4.P 64(R0), [V0.B16, V1.B16, V2.B16, V3.B16]
	VST3.P [V0.B16, V1.B16, V2.B16], 48(R1)
	SUBS $16, R2
	BNE  torgbneon_loop
	RET

// func toGANEON(dst, src *byte, n int)
//
// toGANEON writes the R and A of n RGBA pixels, n a positive multiple of 16.
TEXT ·toGANEON(SB), NOSPLIT, $0-24
	MOVD dst+0(FP), R1
	MOVD src+8(FP), R0
	MOVD n+16(FP), R2

toganeon_loop:
	VLD4.P 64(R0), [V0.B16, V1.B16, V2.B16, V3.B16]
	VMOV V3.B16, V1.B16
	VST2.P [V0.B16, V1.B16], 32(R1)
	SUBS $16, R2
	BNE  toganeon_loop
	RET

// func toGNEON(dst, src *byte, n int)
//
// toGNEON writes the R of n RGBA pixels, n a positive multiple of 16.
TEXT ·toGNEON(SB), NOSPLIT, $0-24
	MOVD dst+0(FP), R1
	MOVD src+8(FP), R0
	MOVD n+16(FP), R2

togneon_loop:
	VLD4.P 64(R0), [V0.B16, V1.B16, V2.B16, V3.B16]
	VST1.P [V0.B16], 16(R1)
	SUBS $16, R2
	BNE  togneon_loop
	RET
