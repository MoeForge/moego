//go:build amd64 && !purego

#include "textflag.h"

DATA uglyQuote<>+0(SB)/8, $0x2222222222222222
DATA uglyQuote<>+8(SB)/8, $0x2222222222222222
DATA uglyQuote<>+16(SB)/8, $0x2222222222222222
DATA uglyQuote<>+24(SB)/8, $0x2222222222222222
GLOBL uglyQuote<>(SB), RODATA|NOPTR, $32

DATA uglySlash<>+0(SB)/8, $0x5c5c5c5c5c5c5c5c
DATA uglySlash<>+8(SB)/8, $0x5c5c5c5c5c5c5c5c
DATA uglySlash<>+16(SB)/8, $0x5c5c5c5c5c5c5c5c
DATA uglySlash<>+24(SB)/8, $0x5c5c5c5c5c5c5c5c
GLOBL uglySlash<>(SB), RODATA|NOPTR, $32

DATA uglySpace<>+0(SB)/8, $0x2020202020202020
DATA uglySpace<>+8(SB)/8, $0x2020202020202020
DATA uglySpace<>+16(SB)/8, $0x2020202020202020
DATA uglySpace<>+24(SB)/8, $0x2020202020202020
GLOBL uglySpace<>(SB), RODATA|NOPTR, $32

// UGLYMASK compares the 64 bytes in Y0 and Y1 for equality with those in
// a and b and leaves the 64-bit mask, bit i for byte i, in dst. Clobbers
// Y2, Y3 and AX.
#define UGLYMASK(a, b, dst) \
    VPCMPEQB a, Y0, Y2; \
    VPCMPEQB b, Y1, Y3; \
    VPMOVMSKB Y2, dst; \
    VPMOVMSKB Y3, AX; \
    SHLQ $32, AX; \
    ORQ AX, dst

// UGLYGROUP writes the kept bytes of the 8-byte group at src[CX+off], whose
// keep bits are the low byte of R11, to dst[R8:] and moves R8 past them.
// The store is 8 bytes wide; the bytes past the kept ones are overwritten
// by the next group or lie past the output.
#define UGLYGROUP(off) \
    MOVBQZX R11, AX; \
    SHRQ $8, R11; \
    VMOVQ (R10)(AX*8), X5; \
    VMOVQ off(SI)(CX*1), X6; \
    VPSHUFB X5, X6, X6; \
    VMOVQ X6, (DI)(R8*1); \
    POPCNTL AX, AX; \
    ADDQ AX, R8

// func uglyAVX2(dst, src *byte, n, i, w int, in uint64, shuf *[256]uint64) (int, int, uint64)
//
// Runs @ugly on src[i:n], 64 bytes per step, from the string state in (0,
// or all ones inside a string), and stops before a block that holds a
// backslash or before the last n-i < 64 bytes. A block drops its bytes up
// to ' ' that lie outside strings; the bytes inside strings come from a
// carry-less multiply of the quote mask by all ones. With dst nil it only
// checks, and also stops before the first block with a byte to drop.
// Otherwise it writes the kept bytes to dst[w:], which holds n bytes; w
// never passes i, so the 8-byte group stores stay inside it. It returns
// the new i and w and the string state at i.
TEXT ·uglyAVX2(SB), NOSPLIT, $0-80
    MOVQ dst+0(FP), DI
    MOVQ src+8(FP), SI
    MOVQ n+16(FP), DX
    MOVQ i+24(FP), CX
    MOVQ w+32(FP), R8
    MOVQ in+40(FP), R9
    MOVQ shuf+48(FP), R10
    VMOVDQU uglyQuote<>(SB), Y15
    VMOVDQU uglySlash<>(SB), Y14
    VMOVDQU uglySpace<>(SB), Y13
    MOVQ $-1, AX
    VMOVQ AX, X12           // all ones, for the prefix XOR

ugly_block:
    LEAQ 64(CX), AX
    CMPQ AX, DX
    JA ugly_done
    VMOVDQU (SI)(CX*1), Y0
    VMOVDQU 32(SI)(CX*1), Y1
    VPCMPEQB Y14, Y0, Y2
    VPCMPEQB Y14, Y1, Y3
    VPOR Y3, Y2, Y2
    VPTEST Y2, Y2
    JNZ ugly_done
    UGLYMASK(Y15, Y15, BX)  // quotes
    VPMINUB Y13, Y0, Y4
    VPMINUB Y13, Y1, Y5
    UGLYMASK(Y4, Y5, R11)   // bytes up to ' '
    VMOVQ BX, X4
    VPCLMULQDQ $0, X12, X4, X4
    VMOVQ X4, BX
    XORQ R9, BX             // BX: bytes inside strings
    ANDNQ R11, BX, R11      // R11: bytes to drop
    SARQ $63, BX            // BX: the string state after the block
    TESTQ R11, R11
    JNZ ugly_dirty
    TESTQ DI, DI
    JZ ugly_next
    VMOVDQU Y0, (DI)(R8*1)
    VMOVDQU Y1, 32(DI)(R8*1)
    ADDQ $64, R8

ugly_next:
    MOVQ BX, R9
    ADDQ $64, CX
    JMP ugly_block

ugly_dirty:
    TESTQ DI, DI
    JZ ugly_done
    NOTQ R11                // R11: bytes to keep
    UGLYGROUP(0)
    UGLYGROUP(8)
    UGLYGROUP(16)
    UGLYGROUP(24)
    UGLYGROUP(32)
    UGLYGROUP(40)
    UGLYGROUP(48)
    UGLYGROUP(56)
    JMP ugly_next

ugly_done:
    VZEROUPPER
    MOVQ CX, ret+56(FP)
    MOVQ R8, ret1+64(FP)
    MOVQ R9, ret2+72(FP)
    RET
