//go:build amd64 && !purego

#include "textflag.h"

// func cpuHasAES() bool
TEXT ·cpuHasAES(SB), NOSPLIT, $0-1
    MOVL $1, AX
    XORL CX, CX
    CPUID
    // AES (bit 25).
    SHRL $25, CX
    ANDL $1, CX
    MOVB CX, ret+0(FP)
    RET

// func hashAES(key *[24]uint64, p *byte, n int, tail *[128]byte, total int) uint64
//
// Hashes p[:n], n a positive multiple of 128, and then tail, in eight
// lanes of 16 bytes that start as the key bytes 0 to 127. Each 128 bytes
// give each lane an AES round with its 16 bytes as the round key; those of
// tail are first XORed with the key bytes 128 to 143. The lanes are then
// folded into one by AES rounds, which takes total and two more rounds
// with the key bytes 144 to 175. It keeps to 128-bit instructions, which
// leave the clock as it is, where 256-bit multiplies may lower it.
TEXT ·hashAES(SB), NOSPLIT, $0-48
    MOVQ key+0(FP), R8
    MOVQ p+8(FP), SI
    MOVQ n+16(FP), CX
    MOVQ tail+24(FP), R9
    VMOVDQU 0(R8), X0
    VMOVDQU 16(R8), X1
    VMOVDQU 32(R8), X2
    VMOVDQU 48(R8), X3
    VMOVDQU 64(R8), X4
    VMOVDQU 80(R8), X5
    VMOVDQU 96(R8), X6
    VMOVDQU 112(R8), X7

hash_loop:
    VAESENC 0(SI), X0, X0
    VAESENC 16(SI), X1, X1
    VAESENC 32(SI), X2, X2
    VAESENC 48(SI), X3, X3
    VAESENC 64(SI), X4, X4
    VAESENC 80(SI), X5, X5
    VAESENC 96(SI), X6, X6
    VAESENC 112(SI), X7, X7
    ADDQ $128, SI
    SUBQ $128, CX
    JNZ hash_loop

    VMOVDQU 128(R8), X8
    VPXOR 0(R9), X8, X9
    VAESENC X9, X0, X0
    VPXOR 16(R9), X8, X9
    VAESENC X9, X1, X1
    VPXOR 32(R9), X8, X9
    VAESENC X9, X2, X2
    VPXOR 48(R9), X8, X9
    VAESENC X9, X3, X3
    VPXOR 64(R9), X8, X9
    VAESENC X9, X4, X4
    VPXOR 80(R9), X8, X9
    VAESENC X9, X5, X5
    VPXOR 96(R9), X8, X9
    VAESENC X9, X6, X6
    VPXOR 112(R9), X8, X9
    VAESENC X9, X7, X7
    VAESENC X1, X0, X0
    VAESENC X3, X2, X2
    VAESENC X5, X4, X4
    VAESENC X7, X6, X6
    VAESENC X2, X0, X0
    VAESENC X6, X4, X4
    VAESENC X4, X0, X0
    MOVQ total+32(FP), R10
    VMOVQ R10, X1
    VPXOR X1, X0, X0
    VMOVDQU 144(R8), X2
    VMOVDQU 160(R8), X3
    VAESENC X2, X0, X0
    VAESENC X3, X0, X0
    VAESENC X2, X0, X0
    VMOVQ X0, AX
    MOVQ AX, ret+40(FP)
    RET
