//go:build amd64 && !purego

#include "textflag.h"

// func cpuHasAVX2() bool
TEXT ·cpuHasAVX2(SB), NOSPLIT, $0-1
    MOVB $0, ret+0(FP)
    XORL AX, AX
    CPUID
    CMPL AX, $7
    JB no_avx2
    MOVL $1, AX
    CPUID
    // PCLMULQDQ (bit 1), POPCNT (23), OSXSAVE (27) and AVX (28).
    ANDL $0x18800002, CX
    CMPL CX, $0x18800002
    JNE no_avx2
    XORL CX, CX
    XGETBV
    ANDL $6, AX
    CMPL AX, $6
    JNE no_avx2
    MOVL $7, AX
    XORL CX, CX
    CPUID
    // BMI1 (bit 3), AVX2 (5) and BMI2 (8).
    ANDL $0x128, BX
    CMPL BX, $0x128
    JNE no_avx2
    MOVB $1, ret+0(FP)
no_avx2:
    RET

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

// VALID_SETUP loads the constants of the vmask kernels.
#define VALID_SETUP \
    VBCAST(0x22, X15, Y15); \
    VBCAST(0x5c, X14, Y14); \
    VBCAST(0x1f, X13, Y13); \
    VBCAST(0x20, X12, Y12); \
    VMOVDQU validWS<>(SB), Y11; \
    VMOVDQU validOp<>(SB), Y10; \
    MOVQ $-1, AX; \
    VMOVQ AX, X9

// VALID_MAIN stores at DI the quote, r0, bslash, ws, ctrl and op masks of
// the block in Y0 and Y1, with esc and u zero, and leaves bslash in R8. The
// prefix XOR of the quotes multiplies them by the all ones in X9; control
// bytes fold onto ':' and ',', so op drops them.
#define VALID_MAIN \
    VEQ(Y15); \
    VMASK(Y2, Y3, BX); \
    MOVQ BX, 0(DI); \
    VMOVQ BX, X4; \
    VPCLMULQDQ $0, X9, X4, X4; \
    VMOVQ X4, 56(DI); \
    VEQ(Y14); \
    VMASK(Y2, Y3, R8); \
    MOVQ R8, 8(DI); \
    VPSHUFB Y0, Y11, Y2; \
    VPCMPEQB Y2, Y0, Y2; \
    VPSHUFB Y1, Y11, Y3; \
    VPCMPEQB Y3, Y1, Y3; \
    VMASK(Y2, Y3, BX); \
    MOVQ BX, 16(DI); \
    VPMINUB Y13, Y0, Y2; \
    VPCMPEQB Y2, Y0, Y2; \
    VPMINUB Y13, Y1, Y3; \
    VPCMPEQB Y3, Y1, Y3; \
    VMASK(Y2, Y3, DX); \
    MOVQ DX, 32(DI); \
    VPOR Y12, Y0, Y4; \
    VPSHUFB Y4, Y10, Y2; \
    VPCMPEQB Y2, Y4, Y2; \
    VPOR Y12, Y1, Y5; \
    VPSHUFB Y5, Y10, Y3; \
    VPCMPEQB Y3, Y5, Y3; \
    VMASK(Y2, Y3, BX); \
    ANDNQ BX, DX, BX; \
    MOVQ BX, 24(DI); \
    MOVQ $0, 40(DI); \
    MOVQ $0, 48(DI)

// VALID_ESC stores at DI the esc and u masks of the block in Y0 and Y1:
// the bytes that may follow a backslash, and the 'u's.
#define VALID_ESC \
    VEQ(Y15); \
    VMOVDQU Y2, Y6; \
    VMOVDQU Y3, Y7; \
    VEQ(Y14); \
    VPOR Y2, Y6, Y6; \
    VPOR Y3, Y7, Y7; \
    VESC(0x2f); \
    VESC(0x62); \
    VESC(0x66); \
    VESC(0x6e); \
    VESC(0x72); \
    VESC(0x74); \
    VMASK(Y6, Y7, BX); \
    MOVQ BX, 40(DI); \
    VBCAST(0x75, X4, Y4); \
    VEQ(Y4); \
    VMASK(Y2, Y3, BX); \
    MOVQ BX, 48(DI)

// func validMasksAVX2(p *byte, nb int, out *vmask)
//
// Fills out[0:nb] with the vmask of the 64-byte blocks at p.
TEXT ·validMasksAVX2(SB), NOSPLIT, $0-24
    MOVQ p+0(FP), SI
    MOVQ nb+8(FP), CX
    MOVQ out+16(FP), DI
    VALID_SETUP

valid_block:
    TESTQ CX, CX
    JZ valid_done
    VMOVDQU (SI), Y0
    VMOVDQU 32(SI), Y1
    VALID_MAIN
    TESTQ R8, R8
    JZ valid_next
    VALID_ESC

valid_next:
    ADDQ $64, SI
    ADDQ $64, DI
    DECQ CX
    JMP valid_block

valid_done:
    VZEROUPPER
    RET

// func validMasksHiAVX2(p *byte, nb int, out *vmask, hi *uint64)
//
// validMasksAVX2 that also fills hi[0:nb] with the masks of the bytes of
// 0x80 and up of the blocks.
TEXT ·validMasksHiAVX2(SB), NOSPLIT, $0-32
    MOVQ p+0(FP), SI
    MOVQ nb+8(FP), CX
    MOVQ out+16(FP), DI
    MOVQ hi+24(FP), R9
    VALID_SETUP

hi_block:
    TESTQ CX, CX
    JZ hi_done
    VMOVDQU (SI), Y0
    VMOVDQU 32(SI), Y1
    VMASK(Y0, Y1, BX)
    MOVQ BX, (R9)
    ADDQ $8, R9
    VALID_MAIN
    TESTQ R8, R8
    JZ hi_next
    VALID_ESC

hi_next:
    ADDQ $64, SI
    ADDQ $64, DI
    DECQ CX
    JMP hi_block

hi_done:
    VZEROUPPER
    RET

// func countMasksAVX2(p *byte, nb int, out *cmask)
//
// Fills out[0:nb] with the cmask of the 64-byte blocks at p.
TEXT ·countMasksAVX2(SB), NOSPLIT, $0-24
    MOVQ p+0(FP), SI
    MOVQ nb+8(FP), CX
    MOVQ out+16(FP), DI
    VBCAST(0x22, X15, Y15)
    VBCAST(0x5c, X14, Y14)
    VBCAST(0x20, X13, Y13)
    VBCAST(0x7b, X12, Y12)  // '{', and '[' with bit 5 set
    VBCAST(0x7d, X11, Y11)  // '}', and ']' with bit 5 set
    VBCAST(0x2c, X10, Y10)
    MOVQ $-1, AX
    VMOVQ AX, X9            // all ones, for the prefix XOR

count_masks_block:
    TESTQ CX, CX
    JZ count_masks_done
    VMOVDQU (SI), Y0
    VMOVDQU 32(SI), Y1
    VEQ(Y15)
    VMASK(Y2, Y3, BX)
    MOVQ BX, 0(DI)          // quote
    VMOVQ BX, X4
    VPCLMULQDQ $0, X9, X4, X4
    VMOVQ X4, 40(DI)        // r0
    VEQ(Y14)
    VMASK(Y2, Y3, BX)
    MOVQ BX, 8(DI)          // bslash
    VEQ(Y10)
    VMASK(Y2, Y3, BX)
    MOVQ BX, 32(DI)         // comma
    VPOR Y13, Y0, Y0
    VPOR Y13, Y1, Y1
    VEQ(Y12)
    VMASK(Y2, Y3, BX)
    MOVQ BX, 16(DI)         // open
    VEQ(Y11)
    VMASK(Y2, Y3, BX)
    MOVQ BX, 24(DI)         // close
    ADDQ $64, SI
    ADDQ $48, DI
    DECQ CX
    JMP count_masks_block

count_masks_done:
    VZEROUPPER
    RET

// WIDESTOPS marks in dst, for 128-byte steps, the quotes, backslashes and
// control bytes of src, and with cmp VPCMPGTB the bytes of 0x80 and above
// too, or with cmp VPSUBUSB none of them. The quote, xored with 0x02, is
// 0x20, the control bytes stay below it and the others go above it: so
// the bytes below 0x21 are then the quote and the control bytes, and as
// signed bytes those of 0x80 and above too. Clobbers t.
#define WIDESTOPS(cmp, src, dst, t) \
    VPXOR Y11, src, dst; \
    cmp dst, Y12, dst; \
    VPCMPEQB Y14, src, t; \
    VPOR t, dst, dst

// func strSkipAVX2(p *byte, n, i int) (int, bool)
//
// Returns the start of the first 64-byte block of p[i:n] that holds a
// quote, a backslash or a control byte, or the first i+64k past which no
// whole block is left, and whether a byte of the blocks before it is 0x80
// or above.
TEXT ·strSkipAVX2(SB), NOSPLIT, $0-33
    MOVQ p+0(FP), SI
    MOVQ n+8(FP), DX
    MOVQ i+16(FP), CX
    VBCAST(0x22, X15, Y15)
    VBCAST(0x5c, X14, Y14)
    VBCAST(0x1f, X13, Y13)
    VBCAST(0x21, X12, Y12)
    VBCAST(0x02, X11, Y11)
    VPXOR Y8, Y8, Y8
    LEAQ -128(DX), R10

    // 128 bytes a step, as strEndAVX2 takes them; a step that meets a
    // stop byte, or a byte of 0x80 or above before one was seen, goes on
    // one block at a time.
skip_next:
    CMPQ CX, R10
    JGT skip_block
    VPMOVMSKB Y8, AX
    TESTL AX, AX
    JNZ skip_wideh
skip_wide:
    VMOVDQU (SI)(CX*1), Y0
    VMOVDQU 32(SI)(CX*1), Y1
    VMOVDQU 64(SI)(CX*1), Y9
    VMOVDQU 96(SI)(CX*1), Y10
    WIDESTOPS(VPCMPGTB, Y0, Y2, Y3)
    WIDESTOPS(VPCMPGTB, Y1, Y4, Y5)
    WIDESTOPS(VPCMPGTB, Y9, Y6, Y3)
    WIDESTOPS(VPCMPGTB, Y10, Y5, Y3)
    VPOR Y4, Y2, Y2
    VPOR Y5, Y6, Y6
    VPOR Y6, Y2, Y2
    VPTEST Y2, Y2
    JNZ skip_block
    ADDQ $128, CX
    CMPQ CX, R10
    JLE skip_wide
    JMP skip_block

skip_wideh:
    VMOVDQU (SI)(CX*1), Y0
    VMOVDQU 32(SI)(CX*1), Y1
    VMOVDQU 64(SI)(CX*1), Y9
    VMOVDQU 96(SI)(CX*1), Y10
    WIDESTOPS(VPSUBUSB, Y0, Y2, Y3)
    WIDESTOPS(VPSUBUSB, Y1, Y4, Y5)
    WIDESTOPS(VPSUBUSB, Y9, Y6, Y3)
    WIDESTOPS(VPSUBUSB, Y10, Y5, Y3)
    VPOR Y4, Y2, Y2
    VPOR Y5, Y6, Y6
    VPOR Y6, Y2, Y2
    VPTEST Y2, Y2
    JNZ skip_block
    ADDQ $128, CX
    CMPQ CX, R10
    JLE skip_wideh

skip_block:
    LEAQ 64(CX), AX
    CMPQ AX, DX
    JA skip_done
    VMOVDQU (SI)(CX*1), Y0
    VMOVDQU 32(SI)(CX*1), Y1
    VPCMPEQB Y15, Y0, Y2
    VPCMPEQB Y14, Y0, Y3
    VPMINUB Y13, Y0, Y4
    VPCMPEQB Y4, Y0, Y4
    VPOR Y3, Y2, Y2
    VPOR Y4, Y2, Y2
    VPCMPEQB Y15, Y1, Y5
    VPCMPEQB Y14, Y1, Y6
    VPMINUB Y13, Y1, Y7
    VPCMPEQB Y7, Y1, Y7
    VPOR Y6, Y5, Y5
    VPOR Y7, Y5, Y5
    VPOR Y5, Y2, Y2
    VPTEST Y2, Y2
    JNZ skip_done
    VPOR Y0, Y8, Y8
    VPOR Y1, Y8, Y8
    ADDQ $64, CX
    JMP skip_next

skip_done:
    VPMOVMSKB Y8, AX
    TESTL AX, AX
    SETNE ret1+32(FP)
    MOVQ CX, ret+24(FP)
    VZEROUPPER
    RET

// HTMLSTOPS marks in dst, for 128-byte steps of strSkipHTMLAVX2, what
// WIDESTOPS(cmp, src, dst, t) marks, and '&', '<' and '>'. Clobbers t.
#define HTMLSTOPS(cmp, src, dst, t) \
    WIDESTOPS(cmp, src, dst, t); \
    VPCMPEQB Y10, src, t; \
    VPOR t, dst, dst; \
    VPOR Y11, src, t; \
    VPCMPEQB Y7, t, t; \
    VPOR t, dst, dst

// func strSkipHTMLAVX2(p *byte, n, i int) (int, bool)
//
// Is strSkipAVX2, but reports whether a byte of the blocks before the one
// it returns is '<', '>', '&' or 0xE2, which HasHTML looks for.
TEXT ·strSkipHTMLAVX2(SB), NOSPLIT, $0-33
    MOVQ p+0(FP), SI
    MOVQ n+8(FP), DX
    MOVQ i+16(FP), CX
    VBCAST(0x22, X15, Y15)
    VBCAST(0x5c, X14, Y14)
    VBCAST(0x1f, X13, Y13)
    VBCAST(0x21, X12, Y12)
    VBCAST(0x02, X11, Y11)
    VBCAST(0x26, X10, Y10)
    VBCAST(0xe2, X9, Y9)
    VBCAST(0x3e, X7, Y7)    // '>', and '<' with bit 1 set
    VPXOR Y8, Y8, Y8
    XORL R9, R9
    LEAQ -128(DX), R10

    // 128 bytes a step, as in strSkipAVX2: while no byte HasHTML looks
    // for was seen, the steps look for those too, and while no byte of
    // 0x80 or above was seen either, for all of those.
hskip_next:
    CMPQ CX, R10
    JGT hskip_block
    VPTEST Y8, Y8
    JNZ hskip_widec
    TESTL R9, R9
    JNZ hskip_wideb
hskip_widea:
    VMOVDQU (SI)(CX*1), Y0
    VMOVDQU 32(SI)(CX*1), Y1
    VMOVDQU 64(SI)(CX*1), Y2
    VMOVDQU 96(SI)(CX*1), Y3
    HTMLSTOPS(VPCMPGTB, Y0, Y4, Y5)
    HTMLSTOPS(VPCMPGTB, Y1, Y5, Y6)
    VPOR Y5, Y4, Y4
    HTMLSTOPS(VPCMPGTB, Y2, Y5, Y6)
    VPOR Y5, Y4, Y4
    HTMLSTOPS(VPCMPGTB, Y3, Y5, Y6)
    VPOR Y5, Y4, Y4
    VPTEST Y4, Y4
    JNZ hskip_block
    ADDQ $128, CX
    CMPQ CX, R10
    JLE hskip_widea
    JMP hskip_block

    // 0xE2 is not marked as a byte of 0x80 or above here.
hskip_wideb:
    VMOVDQU (SI)(CX*1), Y0
    VMOVDQU 32(SI)(CX*1), Y1
    VMOVDQU 64(SI)(CX*1), Y2
    VMOVDQU 96(SI)(CX*1), Y3
    HTMLSTOPS(VPSUBUSB, Y0, Y4, Y5)
    VPCMPEQB Y9, Y0, Y5
    VPOR Y5, Y4, Y4
    HTMLSTOPS(VPSUBUSB, Y1, Y5, Y6)
    VPOR Y5, Y4, Y4
    VPCMPEQB Y9, Y1, Y5
    VPOR Y5, Y4, Y4
    HTMLSTOPS(VPSUBUSB, Y2, Y5, Y6)
    VPOR Y5, Y4, Y4
    VPCMPEQB Y9, Y2, Y5
    VPOR Y5, Y4, Y4
    HTMLSTOPS(VPSUBUSB, Y3, Y5, Y6)
    VPOR Y5, Y4, Y4
    VPCMPEQB Y9, Y3, Y5
    VPOR Y5, Y4, Y4
    VPTEST Y4, Y4
    JNZ hskip_block
    ADDQ $128, CX
    CMPQ CX, R10
    JLE hskip_wideb
    JMP hskip_block

hskip_widec:
    VMOVDQU (SI)(CX*1), Y0
    VMOVDQU 32(SI)(CX*1), Y1
    VMOVDQU 64(SI)(CX*1), Y2
    VMOVDQU 96(SI)(CX*1), Y3
    WIDESTOPS(VPSUBUSB, Y0, Y4, Y5)
    WIDESTOPS(VPSUBUSB, Y1, Y5, Y6)
    VPOR Y5, Y4, Y4
    WIDESTOPS(VPSUBUSB, Y2, Y5, Y6)
    VPOR Y5, Y4, Y4
    WIDESTOPS(VPSUBUSB, Y3, Y5, Y6)
    VPOR Y5, Y4, Y4
    VPTEST Y4, Y4
    JNZ hskip_block
    ADDQ $128, CX
    CMPQ CX, R10
    JLE hskip_widec

    // One block: its stop bytes, else the bytes HasHTML looks for and
    // whether it holds a byte of 0x80 or above.
hskip_block:
    LEAQ 64(CX), AX
    CMPQ AX, DX
    JA hskip_done
    VMOVDQU (SI)(CX*1), Y0
    VMOVDQU 32(SI)(CX*1), Y1
    VPCMPEQB Y15, Y0, Y2
    VPCMPEQB Y14, Y0, Y3
    VPOR Y3, Y2, Y2
    VPMINUB Y13, Y0, Y3
    VPCMPEQB Y3, Y0, Y3
    VPOR Y3, Y2, Y2
    VPCMPEQB Y15, Y1, Y3
    VPOR Y3, Y2, Y2
    VPCMPEQB Y14, Y1, Y3
    VPOR Y3, Y2, Y2
    VPMINUB Y13, Y1, Y3
    VPCMPEQB Y3, Y1, Y3
    VPOR Y3, Y2, Y2
    VPTEST Y2, Y2
    JNZ hskip_done
    VPOR Y11, Y0, Y2
    VPCMPEQB Y7, Y2, Y2
    VPCMPEQB Y10, Y0, Y3
    VPOR Y3, Y2, Y2
    VPCMPEQB Y9, Y0, Y3
    VPOR Y3, Y2, Y2
    VPOR Y11, Y1, Y3
    VPCMPEQB Y7, Y3, Y3
    VPOR Y3, Y2, Y2
    VPCMPEQB Y10, Y1, Y3
    VPOR Y3, Y2, Y2
    VPCMPEQB Y9, Y1, Y3
    VPOR Y3, Y2, Y2
    VPOR Y2, Y8, Y8
    VPOR Y1, Y0, Y2
    VPMOVMSKB Y2, AX
    ORL AX, R9
    ADDQ $64, CX
    JMP hskip_next

hskip_done:
    VPTEST Y8, Y8
    SETNE ret1+32(FP)
    MOVQ CX, ret+24(FP)
    VZEROUPPER
    RET

// func htmlSkipAVX2(p *byte, n, i int) int
//
// Returns the start of the first 64-byte block of p[i:n] that holds '<',
// '>', '&' or 0xE2, the first byte of U+2028 and U+2029, or the first
// i+64k past which no whole block is left.
TEXT ·htmlSkipAVX2(SB), NOSPLIT, $0-32
    MOVQ p+0(FP), SI
    MOVQ n+8(FP), DX
    MOVQ i+16(FP), CX
    VBCAST(0x3c, X15, Y15)
    VBCAST(0x3e, X14, Y14)
    VBCAST(0x26, X13, Y13)
    VBCAST(0xe2, X12, Y12)

html_block:
    LEAQ 64(CX), AX
    CMPQ AX, DX
    JA html_done
    VMOVDQU (SI)(CX*1), Y0
    VMOVDQU 32(SI)(CX*1), Y1
    VPCMPEQB Y15, Y0, Y2
    VPCMPEQB Y14, Y0, Y3
    VPCMPEQB Y13, Y0, Y4
    VPCMPEQB Y12, Y0, Y5
    VPOR Y3, Y2, Y2
    VPOR Y5, Y4, Y4
    VPOR Y4, Y2, Y2
    VPCMPEQB Y15, Y1, Y6
    VPCMPEQB Y14, Y1, Y7
    VPCMPEQB Y13, Y1, Y8
    VPCMPEQB Y12, Y1, Y9
    VPOR Y7, Y6, Y6
    VPOR Y9, Y8, Y8
    VPOR Y8, Y6, Y6
    VPOR Y6, Y2, Y2
    VPTEST Y2, Y2
    JNZ html_done
    ADDQ $64, CX
    JMP html_block

html_done:
    MOVQ CX, ret+24(FP)
    VZEROUPPER
    RET

// ENDSTOPS marks in dst the quotes, backslashes and control bytes of src,
// with the constants strEndAVX2 loads. Clobbers t.
#define ENDSTOPS(src, dst, t) \
    VPCMPEQB Y15, src, dst; \
    VPCMPEQB Y14, src, t; \
    VPOR t, dst, dst; \
    VPMINUB Y13, src, t; \
    VPCMPEQB t, src, t; \
    VPOR t, dst, dst

// strEndBytes holds the quote, the backslash, 0x1f, 0x02 and 0x21, the
// bytes strEndAVX2 broadcasts.
DATA strEndBytes<>+0(SB)/8, $0x00000021021f5c22
GLOBL strEndBytes<>(SB), RODATA|NOPTR, $8

// func strEndAVX2(p *byte, n, i int) (int, bool)
//
// Returns the index of the first quote, backslash or control byte of
// p[i:n], or, when there is none, the first i+32k past which fewer than 32
// bytes are left, and whether a byte before that index is 0x80 or above.
// It reads no byte past p[n-1].
TEXT ·strEndAVX2(SB), NOSPLIT, $0-33
    MOVQ p+0(FP), SI
    MOVQ n+8(FP), DX
    MOVQ i+16(FP), DI
    VPBROADCASTB strEndBytes<>+0(SB), Y15
    VPBROADCASTB strEndBytes<>+1(SB), Y14
    VPBROADCASTB strEndBytes<>+2(SB), Y13
    VPBROADCASTB strEndBytes<>+3(SB), Y11
    VPBROADCASTB strEndBytes<>+4(SB), Y12
    VPXOR Y8, Y8, Y8
    XORL R8, R8

    // The first 64 bytes, where most strings end.
    LEAQ 64(DI), AX
    CMPQ AX, DX
    JA end_half
    VMOVDQU (SI)(DI*1), Y0
    VMOVDQU 32(SI)(DI*1), Y1
    ENDSTOPS(Y0, Y2, Y4)
    ENDSTOPS(Y1, Y3, Y5)
    VPOR Y3, Y2, Y6
    VPTEST Y6, Y6
    JNZ end_found
    VPOR Y0, Y8, Y8
    VPOR Y1, Y8, Y8
    MOVQ AX, DI

    // Then 128 bytes a step, while none is a stop byte, nor, until one is
    // seen, a byte of 0x80 or above. A step that meets one goes on 64
    // bytes at a time.
end_next:
    LEAQ -128(DX), R10
    CMPQ DI, R10
    JGT end_block
    VPMOVMSKB Y8, BX
    TESTL BX, BX
    JNZ end_wideh
end_wide:
    VMOVDQU (SI)(DI*1), Y0
    VMOVDQU 32(SI)(DI*1), Y1
    VMOVDQU 64(SI)(DI*1), Y9
    VMOVDQU 96(SI)(DI*1), Y10
    WIDESTOPS(VPCMPGTB, Y0, Y2, Y3)
    WIDESTOPS(VPCMPGTB, Y1, Y4, Y5)
    WIDESTOPS(VPCMPGTB, Y9, Y6, Y3)
    WIDESTOPS(VPCMPGTB, Y10, Y5, Y3)
    VPOR Y4, Y2, Y2
    VPOR Y5, Y6, Y6
    VPOR Y6, Y2, Y2
    VPTEST Y2, Y2
    JNZ end_block
    ADDQ $128, DI
    CMPQ DI, R10
    JLE end_wide
    JMP end_block

end_wideh:
    VMOVDQU (SI)(DI*1), Y0
    VMOVDQU 32(SI)(DI*1), Y1
    VMOVDQU 64(SI)(DI*1), Y9
    VMOVDQU 96(SI)(DI*1), Y10
    WIDESTOPS(VPSUBUSB, Y0, Y2, Y3)
    WIDESTOPS(VPSUBUSB, Y1, Y4, Y5)
    WIDESTOPS(VPSUBUSB, Y9, Y6, Y3)
    WIDESTOPS(VPSUBUSB, Y10, Y5, Y3)
    VPOR Y4, Y2, Y2
    VPOR Y5, Y6, Y6
    VPOR Y6, Y2, Y2
    VPTEST Y2, Y2
    JNZ end_block
    ADDQ $128, DI
    CMPQ DI, R10
    JLE end_wideh

end_block:
    LEAQ 64(DI), AX
    CMPQ AX, DX
    JA end_half
    VMOVDQU (SI)(DI*1), Y0
    VMOVDQU 32(SI)(DI*1), Y1
    ENDSTOPS(Y0, Y2, Y4)
    ENDSTOPS(Y1, Y3, Y5)
    VPOR Y3, Y2, Y6
    VPTEST Y6, Y6
    JNZ end_found
    VPOR Y0, Y8, Y8
    VPOR Y1, Y8, Y8
    MOVQ AX, DI
    JMP end_next

end_found:
    VPMOVMSKB Y2, BX
    VPMOVMSKB Y3, CX
    SHLQ $32, CX
    ORQ CX, BX
    VPMOVMSKB Y0, R9
    VPMOVMSKB Y1, CX
    SHLQ $32, CX
    ORQ CX, R9
    TZCNTQ BX, CX
    BZHIQ CX, R9, R9
    ORQ R9, R8
    ADDQ CX, DI
    JMP end_done

end_half:
    LEAQ 32(DI), AX
    CMPQ AX, DX
    JA end_done
    VMOVDQU (SI)(DI*1), Y0
    ENDSTOPS(Y0, Y2, Y4)
    VPMOVMSKB Y2, BX
    VPMOVMSKB Y0, R9
    TESTL BX, BX
    JNZ end_found32
    ORQ R9, R8
    MOVQ AX, DI
    JMP end_done

end_found32:
    TZCNTL BX, CX
    BZHIQ CX, R9, R9
    ORQ R9, R8
    ADDQ CX, DI

end_done:
    VPMOVMSKB Y8, AX
    ORQ R8, AX
    TESTQ AX, AX
    SETNE ret1+32(FP)
    MOVQ DI, ret+24(FP)
    VZEROUPPER
    RET
