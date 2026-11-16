//go:build arm64 && !purego

#include "textflag.h"

// Bit weights for packing 16 byte comparison lanes into 2 mask bytes via ADDP.
DATA validBitMask<>+0(SB)/8, $0x8040201008040201
DATA validBitMask<>+8(SB)/8, $0x8040201008040201
GLOBL validBitMask<>(SB), RODATA|NOPTR, $16

// validWS maps (b & 15) to the whitespace byte with that low nibble:
// ' ' (0x20 at 0), '\t' (0x09 at 9), '\n' (0x0a at 10), '\r' (0x0d at 13).
// Unused entries are 0x80 (low nibble 0 != index), which never equals b.
DATA validWS<>+0(SB)/8, $0x8080808080808020
DATA validWS<>+8(SB)/8, $0x80800d80800a0980
GLOBL validWS<>(SB), RODATA|NOPTR, $16

// validOp maps ((b | 0x20) & 15) to '{' (0x7b at 11), '}' (0x7d at 13),
// ':' (0x3a at 10) and ',' (0x2c at 12). Slot 0 is 0x81 (low nibble 1 != 0)
// and other unused slots are 0x80 (low nibble 0 != index).
DATA validOp<>+0(SB)/8, $0x8080808080808081
DATA validOp<>+8(SB)/8, $0x80807d2c7b3a8080
GLOBL validOp<>(SB), RODATA|NOPTR, $16

// validEscLo and validEscHi classify the valid single-character escape bytes:
// '"' (0x22), '/' (0x2f), '\\' (0x5c), 'b' (0x62), 'f' (0x66), 'n' (0x6e),
// 'r' (0x72), 't' (0x74), plus 'u' (0x75) with its own bit 0x20.
//Low nibbles:
//  0x2: '"' (0x22 -> bit 0), 'b' (0x62 -> bit 2), 'r' (0x72 -> bit 3) => 0x0d
//  0x4: 't' (0x74 -> bit 3) => 0x08
//  0x5: 'u' (0x75 -> bit 5) => 0x20
//  0x6: 'f' (0x66 -> bit 2) => 0x04
//  0xc: '\\' (0x5c -> bit 1) => 0x02
//  0xe: 'n' (0x6e -> bit 2) => 0x04
//  0xf: '/' (0x2f -> bit 0) => 0x01
DATA validEscLo<>+0(SB)/8, $0x00042008000d0000
DATA validEscLo<>+8(SB)/8, $0x0104000200000000
GLOBL validEscLo<>(SB), RODATA|NOPTR, $16

// High nibbles:
//  0x2: 0x01
//  0x5: 0x02
//  0x6: 0x04
//  0x7: 0x28 (bit 3 for r/t, bit 5 for u)
DATA validEscHi<>+0(SB)/8, $0x2804020000010000
DATA validEscHi<>+8(SB)/8, $0x0000000000000000
GLOBL validEscHi<>(SB), RODATA|NOPTR, $16

// VPACK2 packs two 4-vector comparison sets (A0..A3 and B0..B3) into
// A0.D[0] (mask A) and A0.D[1] (mask B) using bit weights in V21.
#define VPACK2(A0, A1, A2, A3, B0, B1, B2, B3) \
    VAND V21.B16, A0.B16, A0.B16; \
    VAND V21.B16, A1.B16, A1.B16; \
    VAND V21.B16, A2.B16, A2.B16; \
    VAND V21.B16, A3.B16, A3.B16; \
    VADDP A1.B16, A0.B16, A0.B16; \
    VADDP A3.B16, A2.B16, A2.B16; \
    VADDP A2.B16, A0.B16, A0.B16; \
    VAND V21.B16, B0.B16, B0.B16; \
    VAND V21.B16, B1.B16, B1.B16; \
    VAND V21.B16, B2.B16, B2.B16; \
    VAND V21.B16, B3.B16, B3.B16; \
    VADDP B1.B16, B0.B16, B0.B16; \
    VADDP B3.B16, B2.B16, B2.B16; \
    VADDP B2.B16, B0.B16, B0.B16; \
    VADDP B0.B16, A0.B16, A0.B16

// VPACK1 packs one 4-vector comparison set (A0..A3) into A0.D[0].
#define VPACK1(A0, A1, A2, A3) \
    VAND V21.B16, A0.B16, A0.B16; \
    VAND V21.B16, A1.B16, A1.B16; \
    VAND V21.B16, A2.B16, A2.B16; \
    VAND V21.B16, A3.B16, A3.B16; \
    VADDP A1.B16, A0.B16, A0.B16; \
    VADDP A3.B16, A2.B16, A2.B16; \
    VADDP A2.B16, A0.B16, A0.B16; \
    VADDP A0.B16, A0.B16, A0.B16

// VALID_SETUP loads the constants of validMasksNEON and validMasksHiNEON.
#define VALID_SETUP \
    VMOVI $0x22, V16.B16; \
    VMOVI $0x5c, V17.B16; \
    VMOVI $0x1f, V18.B16; \
    VMOVI $0x20, V19.B16; \
    VMOVI $0x0f, V20.B16; \
    MOVD $validBitMask<>(SB), R3; \
    VLD1 (R3), [V21.B16]; \
    MOVD $validWS<>(SB), R3; \
    VLD1 (R3), [V22.B16]; \
    MOVD $validOp<>(SB), R3; \
    VLD1 (R3), [V23.B16]; \
    MOVD $validEscLo<>(SB), R3; \
    VLD1 (R3), [V24.B16]; \
    MOVD $validEscHi<>(SB), R3; \
    VLD1 (R3), [V25.B16]

// VALID_BODY computes the vmask of the 64 bytes in V0..V3 into *R2.
#define VALID_BODY(next_label) \
    VCMEQ V16.B16, V0.B16, V4.B16; \
    VCMEQ V16.B16, V1.B16, V5.B16; \
    VCMEQ V16.B16, V2.B16, V6.B16; \
    VCMEQ V16.B16, V3.B16, V7.B16; \
    VCMEQ V17.B16, V0.B16, V8.B16; \
    VCMEQ V17.B16, V1.B16, V9.B16; \
    VCMEQ V17.B16, V2.B16, V10.B16; \
    VCMEQ V17.B16, V3.B16, V11.B16; \
    VPACK2(V4, V5, V6, V7, V8, V9, V10, V11); \
    VMOV V4.D[0], R4; \
    VMOV V4.D[1], R8; \
    MOVD R4, 0(R2); \
    MOVD R8, 8(R2); \
    EOR R4<<1, R4, R5; \
    EOR R5<<2, R5, R5; \
    EOR R5<<4, R5, R5; \
    EOR R5<<8, R5, R5; \
    EOR R5<<16, R5, R5; \
    EOR R5<<32, R5, R5; \
    MOVD R5, 56(R2); \
    VAND V20.B16, V0.B16, V4.B16; \
    VAND V20.B16, V1.B16, V5.B16; \
    VAND V20.B16, V2.B16, V6.B16; \
    VAND V20.B16, V3.B16, V7.B16; \
    VTBL V4.B16, [V22.B16], V4.B16; \
    VTBL V5.B16, [V22.B16], V5.B16; \
    VTBL V6.B16, [V22.B16], V6.B16; \
    VTBL V7.B16, [V22.B16], V7.B16; \
    VCMEQ V4.B16, V0.B16, V4.B16; \
    VCMEQ V5.B16, V1.B16, V5.B16; \
    VCMEQ V6.B16, V2.B16, V6.B16; \
    VCMEQ V7.B16, V3.B16, V7.B16; \
    VUMIN V18.B16, V0.B16, V8.B16; \
    VUMIN V18.B16, V1.B16, V9.B16; \
    VUMIN V18.B16, V2.B16, V10.B16; \
    VUMIN V18.B16, V3.B16, V11.B16; \
    VCMEQ V8.B16, V0.B16, V8.B16; \
    VCMEQ V9.B16, V1.B16, V9.B16; \
    VCMEQ V10.B16, V2.B16, V10.B16; \
    VCMEQ V11.B16, V3.B16, V11.B16; \
    VPACK2(V4, V5, V6, V7, V8, V9, V10, V11); \
    VMOV V4.D[0], R4; \
    VMOV V4.D[1], R6; \
    MOVD R4, 16(R2); \
    MOVD R6, 32(R2); \
    VORR V19.B16, V0.B16, V12.B16; \
    VORR V19.B16, V1.B16, V13.B16; \
    VORR V19.B16, V2.B16, V14.B16; \
    VORR V19.B16, V3.B16, V15.B16; \
    VAND V20.B16, V12.B16, V4.B16; \
    VAND V20.B16, V13.B16, V5.B16; \
    VAND V20.B16, V14.B16, V6.B16; \
    VAND V20.B16, V15.B16, V7.B16; \
    VTBL V4.B16, [V23.B16], V4.B16; \
    VTBL V5.B16, [V23.B16], V5.B16; \
    VTBL V6.B16, [V23.B16], V6.B16; \
    VTBL V7.B16, [V23.B16], V7.B16; \
    VCMEQ V4.B16, V12.B16, V4.B16; \
    VCMEQ V5.B16, V13.B16, V5.B16; \
    VCMEQ V6.B16, V14.B16, V6.B16; \
    VCMEQ V7.B16, V15.B16, V7.B16; \
    VPACK1(V4, V5, V6, V7); \
    VMOV V4.D[0], R4; \
    BIC R6, R4, R4; \
    MOVD R4, 24(R2); \
    MOVD $0, 40(R2); \
    MOVD $0, 48(R2); \
    CBZ R8, next_label; \
    VUSHR $4, V0.B16, V4.B16; \
    VUSHR $4, V1.B16, V5.B16; \
    VUSHR $4, V2.B16, V6.B16; \
    VUSHR $4, V3.B16, V7.B16; \
    VTBL V4.B16, [V25.B16], V4.B16; \
    VTBL V5.B16, [V25.B16], V5.B16; \
    VTBL V6.B16, [V25.B16], V6.B16; \
    VTBL V7.B16, [V25.B16], V7.B16; \
    VAND V20.B16, V0.B16, V8.B16; \
    VAND V20.B16, V1.B16, V9.B16; \
    VAND V20.B16, V2.B16, V10.B16; \
    VAND V20.B16, V3.B16, V11.B16; \
    VTBL V8.B16, [V24.B16], V8.B16; \
    VTBL V9.B16, [V24.B16], V9.B16; \
    VTBL V10.B16, [V24.B16], V10.B16; \
    VTBL V11.B16, [V24.B16], V11.B16; \
    VAND V8.B16, V4.B16, V4.B16; \
    VAND V9.B16, V5.B16, V5.B16; \
    VAND V10.B16, V6.B16, V6.B16; \
    VAND V11.B16, V7.B16, V7.B16; \
    VCMTST V20.B16, V4.B16, V8.B16; \
    VCMTST V20.B16, V5.B16, V9.B16; \
    VCMTST V20.B16, V6.B16, V10.B16; \
    VCMTST V20.B16, V7.B16, V11.B16; \
    VCMTST V19.B16, V4.B16, V4.B16; \
    VCMTST V19.B16, V5.B16, V5.B16; \
    VCMTST V19.B16, V6.B16, V6.B16; \
    VCMTST V19.B16, V7.B16, V7.B16; \
    VPACK2(V8, V9, V10, V11, V4, V5, V6, V7); \
    VMOV V8.D[0], R4; \
    VMOV V8.D[1], R5; \
    MOVD R4, 40(R2); \
    MOVD R5, 48(R2)

// func validMasksNEON(p *byte, nb int, out *vmask)
//
// Fills out[0:nb] with the vmask of the 64-byte blocks at p.
TEXT ·validMasksNEON(SB), NOSPLIT, $0-24
    MOVD p+0(FP), R0
    MOVD nb+8(FP), R1
    MOVD out+16(FP), R2
    VALID_SETUP

valid_block:
    CBZ R1, valid_done
    VLD1.P 64(R0), [V0.B16, V1.B16, V2.B16, V3.B16]
    VALID_BODY(valid_next)

valid_next:
    ADD $64, R2, R2
    SUB $1, R1, R1
    B valid_block

valid_done:
    RET

// func validMasksHiNEON(p *byte, nb int, out *vmask, hi *uint64)
//
// validMasksNEON that also fills hi[0:nb] with the masks of bytes >= 0x80.
TEXT ·validMasksHiNEON(SB), NOSPLIT, $0-32
    MOVD p+0(FP), R0
    MOVD nb+8(FP), R1
    MOVD out+16(FP), R2
    MOVD hi+24(FP), R9
    VALID_SETUP
    VMOVI $0x80, V26.B16

hi_block:
    CBZ R1, hi_done
    VLD1.P 64(R0), [V0.B16, V1.B16, V2.B16, V3.B16]
    VORR V1.B16, V0.B16, V4.B16
    VORR V3.B16, V2.B16, V5.B16
    VORR V5.B16, V4.B16, V4.B16
    VAND V26.B16, V4.B16, V4.B16
    VMOV V4.D[0], R4
    VMOV V4.D[1], R5
    ORR R5, R4, R4
    CBZ R4, hi_zero
    VCMTST V26.B16, V0.B16, V4.B16
    VCMTST V26.B16, V1.B16, V5.B16
    VCMTST V26.B16, V2.B16, V6.B16
    VCMTST V26.B16, V3.B16, V7.B16
    VPACK1(V4, V5, V6, V7)
    VMOV V4.D[0], R4

hi_zero:
    MOVD R4, (R9)
    ADD $8, R9, R9
    VALID_BODY(hi_next)

hi_next:
    ADD $64, R2, R2
    SUB $1, R1, R1
    B hi_block

hi_done:
    RET

// func countMasksNEON(p *byte, nb int, out *cmask)
//
// Fills out[0:nb] with the cmask of the 64-byte blocks at p.
TEXT ·countMasksNEON(SB), NOSPLIT, $0-24
    MOVD p+0(FP), R0
    MOVD nb+8(FP), R1
    MOVD out+16(FP), R2
    VMOVI $0x22, V16.B16
    VMOVI $0x5c, V17.B16
    VMOVI $0x20, V18.B16
    VMOVI $0x7b, V19.B16
    VMOVI $0x7d, V20.B16
    VMOVI $0x2c, V22.B16
    MOVD $validBitMask<>(SB), R3
    VLD1 (R3), [V21.B16]

count_masks_block:
    CBZ R1, count_masks_done
    VLD1.P 64(R0), [V0.B16, V1.B16, V2.B16, V3.B16]
    VCMEQ V16.B16, V0.B16, V4.B16
    VCMEQ V16.B16, V1.B16, V5.B16
    VCMEQ V16.B16, V2.B16, V6.B16
    VCMEQ V16.B16, V3.B16, V7.B16
    VCMEQ V17.B16, V0.B16, V8.B16
    VCMEQ V17.B16, V1.B16, V9.B16
    VCMEQ V17.B16, V2.B16, V10.B16
    VCMEQ V17.B16, V3.B16, V11.B16
    VPACK2(V4, V5, V6, V7, V8, V9, V10, V11)
    VMOV V4.D[0], R4        // quote
    VMOV V4.D[1], R5        // bslash
    MOVD R4, 0(R2)
    MOVD R5, 8(R2)
    EOR R4<<1, R4, R6
    EOR R6<<2, R6, R6
    EOR R6<<4, R6, R6
    EOR R6<<8, R6, R6
    EOR R6<<16, R6, R6
    EOR R6<<32, R6, R6
    MOVD R6, 40(R2)         // r0

    VCMEQ V22.B16, V0.B16, V4.B16
    VCMEQ V22.B16, V1.B16, V5.B16
    VCMEQ V22.B16, V2.B16, V6.B16
    VCMEQ V22.B16, V3.B16, V7.B16
    VPACK1(V4, V5, V6, V7)
    VMOV V4.D[0], R4
    MOVD R4, 32(R2)         // comma

    VORR V18.B16, V0.B16, V0.B16
    VORR V18.B16, V1.B16, V1.B16
    VORR V18.B16, V2.B16, V2.B16
    VORR V18.B16, V3.B16, V3.B16
    VCMEQ V19.B16, V0.B16, V4.B16
    VCMEQ V19.B16, V1.B16, V5.B16
    VCMEQ V19.B16, V2.B16, V6.B16
    VCMEQ V19.B16, V3.B16, V7.B16
    VCMEQ V20.B16, V0.B16, V8.B16
    VCMEQ V20.B16, V1.B16, V9.B16
    VCMEQ V20.B16, V2.B16, V10.B16
    VCMEQ V20.B16, V3.B16, V11.B16
    VPACK2(V4, V5, V6, V7, V8, V9, V10, V11)
    VMOV V4.D[0], R4        // open
    VMOV V4.D[1], R5        // close
    MOVD R4, 16(R2)
    MOVD R5, 24(R2)

    ADD $48, R2, R2
    SUB $1, R1, R1
    B count_masks_block

count_masks_done:
    RET

// STOP_CMGT marks in dst (0xff/0x00) quotes, control bytes, >= 0x80, and
// backslashes of src using V20=0x02, V21=0x21, V17=0x5c. Clobbers t.
#define STOP_CMGT(src, dst, t, cmgt_word) \
    VEOR V20.B16, src.B16, dst.B16; \
    WORD $cmgt_word; \
    VCMEQ V17.B16, src.B16, t.B16; \
    VORR t.B16, dst.B16, dst.B16

// STOP_CMHI marks in dst (0xff/0x00) quotes, control bytes, and
// backslashes of src (ignoring >= 0x80). Clobbers t.
#define STOP_CMHI(src, dst, t, cmhi_word) \
    VEOR V20.B16, src.B16, dst.B16; \
    WORD $cmhi_word; \
    VCMEQ V17.B16, src.B16, t.B16; \
    VORR t.B16, dst.B16, dst.B16

// HTML_NOE2 marks '<', '>', and '&' of src onto dst.
// HTML_ALL additionally marks 0xe2 of src onto dst.
// Uses V20=0x02, V23=0x3e, V24=0x26, V25=0xe2. Clobbers t1, t2.
#define HTML_NOE2(src, dst, t1, t2) \
    VORR V20.B16, src.B16, t1.B16; \
    VCMEQ V23.B16, t1.B16, t1.B16; \
    VCMEQ V24.B16, src.B16, t2.B16; \
    VORR t2.B16, t1.B16, t1.B16; \
    VORR t1.B16, dst.B16, dst.B16

#define HTML_ALL(src, dst, t1, t2) \
    VORR V20.B16, src.B16, t1.B16; \
    VCMEQ V23.B16, t1.B16, t1.B16; \
    VCMEQ V24.B16, src.B16, t2.B16; \
    VORR t2.B16, t1.B16, t1.B16; \
    VCMEQ V25.B16, src.B16, t2.B16; \
    VORR t2.B16, t1.B16, t1.B16; \
    VORR t1.B16, dst.B16, dst.B16

// VPACK1_26 packs V4..V7 into V4.D[0] using bit weights in V26.
#define VPACK1_26(A0, A1, A2, A3) \
    VAND V26.B16, A0.B16, A0.B16; \
    VAND V26.B16, A1.B16, A1.B16; \
    VAND V26.B16, A2.B16, A2.B16; \
    VAND V26.B16, A3.B16, A3.B16; \
    VADDP A1.B16, A0.B16, A0.B16; \
    VADDP A3.B16, A2.B16, A2.B16; \
    VADDP A2.B16, A0.B16, A0.B16; \
    VADDP A0.B16, A0.B16, A0.B16

// func strSkipNEON(p *byte, n, i int) (int, bool)
TEXT ·strSkipNEON(SB), NOSPLIT, $0-33
    MOVD p+0(FP), R0
    MOVD n+8(FP), R1
    MOVD i+16(FP), R2
    VMOVI $0x5c, V17.B16
    VMOVI $0x80, V19.B16
    VMOVI $0x02, V20.B16
    VMOVI $0x21, V21.B16
    VEOR V8.B16, V8.B16, V8.B16

skip_next:
    ADD $128, R2, R3
    CMP R1, R3
    BHI skip_block
    VAND V19.B16, V8.B16, V4.B16
    WORD $0x6e30a884            // UMAXV B4, V4.16B
    VMOV V4.B[0], R4
    CBNZ R4, skip_wideh

skip_wide:
    ADD R2, R0, R3
    VLD1.P 64(R3), [V0.B16, V1.B16, V2.B16, V3.B16]
    VLD1 (R3), [V9.B16, V10.B16, V11.B16, V12.B16]
    STOP_CMGT(V0, V4, V23, 0x4e2436a4)
    STOP_CMGT(V1, V5, V23, 0x4e2536a5)
    STOP_CMGT(V2, V6, V23, 0x4e2636a6)
    STOP_CMGT(V3, V7, V23, 0x4e2736a7)
    STOP_CMGT(V9, V13, V23, 0x4e2d36ad)
    STOP_CMGT(V10, V14, V23, 0x4e2e36ae)
    STOP_CMGT(V11, V15, V23, 0x4e2f36af)
    STOP_CMGT(V12, V22, V23, 0x4e3636b6)
    VORR V5.B16, V4.B16, V4.B16
    VORR V7.B16, V6.B16, V6.B16
    VORR V14.B16, V13.B16, V13.B16
    VORR V22.B16, V15.B16, V15.B16
    VORR V6.B16, V4.B16, V4.B16
    VORR V15.B16, V13.B16, V13.B16
    VORR V13.B16, V4.B16, V4.B16
    WORD $0x6e30a884            // UMAXV B4, V4.16B
    VMOV V4.B[0], R4
    CBNZ R4, skip_block
    ADD $128, R2, R2
    ADD $128, R2, R3
    CMP R1, R3
    BLS skip_wide
    B skip_block

skip_wideh:
    ADD R2, R0, R3
    VLD1.P 64(R3), [V0.B16, V1.B16, V2.B16, V3.B16]
    VLD1 (R3), [V9.B16, V10.B16, V11.B16, V12.B16]
    STOP_CMHI(V0, V4, V23, 0x6e2436a4)
    STOP_CMHI(V1, V5, V23, 0x6e2536a5)
    STOP_CMHI(V2, V6, V23, 0x6e2636a6)
    STOP_CMHI(V3, V7, V23, 0x6e2736a7)
    STOP_CMHI(V9, V13, V23, 0x6e2d36ad)
    STOP_CMHI(V10, V14, V23, 0x6e2e36ae)
    STOP_CMHI(V11, V15, V23, 0x6e2f36af)
    STOP_CMHI(V12, V22, V23, 0x6e3636b6)
    VORR V5.B16, V4.B16, V4.B16
    VORR V7.B16, V6.B16, V6.B16
    VORR V14.B16, V13.B16, V13.B16
    VORR V22.B16, V15.B16, V15.B16
    VORR V6.B16, V4.B16, V4.B16
    VORR V15.B16, V13.B16, V13.B16
    VORR V13.B16, V4.B16, V4.B16
    WORD $0x6e30a884            // UMAXV B4, V4.16B
    VMOV V4.B[0], R4
    CBNZ R4, skip_block
    ADD $128, R2, R2
    ADD $128, R2, R3
    CMP R1, R3
    BLS skip_wideh

skip_block:
    ADD $64, R2, R3
    CMP R1, R3
    BHI skip_done
    ADD R2, R0, R3
    VLD1 (R3), [V0.B16, V1.B16, V2.B16, V3.B16]
    STOP_CMHI(V0, V4, V23, 0x6e2436a4)
    STOP_CMHI(V1, V5, V23, 0x6e2536a5)
    STOP_CMHI(V2, V6, V23, 0x6e2636a6)
    STOP_CMHI(V3, V7, V23, 0x6e2736a7)
    VORR V5.B16, V4.B16, V4.B16
    VORR V7.B16, V6.B16, V6.B16
    VORR V6.B16, V4.B16, V4.B16
    WORD $0x6e30a884            // UMAXV B4, V4.16B
    VMOV V4.B[0], R4
    CBNZ R4, skip_done
    VORR V1.B16, V0.B16, V4.B16
    VORR V3.B16, V2.B16, V5.B16
    VORR V5.B16, V4.B16, V4.B16
    VORR V4.B16, V8.B16, V8.B16
    ADD $64, R2, R2
    B skip_next

skip_done:
    VAND V19.B16, V8.B16, V4.B16
    WORD $0x6e30a884            // UMAXV B4, V4.16B
    VMOV V4.B[0], R4
    CMP $0, R4
    CSET NE, R4
    MOVB R4, ret1+32(FP)
    MOVD R2, ret+24(FP)
    RET

// func strSkipHTMLNEON(p *byte, n, i int) (int, bool)
TEXT ·strSkipHTMLNEON(SB), NOSPLIT, $0-33
    MOVD p+0(FP), R0
    MOVD n+8(FP), R1
    MOVD i+16(FP), R2
    VMOVI $0x5c, V17.B16
    VMOVI $0x80, V19.B16
    VMOVI $0x02, V20.B16
    VMOVI $0x21, V21.B16
    VMOVI $0x3e, V23.B16
    VMOVI $0x26, V24.B16
    VMOVI $0xe2, V25.B16
    MOVD $0, R8                 // html candidate seen
    MOVD $0, R9                 // >= 0x80 seen

hskip_next:
    ADD $128, R2, R3
    CMP R1, R3
    BHI hskip_block
    CBNZ R8, hskip_widec
    CBNZ R9, hskip_wideb

hskip_widea:
    ADD R2, R0, R3
    VLD1.P 64(R3), [V0.B16, V1.B16, V2.B16, V3.B16]
    VLD1 (R3), [V9.B16, V10.B16, V11.B16, V12.B16]
    STOP_CMGT(V0, V4, V26, 0x4e2436a4)
    HTML_NOE2(V0, V4, V26, V27)
    STOP_CMGT(V1, V5, V26, 0x4e2536a5)
    HTML_NOE2(V1, V5, V26, V27)
    STOP_CMGT(V2, V6, V26, 0x4e2636a6)
    HTML_NOE2(V2, V6, V26, V27)
    STOP_CMGT(V3, V7, V26, 0x4e2736a7)
    HTML_NOE2(V3, V7, V26, V27)
    STOP_CMGT(V9, V13, V26, 0x4e2d36ad)
    HTML_NOE2(V9, V13, V26, V27)
    STOP_CMGT(V10, V14, V26, 0x4e2e36ae)
    HTML_NOE2(V10, V14, V26, V27)
    STOP_CMGT(V11, V15, V26, 0x4e2f36af)
    HTML_NOE2(V11, V15, V26, V27)
    STOP_CMGT(V12, V22, V26, 0x4e3636b6)
    HTML_NOE2(V12, V22, V26, V27)
    VORR V5.B16, V4.B16, V4.B16
    VORR V7.B16, V6.B16, V6.B16
    VORR V14.B16, V13.B16, V13.B16
    VORR V22.B16, V15.B16, V15.B16
    VORR V6.B16, V4.B16, V4.B16
    VORR V15.B16, V13.B16, V13.B16
    VORR V13.B16, V4.B16, V4.B16
    WORD $0x6e30a884            // UMAXV B4, V4.16B
    VMOV V4.B[0], R4
    CBNZ R4, hskip_block
    ADD $128, R2, R2
    ADD $128, R2, R3
    CMP R1, R3
    BLS hskip_widea
    B hskip_block

hskip_wideb:
    ADD R2, R0, R3
    VLD1.P 64(R3), [V0.B16, V1.B16, V2.B16, V3.B16]
    VLD1 (R3), [V9.B16, V10.B16, V11.B16, V12.B16]
    STOP_CMHI(V0, V4, V26, 0x6e2436a4)
    HTML_ALL(V0, V4, V26, V27)
    STOP_CMHI(V1, V5, V26, 0x6e2536a5)
    HTML_ALL(V1, V5, V26, V27)
    STOP_CMHI(V2, V6, V26, 0x6e2636a6)
    HTML_ALL(V2, V6, V26, V27)
    STOP_CMHI(V3, V7, V26, 0x6e2736a7)
    HTML_ALL(V3, V7, V26, V27)
    STOP_CMHI(V9, V13, V26, 0x6e2d36ad)
    HTML_ALL(V9, V13, V26, V27)
    STOP_CMHI(V10, V14, V26, 0x6e2e36ae)
    HTML_ALL(V10, V14, V26, V27)
    STOP_CMHI(V11, V15, V26, 0x6e2f36af)
    HTML_ALL(V11, V15, V26, V27)
    STOP_CMHI(V12, V22, V26, 0x6e3636b6)
    HTML_ALL(V12, V22, V26, V27)
    VORR V5.B16, V4.B16, V4.B16
    VORR V7.B16, V6.B16, V6.B16
    VORR V14.B16, V13.B16, V13.B16
    VORR V22.B16, V15.B16, V15.B16
    VORR V6.B16, V4.B16, V4.B16
    VORR V15.B16, V13.B16, V13.B16
    VORR V13.B16, V4.B16, V4.B16
    WORD $0x6e30a884            // UMAXV B4, V4.16B
    VMOV V4.B[0], R4
    CBNZ R4, hskip_block
    ADD $128, R2, R2
    ADD $128, R2, R3
    CMP R1, R3
    BLS hskip_wideb
    B hskip_block

hskip_widec:
    ADD R2, R0, R3
    VLD1.P 64(R3), [V0.B16, V1.B16, V2.B16, V3.B16]
    VLD1 (R3), [V9.B16, V10.B16, V11.B16, V12.B16]
    STOP_CMHI(V0, V4, V26, 0x6e2436a4)
    STOP_CMHI(V1, V5, V26, 0x6e2536a5)
    STOP_CMHI(V2, V6, V26, 0x6e2636a6)
    STOP_CMHI(V3, V7, V26, 0x6e2736a7)
    STOP_CMHI(V9, V13, V26, 0x6e2d36ad)
    STOP_CMHI(V10, V14, V26, 0x6e2e36ae)
    STOP_CMHI(V11, V15, V26, 0x6e2f36af)
    STOP_CMHI(V12, V22, V26, 0x6e3636b6)
    VORR V5.B16, V4.B16, V4.B16
    VORR V7.B16, V6.B16, V6.B16
    VORR V14.B16, V13.B16, V13.B16
    VORR V22.B16, V15.B16, V15.B16
    VORR V6.B16, V4.B16, V4.B16
    VORR V15.B16, V13.B16, V13.B16
    VORR V13.B16, V4.B16, V4.B16
    WORD $0x6e30a884            // UMAXV B4, V4.16B
    VMOV V4.B[0], R4
    CBNZ R4, hskip_block
    ADD $128, R2, R2
    ADD $128, R2, R3
    CMP R1, R3
    BLS hskip_widec

hskip_block:
    ADD $64, R2, R3
    CMP R1, R3
    BHI hskip_done
    ADD R2, R0, R3
    VLD1 (R3), [V0.B16, V1.B16, V2.B16, V3.B16]
    STOP_CMHI(V0, V4, V26, 0x6e2436a4)
    STOP_CMHI(V1, V5, V26, 0x6e2536a5)
    STOP_CMHI(V2, V6, V26, 0x6e2636a6)
    STOP_CMHI(V3, V7, V26, 0x6e2736a7)
    VORR V5.B16, V4.B16, V4.B16
    VORR V7.B16, V6.B16, V6.B16
    VORR V6.B16, V4.B16, V4.B16
    WORD $0x6e30a884            // UMAXV B4, V4.16B
    VMOV V4.B[0], R4
    CBNZ R4, hskip_done
    CBNZ R8, hskip_advance
    VEOR V4.B16, V4.B16, V4.B16
    HTML_ALL(V0, V4, V26, V27)
    HTML_ALL(V1, V4, V26, V27)
    HTML_ALL(V2, V4, V26, V27)
    HTML_ALL(V3, V4, V26, V27)
    WORD $0x6e30a884            // UMAXV B4, V4.16B
    VMOV V4.B[0], R8
    CBNZ R9, hskip_advance
    VORR V1.B16, V0.B16, V4.B16
    VORR V3.B16, V2.B16, V5.B16
    VORR V5.B16, V4.B16, V4.B16
    VAND V19.B16, V4.B16, V4.B16
    WORD $0x6e30a884            // UMAXV B4, V4.16B
    VMOV V4.B[0], R9

hskip_advance:
    ADD $64, R2, R2
    B hskip_next

hskip_done:
    CMP $0, R8
    CSET NE, R8
    MOVB R8, ret1+32(FP)
    MOVD R2, ret+24(FP)
    RET

// func htmlSkipNEON(p *byte, n, i int) int
TEXT ·htmlSkipNEON(SB), NOSPLIT, $0-32
    MOVD p+0(FP), R0
    MOVD n+8(FP), R1
    MOVD i+16(FP), R2
    VMOVI $0x02, V20.B16
    VMOVI $0x3e, V23.B16
    VMOVI $0x26, V24.B16
    VMOVI $0xe2, V25.B16

html_block:
    ADD $64, R2, R3
    CMP R1, R3
    BHI html_done
    ADD R2, R0, R3
    VLD1 (R3), [V0.B16, V1.B16, V2.B16, V3.B16]
    VEOR V4.B16, V4.B16, V4.B16
    HTML_ALL(V0, V4, V26, V27)
    HTML_ALL(V1, V4, V26, V27)
    HTML_ALL(V2, V4, V26, V27)
    HTML_ALL(V3, V4, V26, V27)
    WORD $0x6e30a884            // UMAXV B4, V4.16B
    VMOV V4.B[0], R4
    CBNZ R4, html_done
    ADD $64, R2, R2
    B html_block

html_done:
    MOVD R2, ret+24(FP)
    RET

// func strEndNEON(p *byte, n, i int) (int, bool)
TEXT ·strEndNEON(SB), NOSPLIT, $0-33
    MOVD p+0(FP), R0
    MOVD n+8(FP), R1
    MOVD i+16(FP), R2
    VMOVI $0x5c, V17.B16
    VMOVI $0x80, V19.B16
    VMOVI $0x02, V20.B16
    VMOVI $0x21, V21.B16
    MOVD $validBitMask<>(SB), R3
    VLD1 (R3), [V26.B16]
    VEOR V8.B16, V8.B16, V8.B16
    MOVD $0, R8

    // The first 64 bytes, where most strings end.
    ADD $64, R2, R3
    CMP R1, R3
    BHI end_half
    ADD R2, R0, R3
    VLD1 (R3), [V0.B16, V1.B16, V2.B16, V3.B16]
    STOP_CMHI(V0, V4, V23, 0x6e2436a4)
    STOP_CMHI(V1, V5, V23, 0x6e2536a5)
    STOP_CMHI(V2, V6, V23, 0x6e2636a6)
    STOP_CMHI(V3, V7, V23, 0x6e2736a7)
    VORR V5.B16, V4.B16, V23.B16
    VORR V7.B16, V6.B16, V24.B16
    VORR V24.B16, V23.B16, V23.B16
    VMOV V23.D[0], R4
    VMOV V23.D[1], R5
    ORR R5, R4, R4
    CBNZ R4, end_found
    VORR V1.B16, V0.B16, V23.B16
    VORR V3.B16, V2.B16, V24.B16
    VORR V24.B16, V23.B16, V23.B16
    VORR V23.B16, V8.B16, V8.B16
    ADD $64, R2, R2

end_next:
    ADD $128, R2, R3
    CMP R1, R3
    BHI end_block
    VAND V19.B16, V8.B16, V4.B16
    WORD $0x6e30a884            // UMAXV B4, V4.16B
    VMOV V4.B[0], R4
    CBNZ R4, end_wideh

end_wide:
    ADD R2, R0, R3
    VLD1.P 64(R3), [V0.B16, V1.B16, V2.B16, V3.B16]
    VLD1 (R3), [V9.B16, V10.B16, V11.B16, V12.B16]
    STOP_CMGT(V0, V4, V23, 0x4e2436a4)
    STOP_CMGT(V1, V5, V23, 0x4e2536a5)
    STOP_CMGT(V2, V6, V23, 0x4e2636a6)
    STOP_CMGT(V3, V7, V23, 0x4e2736a7)
    STOP_CMGT(V9, V13, V23, 0x4e2d36ad)
    STOP_CMGT(V10, V14, V23, 0x4e2e36ae)
    STOP_CMGT(V11, V15, V23, 0x4e2f36af)
    STOP_CMGT(V12, V22, V23, 0x4e3636b6)
    VORR V5.B16, V4.B16, V4.B16
    VORR V7.B16, V6.B16, V6.B16
    VORR V14.B16, V13.B16, V13.B16
    VORR V22.B16, V15.B16, V15.B16
    VORR V6.B16, V4.B16, V4.B16
    VORR V15.B16, V13.B16, V13.B16
    VORR V13.B16, V4.B16, V4.B16
    WORD $0x6e30a884            // UMAXV B4, V4.16B
    VMOV V4.B[0], R4
    CBNZ R4, end_block
    ADD $128, R2, R2
    ADD $128, R2, R3
    CMP R1, R3
    BLS end_wide
    B end_block

end_wideh:
    ADD R2, R0, R3
    VLD1.P 64(R3), [V0.B16, V1.B16, V2.B16, V3.B16]
    VLD1 (R3), [V9.B16, V10.B16, V11.B16, V12.B16]
    STOP_CMHI(V0, V4, V23, 0x6e2436a4)
    STOP_CMHI(V1, V5, V23, 0x6e2536a5)
    STOP_CMHI(V2, V6, V23, 0x6e2636a6)
    STOP_CMHI(V3, V7, V23, 0x6e2736a7)
    STOP_CMHI(V9, V13, V23, 0x6e2d36ad)
    STOP_CMHI(V10, V14, V23, 0x6e2e36ae)
    STOP_CMHI(V11, V15, V23, 0x6e2f36af)
    STOP_CMHI(V12, V22, V23, 0x6e3636b6)
    VORR V5.B16, V4.B16, V4.B16
    VORR V7.B16, V6.B16, V6.B16
    VORR V14.B16, V13.B16, V13.B16
    VORR V22.B16, V15.B16, V15.B16
    VORR V6.B16, V4.B16, V4.B16
    VORR V15.B16, V13.B16, V13.B16
    VORR V13.B16, V4.B16, V4.B16
    WORD $0x6e30a884            // UMAXV B4, V4.16B
    VMOV V4.B[0], R4
    CBNZ R4, end_block
    ADD $128, R2, R2
    ADD $128, R2, R3
    CMP R1, R3
    BLS end_wideh

end_block:
    ADD $64, R2, R3
    CMP R1, R3
    BHI end_half
    ADD R2, R0, R3
    VLD1 (R3), [V0.B16, V1.B16, V2.B16, V3.B16]
    STOP_CMHI(V0, V4, V23, 0x6e2436a4)
    STOP_CMHI(V1, V5, V23, 0x6e2536a5)
    STOP_CMHI(V2, V6, V23, 0x6e2636a6)
    STOP_CMHI(V3, V7, V23, 0x6e2736a7)
    VORR V5.B16, V4.B16, V23.B16
    VORR V7.B16, V6.B16, V24.B16
    VORR V24.B16, V23.B16, V23.B16
    VMOV V23.D[0], R4
    VMOV V23.D[1], R5
    ORR R5, R4, R4
    CBNZ R4, end_found
    VORR V1.B16, V0.B16, V23.B16
    VORR V3.B16, V2.B16, V24.B16
    VORR V24.B16, V23.B16, V23.B16
    VORR V23.B16, V8.B16, V8.B16
    ADD $64, R2, R2
    B end_next

end_found:
    VPACK1_26(V4, V5, V6, V7)
    VMOV V4.D[0], R4
    RBIT R4, R5
    CLZ R5, R5
    ADD R5, R2, R2
    VORR V1.B16, V0.B16, V23.B16
    VORR V3.B16, V2.B16, V24.B16
    VORR V24.B16, V23.B16, V23.B16
    VAND V19.B16, V23.B16, V23.B16
    VMOV V23.D[0], R6
    VMOV V23.D[1], R7
    ORR R7, R6, R6
    CBZ R6, end_done
    VCMTST V19.B16, V0.B16, V0.B16
    VCMTST V19.B16, V1.B16, V1.B16
    VCMTST V19.B16, V2.B16, V2.B16
    VCMTST V19.B16, V3.B16, V3.B16
    VPACK1_26(V0, V1, V2, V3)
    VMOV V0.D[0], R6
    MOVD $1, R7
    LSL R5, R7, R7
    SUB $1, R7, R7
    AND R7, R6, R6
    ORR R6, R8, R8
    B end_done

end_half:
    ADD $32, R2, R3
    CMP R1, R3
    BHI end_done
    ADD R2, R0, R3
    VLD1 (R3), [V0.B16, V1.B16]
    STOP_CMHI(V0, V4, V23, 0x6e2436a4)
    STOP_CMHI(V1, V5, V23, 0x6e2536a5)
    VORR V5.B16, V4.B16, V23.B16
    VMOV V23.D[0], R4
    VMOV V23.D[1], R5
    ORR R5, R4, R4
    CBNZ R4, end_found32
    VORR V1.B16, V0.B16, V23.B16
    VORR V23.B16, V8.B16, V8.B16
    ADD $32, R2, R2
    B end_done

end_found32:
    VPACK1_26(V4, V5, V6, V7)
    VMOV V4.S[0], R4
    RBIT R4, R5
    CLZ R5, R5
    ADD R5, R2, R2
    VORR V1.B16, V0.B16, V23.B16
    VAND V19.B16, V23.B16, V23.B16
    VMOV V23.D[0], R6
    VMOV V23.D[1], R7
    ORR R7, R6, R6
    CBZ R6, end_done
    VCMTST V19.B16, V0.B16, V0.B16
    VCMTST V19.B16, V1.B16, V1.B16
    VPACK1_26(V0, V1, V2, V3)
    VMOV V0.S[0], R6
    MOVD $1, R7
    LSL R5, R7, R7
    SUB $1, R7, R7
    AND R7, R6, R6
    ORR R6, R8, R8

end_done:
    VAND V19.B16, V8.B16, V8.B16
    WORD $0x6e30a908            // UMAXV B8, V8.16B
    VMOV V8.B[0], R4
    ORR R4, R8, R8
    CMP $0, R8
    CSET NE, R8
    MOVB R8, ret1+32(FP)
    MOVD R2, ret+24(FP)
    RET
