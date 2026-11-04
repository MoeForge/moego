//go:build arm64 && !purego

#include "textflag.h"

DATA uglyBitMask<>+0(SB)/8, $0x8040201008040201
DATA uglyBitMask<>+8(SB)/8, $0x8040201008040201
GLOBL uglyBitMask<>(SB), RODATA|NOPTR, $16

// UGLYGROUP writes the kept bytes of the 8-byte group at R6+off, whose keep
// bits are the low byte of R11 (and whose popcount is the low byte of R12),
// to dst[R4:] and moves R4 past them.
#define UGLYGROUP(off) \
    AND $0xff, R11, R5; \
    LSR $8, R11, R11; \
    LSL $3, R5, R5; \
    ADD R5, R10, R5; \
    FMOVD (R5), F5; \
    FMOVD off(R6), F6; \
    VTBL V5.B8, [V6.B16], V5.B8; \
    ADD R4, R0, R5; \
    FMOVD F5, (R5); \
    AND $0xff, R12, R5; \
    LSR $8, R12, R12; \
    ADD R5, R4, R4

// func uglyNEON(dst, src *byte, n, i, w int, in uint64, shuf *[256]uint64) (int, int, uint64)
TEXT ·uglyNEON(SB), NOSPLIT, $0-80
    MOVD dst+0(FP), R0
    MOVD src+8(FP), R1
    MOVD n+16(FP), R2
    MOVD i+24(FP), R3
    MOVD w+32(FP), R4
    MOVD in+40(FP), R9
    MOVD shuf+48(FP), R10
    VMOVI $0x22, V16.B16
    VMOVI $0x5c, V17.B16
    VMOVI $0x20, V18.B16
    MOVD $uglyBitMask<>(SB), R5
    VLD1 (R5), [V21.B16]

ugly_block:
    ADD $64, R3, R5
    CMP R2, R5
    BHI ugly_done
    ADD R3, R1, R6
    VLD1 (R6), [V0.B16, V1.B16, V2.B16, V3.B16]
    VCMEQ V17.B16, V0.B16, V4.B16
    VCMEQ V17.B16, V1.B16, V5.B16
    VCMEQ V17.B16, V2.B16, V6.B16
    VCMEQ V17.B16, V3.B16, V7.B16
    VORR V5.B16, V4.B16, V4.B16
    VORR V7.B16, V6.B16, V6.B16
    VORR V6.B16, V4.B16, V4.B16
    WORD $0x6e30a884            // UMAXV B4, V4.16B
    VMOV V4.B[0], R5
    CBNZ R5, ugly_done

    // Quotes (V4..V7) and bytes <= ' ' (V8..V11).
    VCMEQ V16.B16, V0.B16, V4.B16
    VCMEQ V16.B16, V1.B16, V5.B16
    VCMEQ V16.B16, V2.B16, V6.B16
    VCMEQ V16.B16, V3.B16, V7.B16
    VUMIN V18.B16, V0.B16, V8.B16
    VUMIN V18.B16, V1.B16, V9.B16
    VUMIN V18.B16, V2.B16, V10.B16
    VUMIN V18.B16, V3.B16, V11.B16
    VCMEQ V8.B16, V0.B16, V8.B16
    VCMEQ V9.B16, V1.B16, V9.B16
    VCMEQ V10.B16, V2.B16, V10.B16
    VCMEQ V11.B16, V3.B16, V11.B16
    VAND V21.B16, V4.B16, V4.B16
    VAND V21.B16, V5.B16, V5.B16
    VAND V21.B16, V6.B16, V6.B16
    VAND V21.B16, V7.B16, V7.B16
    VADDP V5.B16, V4.B16, V4.B16
    VADDP V7.B16, V6.B16, V6.B16
    VADDP V6.B16, V4.B16, V4.B16
    VAND V21.B16, V8.B16, V8.B16
    VAND V21.B16, V9.B16, V9.B16
    VAND V21.B16, V10.B16, V10.B16
    VAND V21.B16, V11.B16, V11.B16
    VADDP V9.B16, V8.B16, V8.B16
    VADDP V11.B16, V10.B16, V10.B16
    VADDP V10.B16, V8.B16, V8.B16
    VADDP V8.B16, V4.B16, V4.B16
    VMOV V4.D[0], R7            // quotes
    VMOV V4.D[1], R11           // bytes <= ' '
    EOR R7<<1, R7, R7
    EOR R7<<2, R7, R7
    EOR R7<<4, R7, R7
    EOR R7<<8, R7, R7
    EOR R7<<16, R7, R7
    EOR R7<<32, R7, R7
    EOR R9, R7, R7              // R7: bytes inside strings
    BIC R7, R11, R11            // R11: bytes to drop
    ASR $63, R7, R7             // R7: string state after the block
    CBNZ R11, ugly_dirty
    CBZ R0, ugly_next
    ADD R4, R0, R5
    VST1 [V0.B16, V1.B16, V2.B16, V3.B16], (R5)
    ADD $64, R4, R4

ugly_next:
    MOVD R7, R9
    ADD $64, R3, R3
    B ugly_block

ugly_dirty:
    CBZ R0, ugly_done
    MVN R11, R11                // R11: bytes to keep
    FMOVD R11, F7
    VCNT V7.B8, V7.B8
    VMOV V7.D[0], R12
    UGLYGROUP(0)
    UGLYGROUP(8)
    UGLYGROUP(16)
    UGLYGROUP(24)
    UGLYGROUP(32)
    UGLYGROUP(40)
    UGLYGROUP(48)
    UGLYGROUP(56)
    B ugly_next

ugly_done:
    MOVD R3, ret+56(FP)
    MOVD R4, ret1+64(FP)
    MOVD R9, ret2+72(FP)
    RET
