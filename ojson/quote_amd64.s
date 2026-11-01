//go:build amd64 && !purego

#include "textflag.h"

// QBCAST puts the byte c in all 32 lanes of y, through its low half x.
// Clobbers AX.
#define QBCAST(c, x, y) \
    MOVL $c, AX; \
    VMOVD AX, x; \
    VPBROADCASTB x, y

// QSETUP loads the constants of the quote kernels: quotes (Y15),
// backslashes (Y14), 0x1f (Y13), the sign bits (Y9), and '<', '>' and '&'
// (Y12, Y11, Y10) when the byte in r is not zero, else zero bytes, which
// match only control bytes. Clobbers AX.
#define QSETUP(r, label) \
    QBCAST(0x22, X15, Y15); \
    QBCAST(0x5c, X14, Y14); \
    QBCAST(0x1f, X13, Y13); \
    QBCAST(0x80, X9, Y9); \
    VPXOR Y12, Y12, Y12; \
    VPXOR Y11, Y11, Y11; \
    VPXOR Y10, Y10, Y10; \
    TESTB r, r; \
    JZ label; \
    QBCAST(0x3c, X12, Y12); \
    QBCAST(0x3e, X11, Y11); \
    QBCAST(0x26, X10, Y10); \
label:

// QOTHER1 marks with their sign bit, in dst, the bytes of src that need an
// escape other than a backslash: control bytes, bytes of multi-byte UTF-8
// and, with html, '<', '>' and '&'. Clobbers t.
#define QOTHER1(src, dst, t) \
    VPMINUB Y13, src, dst; \
    VPCMPEQB dst, src, dst; \
    VPOR src, dst, dst; \
    VPCMPEQB Y12, src, t; \
    VPOR t, dst, dst; \
    VPCMPEQB Y11, src, t; \
    VPOR t, dst, dst; \
    VPCMPEQB Y10, src, t; \
    VPOR t, dst, dst

// QOTHER sets ZF when none of the 64 bytes in Y0 and Y1 needs such an
// escape. Clobbers Y2 to Y5.
#define QOTHER \
    QOTHER1(Y0, Y2, Y3); \
    QOTHER1(Y1, Y4, Y5); \
    VPOR Y4, Y2, Y2; \
    VPTEST Y9, Y2

// QSIMPLE leaves in dst the 64-bit mask of the quotes and backslashes in Y0
// and Y1. Clobbers Y2 to Y5 and AX.
#define QSIMPLE(dst) \
    VPCMPEQB Y15, Y0, Y2; \
    VPCMPEQB Y14, Y0, Y3; \
    VPOR Y3, Y2, Y2; \
    VPCMPEQB Y15, Y1, Y4; \
    VPCMPEQB Y14, Y1, Y5; \
    VPOR Y5, Y4, Y4; \
    VPMOVMSKB Y2, dst; \
    VPMOVMSKB Y4, AX; \
    SHLQ $32, AX; \
    ORQ AX, dst

// func quoteCountAVX2(src *byte, n, i int, html bool) (int, int)
//
// Counts the quotes and backslashes in the 64-byte blocks of src[i:n]
// while no byte of a block needs another escape. It returns the index of
// the first block it did not count and the count.
TEXT ·quoteCountAVX2(SB), NOSPLIT, $0-48
    MOVQ src+0(FP), SI
    MOVQ n+8(FP), DX
    MOVQ i+16(FP), CX
    MOVB html+24(FP), R9
    XORQ R8, R8
    QSETUP(R9, count_setup)

count_block:
    LEAQ 64(CX), AX
    CMPQ AX, DX
    JA count_done
    VMOVDQU (SI)(CX*1), Y0
    VMOVDQU 32(SI)(CX*1), Y1
    QOTHER
    JNZ count_done
    QSIMPLE(BX)
    POPCNTQ BX, BX
    ADDQ BX, R8
    ADDQ $64, CX
    JMP count_block

count_done:
    VZEROUPPER
    MOVQ CX, ret+32(FP)
    MOVQ R8, ret1+40(FP)
    RET

// QGROUP writes the 8-byte group at src[CX+off], whose quotes and
// backslashes are the bits of the low byte of R11, to dst[R8:] with a
// backslash before each of them, and moves R8 past it. The store is 16
// bytes wide; the bytes past the group are overwritten next or lie in the
// room the caller checked.
#define QGROUP(off) \
    MOVBQZX R11, AX; \
    SHRQ $8, R11; \
    VMOVQ off(SI)(CX*1), X6; \
    VPUNPCKLQDQ X14, X6, X6; \
    MOVQ AX, BX; \
    SHLQ $4, BX; \
    VMOVDQU (R10)(BX*1), X5; \
    VPSHUFB X5, X6, X6; \
    VMOVDQU X6, (DI)(R8*1); \
    POPCNTL AX, AX; \
    LEAQ 8(R8)(AX*1), R8

// func quoteAVX2(dst *byte, dn int, src *byte, n, i, w int, html bool, shuf *[256][2]uint64) (int, int)
//
// Writes the 64-byte blocks of src[i:n] to dst[w:dn] as JSON string
// contents, while no byte of a block needs an escape other than a
// backslash and dst has room for the longest result of a block, 128 bytes,
// and the 8 bytes a group store may pass it. It returns the index of the
// first block it did not write and the new w.
TEXT ·quoteAVX2(SB), NOSPLIT, $0-80
    MOVQ dst+0(FP), DI
    MOVQ dn+8(FP), R12
    MOVQ src+16(FP), SI
    MOVQ n+24(FP), DX
    MOVQ i+32(FP), CX
    MOVQ w+40(FP), R8
    MOVB html+48(FP), R9
    MOVQ shuf+56(FP), R10
    QSETUP(R9, write_setup)
    SUBQ $136, R12          // the last w a block may start at

write_block:
    LEAQ 64(CX), AX
    CMPQ AX, DX
    JA write_done
    CMPQ R8, R12
    JGT write_done
    VMOVDQU (SI)(CX*1), Y0
    VMOVDQU 32(SI)(CX*1), Y1
    QOTHER
    JNZ write_done
    QSIMPLE(R11)
    TESTQ R11, R11
    JNZ write_escapes
    VMOVDQU Y0, (DI)(R8*1)
    VMOVDQU Y1, 32(DI)(R8*1)
    ADDQ $64, R8

write_next:
    ADDQ $64, CX
    JMP write_block

write_escapes:
    QGROUP(0)
    QGROUP(8)
    QGROUP(16)
    QGROUP(24)
    QGROUP(32)
    QGROUP(40)
    QGROUP(48)
    QGROUP(56)
    JMP write_next

write_done:
    VZEROUPPER
    MOVQ CX, ret+64(FP)
    MOVQ R8, ret1+72(FP)
    RET
