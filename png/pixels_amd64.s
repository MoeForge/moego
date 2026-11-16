//go:build amd64 && !purego

#include "textflag.h"

// func rgbaFlagsAVX2(p *byte, n int) uint64
//
// rgbaFlagsAVX2 compares the bytes of p, n a positive multiple of 32. The
// low 32 bits of the result have bit k set when byte k mod 32 is 0xff in
// every block; the high 32 bits when byte k equals byte k+1 within its
// 4-byte group in every block (never for the last byte of a group).
TEXT ·rgbaFlagsAVX2(SB), NOSPLIT, $0-24
	MOVQ     p+0(FP), SI
	MOVQ     n+8(FP), CX
	VPCMPEQB Y0, Y0, Y0
	VMOVDQA  Y0, Y1
	VMOVDQA  Y0, Y2

flags_loop:
	VMOVDQU  (SI), Y3
	VPCMPEQB Y0, Y3, Y4
	VPAND    Y4, Y1, Y1
	VPSRLD   $8, Y3, Y5
	VPCMPEQB Y5, Y3, Y5
	VPAND    Y5, Y2, Y2
	ADDQ     $32, SI
	SUBQ     $32, CX
	JNZ      flags_loop

	VPMOVMSKB Y1, AX
	VPMOVMSKB Y2, BX
	SHLQ      $32, BX
	ORQ       BX, AX
	MOVQ      AX, ret+16(FP)
	VZEROUPPER
	RET

// func shuffleAVX2(dst, src *byte, blocks int, ctl *[32]byte, perm *[8]uint32, step int)
//
// shuffleAVX2 picks bytes out of each 32-byte block of src with VPSHUFB
// (within 16-byte lanes) and VPERMD, and stores the 32 bytes at dst,
// advancing dst by step per block.
TEXT ·shuffleAVX2(SB), NOSPLIT, $0-48
	MOVQ    dst+0(FP), DI
	MOVQ    src+8(FP), SI
	MOVQ    blocks+16(FP), CX
	MOVQ    ctl+24(FP), AX
	MOVQ    perm+32(FP), BX
	MOVQ    step+40(FP), DX
	VMOVDQU (AX), Y6
	VMOVDQU (BX), Y7

shuffle_loop:
	VMOVDQU (SI), Y0
	VPSHUFB Y6, Y0, Y0
	VPERMD  Y0, Y7, Y0
	VMOVDQU Y0, (DI)
	ADDQ    $32, SI
	ADDQ    DX, DI
	DECQ    CX
	JNZ     shuffle_loop

	VZEROUPPER
	RET
