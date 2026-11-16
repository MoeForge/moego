//go:build amd64 && !purego

#include "textflag.h"

// func cpuHasAVX2() bool
TEXT ·cpuHasAVX2(SB), NOSPLIT, $0-1
	MOVB $0, ret+0(FP)
	XORL AX, AX
	CPUID
	CMPL AX, $7
	JB   none
	MOVL $1, AX
	CPUID
	// The OS uses XSAVE (bit 27) and the CPU has AVX (bit 28).
	ANDL $0x18000000, CX
	CMPL CX, $0x18000000
	JNE  none
	// The OS saves XMM and YMM state.
	XORL CX, CX
	XGETBV
	ANDL $6, AX
	CMPL AX, $6
	JNE  none
	MOVL $7, AX
	XORL CX, CX
	CPUID
	// AVX2 is bit 5 of EBX.
	TESTL $32, BX
	JZ    none
	MOVB  $1, ret+0(FP)

none:
	RET

// The weight of each byte of a stride, 32 down to 1.
DATA adlerWeights<>+0(SB)/8, $0x191a1b1c1d1e1f20
DATA adlerWeights<>+8(SB)/8, $0x1112131415161718
DATA adlerWeights<>+16(SB)/8, $0x090a0b0c0d0e0f10
DATA adlerWeights<>+24(SB)/8, $0x0102030405060708
GLOBL adlerWeights<>(SB), RODATA|NOPTR, $32

DATA adlerOnes<>+0(SB)/8, $0x0001000100010001
DATA adlerOnes<>+8(SB)/8, $0x0001000100010001
DATA adlerOnes<>+16(SB)/8, $0x0001000100010001
DATA adlerOnes<>+24(SB)/8, $0x0001000100010001
GLOBL adlerOnes<>(SB), RODATA|NOPTR, $32

// func adlerSums(p *byte, n int, lanes *[24]uint32)
TEXT ·adlerSums(SB), NOSPLIT, $0-24
	MOVQ    p+0(FP), SI
	MOVQ    n+8(FP), CX
	MOVQ    lanes+16(FP), DI
	VPXOR   Y0, Y0, Y0 // zero
	VPXOR   Y1, Y1, Y1 // byte sums
	VPXOR   Y2, Y2, Y2 // running totals of the byte sums
	VPXOR   Y3, Y3, Y3 // weighted sums
	VMOVDQU adlerWeights<>(SB), Y4
	VMOVDQU adlerOnes<>(SB), Y5

loop:
	VMOVDQU    (SI), Y6
	VPADDD     Y1, Y2, Y2
	// Four sums of eight bytes, in the low half of each quadword.
	VPSADBW    Y0, Y6, Y7
	VPADDD     Y7, Y1, Y1
	// Byte times weight, adjacent pairs added into words, then adjacent
	// words into doublewords.
	VPMADDUBSW Y4, Y6, Y8
	VPMADDWD   Y5, Y8, Y8
	VPADDD     Y8, Y3, Y3
	ADDQ       $32, SI
	SUBQ       $32, CX
	JNZ        loop

	VMOVDQU Y1, 0(DI)
	VMOVDQU Y2, 32(DI)
	VMOVDQU Y3, 64(DI)
	VZEROUPPER
	RET
