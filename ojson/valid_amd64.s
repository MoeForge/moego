//go:build amd64 && !purego

#include "textflag.h"

// validWS maps the low nibble of an ASCII byte to the whitespace byte with
// that nibble, if any: ' ', '\t', '\n' and '\r'. Other entries match no
// ASCII byte, and VPSHUFB gives 0 for the others.
DATA validWS<>+0(SB)/8, $0xffffffffffffff20
DATA validWS<>+8(SB)/8, $0xffff0dffff0a09ff
DATA validWS<>+16(SB)/8, $0xffffffffffffff20
DATA validWS<>+24(SB)/8, $0xffff0dffff0a09ff
GLOBL validWS<>(SB), RODATA|NOPTR, $32

// validOp is validWS for '{', '}', ':' and ',' in bytes ORed with 0x20,
// which folds '[' and ']' into '{' and '}'.
DATA validOp<>+0(SB)/8, $0xffffffffffffffff
DATA validOp<>+8(SB)/8, $0xffff7d2c7b3affff
DATA validOp<>+16(SB)/8, $0xffffffffffffffff
DATA validOp<>+24(SB)/8, $0xffff7d2c7b3affff
GLOBL validOp<>(SB), RODATA|NOPTR, $32

// VBCAST puts the byte c in all 32 lanes of y, through its low half x.
// Clobbers AX.
#define VBCAST(c, x, y) \
    MOVL $c, AX; \
    VMOVD AX, x; \
    VPBROADCASTB x, y

// VMASK leaves in dst the 64-bit mask of the high bits of the low half a
// and the high half b. Clobbers AX.
#define VMASK(a, b, dst) \
    VPMOVMSKB a, dst; \
    VPMOVMSKB b, AX; \
    SHLQ $32, AX; \
    ORQ AX, dst

// VEQ compares the 64 bytes in Y0 and Y1 with the lanes of c into Y2 and
// Y3.
#define VEQ(c) \
    VPCMPEQB c, Y0, Y2; \
    VPCMPEQB c, Y1, Y3

// VESC adds the bytes equal to c to the escape mask in Y6 and Y7. Clobbers
// Y2 to Y4 and AX.
#define VESC(c) \
    VBCAST(c, X4, Y4); \
    VEQ(Y4); \
    VPOR Y2, Y6, Y6; \
    VPOR Y3, Y7, Y7

// func validMasksAVX2(p *byte, nb int, out *vmask)
//
// Fills out[0:nb] with the vmask of the 64-byte blocks at p.
TEXT ·validMasksAVX2(SB), NOSPLIT, $0-24
    MOVQ p+0(FP), SI
    MOVQ nb+8(FP), CX
    MOVQ out+16(FP), DI
    VBCAST(0x22, X15, Y15)
    VBCAST(0x5c, X14, Y14)
    VBCAST(0x1f, X13, Y13)
    VBCAST(0x20, X12, Y12)
    VMOVDQU validWS<>(SB), Y11
    VMOVDQU validOp<>(SB), Y10
    MOVQ $-1, AX
    VMOVQ AX, X9            // all ones, for the prefix XOR

valid_block:
    TESTQ CX, CX
    JZ valid_done
    VMOVDQU (SI), Y0
    VMOVDQU 32(SI), Y1
    VEQ(Y15)
    VMASK(Y2, Y3, BX)
    MOVQ BX, 0(DI)          // quote
    VMOVQ BX, X4
    VPCLMULQDQ $0, X9, X4, X4
    VMOVQ X4, 56(DI)        // r0
    VEQ(Y14)
    VMASK(Y2, Y3, R8)
    MOVQ R8, 8(DI)          // bslash
    VPSHUFB Y0, Y11, Y2
    VPCMPEQB Y2, Y0, Y2
    VPSHUFB Y1, Y11, Y3
    VPCMPEQB Y3, Y1, Y3
    VMASK(Y2, Y3, BX)
    MOVQ BX, 16(DI)         // ws
    VPMINUB Y13, Y0, Y2
    VPCMPEQB Y2, Y0, Y2
    VPMINUB Y13, Y1, Y3
    VPCMPEQB Y3, Y1, Y3
    VMASK(Y2, Y3, DX)
    MOVQ DX, 32(DI)         // ctrl
    VPOR Y12, Y0, Y4
    VPSHUFB Y4, Y10, Y2
    VPCMPEQB Y2, Y4, Y2
    VPOR Y12, Y1, Y5
    VPSHUFB Y5, Y10, Y3
    VPCMPEQB Y3, Y5, Y3
    VMASK(Y2, Y3, BX)
    ANDNQ BX, DX, BX        // control bytes fold onto ':' and ','
    MOVQ BX, 24(DI)         // op
    MOVQ $0, 40(DI)
    MOVQ $0, 48(DI)
    TESTQ R8, R8
    JZ valid_next
    VEQ(Y15)
    VMOVDQU Y2, Y6
    VMOVDQU Y3, Y7
    VEQ(Y14)
    VPOR Y2, Y6, Y6
    VPOR Y3, Y7, Y7
    VESC(0x2f)              // '/'
    VESC(0x62)              // 'b'
    VESC(0x66)              // 'f'
    VESC(0x6e)              // 'n'
    VESC(0x72)              // 'r'
    VESC(0x74)              // 't'
    VMASK(Y6, Y7, BX)
    MOVQ BX, 40(DI)         // esc
    VBCAST(0x75, X4, Y4)
    VEQ(Y4)
    VMASK(Y2, Y3, BX)
    MOVQ BX, 48(DI)         // u

valid_next:
    ADDQ $64, SI
    ADDQ $64, DI
    DECQ CX
    JMP valid_block

valid_done:
    VZEROUPPER
    RET
