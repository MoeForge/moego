//go:build amd64 && !purego

#include "textflag.h"

// func cpuHasAVX2() bool
TEXT ·cpuHasAVX2(SB), NOSPLIT, $0-1
	MOVB $0, ret+0(FP)
	XORL AX, AX
	CPUID
	CMPL AX, $7
	JB   no_avx2
	MOVL $1, AX
	CPUID
	// OSXSAVE (bit 27) and AVX (28).
	ANDL $0x18000000, CX
	CMPL CX, $0x18000000
	JNE  no_avx2
	XORL CX, CX
	XGETBV
	ANDL $6, AX
	CMPL AX, $6
	JNE  no_avx2
	MOVL $7, AX
	XORL CX, CX
	CPUID
	// AVX2 (bit 5).
	TESTL $32, BX
	JZ    no_avx2
	MOVB  $1, ret+0(FP)

no_avx2:
	RET

DATA bytes1<>+0(SB)/8, $0x0101010101010101
DATA bytes1<>+8(SB)/8, $0x0101010101010101
DATA bytes1<>+16(SB)/8, $0x0101010101010101
DATA bytes1<>+24(SB)/8, $0x0101010101010101
GLOBL bytes1<>(SB), RODATA|NOPTR, $32

DATA words1<>+0(SB)/8, $0x0001000100010001
DATA words1<>+8(SB)/8, $0x0001000100010001
DATA words1<>+16(SB)/8, $0x0001000100010001
DATA words1<>+24(SB)/8, $0x0001000100010001
GLOBL words1<>(SB), RODATA|NOPTR, $32

DATA black<>+0(SB)/8, $0xff000000ff000000
DATA black<>+8(SB)/8, $0xff000000ff000000
DATA black<>+16(SB)/8, $0xff000000ff000000
DATA black<>+24(SB)/8, $0xff000000ff000000
GLOBL black<>(SB), RODATA|NOPTR, $32

// greenShuf copies each pixel's green byte to its blue and red bytes and
// zeroes green and alpha.
DATA greenShuf<>+0(SB)/8, $0x8005800580018001
DATA greenShuf<>+8(SB)/8, $0x800d800d80098009
DATA greenShuf<>+16(SB)/8, $0x8005800580018001
DATA greenShuf<>+24(SB)/8, $0x800d800d80098009
GLOBL greenShuf<>(SB), RODATA|NOPTR, $32

// AVG sets dst to the per-byte floor((a+b)/2): VPAVGB rounds up, so
// subtract the low bit of a^b. Clobbers tmp.
#define AVG(a, b, tmp, dst) \
	VPAVGB b, a, dst; \
	VPXOR  b, a, tmp; \
	VPAND  bytes1<>(SB), tmp, tmp; \
	VPSUBB tmp, dst, dst

// SAD4 sets dst to the sum of the absolute byte differences of a and b in
// each 32-bit lane. Clobbers tmp.
#define SAD4(a, b, tmp, dst) \
	VPMAXUB   b, a, tmp; \
	VPMINUB   b, a, dst; \
	VPSUBB    dst, tmp, dst; \
	VPMADDUBSW bytes1<>(SB), dst, dst; \
	VPMADDWD  words1<>(SB), dst, dst

// Registers of the residual loops: SI = &cur[k] (L), DI = &top[k] (TL),
// DX = &dst[k], BX = k, CX = last k of a whole block. The loads give
// Y0 = L, Y1 = P, Y2 = TL, Y3 = T, Y4 = TR for pixels k..k+7.
#define LOADL   VMOVDQU (SI), Y0
#define LOADP   VMOVDQU 4(SI), Y1
#define LOADTL  VMOVDQU (DI), Y2
#define LOADT   VMOVDQU 4(DI), Y3
#define LOADTR  VMOVDQU 8(DI), Y4
#define STORE(pred) \
	VPSUBB  pred, Y1, Y1; \
	VMOVDQU Y1, (DX)
#define NEXT(label) \
	ADDQ $32, SI; \
	ADDQ $32, DI; \
	ADDQ $32, DX; \
	ADDQ $8, BX; \
	CMPQ BX, CX; \
	JLE  label; \
	JMP  res_done

// func residualsAVX2(m int, dst, cur, top *uint32, n int) int
//
// residualsAVX2 writes the residuals of mode m for pixels 0 to n-1 in
// whole blocks of 8 and returns the number written. Pixel k is cur[k+1];
// its neighbors are L = cur[k], TL = top[k], T = top[k+1], TR = top[k+2].
TEXT ·residualsAVX2(SB), NOSPLIT, $0-48
	MOVQ m+0(FP), AX
	MOVQ dst+8(FP), DX
	MOVQ cur+16(FP), SI
	MOVQ top+24(FP), DI
	MOVQ n+32(FP), CX
	XORQ BX, BX
	SUBQ $8, CX
	JL   res_none
	CMPQ AX, $0
	JEQ  res_m0
	CMPQ AX, $1
	JEQ  res_m1
	CMPQ AX, $2
	JEQ  res_m2
	CMPQ AX, $3
	JEQ  res_m3
	CMPQ AX, $4
	JEQ  res_m4
	CMPQ AX, $5
	JEQ  res_m5
	CMPQ AX, $6
	JEQ  res_m6
	CMPQ AX, $7
	JEQ  res_m7
	CMPQ AX, $8
	JEQ  res_m8
	CMPQ AX, $9
	JEQ  res_m9
	CMPQ AX, $10
	JEQ  res_m10
	CMPQ AX, $11
	JEQ  res_m11
	CMPQ AX, $12
	JEQ  res_m12
	CMPQ AX, $13
	JEQ  res_m13
	JMP  res_none

res_m0:
	VMOVDQU black<>(SB), Y5
res_m0_loop:
	LOADP
	STORE(Y5)
	NEXT(res_m0_loop)

res_m1:
	LOADL
	LOADP
	STORE(Y0)
	NEXT(res_m1)

res_m2:
	LOADP
	LOADT
	STORE(Y3)
	NEXT(res_m2)

res_m3:
	LOADP
	LOADTR
	STORE(Y4)
	NEXT(res_m3)

res_m4:
	LOADP
	LOADTL
	STORE(Y2)
	NEXT(res_m4)

res_m5:
	LOADL
	LOADP
	LOADT
	LOADTR
	AVG(Y0, Y4, Y6, Y5)
	AVG(Y5, Y3, Y6, Y7)
	STORE(Y7)
	NEXT(res_m5)

res_m6:
	LOADL
	LOADP
	LOADTL
	AVG(Y0, Y2, Y6, Y5)
	STORE(Y5)
	NEXT(res_m6)

res_m7:
	LOADL
	LOADP
	LOADT
	AVG(Y0, Y3, Y6, Y5)
	STORE(Y5)
	NEXT(res_m7)

res_m8:
	LOADP
	LOADTL
	LOADT
	AVG(Y2, Y3, Y6, Y5)
	STORE(Y5)
	NEXT(res_m8)

res_m9:
	LOADP
	LOADT
	LOADTR
	AVG(Y3, Y4, Y6, Y5)
	STORE(Y5)
	NEXT(res_m9)

res_m10:
	LOADL
	LOADP
	LOADTL
	LOADT
	LOADTR
	AVG(Y0, Y2, Y6, Y5)
	AVG(Y3, Y4, Y6, Y7)
	AVG(Y5, Y7, Y6, Y8)
	STORE(Y8)
	NEXT(res_m10)

res_m11:
	// Select: L when sum|T-TL| < sum|L-TL|, else T.
	LOADL
	LOADP
	LOADTL
	LOADT
	SAD4(Y3, Y2, Y6, Y5)
	SAD4(Y0, Y2, Y6, Y7)
	VPCMPGTD  Y5, Y7, Y7
	VPBLENDVB Y7, Y0, Y3, Y8
	STORE(Y8)
	NEXT(res_m11)

res_m12:
	// Clamp(L + T - TL) in 16 bits, packed back with unsigned saturation.
	VPXOR Y15, Y15, Y15
res_m12_loop:
	LOADL
	LOADP
	LOADTL
	LOADT
	VPUNPCKLBW Y15, Y0, Y5
	VPUNPCKHBW Y15, Y0, Y6
	VPUNPCKLBW Y15, Y3, Y7
	VPUNPCKHBW Y15, Y3, Y8
	VPADDW     Y7, Y5, Y5
	VPADDW     Y8, Y6, Y6
	VPUNPCKLBW Y15, Y2, Y7
	VPUNPCKHBW Y15, Y2, Y8
	VPSUBW     Y7, Y5, Y5
	VPSUBW     Y8, Y6, Y6
	VPACKUSWB  Y6, Y5, Y5
	STORE(Y5)
	NEXT(res_m12_loop)

res_m13:
	// a = Average2(L, T); Clamp(a + (a - TL) / 2), the division
	// truncating: (d - (d >> 15)) >> 1.
	VPXOR Y15, Y15, Y15
res_m13_loop:
	LOADL
	LOADP
	LOADTL
	LOADT
	AVG(Y0, Y3, Y6, Y9)
	VPUNPCKLBW Y15, Y9, Y5
	VPUNPCKHBW Y15, Y9, Y6
	VPUNPCKLBW Y15, Y2, Y7
	VPUNPCKHBW Y15, Y2, Y8
	VPSUBW     Y7, Y5, Y7
	VPSUBW     Y8, Y6, Y8
	VPSRAW     $15, Y7, Y10
	VPSRAW     $15, Y8, Y11
	VPSUBW     Y10, Y7, Y7
	VPSUBW     Y11, Y8, Y8
	VPSRAW     $1, Y7, Y7
	VPSRAW     $1, Y8, Y8
	VPADDW     Y7, Y5, Y5
	VPADDW     Y8, Y6, Y6
	VPACKUSWB  Y6, Y5, Y5
	STORE(Y5)
	NEXT(res_m13_loop)

res_done:
	VZEROUPPER

res_none:
	MOVQ BX, ret+40(FP)
	RET

// func subGreenAVX2(dst, src *uint32, n int) int
//
// subGreenAVX2 subtracts green from red and blue for whole blocks of 8
// pixels and returns the number done.
TEXT ·subGreenAVX2(SB), NOSPLIT, $0-32
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ n+16(FP), CX
	XORQ BX, BX
	SUBQ $8, CX
	JL   sg_none
	VMOVDQU greenShuf<>(SB), Y5

sg_loop:
	VMOVDQU (SI)(BX*4), Y0
	VPSHUFB Y5, Y0, Y1
	VPSUBB  Y1, Y0, Y0
	VMOVDQU Y0, (DI)(BX*4)
	ADDQ    $8, BX
	CMPQ    BX, CX
	JLE     sg_loop
	VZEROUPPER

sg_none:
	MOVQ BX, ret+24(FP)
	RET

// func addGreenAVX2(dst, src *uint32, n int) int
//
// addGreenAVX2 adds green to red and blue, undoing subtract green, for
// whole blocks of 8 pixels and returns the number done.
TEXT ·addGreenAVX2(SB), NOSPLIT, $0-32
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ n+16(FP), CX
	XORQ BX, BX
	SUBQ $8, CX
	JL   ag_none
	VMOVDQU greenShuf<>(SB), Y5

ag_loop:
	VMOVDQU (SI)(BX*4), Y0
	VPSHUFB Y5, Y0, Y1
	VPADDB  Y1, Y0, Y0
	VMOVDQU Y0, (DI)(BX*4)
	ADDQ    $8, BX
	CMPQ    BX, CX
	JLE     ag_loop
	VZEROUPPER

ag_none:
	MOVQ BX, ret+24(FP)
	RET

// rgbaShuf swaps the R and B bytes of each pixel: RGBA in memory order
// becomes the little-endian word 0xAARRGGBB.
DATA rgbaShuf<>+0(SB)/8, $0x0704050603000102
DATA rgbaShuf<>+8(SB)/8, $0x0f0c0d0e0b08090a
DATA rgbaShuf<>+16(SB)/8, $0x0704050603000102
DATA rgbaShuf<>+24(SB)/8, $0x0f0c0d0e0b08090a
GLOBL rgbaShuf<>(SB), RODATA|NOPTR, $32

// func fromNRGBAAVX2(dst *uint32, src *byte, n int) (int, uint32)
//
// fromNRGBAAVX2 converts whole blocks of 8 RGBA pixels and returns the
// number done and the AND of the pixels converted.
TEXT ·fromNRGBAAVX2(SB), NOSPLIT, $0-36
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ n+16(FP), CX
	XORQ BX, BX
	VPCMPEQD X6, X6, X6
	SUBQ $8, CX
	JL   rgba_none
	VMOVDQU rgbaShuf<>(SB), Y5
	VPCMPEQD Y6, Y6, Y6

rgba_loop:
	VMOVDQU (SI)(BX*4), Y0
	VPSHUFB Y5, Y0, Y0
	VPAND   Y0, Y6, Y6
	VMOVDQU Y0, (DI)(BX*4)
	ADDQ    $8, BX
	CMPQ    BX, CX
	JLE     rgba_loop
	VEXTRACTI128 $1, Y6, X1
	VPAND   X1, X6, X6
	VPSHUFD $0x4e, X6, X1
	VPAND   X1, X6, X6
	VPSHUFD $0xb1, X6, X1
	VPAND   X1, X6, X6
	VZEROUPPER

rgba_none:
	MOVQ BX, ret+24(FP)
	MOVL X6, ret1+32(FP)
	RET

DATA bytes255<>+0(SB)/8, $0x000000ff000000ff
DATA bytes255<>+8(SB)/8, $0x000000ff000000ff
DATA bytes255<>+16(SB)/8, $0x000000ff000000ff
DATA bytes255<>+24(SB)/8, $0x000000ff000000ff
GLOBL bytes255<>(SB), RODATA|NOPTR, $32

DATA words255<>+0(SB)/8, $0x00ff00ff00ff00ff
DATA words255<>+8(SB)/8, $0x00ff00ff00ff00ff
DATA words255<>+16(SB)/8, $0x00ff00ff00ff00ff
DATA words255<>+24(SB)/8, $0x00ff00ff00ff00ff
GLOBL words255<>(SB), RODATA|NOPTR, $32

// GATHER adds to acc the entries of the table at off(SI) indexed by idx.
// Clobbers Y14 (the mask) and Y15.
#define GATHER(off, idx, acc) \
	VPCMPEQD   Y14, Y14, Y14; \
	VPGATHERDD Y14, off(SI)(idx*4), Y15; \
	VPADDD     Y15, acc, acc

// SUM8 adds the costs in Y3 to out[k..k+7].
#define SUM8 \
	VPADDD  (DI)(BX*4), Y3, Y3; \
	VMOVDQU Y3, (DI)(BX*4)

// func pixelCostsAVX2(tab *[4][256]uint32, res *uint32, n int, out *uint32, chans int) int
//
// pixelCostsAVX2 adds to out[k] the cost of residual k for whole blocks of
// 8 residuals, the sum of tab[c][byte c] over the channels: green alone
// (chans 1), red, green and blue (3) or all (4). It returns the number of
// residuals done.
TEXT ·pixelCostsAVX2(SB), NOSPLIT, $0-48
	MOVQ tab+0(FP), SI
	MOVQ res+8(FP), DX
	MOVQ n+16(FP), CX
	MOVQ out+24(FP), DI
	MOVQ chans+32(FP), AX
	XORQ BX, BX
	SUBQ $8, CX
	JL   pc_none
	VMOVDQU bytes255<>(SB), Y13
	CMPQ AX, $1
	JEQ  pc_g
	CMPQ AX, $3
	JEQ  pc_rgb

pc_all:
	VMOVDQU (DX)(BX*4), Y0
	VPAND   Y13, Y0, Y2
	VPXOR   Y3, Y3, Y3
	GATHER(0, Y2, Y3)
	VPSRLD  $8, Y0, Y2
	VPAND   Y13, Y2, Y2
	GATHER(1024, Y2, Y3)
	VPSRLD  $16, Y0, Y2
	VPAND   Y13, Y2, Y2
	GATHER(2048, Y2, Y3)
	VPSRLD  $24, Y0, Y2
	GATHER(3072, Y2, Y3)
	SUM8
	ADDQ    $8, BX
	CMPQ    BX, CX
	JLE     pc_all
	JMP     pc_done

pc_rgb:
	VMOVDQU (DX)(BX*4), Y0
	VPAND   Y13, Y0, Y2
	VPXOR   Y3, Y3, Y3
	GATHER(0, Y2, Y3)
	VPSRLD  $8, Y0, Y2
	VPAND   Y13, Y2, Y2
	GATHER(1024, Y2, Y3)
	VPSRLD  $16, Y0, Y2
	VPAND   Y13, Y2, Y2
	GATHER(2048, Y2, Y3)
	SUM8
	ADDQ    $8, BX
	CMPQ    BX, CX
	JLE     pc_rgb
	JMP     pc_done

pc_g:
	VMOVDQU (DX)(BX*4), Y0
	VPSRLD  $8, Y0, Y2
	VPAND   Y13, Y2, Y2
	VPXOR   Y3, Y3, Y3
	GATHER(1024, Y2, Y3)
	SUM8
	ADDQ    $8, BX
	CMPQ    BX, CX
	JLE     pc_g

pc_done:
	VZEROUPPER

pc_none:
	MOVQ BX, ret+40(FP)
	RET

// func ctCostAVX2(tab *[256]uint32, x, y *int8, n int, m int) (int, uint32)
//
// ctCostAVX2 returns the number of values done, whole blocks of 16, and
// the sum over them of tab[uint8(x[i] - m*y[i]>>5)]; only the low byte of
// the difference matters, so x is zero-extended.
TEXT ·ctCostAVX2(SB), NOSPLIT, $0-52
	MOVQ tab+0(FP), SI
	MOVQ x+8(FP), DX
	MOVQ y+16(FP), R8
	MOVQ n+24(FP), CX
	MOVQ m+32(FP), AX
	XORQ BX, BX
	VPXOR X3, X3, X3
	SUBQ $16, CX
	JL   ct_none
	MOVQ AX, X5
	VPBROADCASTW X5, Y5
	VMOVDQU words255<>(SB), Y13

ct_loop:
	VPMOVSXBW (R8)(BX*1), Y1
	VPMOVZXBW (DX)(BX*1), Y2
	VPMULLW   Y5, Y1, Y1
	VPSRAW    $5, Y1, Y1
	VPSUBW    Y1, Y2, Y2
	VPAND     Y13, Y2, Y2
	VPMOVZXWD X2, Y4
	GATHER(0, Y4, Y3)
	VEXTRACTI128 $1, Y2, X2
	VPMOVZXWD X2, Y4
	GATHER(0, Y4, Y3)
	ADDQ $16, BX
	CMPQ BX, CX
	JLE  ct_loop
	VEXTRACTI128 $1, Y3, X1
	VPADDD  X1, X3, X3
	VPSHUFD $0x4e, X3, X1
	VPADDD  X1, X3, X3
	VPSHUFD $0xb1, X3, X1
	VPADDD  X1, X3, X3
	VZEROUPPER

ct_none:
	MOVQ BX, ret+40(FP)
	MOVL X3, ret1+48(FP)
	RET

// func addTopAVX2(m int, cur, top *uint32, n int) int
//
// addTopAVX2 adds the predictions of mode m (0, 2, 3, 4, 8 or 9: those
// that read only the row above) to cur for whole blocks of 8 pixels and
// returns the number done. top[k], top[k+1] and top[k+2] are the
// top-left, top and top-right of cur[k].
TEXT ·addTopAVX2(SB), NOSPLIT, $0-40
	MOVQ m+0(FP), AX
	MOVQ cur+8(FP), DX
	MOVQ top+16(FP), DI
	MOVQ n+24(FP), CX
	XORQ BX, BX
	SUBQ $8, CX
	JL   at_none
	CMPQ AX, $0
	JEQ  at_m0
	CMPQ AX, $2
	JEQ  at_m2
	CMPQ AX, $3
	JEQ  at_m3
	CMPQ AX, $4
	JEQ  at_m4
	CMPQ AX, $8
	JEQ  at_m8
	CMPQ AX, $9
	JEQ  at_m9
	JMP  at_none

at_m0:
	VMOVDQU black<>(SB), Y5
at_m0_loop:
	VMOVDQU (DX)(BX*4), Y1
	VPADDB  Y5, Y1, Y1
	VMOVDQU Y1, (DX)(BX*4)
	ADDQ    $8, BX
	CMPQ    BX, CX
	JLE     at_m0_loop
	JMP     at_done

at_m2:
	VMOVDQU (DX)(BX*4), Y1
	VMOVDQU 4(DI)(BX*4), Y3
	VPADDB  Y3, Y1, Y1
	VMOVDQU Y1, (DX)(BX*4)
	ADDQ    $8, BX
	CMPQ    BX, CX
	JLE     at_m2
	JMP     at_done

at_m3:
	VMOVDQU (DX)(BX*4), Y1
	VMOVDQU 8(DI)(BX*4), Y4
	VPADDB  Y4, Y1, Y1
	VMOVDQU Y1, (DX)(BX*4)
	ADDQ    $8, BX
	CMPQ    BX, CX
	JLE     at_m3
	JMP     at_done

at_m4:
	VMOVDQU (DX)(BX*4), Y1
	VMOVDQU (DI)(BX*4), Y2
	VPADDB  Y2, Y1, Y1
	VMOVDQU Y1, (DX)(BX*4)
	ADDQ    $8, BX
	CMPQ    BX, CX
	JLE     at_m4
	JMP     at_done

at_m8:
	VMOVDQU (DX)(BX*4), Y1
	VMOVDQU (DI)(BX*4), Y2
	VMOVDQU 4(DI)(BX*4), Y3
	AVG(Y2, Y3, Y6, Y5)
	VPADDB  Y5, Y1, Y1
	VMOVDQU Y1, (DX)(BX*4)
	ADDQ    $8, BX
	CMPQ    BX, CX
	JLE     at_m8
	JMP     at_done

at_m9:
	VMOVDQU (DX)(BX*4), Y1
	VMOVDQU 4(DI)(BX*4), Y3
	VMOVDQU 8(DI)(BX*4), Y4
	AVG(Y3, Y4, Y6, Y5)
	VPADDB  Y5, Y1, Y1
	VMOVDQU Y1, (DX)(BX*4)
	ADDQ    $8, BX
	CMPQ    BX, CX
	JLE     at_m9

at_done:
	VZEROUPPER

at_none:
	MOVQ BX, ret+32(FP)
	RET

DATA greenMask<>+0(SB)/8, $0x0000ff000000ff00
DATA greenMask<>+8(SB)/8, $0x0000ff000000ff00
DATA greenMask<>+16(SB)/8, $0x0000ff000000ff00
DATA greenMask<>+24(SB)/8, $0x0000ff000000ff00
GLOBL greenMask<>(SB), RODATA|NOPTR, $32

// CTSETUP loads the arguments of the color transform kernels: Y5 holds
// m1 (green_to_red*8 in the high word, green_to_blue*8 in the low word of
// each pixel), Y6 m2 (red_to_blue*8 in the low word). The terms are
// VPMULHW of a channel in the high byte of a word, its value times 256,
// by the multiplier times 8: (t*c) >> 5, as ColorTransformDelta.
#define CTSETUP \
	MOVQ dst+0(FP), DI; \
	MOVQ src+8(FP), SI; \
	MOVQ n+16(FP), CX; \
	MOVL m1+24(FP), AX; \
	MOVL m2+28(FP), DX; \
	XORQ BX, BX; \
	SUBQ $8, CX; \
	JL   ct_none; \
	MOVL AX, X5; \
	VPBROADCASTD X5, Y5; \
	MOVL DX, X6; \
	VPBROADCASTD X6, Y6; \
	VMOVDQU greenMask<>(SB), Y7; \
	VMOVDQU words255<>(SB), Y8

// GREENTERMS sets Y1 to the green terms of the pixels in Y0: the red one
// in the high word, the blue one in the low word, low bytes only.
#define GREENTERMS \
	VPAND   Y7, Y0, Y1; \
	VPSLLD  $16, Y1, Y2; \
	VPOR    Y2, Y1, Y1; \
	VPMULHW Y5, Y1, Y1; \
	VPAND   Y8, Y1, Y1

// REDTERM sets Y2 to the red term of the blue of the pixels in Y0.
#define REDTERM \
	VPSRLD  $8, Y0, Y2; \
	VPAND   Y7, Y2, Y2; \
	VPMULHW Y6, Y2, Y2; \
	VPAND   Y8, Y2, Y2

// func colorTransformAVX2(dst, src *uint32, n int, m1, m2 uint32) int
//
// colorTransformAVX2 applies the color transform of one element to whole
// blocks of 8 pixels and returns the number done.
TEXT ·colorTransformAVX2(SB), NOSPLIT, $0-40
	CTSETUP

ctf_loop:
	VMOVDQU (SI)(BX*4), Y0
	GREENTERMS
	REDTERM
	VPSUBB  Y1, Y0, Y0
	VPSUBB  Y2, Y0, Y0
	VMOVDQU Y0, (DI)(BX*4)
	ADDQ    $8, BX
	CMPQ    BX, CX
	JLE     ctf_loop
	VZEROUPPER

ct_none:
	MOVQ BX, ret+32(FP)
	RET

// func undoColorTransformAVX2(dst, src *uint32, n int, m1, m2 uint32) int
//
// undoColorTransformAVX2 inverts the color transform of one element for
// whole blocks of 8 pixels and returns the number done: the blue's red
// term uses the restored red.
TEXT ·undoColorTransformAVX2(SB), NOSPLIT, $0-40
	CTSETUP

cti_loop:
	VMOVDQU (SI)(BX*4), Y0
	GREENTERMS
	VPADDB  Y1, Y0, Y0
	REDTERM
	VPADDB  Y2, Y0, Y0
	VMOVDQU Y0, (DI)(BX*4)
	ADDQ    $8, BX
	CMPQ    BX, CX
	JLE     cti_loop
	VZEROUPPER

ct_none:
	MOVQ BX, ret+32(FP)
	RET

// ctShuf groups the bytes of each lane's 4 pixels by channel: blue, green,
// red, alpha.
DATA ctShuf<>+0(SB)/8, $0x0d0905010c080400
DATA ctShuf<>+8(SB)/8, $0x0f0b07030e0a0602
DATA ctShuf<>+16(SB)/8, $0x0d0905010c080400
DATA ctShuf<>+24(SB)/8, $0x0f0b07030e0a0602
GLOBL ctShuf<>(SB), RODATA|NOPTR, $32

// ctPerm puts the channel groups of the two lanes side by side.
DATA ctPerm<>+0(SB)/8, $0x0000000400000000
DATA ctPerm<>+8(SB)/8, $0x0000000500000001
DATA ctPerm<>+16(SB)/8, $0x0000000600000002
DATA ctPerm<>+24(SB)/8, $0x0000000700000003
GLOBL ctPerm<>(SB), RODATA|NOPTR, $32

// HSUM8 adds up the 8 dwords of Y into its low dword. Clobbers Y5.
#define HSUM8(Y, X) \
	VEXTRACTI128 $1, Y, X5; \
	VPADDD       X5, X, X; \
	VPSHUFD      $0x4e, X, X5; \
	VPADDD       X5, X, X; \
	VPSHUFD      $0xb1, X, X5; \
	VPADDD       X5, X, X

// func ctTileAVX2(src *uint32, stride, w, rows int, tg, tr, tb *int8, sums *[5]int32)
//
// ctTileAVX2 writes the green, red and blue bytes of rows rows of w pixels
// (a multiple of 16), stride pixels apart, to tg, tr and tb in order, and the
// sums of g*g, g*r, r*r, g*b and r*b over them, as signed bytes, to sums.
TEXT ·ctTileAVX2(SB), NOSPLIT, $0-64
	MOVQ src+0(FP), SI
	MOVQ stride+8(FP), R9
	MOVQ w+16(FP), R10
	MOVQ rows+24(FP), R11
	MOVQ tg+32(FP), R12
	MOVQ tr+40(FP), R13
	MOVQ tb+48(FP), R14
	MOVQ sums+56(FP), DI
	SHLQ $2, R9
	VMOVDQU ctShuf<>(SB), Y12
	VMOVDQU ctPerm<>(SB), Y11
	VPXOR   Y6, Y6, Y6
	VPXOR   Y7, Y7, Y7
	VPXOR   Y8, Y8, Y8
	VPXOR   Y9, Y9, Y9
	VPXOR   Y10, Y10, Y10
	XORQ    DX, DX

cttile_row:
	XORQ BX, BX

cttile_blk:
	VMOVDQU     (SI)(BX*4), Y0
	VMOVDQU     32(SI)(BX*4), Y1
	VPSHUFB     Y12, Y0, Y0
	VPSHUFB     Y12, Y1, Y1
	VPERMD      Y0, Y11, Y0
	VPERMD      Y1, Y11, Y1
	VPUNPCKLQDQ Y1, Y0, Y2
	VPUNPCKHQDQ Y1, Y0, Y3
	VEXTRACTI128 $1, Y2, X4
	VMOVDQU     X2, (R14)(DX*1)
	VMOVDQU     X4, (R13)(DX*1)
	VMOVDQU     X3, (R12)(DX*1)
	VPMOVSXBW   X3, Y3
	VPMOVSXBW   X4, Y4
	VPMOVSXBW   X2, Y2
	VPMADDWD    Y3, Y3, Y5
	VPADDD      Y5, Y6, Y6
	VPMADDWD    Y4, Y3, Y5
	VPADDD      Y5, Y7, Y7
	VPMADDWD    Y4, Y4, Y5
	VPADDD      Y5, Y8, Y8
	VPMADDWD    Y2, Y3, Y5
	VPADDD      Y5, Y9, Y9
	VPMADDWD    Y2, Y4, Y5
	VPADDD      Y5, Y10, Y10
	ADDQ        $16, DX
	ADDQ        $16, BX
	CMPQ        BX, R10
	JLT         cttile_blk
	ADDQ        R9, SI
	DECQ        R11
	JNZ         cttile_row

	HSUM8(Y6, X6)
	HSUM8(Y7, X7)
	HSUM8(Y8, X8)
	HSUM8(Y9, X9)
	HSUM8(Y10, X10)
	VMOVD X6, 0(DI)
	VMOVD X7, 4(DI)
	VMOVD X8, 8(DI)
	VMOVD X9, 12(DI)
	VMOVD X10, 16(DI)
	VZEROUPPER
	RET
