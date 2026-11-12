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

// func copyPlainAVX2(dst, src *byte, n int, html bool) int
//
// Copies the 64-byte blocks of src[:n] to dst while no byte of a block
// needs an escape, quotes and backslashes included, and returns the length
// copied.
TEXT ·copyPlainAVX2(SB), NOSPLIT, $0-40
    MOVQ dst+0(FP), DI
    MOVQ src+8(FP), SI
    MOVQ n+16(FP), DX
    MOVB html+24(FP), R9
    XORQ CX, CX
    QSETUP(R9, plain_setup)

plain_block:
    LEAQ 64(CX), AX
    CMPQ AX, DX
    JA plain_done
    VMOVDQU (SI)(CX*1), Y0
    VMOVDQU 32(SI)(CX*1), Y1
    QOTHER1(Y0, Y2, Y3)
    QOTHER1(Y1, Y4, Y5)
    VPOR Y4, Y2, Y2
    VPCMPEQB Y15, Y0, Y3
    VPCMPEQB Y14, Y0, Y5
    VPOR Y3, Y2, Y2
    VPOR Y5, Y2, Y2
    VPCMPEQB Y15, Y1, Y3
    VPCMPEQB Y14, Y1, Y5
    VPOR Y3, Y2, Y2
    VPOR Y5, Y2, Y2
    VPTEST Y9, Y2
    JNZ plain_done
    VMOVDQU Y0, (DI)(CX*1)
    VMOVDQU Y1, 32(DI)(CX*1)
    ADDQ $64, CX
    JMP plain_block

plain_done:
    VZEROUPPER
    MOVQ CX, ret+32(FP)
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

// func quoteAVX2(dst *byte, dn int, src *byte, n, i, w int, html bool, shuf *[256][16]byte) (int, int)
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

// Constants of CopyShortAVX2, 32 bytes each: 0x1f, quotes, backslashes,
// '<', '>' and '&'.
DATA shortConst<>+0x00(SB)/8, $0x1f1f1f1f1f1f1f1f
DATA shortConst<>+0x08(SB)/8, $0x1f1f1f1f1f1f1f1f
DATA shortConst<>+0x10(SB)/8, $0x1f1f1f1f1f1f1f1f
DATA shortConst<>+0x18(SB)/8, $0x1f1f1f1f1f1f1f1f
DATA shortConst<>+0x20(SB)/8, $0x2222222222222222
DATA shortConst<>+0x28(SB)/8, $0x2222222222222222
DATA shortConst<>+0x30(SB)/8, $0x2222222222222222
DATA shortConst<>+0x38(SB)/8, $0x2222222222222222
DATA shortConst<>+0x40(SB)/8, $0x5c5c5c5c5c5c5c5c
DATA shortConst<>+0x48(SB)/8, $0x5c5c5c5c5c5c5c5c
DATA shortConst<>+0x50(SB)/8, $0x5c5c5c5c5c5c5c5c
DATA shortConst<>+0x58(SB)/8, $0x5c5c5c5c5c5c5c5c
DATA shortConst<>+0x60(SB)/8, $0x3c3c3c3c3c3c3c3c
DATA shortConst<>+0x68(SB)/8, $0x3c3c3c3c3c3c3c3c
DATA shortConst<>+0x70(SB)/8, $0x3c3c3c3c3c3c3c3c
DATA shortConst<>+0x78(SB)/8, $0x3c3c3c3c3c3c3c3c
DATA shortConst<>+0x80(SB)/8, $0x3e3e3e3e3e3e3e3e
DATA shortConst<>+0x88(SB)/8, $0x3e3e3e3e3e3e3e3e
DATA shortConst<>+0x90(SB)/8, $0x3e3e3e3e3e3e3e3e
DATA shortConst<>+0x98(SB)/8, $0x3e3e3e3e3e3e3e3e
DATA shortConst<>+0xa0(SB)/8, $0x2626262626262626
DATA shortConst<>+0xa8(SB)/8, $0x2626262626262626
DATA shortConst<>+0xb0(SB)/8, $0x2626262626262626
DATA shortConst<>+0xb8(SB)/8, $0x2626262626262626
GLOBL shortConst<>(SB), RODATA|NOPTR, $0xc0

// SPLAIN leaves in dst the bytes of src that need no escape set to all
// ones, others to zero: ASCII above 0x1f other than quotes, backslashes
// and, when the byte in R9 is not zero, '<', '>' and '&'. Clobbers t.
#define SPLAIN(src, dst, t, label) \
    VPCMPGTB shortConst<>+0x00(SB), src, dst; \
    VPCMPEQB shortConst<>+0x20(SB), src, t; \
    VPANDN dst, t, dst; \
    VPCMPEQB shortConst<>+0x40(SB), src, t; \
    VPANDN dst, t, dst; \
    TESTB R9, R9; \
    JZ label; \
    VPCMPEQB shortConst<>+0x60(SB), src, t; \
    VPANDN dst, t, dst; \
    VPCMPEQB shortConst<>+0x80(SB), src, t; \
    VPANDN dst, t, dst; \
    VPCMPEQB shortConst<>+0xa0(SB), src, t; \
    VPANDN dst, t, dst; \
label:

// func CopyShortAVX2(dst, src *byte, n int, html bool) bool
//
// Copies src[:n], 0 < n < 64, to dst when no byte of it needs an escape
// in a JSON string, and reports whether it did; or reports false. Under
// 32 bytes it reads 32 bytes from src, which must not cross into the next
// page, and writes 32 bytes to dst.
TEXT ·CopyShortAVX2(SB), NOSPLIT, $0-33
    MOVQ dst+0(FP), DI
    MOVQ src+8(FP), SI
    MOVQ n+16(FP), DX
    MOVB html+24(FP), R9
    CMPQ DX, $32
    JB short_one
    VMOVDQU (SI), Y0
    VMOVDQU -32(SI)(DX*1), Y1
    SPLAIN(Y0, Y2, Y3, short_h0)
    SPLAIN(Y1, Y4, Y5, short_h1)
    VPAND Y4, Y2, Y2
    VPMOVMSKB Y2, AX
    CMPL AX, $0xffffffff
    JNE short_fail
    VMOVDQU Y0, (DI)
    VMOVDQU Y1, -32(DI)(DX*1)
    VZEROUPPER
    MOVB $1, ret+32(FP)
    RET

short_one:
    VMOVDQU (SI), Y0
    SPLAIN(Y0, Y2, Y3, short_h2)
    VPMOVMSKB Y2, AX
    NOTL AX
    MOVQ DX, CX
    MOVL $1, BX
    SHLL CX, BX
    DECL BX
    TESTL BX, AX
    JNZ short_fail
    VMOVDQU Y0, (DI)
    VZEROUPPER
    MOVB $1, ret+32(FP)
    RET

short_fail:
    VZEROUPPER
    MOVB $0, ret+32(FP)
    RET
