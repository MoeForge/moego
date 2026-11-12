//go:build arm64 && !purego

#include "textflag.h"

DATA quoteBitMask<>+0(SB)/8, $0x8040201008040201
DATA quoteBitMask<>+8(SB)/8, $0x8040201008040201
GLOBL quoteBitMask<>(SB), RODATA|NOPTR, $16

// QSETUP loads the constants of the quote kernels: quotes (V16),
// backslashes (V17), 0x1f (V18), 0x80 (V19), and '<', '>' and '&'
// (V20, V21, V22) when the byte in r is not zero, else zero bytes, which
// match only control bytes.
#define QSETUP(r, label) \
    VMOVI $0x22, V16.B16; \
    VMOVI $0x5c, V17.B16; \
    VMOVI $0x1f, V18.B16; \
    VMOVI $0x80, V19.B16; \
    VEOR V20.B16, V20.B16, V20.B16; \
    VEOR V21.B16, V21.B16, V21.B16; \
    VEOR V22.B16, V22.B16, V22.B16; \
    CBZ r, label; \
    VMOVI $0x3c, V20.B16; \
    VMOVI $0x3e, V21.B16; \
    VMOVI $0x26, V22.B16; \
label:

// QOTHER1 marks in dst (with bit 7 set) the bytes of src that need an escape
// other than a backslash: control bytes (<= 0x1f), high bytes (>= 0x80), and,
// with html, '<', '>' and '&'. Clobbers t.
#define QOTHER1(src, dst, t) \
    VUMIN V18.B16, src.B16, dst.B16; \
    VCMEQ dst.B16, src.B16, dst.B16; \
    VORR src.B16, dst.B16, dst.B16; \
    VCMEQ V20.B16, src.B16, t.B16; \
    VORR t.B16, dst.B16, dst.B16; \
    VCMEQ V21.B16, src.B16, t.B16; \
    VORR t.B16, dst.B16, dst.B16; \
    VCMEQ V22.B16, src.B16, t.B16; \
    VORR t.B16, dst.B16, dst.B16

// QOTHER checks whether any of the 64 bytes in V0..V3 needs an escape other
// than a backslash, leaving a non-zero value in R7 when so. Clobbers V4..V7.
#define QOTHER \
    QOTHER1(V0, V4, V5); \
    QOTHER1(V1, V6, V5); \
    VORR V6.B16, V4.B16, V4.B16; \
    QOTHER1(V2, V6, V5); \
    QOTHER1(V3, V7, V5); \
    VORR V7.B16, V6.B16, V6.B16; \
    VORR V6.B16, V4.B16, V4.B16; \
    VAND V19.B16, V4.B16, V4.B16; \
    WORD $0x6e30a884; \
    VMOV V4.B[0], R7

// func quoteCountNEON(src *byte, n, i int, html bool) (int, int)
TEXT ·quoteCountNEON(SB), NOSPLIT, $0-48
    MOVD src+0(FP), R0
    MOVD n+8(FP), R1
    MOVD i+16(FP), R2
    MOVBU html+24(FP), R3
    MOVD $0, R4
    QSETUP(R3, count_setup)

count_block:
    ADD $64, R2, R5
    CMP R1, R5
    BHI count_done
    ADD R2, R0, R6
    VLD1 (R6), [V0.B16, V1.B16, V2.B16, V3.B16]
    QOTHER
    CBNZ R7, count_done
    VCMEQ V16.B16, V0.B16, V4.B16
    VCMEQ V17.B16, V0.B16, V5.B16
    VORR V5.B16, V4.B16, V4.B16
    VCMEQ V16.B16, V1.B16, V5.B16
    VCMEQ V17.B16, V1.B16, V6.B16
    VORR V6.B16, V5.B16, V5.B16
    VCMEQ V16.B16, V2.B16, V6.B16
    VCMEQ V17.B16, V2.B16, V7.B16
    VORR V7.B16, V6.B16, V6.B16
    VCMEQ V16.B16, V3.B16, V7.B16
    VCMEQ V17.B16, V3.B16, V8.B16
    VORR V8.B16, V7.B16, V7.B16
    // Each matching lane is 0xff (-1); subtracting from zero adds 1 per match.
    VEOR V8.B16, V8.B16, V8.B16
    VSUB V4.B16, V8.B16, V8.B16
    VSUB V5.B16, V8.B16, V8.B16
    VSUB V6.B16, V8.B16, V8.B16
    VSUB V7.B16, V8.B16, V8.B16
    WORD $0x6e303908            // UADDLV H8, V8.16B
    VMOV V8.D[0], R5
    ADD R5, R4, R4
    ADD $64, R2, R2
    B count_block

count_done:
    MOVD R2, ret+32(FP)
    MOVD R4, ret1+40(FP)
    RET

// QGROUP writes the 8-byte group at R6+off, whose quotes and backslashes
// are the low byte of R11 (and whose popcount is the low byte of R12), to
// dst[R4:] with a backslash before each of them, and moves R4 past it.
// V24 holds backslashes in its high 8 bytes.
#define QGROUP(off) \
    AND $0xff, R11, R5; \
    LSR $8, R11, R11; \
    MOVD off(R6), R7; \
    VMOV R7, V24.D[0]; \
    LSL $4, R5, R5; \
    ADD R5, R10, R5; \
    VLD1 (R5), [V5.B16]; \
    VTBL V5.B16, [V24.B16], V5.B16; \
    ADD R4, R0, R5; \
    VST1 [V5.B16], (R5); \
    AND $0xff, R12, R5; \
    LSR $8, R12, R12; \
    ADD $8, R5, R5; \
    ADD R5, R4, R4

// func quoteNEON(dst *byte, dn int, src *byte, n, i, w int, html bool, shuf *[256][2]uint64) (int, int)
TEXT ·quoteNEON(SB), NOSPLIT, $0-80
    MOVD dst+0(FP), R0
    MOVD dn+8(FP), R13
    MOVD src+16(FP), R1
    MOVD n+24(FP), R2
    MOVD i+32(FP), R3
    MOVD w+40(FP), R4
    MOVBU html+48(FP), R9
    MOVD shuf+56(FP), R10
    QSETUP(R9, write_setup)
    MOVD $quoteBitMask<>(SB), R5
    VLD1 (R5), [V23.B16]
    VMOV V17.B16, V24.B16
    SUB $136, R13, R13          // the last w a block may start at

write_block:
    ADD $64, R3, R5
    CMP R2, R5
    BHI write_done
    CMP R13, R4
    BGT write_done
    ADD R3, R1, R6
    VLD1 (R6), [V0.B16, V1.B16, V2.B16, V3.B16]
    QOTHER
    CBNZ R7, write_done
    VCMEQ V16.B16, V0.B16, V4.B16
    VCMEQ V17.B16, V0.B16, V8.B16
    VORR V8.B16, V4.B16, V4.B16
    VCMEQ V16.B16, V1.B16, V5.B16
    VCMEQ V17.B16, V1.B16, V8.B16
    VORR V8.B16, V5.B16, V5.B16
    VCMEQ V16.B16, V2.B16, V6.B16
    VCMEQ V17.B16, V2.B16, V8.B16
    VORR V8.B16, V6.B16, V6.B16
    VCMEQ V16.B16, V3.B16, V7.B16
    VCMEQ V17.B16, V3.B16, V8.B16
    VORR V8.B16, V7.B16, V7.B16
    VAND V23.B16, V4.B16, V4.B16
    VAND V23.B16, V5.B16, V5.B16
    VAND V23.B16, V6.B16, V6.B16
    VAND V23.B16, V7.B16, V7.B16
    VADDP V5.B16, V4.B16, V4.B16
    VADDP V7.B16, V6.B16, V6.B16
    VADDP V6.B16, V4.B16, V4.B16
    VADDP V4.B16, V4.B16, V4.B16
    VMOV V4.D[0], R11
    CBNZ R11, write_escapes
    ADD R4, R0, R5
    VST1 [V0.B16, V1.B16, V2.B16, V3.B16], (R5)
    ADD $64, R4, R4

write_next:
    ADD $64, R3, R3
    B write_block

write_escapes:
    FMOVD R11, F7
    VCNT V7.B8, V7.B8
    VMOV V7.D[0], R12
    QGROUP(0)
    QGROUP(8)
    QGROUP(16)
    QGROUP(24)
    QGROUP(32)
    QGROUP(40)
    QGROUP(48)
    QGROUP(56)
    B write_next

write_done:
    MOVD R3, ret+64(FP)
    MOVD R4, ret1+72(FP)
    RET
