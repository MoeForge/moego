//go:build amd64 && !purego

#include "textflag.h"

// func copyNTAVX2(dst, src *byte, n int)
//
// Copies n bytes, a multiple of 128, from src to dst, which is 32-byte
// aligned, with stores that bypass the caches.
TEXT ·copyNTAVX2(SB), NOSPLIT, $0-24
    MOVQ dst+0(FP), DI
    MOVQ src+8(FP), SI
    MOVQ n+16(FP), DX
    XORQ CX, CX

nt_block:
    CMPQ CX, DX
    JAE nt_done
    VMOVDQU (SI)(CX*1), Y0
    VMOVDQU 32(SI)(CX*1), Y1
    VMOVDQU 64(SI)(CX*1), Y2
    VMOVDQU 96(SI)(CX*1), Y3
    VMOVNTDQ Y0, (DI)(CX*1)
    VMOVNTDQ Y1, 32(DI)(CX*1)
    VMOVNTDQ Y2, 64(DI)(CX*1)
    VMOVNTDQ Y3, 96(DI)(CX*1)
    ADDQ $128, CX
    JMP nt_block

nt_done:
    SFENCE
    VZEROUPPER
    RET

// func zeroAVX2(p *byte, n int) bool
//
// Reports whether the n bytes at p, at least 32, are all zero. It reads
// only those bytes: the last 32 first, then 128 and 32 at a time.
TEXT ·zeroAVX2(SB), NOSPLIT, $0-17
    MOVQ p+0(FP), SI
    MOVQ n+8(FP), CX
    VMOVDQU -32(SI)(CX*1), Y0
    CMPQ CX, $128
    JB zero_tail

zero_loop:
    VPOR (SI), Y0, Y0
    VMOVDQU 32(SI), Y1
    VPOR 64(SI), Y1, Y1
    VPOR 96(SI), Y0, Y0
    VPOR Y1, Y0, Y0
    VPTEST Y0, Y0
    JNZ zero_done
    ADDQ $128, SI
    SUBQ $128, CX
    CMPQ CX, $128
    JAE zero_loop

zero_tail:
    CMPQ CX, $32
    JB zero_done
    VPOR (SI), Y0, Y0
    ADDQ $32, SI
    SUBQ $32, CX
    JMP zero_tail

zero_done:
    VPTEST Y0, Y0
    SETEQ ret+16(FP)
    VZEROUPPER
    RET
