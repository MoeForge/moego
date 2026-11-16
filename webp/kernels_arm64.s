//go:build arm64 && !purego

#include "textflag.h"

// Instructions missing from Go's arm64 assembler are written as WORD
// (three-same and two-register-misc encodings, register numbers m, n, d):
// UHADD (floor of the halved sum), UQADD and UQSUB (saturating), UABD
// (absolute difference), UADDLP (pairwise widening add), CMHI (unsigned
// compare greater) and SQDMULH (signed doubling multiply, high half).
#define UHADD(m, n, d) WORD $(0x6e200400 | (m)<<16 | (n)<<5 | (d))
#define UQADD(m, n, d) WORD $(0x6e200c00 | (m)<<16 | (n)<<5 | (d))
#define UQSUB(m, n, d) WORD $(0x6e202c00 | (m)<<16 | (n)<<5 | (d))
#define UABD(m, n, d) WORD $(0x6e207400 | (m)<<16 | (n)<<5 | (d))
#define UADDLP8H(n, d) WORD $(0x6e202800 | (n)<<5 | (d))
#define UADDLP4S(n, d) WORD $(0x6e602800 | (n)<<5 | (d))
#define CMHI4S(m, n, d) WORD $(0x6ea03400 | (m)<<16 | (n)<<5 | (d))

// SQDMULH8H is the high half of twice the signed product of 16-bit lanes.
#define SQDMULH8H(m, n, d) WORD $(0x4e60b400 | (m)<<16 | (n)<<5 | (d))

// Color transform kernels: V5 holds m1 (green_to_red*4 in the high and
// green_to_blue*4 in the low 16-bit half of each pixel), V6 m2
// (red_to_blue*4 in the low half). A channel moved to the high byte of a
// half is its value times 256; SQDMULH by the multiplier times 4 gives
// (t*c) >> 5, as ColorTransformDelta.
#define CTSETUP \
	MOVD  dst+0(FP), R0; \
	MOVD  src+8(FP), R1; \
	MOVD  n+16(FP), R3; \
	MOVWU m1+24(FP), R5; \
	MOVWU m2+28(FP), R6; \
	MOVD  $0, R4; \
	SUBS  $4, R3; \
	BLT   ct_none; \
	VDUP  R5, V5.S4; \
	VDUP  R6, V6.S4; \
	MOVW  $0xff00, R9; \
	VDUP  R9, V7.S4; \
	MOVW  $0x00ff00ff, R9; \
	VDUP  R9, V8.S4

// GREENTERMS sets V1 to the green terms of the pixels in V0: red's in the
// high half, blue's in the low half, low bytes only.
#define GREENTERMS \
	VAND V7.B16, V0.B16, V1.B16; \
	VSHL $16, V1.S4, V2.S4; \
	VORR V2.B16, V1.B16, V1.B16; \
	SQDMULH8H(5, 1, 1); \
	VAND V8.B16, V1.B16, V1.B16

// REDTERM sets V2 to the red term of the blue of the pixels in V0.
#define REDTERM \
	VUSHR $8, V0.S4, V2.S4; \
	VAND  V7.B16, V2.B16, V2.B16; \
	SQDMULH8H(6, 2, 2); \
	VAND  V8.B16, V2.B16, V2.B16

// Registers of the residual loops: R1 = &cur[k] (L), R5 = &cur[k+1] (P),
// R2 = &top[k] (TL), R6 = &top[k+1] (T), R7 = &top[k+2] (TR), R0 = &dst[k],
// R4 = k, R3 = the last k of a whole block. LOAD gives V0 = L, V1 = P,
// V2 = TL, V3 = T, V4 = TR for pixels k to k+3.
#define LOAD \
	VLD1.P 16(R1), [V0.B16]; \
	VLD1.P 16(R5), [V1.B16]; \
	VLD1.P 16(R2), [V2.B16]; \
	VLD1.P 16(R6), [V3.B16]; \
	VLD1.P 16(R7), [V4.B16]

#define STORE(pred, label) \
	VSUB   pred, V1.B16, V1.B16; \
	VST1.P [V1.B16], 16(R0); \
	ADD    $4, R4; \
	CMP    R3, R4; \
	BLE    label; \
	B      res_done

// func residualsNEON(m int, dst, cur, top *uint32, n int) int
//
// residualsNEON writes the residuals of mode m for pixels 0 to n-1 in
// whole blocks of 4 and returns the number written. Pixel k is cur[k+1];
// its neighbors are L = cur[k], TL = top[k], T = top[k+1], TR = top[k+2].
TEXT ·residualsNEON(SB), NOSPLIT, $0-48
	MOVD m+0(FP), R8
	MOVD dst+8(FP), R0
	MOVD cur+16(FP), R1
	MOVD top+24(FP), R2
	MOVD n+32(FP), R3
	MOVD $0, R4
	SUBS $4, R3
	BLT  res_none
	ADD  $4, R1, R5
	ADD  $4, R2, R6
	ADD  $8, R2, R7
	CMP  $0, R8
	BEQ  res_m0
	CMP  $1, R8
	BEQ  res_m1
	CMP  $2, R8
	BEQ  res_m2
	CMP  $3, R8
	BEQ  res_m3
	CMP  $4, R8
	BEQ  res_m4
	CMP  $5, R8
	BEQ  res_m5
	CMP  $6, R8
	BEQ  res_m6
	CMP  $7, R8
	BEQ  res_m7
	CMP  $8, R8
	BEQ  res_m8
	CMP  $9, R8
	BEQ  res_m9
	CMP  $10, R8
	BEQ  res_m10
	CMP  $11, R8
	BEQ  res_m11
	CMP  $12, R8
	BEQ  res_m12
	CMP  $13, R8
	BEQ  res_m13
	B    res_none

res_m0:
	MOVW $0xff000000, R9
	VDUP R9, V5.S4

res_m0_loop:
	LOAD
	STORE(V5.B16, res_m0_loop)

res_m1:
	LOAD
	STORE(V0.B16, res_m1)

res_m2:
	LOAD
	STORE(V3.B16, res_m2)

res_m3:
	LOAD
	STORE(V4.B16, res_m3)

res_m4:
	LOAD
	STORE(V2.B16, res_m4)

res_m5:
	LOAD
	UHADD(4, 0, 5)
	UHADD(3, 5, 5)
	STORE(V5.B16, res_m5)

res_m6:
	LOAD
	UHADD(2, 0, 5)
	STORE(V5.B16, res_m6)

res_m7:
	LOAD
	UHADD(3, 0, 5)
	STORE(V5.B16, res_m7)

res_m8:
	LOAD
	UHADD(3, 2, 5)
	STORE(V5.B16, res_m8)

res_m9:
	LOAD
	UHADD(4, 3, 5)
	STORE(V5.B16, res_m9)

res_m10:
	LOAD
	UHADD(2, 0, 5)
	UHADD(4, 3, 6)
	UHADD(6, 5, 5)
	STORE(V5.B16, res_m10)

res_m11:
	// Select: L when sum|T-TL| < sum|L-TL|, else T.
	LOAD
	UABD(2, 3, 6)
	UABD(2, 0, 7)
	UADDLP8H(6, 6)
	UADDLP4S(6, 6)
	UADDLP8H(7, 7)
	UADDLP4S(7, 7)
	CMHI4S(6, 7, 8)
	VMOV V3.B16, V5.B16
	VBIT V8.B16, V0.B16, V5.B16
	STORE(V5.B16, res_m11)

res_m12:
	// Clamp(L + T - TL): one of T-TL and TL-T saturates to zero, so
	// L + (T-TL) - (TL-T) with saturation at each step is exact.
	LOAD
	UQSUB(2, 3, 6)
	UQSUB(3, 2, 7)
	UQADD(6, 0, 5)
	UQSUB(7, 5, 5)
	STORE(V5.B16, res_m12)

res_m13:
	// a = Average2(L, T); Clamp(a + (a - TL) / 2) with the division
	// truncating: a + (a-TL)>>1 when a >= TL, else a - (TL-a)>>1.
	LOAD
	UHADD(3, 0, 9)
	UQSUB(2, 9, 6)
	UQSUB(9, 2, 7)
	VUSHR $1, V6.B16, V6.B16
	VUSHR $1, V7.B16, V7.B16
	UQADD(6, 9, 5)
	UQSUB(7, 5, 5)
	STORE(V5.B16, res_m13)

res_done:
res_none:
	MOVD R4, ret+40(FP)
	RET

// func subGreenNEON(dst, src *uint32, n int) int
//
// subGreenNEON subtracts green from red and blue for whole blocks of 4
// pixels and returns the number done.
TEXT ·subGreenNEON(SB), NOSPLIT, $0-32
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD n+16(FP), R3
	MOVD $0, R4
	SUBS $4, R3
	BLT  sg_none
	MOVW $0xff, R9
	VDUP R9, V5.S4

sg_loop:
	VLD1.P 16(R1), [V0.B16]
	VUSHR  $8, V0.S4, V1.S4
	VAND   V5.B16, V1.B16, V1.B16
	VSHL   $16, V1.S4, V2.S4
	VORR   V2.B16, V1.B16, V1.B16
	VSUB   V1.B16, V0.B16, V0.B16
	VST1.P [V0.B16], 16(R0)
	ADD    $4, R4
	CMP    R3, R4
	BLE    sg_loop

sg_none:
	MOVD R4, ret+24(FP)
	RET

// func addGreenNEON(dst, src *uint32, n int) int
//
// addGreenNEON adds green to red and blue, undoing subtract green, for
// whole blocks of 4 pixels and returns the number done.
TEXT ·addGreenNEON(SB), NOSPLIT, $0-32
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD n+16(FP), R3
	MOVD $0, R4
	SUBS $4, R3
	BLT  ag_none
	MOVW $0xff, R9
	VDUP R9, V5.S4

ag_loop:
	VLD1.P 16(R1), [V0.B16]
	VUSHR  $8, V0.S4, V1.S4
	VAND   V5.B16, V1.B16, V1.B16
	VSHL   $16, V1.S4, V2.S4
	VORR   V2.B16, V1.B16, V1.B16
	VADD   V1.B16, V0.B16, V0.B16
	VST1.P [V0.B16], 16(R0)
	ADD    $4, R4
	CMP    R3, R4
	BLE    ag_loop

ag_none:
	MOVD R4, ret+24(FP)
	RET

// func addTopNEON(m int, cur, top *uint32, n int) int
//
// addTopNEON adds the predictions of mode m (0, 2, 3, 4, 8 or 9: those
// that read only the row above) to cur for whole blocks of 4 pixels and
// returns the number done. top[k], top[k+1] and top[k+2] are the
// top-left, top and top-right of cur[k].
TEXT ·addTopNEON(SB), NOSPLIT, $0-40
	MOVD m+0(FP), R8
	MOVD cur+8(FP), R0
	MOVD top+16(FP), R2
	MOVD n+24(FP), R3
	MOVD $0, R4
	SUBS $4, R3
	BLT  at_none
	MOVD R0, R1
	ADD  $4, R2, R6
	ADD  $8, R2, R7
	CMP  $0, R8
	BEQ  at_m0
	CMP  $2, R8
	BEQ  at_m2
	CMP  $3, R8
	BEQ  at_m3
	CMP  $4, R8
	BEQ  at_m4
	CMP  $8, R8
	BEQ  at_m8
	CMP  $9, R8
	BEQ  at_m9
	B    at_none

#define ATNEXT(label) \
	VADD   V5.B16, V1.B16, V1.B16; \
	VST1.P [V1.B16], 16(R0); \
	ADD    $4, R4; \
	CMP    R3, R4; \
	BLE    label; \
	B      at_none

at_m0:
	MOVW $0xff000000, R9
	VDUP R9, V5.S4

at_m0_loop:
	VLD1.P 16(R1), [V1.B16]
	ATNEXT(at_m0_loop)

at_m2:
	VLD1.P 16(R1), [V1.B16]
	VLD1.P 16(R6), [V5.B16]
	ATNEXT(at_m2)

at_m3:
	VLD1.P 16(R1), [V1.B16]
	VLD1.P 16(R7), [V5.B16]
	ATNEXT(at_m3)

at_m4:
	VLD1.P 16(R1), [V1.B16]
	VLD1.P 16(R2), [V5.B16]
	ATNEXT(at_m4)

at_m8:
	VLD1.P 16(R1), [V1.B16]
	VLD1.P 16(R2), [V2.B16]
	VLD1.P 16(R6), [V3.B16]
	UHADD(3, 2, 5)
	ATNEXT(at_m8)

at_m9:
	VLD1.P 16(R1), [V1.B16]
	VLD1.P 16(R6), [V3.B16]
	VLD1.P 16(R7), [V4.B16]
	UHADD(4, 3, 5)
	ATNEXT(at_m9)

at_none:
	MOVD R4, ret+32(FP)
	RET

// func colorTransformNEON(dst, src *uint32, n int, m1, m2 uint32) int
//
// colorTransformNEON applies the color transform of one element to whole
// blocks of 4 pixels and returns the number done.
TEXT ·colorTransformNEON(SB), NOSPLIT, $0-40
	CTSETUP

ctf_loop:
	VLD1.P 16(R1), [V0.B16]
	GREENTERMS
	REDTERM
	VSUB   V1.B16, V0.B16, V0.B16
	VSUB   V2.B16, V0.B16, V0.B16
	VST1.P [V0.B16], 16(R0)
	ADD    $4, R4
	CMP    R3, R4
	BLE    ctf_loop

ct_none:
	MOVD R4, ret+32(FP)
	RET

// func undoColorTransformNEON(dst, src *uint32, n int, m1, m2 uint32) int
//
// undoColorTransformNEON inverts the color transform of one element for
// whole blocks of 4 pixels and returns the number done: the blue's red
// term uses the restored red.
TEXT ·undoColorTransformNEON(SB), NOSPLIT, $0-40
	CTSETUP

cti_loop:
	VLD1.P 16(R1), [V0.B16]
	GREENTERMS
	VADD   V1.B16, V0.B16, V0.B16
	REDTERM
	VADD   V2.B16, V0.B16, V0.B16
	VST1.P [V0.B16], 16(R0)
	ADD    $4, R4
	CMP    R3, R4
	BLE    cti_loop

ct_none:
	MOVD R4, ret+32(FP)
	RET
