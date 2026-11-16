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

DATA ones<>+0(SB)/8, $0x0101010101010101
DATA ones<>+8(SB)/8, $0x0101010101010101
DATA ones<>+16(SB)/8, $0x0101010101010101
DATA ones<>+24(SB)/8, $0x0101010101010101
GLOBL ones<>(SB), RODATA|NOPTR, $32

// AVG sets dst to the Average predictor of a and b, floor((a+b)/2):
// VPAVGB rounds up, so subtract the low bit of a^b. Clobbers tmp.
#define AVG(a, b, tmp, dst) \
	VPAVGB b, a, dst; \
	VPXOR  b, a, tmp; \
	VPAND  ones<>(SB), tmp, tmp; \
	VPSUBB tmp, dst, dst

// PAETH sets dst to the Paeth predictor of a, b and c. With pa = |b-c|
// and pb = |a-c|, pc = |(b-c)+(a-c)| is pa+pb when b-c and a-c have the
// same sign, else |pa-pb|. pa+pb saturates at 255, which keeps it at or
// above both pa and pb, so every comparison comes out as with exact
// values. Clobbers t0 to t4.
#define PAETH(a, b, c, t0, t1, t2, t3, t4, dst) \
	VPMINUB  c, b, t0; \
	VPMAXUB  c, b, t1; \
	VPCMPEQB t1, b, t2; \
	VPSUBB   t0, t1, t0; \
	VPMINUB  c, a, t1; \
	VPMAXUB  c, a, t3; \
	VPCMPEQB t3, a, t4; \
	VPSUBB   t1, t3, t1; \
	VPXOR    t4, t2, t2; \
	VPADDUSB t1, t0, t3; \
	VPMINUB  t1, t0, t4; \
	VPMAXUB  t1, t0, dst; \
	VPSUBB   t4, dst, dst; \
	VPBLENDVB t2, dst, t3, t3; \
	VPCMPEQB t4, t0, t4; \
	VPMINUB  t3, t0, t2; \
	VPCMPEQB t2, t0, t2; \
	VPAND    t2, t4, t4; \
	VPMINUB  t3, t1, t2; \
	VPCMPEQB t2, t1, t2; \
	VPBLENDVB t2, b, c, dst; \
	VPBLENDVB t4, a, dst, dst

// COST adds the sum of the magnitudes of the signed bytes of r to the four
// quadwords of acc. Y15 is zero.
#define COST(r, acc) \
	VPABSB  r, r; \
	VPSADBW Y15, r, r; \
	VPADDQ  r, acc, acc

// REDUCE adds the four quadwords of acc to the uint64 at off(DX).
#define REDUCE(acc, xacc, off) \
	VEXTRACTI128 $1, acc, X0; \
	VPADDQ       X0, xacc, xacc; \
	VPSHUFD      $0x4e, xacc, X0; \
	VPADDQ       X0, xacc, xacc; \
	VMOVQ        xacc, AX; \
	ADDQ         AX, off(DX)

// func costsAVX2(cur, prev *byte, n, bpp int, c *[5]uint64) int
//
// costsAVX2 adds to c the cost of each filter for the bytes from bpp on,
// in whole 32-byte blocks, and returns the end of the last block.
TEXT ·costsAVX2(SB), NOSPLIT, $0-48
	MOVQ cur+0(FP), SI
	MOVQ prev+8(FP), DI
	MOVQ n+16(FP), CX
	MOVQ bpp+24(FP), BX
	MOVQ c+32(FP), DX
	MOVQ BX, R10
	LEAQ (SI)(BX*1), R11 // &cur[i]
	LEAQ (DI)(BX*1), R12 // &prev[i]
	MOVQ SI, R8          // &cur[i-bpp]
	MOVQ DI, R9          // &prev[i-bpp]
	SUBQ $32, CX
	CMPQ R10, CX
	JGT  costs_none
	VPXOR Y15, Y15, Y15
	VPXOR Y9, Y9, Y9
	VPXOR Y10, Y10, Y10
	VPXOR Y11, Y11, Y11
	VPXOR Y12, Y12, Y12
	VPXOR Y13, Y13, Y13

costs_loop:
	VMOVDQU (R11), Y0 // x
	VMOVDQU (R8), Y1  // a
	VMOVDQU (R12), Y2 // b
	VMOVDQU (R9), Y3  // c
	VMOVDQA Y0, Y4
	COST(Y4, Y9)
	VPSUBB Y1, Y0, Y4
	COST(Y4, Y10)
	VPSUBB Y2, Y0, Y4
	COST(Y4, Y11)
	AVG(Y1, Y2, Y5, Y4)
	VPSUBB Y4, Y0, Y4
	COST(Y4, Y12)
	PAETH(Y1, Y2, Y3, Y4, Y5, Y6, Y7, Y8, Y14)
	VPSUBB Y14, Y0, Y14
	COST(Y14, Y13)
	ADDQ $32, R11
	ADDQ $32, R12
	ADDQ $32, R8
	ADDQ $32, R9
	ADDQ $32, R10
	CMPQ R10, CX
	JLE  costs_loop

	REDUCE(Y9, X9, 0)
	REDUCE(Y10, X10, 8)
	REDUCE(Y11, X11, 16)
	REDUCE(Y12, X12, 24)
	REDUCE(Y13, X13, 32)
	VZEROUPPER

costs_none:
	MOVQ R10, ret+40(FP)
	RET

// func filterAVX2(dst, cur, prev *byte, n, bpp, f int) int
//
// filterAVX2 writes filter f's residuals for the bytes from bpp on, in
// whole 32-byte blocks, and returns the end of the last block. f is Sub,
// Up, Average or Paeth.
TEXT ·filterAVX2(SB), NOSPLIT, $0-56
	MOVQ dst+0(FP), DI
	MOVQ cur+8(FP), SI
	MOVQ prev+16(FP), DX
	MOVQ n+24(FP), CX
	MOVQ bpp+32(FP), BX
	MOVQ f+40(FP), AX
	MOVQ BX, R10
	LEAQ (DI)(BX*1), R13 // &dst[i]
	LEAQ (SI)(BX*1), R11 // &cur[i]
	LEAQ (DX)(BX*1), R12 // &prev[i]
	MOVQ SI, R8          // &cur[i-bpp]
	MOVQ DX, R9          // &prev[i-bpp]
	SUBQ $32, CX
	CMPQ R10, CX
	JGT  filter_none
	CMPQ AX, $1
	JEQ  filter_sub
	CMPQ AX, $2
	JEQ  filter_up
	CMPQ AX, $3
	JEQ  filter_avg

filter_paeth:
	VMOVDQU (R11), Y0
	VMOVDQU (R8), Y1
	VMOVDQU (R12), Y2
	VMOVDQU (R9), Y3
	PAETH(Y1, Y2, Y3, Y4, Y5, Y6, Y7, Y8, Y9)
	VPSUBB  Y9, Y0, Y9
	VMOVDQU Y9, (R13)
	ADDQ    $32, R13
	ADDQ    $32, R11
	ADDQ    $32, R12
	ADDQ    $32, R8
	ADDQ    $32, R9
	ADDQ    $32, R10
	CMPQ    R10, CX
	JLE     filter_paeth
	JMP     filter_done

filter_sub:
	VMOVDQU (R11), Y0
	VMOVDQU (R8), Y1
	VPSUBB  Y1, Y0, Y0
	VMOVDQU Y0, (R13)
	ADDQ    $32, R13
	ADDQ    $32, R11
	ADDQ    $32, R8
	ADDQ    $32, R10
	CMPQ    R10, CX
	JLE     filter_sub
	JMP     filter_done

filter_up:
	VMOVDQU (R11), Y0
	VMOVDQU (R12), Y2
	VPSUBB  Y2, Y0, Y0
	VMOVDQU Y0, (R13)
	ADDQ    $32, R13
	ADDQ    $32, R11
	ADDQ    $32, R12
	ADDQ    $32, R10
	CMPQ    R10, CX
	JLE     filter_up
	JMP     filter_done

filter_avg:
	VMOVDQU (R11), Y0
	VMOVDQU (R8), Y1
	VMOVDQU (R12), Y2
	AVG(Y1, Y2, Y5, Y4)
	VPSUBB  Y4, Y0, Y4
	VMOVDQU Y4, (R13)
	ADDQ    $32, R13
	ADDQ    $32, R11
	ADDQ    $32, R12
	ADDQ    $32, R8
	ADDQ    $32, R10
	CMPQ    R10, CX
	JLE     filter_avg

filter_done:
	VZEROUPPER

filter_none:
	MOVQ R10, ret+48(FP)
	RET
