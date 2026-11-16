//go:build arm64 && !purego

#include "textflag.h"

// Instructions missing from Go's arm64 assembler are written as WORD,
// with the instruction in the comment.

// The weight of each byte of a stride, 32 down to 1.
DATA adlerWeights<>+0(SB)/8, $0x191a1b1c1d1e1f20
DATA adlerWeights<>+8(SB)/8, $0x1112131415161718
DATA adlerWeights<>+16(SB)/8, $0x090a0b0c0d0e0f10
DATA adlerWeights<>+24(SB)/8, $0x0102030405060708
GLOBL adlerWeights<>(SB), RODATA|NOPTR, $32

// func adlerSums(p *byte, n int, lanes *[24]uint32)
TEXT ·adlerSums(SB), NOSPLIT, $0-24
	MOVD p+0(FP), R0
	MOVD n+8(FP), R1
	MOVD lanes+16(FP), R2
	MOVD $adlerWeights<>(SB), R3
	VLD1 (R3), [V20.B16, V21.B16]
	VEOR V16.B16, V16.B16, V16.B16 // byte sums
	VEOR V17.B16, V17.B16, V17.B16 // running totals of the byte sums
	VEOR V18.B16, V18.B16, V18.B16 // weighted sums

loop:
	VLD1.P 32(R0), [V0.B16, V1.B16]
	VADD   V16.S4, V17.S4, V17.S4
	WORD   $0x6e202802             // UADDLP V2.8H, V0.16B
	WORD   $0x6e206822             // UADALP V2.8H, V1.16B
	WORD   $0x6e606850             // UADALP V16.4S, V2.8H
	WORD   $0x2e34c003             // UMULL  V3.8H, V0.8B, V20.8B
	WORD   $0x6e348003             // UMLAL2 V3.8H, V0.16B, V20.16B
	WORD   $0x2e358023             // UMLAL  V3.8H, V1.8B, V21.8B
	WORD   $0x6e358023             // UMLAL2 V3.8H, V1.16B, V21.16B
	WORD   $0x6e606872             // UADALP V18.4S, V3.8H
	SUBS   $32, R1, R1
	BNE    loop

	VST1 [V16.S4], (R2)
	ADD  $32, R2, R4
	VST1 [V17.S4], (R4)
	ADD  $64, R2, R5
	VST1 [V18.S4], (R5)
	RET
