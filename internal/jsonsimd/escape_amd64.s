//go:build amd64 && !purego

#include "textflag.h"

// EBCAST puts the byte c in all 32 lanes of y, through its low half x.
// Clobbers AX.
#define EBCAST(c, x, y) \
    MOVL $c, AX; \
    VMOVD AX, x; \
    VPBROADCASTB x, y

// ESCMASK sets the bytes of Y1 where Y0 holds a byte that needs an escape:
// a control byte, quote or backslash, and with R9 set '<', '>' or '&'.
// Y13, Y15 and Y14 hold 0x1f, '"' and '\\'; Y12, Y11 and Y10 hold '<', '>'
// and '&'. The branch on R9 goes the same way every time.
#define ESCMASK \
    VPMINUB Y13, Y0, Y1; \
    VPCMPEQB Y1, Y0, Y1; \
    VPCMPEQB Y15, Y0, Y2; \
    VPOR Y2, Y1, Y1; \
    VPCMPEQB Y14, Y0, Y2; \
    VPOR Y2, Y1, Y1; \
    TESTB R9, R9; \
    JZ 7(PC); \
    VPCMPEQB Y12, Y0, Y2; \
    VPCMPEQB Y11, Y0, Y3; \
    VPOR Y3, Y2, Y2; \
    VPCMPEQB Y10, Y0, Y3; \
    VPOR Y3, Y2, Y2; \
    VPOR Y2, Y1, Y1

// UNESCMASK sets the bytes of Y1 where Y0 holds a control byte, quote or
// backslash, with 0x1f, '"' and '\\' in Y13, Y15 and Y14.
#define UNESCMASK \
    VPMINUB Y13, Y0, Y1; \
    VPCMPEQB Y1, Y0, Y1; \
    VPCMPEQB Y15, Y0, Y2; \
    VPOR Y2, Y1, Y1; \
    VPCMPEQB Y14, Y0, Y2; \
    VPOR Y2, Y1, Y1

// The fake escapes of a step with no byte left in its block: a plain byte
// for escapeAVX2 and an escape of two bytes for unescapeAVX2, each followed
// by bytes for the 32-byte load after it.
DATA escFake<>+0x00(SB)/8, $0x6161616161616161
DATA escFake<>+0x08(SB)/8, $0x6161616161616161
DATA escFake<>+0x10(SB)/8, $0x6161616161616161
DATA escFake<>+0x18(SB)/8, $0x6161616161616161
DATA escFake<>+0x20(SB)/8, $0x6161616161616161
GLOBL escFake<>(SB), RODATA|NOPTR, $40

DATA unescData<>+0x00(SB)/8, $0x7878787878786e5c
DATA unescData<>+0x08(SB)/8, $0x7878787878787878
DATA unescData<>+0x10(SB)/8, $0x7878787878787878
DATA unescData<>+0x18(SB)/8, $0x7878787878787878
DATA unescData<>+0x20(SB)/8, $0x7878787878787878
// From 0x40 on, the constants of unescapeAVX2. At 0x40 and 0x60, the
// classes of bytes by their low and high four bits: a byte is the letter
// of an escape of two bytes when both share a bit.
DATA unescData<>+0x40(SB)/8, $0x00040008000d0000
DATA unescData<>+0x48(SB)/8, $0x0104000200000000
DATA unescData<>+0x50(SB)/8, $0x00040008000d0000
DATA unescData<>+0x58(SB)/8, $0x0104000200000000
DATA unescData<>+0x60(SB)/8, $0x0804020000010000
DATA unescData<>+0x68(SB)/8, $0
DATA unescData<>+0x70(SB)/8, $0x0804020000010000
DATA unescData<>+0x78(SB)/8, $0
// At 0x80 and 0xa0, the control bytes of the letters b, f, n and of r, t,
// by their low four bits.
DATA unescData<>+0x80(SB)/8, $0x000c000000080000
DATA unescData<>+0x88(SB)/8, $0x000a000000000000
DATA unescData<>+0x90(SB)/8, $0x000c000000080000
DATA unescData<>+0x98(SB)/8, $0x000a000000000000
DATA unescData<>+0xa0(SB)/8, $0x00000009000d0000
DATA unescData<>+0xa8(SB)/8, $0
DATA unescData<>+0xb0(SB)/8, $0x00000009000d0000
DATA unescData<>+0xb8(SB)/8, $0
// At 0xc0 and 0xe0, the shuffle that puts byte k/8 of a mask in byte k,
// and the bit of byte k in its byte.
DATA unescData<>+0xc0(SB)/8, $0
DATA unescData<>+0xc8(SB)/8, $0x0101010101010101
DATA unescData<>+0xd0(SB)/8, $0x0202020202020202
DATA unescData<>+0xd8(SB)/8, $0x0303030303030303
DATA unescData<>+0xe0(SB)/8, $0x8040201008040201
DATA unescData<>+0xe8(SB)/8, $0x8040201008040201
DATA unescData<>+0xf0(SB)/8, $0x8040201008040201
DATA unescData<>+0xf8(SB)/8, $0x8040201008040201
// At 0x100, 0x120 and 0x140, 0x0f, 6 and 7 in each byte.
DATA unescData<>+0x100(SB)/8, $0x0f0f0f0f0f0f0f0f
DATA unescData<>+0x108(SB)/8, $0x0f0f0f0f0f0f0f0f
DATA unescData<>+0x110(SB)/8, $0x0f0f0f0f0f0f0f0f
DATA unescData<>+0x118(SB)/8, $0x0f0f0f0f0f0f0f0f
DATA unescData<>+0x120(SB)/8, $0x0606060606060606
DATA unescData<>+0x128(SB)/8, $0x0606060606060606
DATA unescData<>+0x130(SB)/8, $0x0606060606060606
DATA unescData<>+0x138(SB)/8, $0x0606060606060606
DATA unescData<>+0x140(SB)/8, $0x0707070707070707
DATA unescData<>+0x148(SB)/8, $0x0707070707070707
DATA unescData<>+0x150(SB)/8, $0x0707070707070707
DATA unescData<>+0x158(SB)/8, $0x0707070707070707
// At 0x160 and 0x168, the masks that spread a character of three and of
// two bytes in UTF-8 over its bytes, its last byte lowest.
DATA unescData<>+0x160(SB)/8, $0x000f3f3f
DATA unescData<>+0x168(SB)/8, $0x00001f3f
GLOBL unescData<>(SB), RODATA|NOPTR, $0x170

// escLetters holds, at the low four bits of each control byte that has an
// escape of a backslash and a letter, that letter.
DATA escLetters<>+0x00(SB)/8, $0
DATA escLetters<>+0x08(SB)/8, $0x00007266006e7462
GLOBL escLetters<>(SB), RODATA|NOPTR, $16

// EGROUP writes the eight bytes of X1 at DI with a backslash before each
// byte whose bit is in the low byte of BX, through the shuffle at R12, and
// moves DI and BX on. X1 holds the bytes in its low half and backslashes
// in its high half.
#define EGROUP \
    MOVBLZX BX, R11; \
    POPCNTL R11, R13; \
    SHLQ $4, R11; \
    VPSHUFB (R12)(R11*1), X1, X1; \
    VMOVDQU X1, (DI); \
    LEAQ 8(DI)(R13*1), DI; \
    SHRL $8, BX

// ESTEP escapes the byte of the block at SI whose bit is the lowest in BX,
// and clears the bit. DI is where the block's first byte goes in the
// output, moved on by the growth of the escapes before; it moves on by
// the growth of this one. The escape from the table at R10 is stored over
// the byte and the 32 bytes after the byte are stored after the escape.
// With no bit in BX, the step takes the plain byte at R14 instead and
// stores from 64 bytes past DI on, which later blocks store over.
#define ESTEP \
    TZCNTQ BX, AX; \
    LEAQ (SI)(AX*1), R11; \
    TESTQ BX, BX; \
    CMOVQEQ R14, R11; \
    MOVBLZX (R11), R13; \
    MOVQ (R10)(R13*8), R13; \
    VMOVDQU 1(R11), Y2; \
    MOVQ R13, (DI)(AX*1); \
    SHRQ $56, R13; \
    ADDQ R13, DI; \
    VMOVDQU Y2, (DI)(AX*1); \
    DECQ DI; \
    BLSRQ BX, BX

// func escapeAVX2(dst *byte, dn int, src *byte, n, i, w int, html bool, tab *[256]uint64, exp *[256][16]byte) (int, int)
//
// Writes src[i:n] to dst[w:dn] as JSON string contents, 32 bytes at a
// time, while 32 bytes of src are left and dst has room for 40 more bytes.
// Each block is stored as it is; then, for each byte of it that needs an
// escape, the escape from tab is stored over it (its length is in the top
// byte of the entry) and the rest of the block after it is stored again
// after the escape. Bytes of 0x80 and above need none. It returns the new
// i and w.
//
// While 64 bytes of src are left and dst has room for 320 more bytes, a
// block whose escapes are all a backslash and a byte is written eight
// bytes at a time, each eight shuffled with exp[m], m the bits of the
// bytes to escape, together with eight backslashes. In other blocks, the
// first three escapes take no branch, and the rest of the block is loaded
// from src after each. After that, a block starts after each escape.
TEXT ·escapeAVX2(SB), NOSPLIT, $0-88
    MOVQ dst+0(FP), DI
    MOVQ dn+8(FP), R8
    MOVQ src+16(FP), SI
    MOVQ n+24(FP), CX
    MOVB html+48(FP), R9
    MOVQ tab+56(FP), R10
    MOVQ exp+64(FP), R12
    LEAQ -64(SI)(CX*1), CX  // the last block start of the first loop
    LEAQ -320(DI)(R8*1), R8 // the last output place of the first loop
    ADDQ i+32(FP), SI
    ADDQ w+40(FP), DI
    LEAQ escFake<>(SB), R14
    EBCAST(0x22, X15, Y15)
    EBCAST(0x5c, X14, Y14)
    EBCAST(0x1f, X13, Y13)
    EBCAST(0x3c, X12, Y12)
    EBCAST(0x3e, X11, Y11)
    EBCAST(0x26, X10, Y10)
    EBCAST(0x0f, X9, Y9)
    VBROADCASTI128 escLetters<>(SB), Y8
    VPXOR Y7, Y7, Y7

esc_block:
    CMPQ SI, CX
    JHI esc_last
    CMPQ DI, R8
    JHI esc_last
    VMOVDQU (SI), Y0
    VMOVDQU Y0, (DI)
    ESCMASK
    VPMOVMSKB Y1, BX
    TESTL BX, BX
    JNZ esc_expand
    ADDQ $32, SI
    ADDQ $32, DI
    JMP esc_block

esc_expand:
    // Y5 marks the control bytes with a letter, Y4 holds the letters.
    VPSHUFB Y0, Y8, Y4
    VPMINUB Y9, Y0, Y5
    VPCMPEQB Y5, Y0, Y5
    VPCMPEQB Y7, Y4, Y6
    VPANDN Y5, Y6, Y5
    // Any other byte to escape, which takes six bytes, goes the other way.
    VPCMPEQB Y15, Y0, Y2
    VPCMPEQB Y14, Y0, Y3
    VPOR Y3, Y2, Y2
    VPOR Y5, Y2, Y2
    VPANDN Y1, Y2, Y6
    VPTEST Y6, Y6
    JNZ esc_steps
    VPBLENDVB Y5, Y4, Y0, Y0
    VEXTRACTI128 $1, Y0, X6
    VPUNPCKLQDQ X14, X0, X1
    EGROUP
    VPUNPCKHQDQ X14, X0, X1
    EGROUP
    VPUNPCKLQDQ X14, X6, X1
    EGROUP
    VPUNPCKHQDQ X14, X6, X1
    EGROUP
    ADDQ $32, SI
    JMP esc_block

esc_steps:
    ESTEP
    ESTEP
    ESTEP
    TESTQ BX, BX
    JNZ esc_more
esc_next:
    ADDQ $32, SI
    ADDQ $32, DI
    JMP esc_block

esc_more:
    ESTEP
    TESTQ BX, BX
    JNZ esc_more
    JMP esc_next

esc_last:
    ADDQ $32, CX            // the last block start
    ADDQ $280, R8           // the last output place
esc_lblock:
    CMPQ SI, CX
    JHI esc_done
    CMPQ DI, R8
    JHI esc_done
    VMOVDQU (SI), Y0
    VMOVDQU Y0, (DI)
    ESCMASK
    VPMOVMSKB Y1, AX
    TESTL AX, AX
    JNZ esc_lfix
    ADDQ $32, SI
    ADDQ $32, DI
    JMP esc_lblock

esc_lfix:
    TZCNTL AX, AX
    ADDQ AX, SI
    ADDQ AX, DI
    MOVBLZX (SI), AX
    MOVQ (R10)(AX*8), BX
    MOVQ BX, (DI)
    SHRQ $56, BX
    ADDQ BX, DI
    INCQ SI
    JMP esc_lblock

esc_done:
    VZEROUPPER
    SUBQ src+16(FP), SI
    SUBQ dst+0(FP), DI
    MOVQ SI, ret+72(FP)
    MOVQ DI, ret1+80(FP)
    RET

// UGROUP writes the bytes of the low half of x whose bit is clear in the
// low byte of BX at DI, through the shuffle at 256(R10), and moves DI and
// BX on.
#define UGROUP(x) \
    MOVBLZX BX, R13; \
    POPCNTL R13, R9; \
    SHLQ $4, R13; \
    VPSHUFB 256(R10)(R13*1), x, X1; \
    VMOVQ X1, (DI); \
    ADDQ $8, DI; \
    SUBQ R9, DI; \
    SHRL $8, BX

// USTEP decodes the escape of the block at SI whose backslash has the
// lowest bit in BX, and clears the bits of the backslash and of the byte
// after it. DI is where the block's first byte goes in the output, moved
// back by the escapes before; it moves back by one. The byte from the
// table at R10 is stored over the backslash and the 32 bytes after the
// escape are stored after it. R12 is set to 1, from DX, when the escape
// ends past the block. A bit for a byte other than a backslash ends the
// kernel there, and a backslash the table has no byte for goes to
// unesc_u. With no bit in BX, the step takes the escape at R14 instead and
// stores from 64 bytes past DI on, which later blocks store over.
#define USTEP \
    TZCNTQ BX, AX; \
    LEAQ (SI)(AX*1), R9; \
    TESTQ BX, BX; \
    CMOVQEQ R14, R9; \
    CMPB (R9), $0x5c; \
    JNE unesc_stop; \
    VMOVDQU 2(R9), Y2; \
    MOVBLZX 1(R9), R13; \
    MOVBLZX (R10)(R13*1), R13; \
    TESTL R13, R13; \
    JZ unesc_u; \
    MOVB R13, (DI)(AX*1); \
    VMOVDQU Y2, 1(DI)(AX*1); \
    CMPQ AX, $31; \
    CMOVQEQ DX, R12; \
    CMPQ BX, $1; \
    ADCQ $-1, DI; \
    BLSMSKQ BX, R13; \
    LEAQ 1(R13)(R13*1), R13; \
    ANDNQ BX, R13, BX

// func unescapeAVX2(dst *byte, dn int, src *byte, n, i, w int, tab *unescapeTables) (int, int, bool)
//
// Writes the JSON string contents src[i:n] to dst[w:dn] decoded, 32 bytes
// at a time, while 34 bytes of src are left and dst has room for 32 more
// bytes. Each block is stored as it is; at its first quote, backslash or
// control byte the kernel stops, unless it is a backslash whose escape
// tab.tab decodes to a byte other than 0: that byte is stored over the
// backslash, the rest of the block after the escape is stored again after
// it, and the next stop byte of the block is taken in turn. It returns the
// new i and w, and whether a block it read held a byte of 0x80 or above.
//
// While 65 bytes of src are left and dst has room for 128 more bytes, a
// block whose stops are all escapes of two bytes is decoded eight bytes at
// a time: its letters become the bytes they stand for, and tab.comp[m]
// shuffles out the backslashes, m their bits. A block that ends with the
// backslash of an escape ends before it. In other blocks, the first three
// escapes take no branch, and the rest of the block is loaded from src
// after each; an escape of six bytes of a character outside the surrogates
// is decoded too, through tab.hex, while 38 bytes of src are left from it.
// After that, a block starts after each escape.
TEXT ·unescapeAVX2(SB), NOSPLIT, $0-73
    MOVQ dst+0(FP), DI
    MOVQ dn+8(FP), R8
    MOVQ src+16(FP), SI
    MOVQ n+24(FP), CX
    MOVQ tab+48(FP), R10
    LEAQ -65(SI)(CX*1), CX  // the last block start of the first loop
    LEAQ -128(DI)(R8*1), R8 // the last output place of the first loop
    ADDQ i+32(FP), SI
    ADDQ w+40(FP), DI
    LEAQ unescData<>(SB), R14
    MOVL $1, DX
    XORL R11, R11           // the high bits seen
    EBCAST(0x22, X15, Y15)
    EBCAST(0x5c, X14, Y14)
    EBCAST(0x1f, X13, Y13)

unesc_block:
    CMPQ SI, CX
    JHI unesc_last
    CMPQ DI, R8
    JHI unesc_last
    VMOVDQU (SI), Y0
    VMOVDQU Y0, (DI)
    UNESCMASK
    VPMOVMSKB Y0, AX
    ORL AX, R11
    VPMOVMSKB Y1, BX
    TESTL BX, BX
    JNZ unesc_compact
    ADDQ $32, SI
    ADDQ $32, DI
    JMP unesc_block

unesc_compact:
    // AX: the backslashes. R12: the bytes they escape, a backslash that
    // ends a run of odd length escaping the byte after it; bit 32 for the
    // first byte of the next block.
    VPCMPEQB Y14, Y0, Y2
    VPMOVMSKB Y2, AX
    MOVQ AX, R9
    SHLQ $1, R9
    MOVQ $0x5555555555555555, R13
    ANDNQ AX, R13, R12
    ANDNQ R12, R9, R12
    ADDQ AX, R12
    SHLQ $1, R12
    XORQ R13, R12
    ANDQ R9, R12
    // R13: the bytes that are no letter of an escape of two bytes.
    VPSRLW $4, Y0, Y3
    VPAND 0x100(R14), Y3, Y3
    VMOVDQU 0x40(R14), Y4
    VPSHUFB Y0, Y4, Y4
    VMOVDQU 0x60(R14), Y5
    VPSHUFB Y3, Y5, Y5
    VPAND Y5, Y4, Y4
    VPXOR Y6, Y6, Y6
    VPCMPEQB Y6, Y4, Y4
    VPMOVMSKB Y4, R13
    // A quote or control byte that is not escaped, or an escape of
    // another kind, goes the other way.
    ANDNQ BX, AX, R9
    ANDNQ R9, R12, R9
    ANDQ R12, R13
    ORQ R13, R9
    JNZ unesc_steps
    // Y4: the control bytes of the letters b, f, n, r and t, else 0.
    VMOVDQU 0x80(R14), Y4
    VPSHUFB Y0, Y4, Y4
    VPCMPEQB 0x120(R14), Y3, Y5
    VPAND Y5, Y4, Y4
    VMOVDQU 0xa0(R14), Y5
    VPSHUFB Y0, Y5, Y5
    VPCMPEQB 0x140(R14), Y3, Y2
    VPAND Y2, Y5, Y5
    VPOR Y5, Y4, Y4
    // Y3: the escaped bytes of R12, as bytes.
    VMOVD R12, X3
    VPBROADCASTD X3, Y3
    VPSHUFB 0xc0(R14), Y3, Y3
    VPAND 0xe0(R14), Y3, Y3
    VPCMPEQB 0xe0(R14), Y3, Y3
    VPCMPEQB Y6, Y4, Y5
    VPANDN Y3, Y5, Y3
    VPBLENDVB Y3, Y4, Y0, Y0
    // Leave out the backslashes, in BX; the block ends before a backslash
    // at its end.
    MOVQ R12, BX
    SHRQ $1, BX
    SHRQ $32, R12
    VEXTRACTI128 $1, Y0, X6
    UGROUP(X0)
    VPUNPCKHQDQ X0, X0, X2
    UGROUP(X2)
    UGROUP(X6)
    VPUNPCKHQDQ X6, X6, X2
    UGROUP(X2)
    ADDQ $32, SI
    SUBQ R12, SI
    JMP unesc_block

unesc_steps:
    XORL R12, R12
    USTEP
    USTEP
    USTEP
    TESTQ BX, BX
    JNZ unesc_more
unesc_next:
    // An escape that ends past the block moves the next block by one.
    ADDQ $32, SI
    ADDQ $32, DI
    ADDQ R12, SI
    ADDQ R12, DI
    JMP unesc_block

unesc_more:
    USTEP
    TESTQ BX, BX
    JNZ unesc_more
    JMP unesc_next

unesc_u:
    // R9: a backslash the table has no byte for, at AX in the block. When
    // it starts an escape of six bytes, \u and four hexadecimal digits, of
    // a character outside the surrogates, and 38 bytes of src are left
    // from it, the character goes in UTF-8 over the backslash, the 32
    // bytes after the escape after it, and the steps go on; else the
    // kernel ends there.
    CMPB 1(R9), $0x75
    JNE unesc_stop
    LEAQ -27(R9), R13
    CMPQ R13, CX
    JHI unesc_stop
    // DX: the four digits, above 0xffff when one is not a digit.
    MOVBLZX 2(R9), R13
    MOVL 4352(R10)(R13*4), DX
    MOVBLZX 3(R9), R13
    MOVL 4352(R10)(R13*4), R13
    SHLQ $4, DX
    ORQ R13, DX
    MOVBLZX 4(R9), R13
    MOVL 4352(R10)(R13*4), R13
    SHLQ $4, DX
    ORQ R13, DX
    MOVBLZX 5(R9), R13
    MOVL 4352(R10)(R13*4), R13
    SHLQ $4, DX
    ORQ R13, DX
    CMPQ DX, $0xffff
    JHI unesc_stop
    MOVL DX, R13
    ANDL $0xf800, R13
    CMPL R13, $0xd800
    JEQ unesc_stop
    // R13: the character in UTF-8, its first byte lowest; DX: its length.
    CMPL DX, $0x80
    JLO unesc_u1
    CMPL DX, $0x800
    JLO unesc_u2
    PDEPL 0x160(R14), DX, R13
    BSWAPL R13
    SHRL $8, R13
    ORL $0x8080e0, R13
    MOVL $3, DX
    JMP unesc_uput
unesc_u2:
    PDEPL 0x168(R14), DX, R13
    ROLW $8, R13
    ORL $0x80c0, R13
    MOVL $2, DX
    JMP unesc_uput
unesc_u1:
    MOVL DX, R13
    MOVL $1, DX
unesc_uput:
    MOVL R13, (DI)(AX*1)
    VMOVDQU 6(R9), Y2
    ADDQ DX, DI
    VMOVDQU Y2, (DI)(AX*1)
    SUBQ $6, DI
    // An escape that ends past the block moves the next block by as much.
    CMPQ AX, $26
    JLS unesc_uin
    LEAQ -26(AX), R12
unesc_uin:
    MOVL $1, DX
    BLSRQ BX, BX
    TESTQ BX, BX
    JNZ unesc_more
    JMP unesc_next

unesc_stop:
    ADDQ AX, SI
    ADDQ AX, DI
    JMP unesc_done

unesc_last:
    ADDQ $31, CX            // the last block start
    ADDQ $96, R8            // the last output place
unesc_lblock:
    CMPQ SI, CX
    JHI unesc_done
    CMPQ DI, R8
    JHI unesc_done
    VMOVDQU (SI), Y0
    VMOVDQU Y0, (DI)
    UNESCMASK
    VPMOVMSKB Y0, BX
    ORL BX, R11
    VPMOVMSKB Y1, AX
    TESTL AX, AX
    JNZ unesc_lstop
    ADDQ $32, SI
    ADDQ $32, DI
    JMP unesc_lblock

unesc_lstop:
    TZCNTL AX, AX
    ADDQ AX, SI
    ADDQ AX, DI
    CMPB (SI), $0x5c
    JNE unesc_done
    MOVBLZX 1(SI), AX
    MOVBLZX (R10)(AX*1), BX
    TESTL BX, BX
    JZ unesc_done
    MOVB BX, (DI)
    ADDQ $2, SI
    INCQ DI
    JMP unesc_lblock

unesc_done:
    VZEROUPPER
    SUBQ src+16(FP), SI
    SUBQ dst+0(FP), DI
    MOVQ SI, ret+56(FP)
    MOVQ DI, ret1+64(FP)
    TESTL R11, R11
    SETNE ret2+72(FP)
    RET

// The lookup tables of the UTF-8 check, each 16 bytes twice, indexed by a
// nibble: the errors the high nibble of a byte may start (utf8Hi1), those
// the low nibble of that byte may start (utf8Lo1), and those the high
// nibble of the byte after it may complete (utf8Hi2). A pair of bytes is
// wrong where all three share a bit. The bits:
//   0x01 too short: a lead byte not followed by a continuation byte
//   0x02 too long: an ASCII byte followed by a continuation byte
//   0x04 overlong 3: E0 followed by 80..9F
//   0x08 too large: F4 followed by 90..BF, or F5..FF
//   0x10 surrogate: ED followed by A0..BF
//   0x20 overlong 2: C0 or C1
//   0x40 overlong 4 or too large: F0 followed by 80..8F, F5..FF followed by 80..8F
//   0x80 two continuation bytes, right only as the third or fourth byte
DATA utf8Hi1<>+0x00(SB)/8, $0x0202020202020202
DATA utf8Hi1<>+0x08(SB)/8, $0x4915012180808080
DATA utf8Hi1<>+0x10(SB)/8, $0x0202020202020202
DATA utf8Hi1<>+0x18(SB)/8, $0x4915012180808080
GLOBL utf8Hi1<>(SB), RODATA|NOPTR, $32

DATA utf8Lo1<>+0x00(SB)/8, $0xcbcbcb8b8383a3e7
DATA utf8Lo1<>+0x08(SB)/8, $0xcbcbdbcbcbcbcbcb
DATA utf8Lo1<>+0x10(SB)/8, $0xcbcbcb8b8383a3e7
DATA utf8Lo1<>+0x18(SB)/8, $0xcbcbdbcbcbcbcbcb
GLOBL utf8Lo1<>(SB), RODATA|NOPTR, $32

DATA utf8Hi2<>+0x00(SB)/8, $0x0101010101010101
DATA utf8Hi2<>+0x08(SB)/8, $0x01010101babaaee6
DATA utf8Hi2<>+0x10(SB)/8, $0x0101010101010101
DATA utf8Hi2<>+0x18(SB)/8, $0x01010101babaaee6
GLOBL utf8Hi2<>(SB), RODATA|NOPTR, $32

// Bytes from which a block ends inside a character: F0 and up in its last
// three bytes, E0 and up in its last two, C0 and up in its last one. They
// are subtracted with saturation, so that what is left is not zero.
DATA utf8Max<>+0x00(SB)/8, $0xffffffffffffffff
DATA utf8Max<>+0x08(SB)/8, $0xffffffffffffffff
DATA utf8Max<>+0x10(SB)/8, $0xffffffffffffffff
DATA utf8Max<>+0x18(SB)/8, $0xbfdfefffffffffff
GLOBL utf8Max<>(SB), RODATA|NOPTR, $32

// Bytes the UTF-8 check compares with or subtracts, 32 of each: 0x60,
// 0x70, 0x80, 0xe2, 0xfe and 0xa8.
DATA utf8Const<>+0x00(SB)/8, $0x6060606060606060
DATA utf8Const<>+0x08(SB)/8, $0x6060606060606060
DATA utf8Const<>+0x10(SB)/8, $0x6060606060606060
DATA utf8Const<>+0x18(SB)/8, $0x6060606060606060
DATA utf8Const<>+0x20(SB)/8, $0x7070707070707070
DATA utf8Const<>+0x28(SB)/8, $0x7070707070707070
DATA utf8Const<>+0x30(SB)/8, $0x7070707070707070
DATA utf8Const<>+0x38(SB)/8, $0x7070707070707070
DATA utf8Const<>+0x40(SB)/8, $0x8080808080808080
DATA utf8Const<>+0x48(SB)/8, $0x8080808080808080
DATA utf8Const<>+0x50(SB)/8, $0x8080808080808080
DATA utf8Const<>+0x58(SB)/8, $0x8080808080808080
DATA utf8Const<>+0x60(SB)/8, $0xe2e2e2e2e2e2e2e2
DATA utf8Const<>+0x68(SB)/8, $0xe2e2e2e2e2e2e2e2
DATA utf8Const<>+0x70(SB)/8, $0xe2e2e2e2e2e2e2e2
DATA utf8Const<>+0x78(SB)/8, $0xe2e2e2e2e2e2e2e2
DATA utf8Const<>+0x80(SB)/8, $0xfefefefefefefefe
DATA utf8Const<>+0x88(SB)/8, $0xfefefefefefefefe
DATA utf8Const<>+0x90(SB)/8, $0xfefefefefefefefe
DATA utf8Const<>+0x98(SB)/8, $0xfefefefefefefefe
DATA utf8Const<>+0xa0(SB)/8, $0xa8a8a8a8a8a8a8a8
DATA utf8Const<>+0xa8(SB)/8, $0xa8a8a8a8a8a8a8a8
DATA utf8Const<>+0xb0(SB)/8, $0xa8a8a8a8a8a8a8a8
DATA utf8Const<>+0xb8(SB)/8, $0xa8a8a8a8a8a8a8a8
GLOBL utf8Const<>(SB), RODATA|NOPTR, $192

// func utf8AVX2(src *byte, n int) int
//
// Checks the 32-byte blocks of src[:n], n a multiple of 32, as UTF-8. It
// returns 1 when a byte is not part of valid UTF-8, plus 2 when U+2028 or
// U+2029 appears. A character the last block ends inside is not checked:
// the caller checks it with what follows.
TEXT ·utf8AVX2(SB), NOSPLIT, $0-24
    MOVQ src+0(FP), SI
    MOVQ n+8(FP), DX
    XORQ CX, CX
    VMOVDQU utf8Hi1<>(SB), Y8
    VMOVDQU utf8Lo1<>(SB), Y9
    VMOVDQU utf8Hi2<>(SB), Y10
    EBCAST(0x0f, X11, Y11)
    VPXOR Y12, Y12, Y12     // the previous block ends inside a character
    VPXOR Y13, Y13, Y13     // line separators
    VPXOR Y14, Y14, Y14     // errors
    VPXOR Y15, Y15, Y15     // the previous block

utf8_block:
    CMPQ CX, DX
    JGE utf8_done
    VMOVDQU (SI)(CX*1), Y0
    ADDQ $32, CX
    VPMOVMSKB Y0, AX
    TESTL AX, AX
    JNZ utf8_high
    // ASCII: right only when the previous block ended a character.
    VPOR Y12, Y14, Y14
    VPXOR Y12, Y12, Y12
    VMOVDQA Y0, Y15
    JMP utf8_block

utf8_high:
    VPERM2I128 $0x21, Y0, Y15, Y1
    VPALIGNR $15, Y1, Y0, Y2    // the byte before each byte
    VPALIGNR $14, Y1, Y0, Y3    // the byte two before
    VPALIGNR $13, Y1, Y0, Y4    // the byte three before
    VPSRLW $4, Y2, Y5
    VPAND Y11, Y5, Y5
    VPSHUFB Y5, Y8, Y5
    VPAND Y11, Y2, Y6
    VPSHUFB Y6, Y9, Y6
    VPAND Y6, Y5, Y5
    VPSRLW $4, Y0, Y6
    VPAND Y11, Y6, Y6
    VPSHUFB Y6, Y10, Y6
    VPAND Y6, Y5, Y5            // the errors of each pair
    // A continuation byte must follow a continuation byte where it is
    // the third byte after E0..FF or the fourth after F0..FF.
    VPSUBUSB utf8Const<>+0x00(SB), Y3, Y6
    VPSUBUSB utf8Const<>+0x20(SB), Y4, Y7
    VPOR Y7, Y6, Y6
    VPAND utf8Const<>+0x40(SB), Y6, Y6
    VPXOR Y6, Y5, Y5
    VPOR Y5, Y14, Y14
    // E2 80 A8 and E2 80 A9.
    VPCMPEQB utf8Const<>+0x40(SB), Y2, Y6
    VPCMPEQB utf8Const<>+0x60(SB), Y3, Y1
    VPAND Y1, Y6, Y6
    VPAND utf8Const<>+0x80(SB), Y0, Y1
    VPCMPEQB utf8Const<>+0xa0(SB), Y1, Y1
    VPAND Y1, Y6, Y6
    VPOR Y6, Y13, Y13
    VPSUBUSB utf8Max<>(SB), Y0, Y12
    VMOVDQA Y0, Y15
    JMP utf8_block

utf8_done:
    XORQ AX, AX
    VPTEST Y14, Y14
    JZ utf8_sep
    MOVQ $1, AX
utf8_sep:
    VPTEST Y13, Y13
    JZ utf8_ret
    ORQ $2, AX
utf8_ret:
    VZEROUPPER
    MOVQ AX, ret+16(FP)
    RET

// Tables of escCountAVX2, 32 bytes each: the class bits of a byte by its
// high nibble, by its low nibble without and with html, the bits of the
// escapes of two bytes and those of the escapes of six. A byte's class is
// the AND of its two entries; bit 0: \b, \t, \n, \f, \r; bit 1: another
// control byte below 0x10; bit 2: a control byte from 0x10; bit 3: '"';
// bit 4: '&'; bit 5: '<' and '>'; bit 6: '\'.
DATA escCount<>+0x00(SB)/8, $0x0000400020180403
DATA escCount<>+0x08(SB)/8, $0x0000000000000000
DATA escCount<>+0x10(SB)/8, $0x0000400020180403
DATA escCount<>+0x18(SB)/8, $0x0000000000000000
DATA escCount<>+0x20(SB)/8, $0x06060606060e0606
DATA escCount<>+0x28(SB)/8, $0x0606054506050505
DATA escCount<>+0x30(SB)/8, $0x06060606060e0606
DATA escCount<>+0x38(SB)/8, $0x0606054506050505
DATA escCount<>+0x40(SB)/8, $0x06160606060e0606
DATA escCount<>+0x48(SB)/8, $0x0626056506050505
DATA escCount<>+0x50(SB)/8, $0x06160606060e0606
DATA escCount<>+0x58(SB)/8, $0x0626056506050505
DATA escCount<>+0x60(SB)/8, $0x4949494949494949
DATA escCount<>+0x68(SB)/8, $0x4949494949494949
DATA escCount<>+0x70(SB)/8, $0x4949494949494949
DATA escCount<>+0x78(SB)/8, $0x4949494949494949
DATA escCount<>+0x80(SB)/8, $0x3636363636363636
DATA escCount<>+0x88(SB)/8, $0x3636363636363636
DATA escCount<>+0x90(SB)/8, $0x3636363636363636
DATA escCount<>+0x98(SB)/8, $0x3636363636363636
GLOBL escCount<>(SB), RODATA|NOPTR, $160

// func escCountAVX2(src *byte, n int, html bool) int
//
// Returns the bytes the escapes of src[:n], n a multiple of 32, add to it
// in a JSON string: one for each quote, backslash, \b, \f, \n, \r and \t,
// five for each other control byte and, with html, each '<', '>' and '&'.
// Bytes of 0x80 and above add none. Byte counters take up to 255 blocks
// before they are summed.
TEXT ·escCountAVX2(SB), NOSPLIT, $0-32
    MOVQ src+0(FP), SI
    MOVQ n+8(FP), DX
    XORQ CX, CX
    VMOVDQU escCount<>+0x00(SB), Y8
    VMOVDQU escCount<>+0x20(SB), Y9
    CMPB html+16(FP), $0
    JEQ cnt_plain
    VMOVDQU escCount<>+0x40(SB), Y9
cnt_plain:
    VMOVDQU escCount<>+0x60(SB), Y12
    VMOVDQU escCount<>+0x80(SB), Y13
    EBCAST(0x0f, X11, Y11)
    EBCAST(0x01, X14, Y14)
    VPXOR Y15, Y15, Y15
    VPXOR Y4, Y4, Y4        // escapes of two bytes, in four sums
    VPXOR Y5, Y5, Y5        // escapes of six bytes

cnt_round:
    MOVQ $255, R9
    VPXOR Y6, Y6, Y6
    VPXOR Y7, Y7, Y7
cnt_block:
    CMPQ CX, DX
    JGE cnt_sum
    VMOVDQU (SI)(CX*1), Y0
    ADDQ $32, CX
    VPSRLW $4, Y0, Y1
    VPAND Y11, Y1, Y1
    VPAND Y11, Y0, Y2
    VPSHUFB Y1, Y8, Y1
    VPSHUFB Y2, Y9, Y2
    VPAND Y2, Y1, Y1
    VPAND Y12, Y1, Y2
    VPMINUB Y14, Y2, Y2
    VPADDB Y2, Y6, Y6
    VPAND Y13, Y1, Y1
    VPMINUB Y14, Y1, Y1
    VPADDB Y1, Y7, Y7
    DECQ R9
    JNZ cnt_block
cnt_sum:
    VPSADBW Y15, Y6, Y6
    VPSADBW Y15, Y7, Y7
    VPADDQ Y6, Y4, Y4
    VPADDQ Y7, Y5, Y5
    CMPQ CX, DX
    JLT cnt_round

    VEXTRACTI128 $1, Y4, X0
    VPADDQ X0, X4, X4
    VPSHUFD $0x4e, X4, X0
    VPADDQ X0, X4, X4
    VMOVQ X4, AX
    VEXTRACTI128 $1, Y5, X0
    VPADDQ X0, X5, X5
    VPSHUFD $0x4e, X5, X0
    VPADDQ X0, X5, X5
    VMOVQ X5, BX
    LEAQ (BX)(BX*4), BX
    ADDQ BX, AX
    VZEROUPPER
    MOVQ AX, ret+24(FP)
    RET
