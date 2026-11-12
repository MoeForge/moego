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

// func validMasksNEON(p *byte, nb int, out *vmask)
//
// Fills out[0:nb] with the vmask of the 64-byte blocks at p.
TEXT ·validMasksNEON(SB), NOSPLIT, $0-24
    MOVD p+0(FP), R0
    MOVD nb+8(FP), R1
    MOVD out+16(FP), R2
    VMOVI $0x22, V16.B16
    VMOVI $0x5c, V17.B16
    VMOVI $0x1f, V18.B16
    VMOVI $0x20, V19.B16
    VMOVI $0x0f, V20.B16
    MOVD $validBitMask<>(SB), R3
    VLD1 (R3), [V21.B16]
    MOVD $validWS<>(SB), R3
    VLD1 (R3), [V22.B16]
    MOVD $validOp<>(SB), R3
    VLD1 (R3), [V23.B16]
    MOVD $validEscLo<>(SB), R3
    VLD1 (R3), [V24.B16]
    MOVD $validEscHi<>(SB), R3
    VLD1 (R3), [V25.B16]

valid_block:
    CBZ R1, valid_done
    VLD1.P 64(R0), [V0.B16, V1.B16, V2.B16, V3.B16]

    // quote (0x22) and bslash (0x5c).
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
    VMOV V4.D[1], R8        // bslash
    MOVD R4, 0(R2)
    MOVD R8, 8(R2)
    EOR R4<<1, R4, R5
    EOR R5<<2, R5, R5
    EOR R5<<4, R5, R5
    EOR R5<<8, R5, R5
    EOR R5<<16, R5, R5
    EOR R5<<32, R5, R5
    MOVD R5, 56(R2)         // r0

    // ws and ctrl (<= 0x1f unsigned).
    VAND V20.B16, V0.B16, V4.B16
    VAND V20.B16, V1.B16, V5.B16
    VAND V20.B16, V2.B16, V6.B16
    VAND V20.B16, V3.B16, V7.B16
    VTBL V4.B16, [V22.B16], V4.B16
    VTBL V5.B16, [V22.B16], V5.B16
    VTBL V6.B16, [V22.B16], V6.B16
    VTBL V7.B16, [V22.B16], V7.B16
    VCMEQ V4.B16, V0.B16, V4.B16
    VCMEQ V5.B16, V1.B16, V5.B16
    VCMEQ V6.B16, V2.B16, V6.B16
    VCMEQ V7.B16, V3.B16, V7.B16
    VUMIN V18.B16, V0.B16, V8.B16
    VUMIN V18.B16, V1.B16, V9.B16
    VUMIN V18.B16, V2.B16, V10.B16
    VUMIN V18.B16, V3.B16, V11.B16
    VCMEQ V8.B16, V0.B16, V8.B16
    VCMEQ V9.B16, V1.B16, V9.B16
    VCMEQ V10.B16, V2.B16, V10.B16
    VCMEQ V11.B16, V3.B16, V11.B16
    VPACK2(V4, V5, V6, V7, V8, V9, V10, V11)
    VMOV V4.D[0], R4        // ws
    VMOV V4.D[1], R6        // ctrl
    MOVD R4, 16(R2)
    MOVD R6, 32(R2)

    // op: '{', '}', '[', ']', ':' and ','.
    VORR V19.B16, V0.B16, V12.B16
    VORR V19.B16, V1.B16, V13.B16
    VORR V19.B16, V2.B16, V14.B16
    VORR V19.B16, V3.B16, V15.B16
    VAND V20.B16, V12.B16, V4.B16
    VAND V20.B16, V13.B16, V5.B16
    VAND V20.B16, V14.B16, V6.B16
    VAND V20.B16, V15.B16, V7.B16
    VTBL V4.B16, [V23.B16], V4.B16
    VTBL V5.B16, [V23.B16], V5.B16
    VTBL V6.B16, [V23.B16], V6.B16
    VTBL V7.B16, [V23.B16], V7.B16
    VCMEQ V4.B16, V12.B16, V4.B16
    VCMEQ V5.B16, V13.B16, V5.B16
    VCMEQ V6.B16, V14.B16, V6.B16
    VCMEQ V7.B16, V15.B16, V7.B16
    VPACK1(V4, V5, V6, V7)
    VMOV V4.D[0], R4
    BIC R6, R4, R4          // control bytes fold onto ':' and ','
    MOVD R4, 24(R2)         // op
    MOVD $0, 40(R2)
    MOVD $0, 48(R2)
    CBZ R8, valid_next

    // Escapes: classify with validEscLo and validEscHi nibble tables.
    // Bits 0..3 mark '"'/'/'/'\\'/'b'/'f'/'n'/'r'/'t' (mask 0x0f in V20),
    // and bit 5 marks 'u' (mask 0x20 in V19).
    VUSHR $4, V0.B16, V4.B16
    VUSHR $4, V1.B16, V5.B16
    VUSHR $4, V2.B16, V6.B16
    VUSHR $4, V3.B16, V7.B16
    VTBL V4.B16, [V25.B16], V4.B16
    VTBL V5.B16, [V25.B16], V5.B16
    VTBL V6.B16, [V25.B16], V6.B16
    VTBL V7.B16, [V25.B16], V7.B16
    VAND V20.B16, V0.B16, V8.B16
    VAND V20.B16, V1.B16, V9.B16
    VAND V20.B16, V2.B16, V10.B16
    VAND V20.B16, V3.B16, V11.B16
    VTBL V8.B16, [V24.B16], V8.B16
    VTBL V9.B16, [V24.B16], V9.B16
    VTBL V10.B16, [V24.B16], V10.B16
    VTBL V11.B16, [V24.B16], V11.B16
    VAND V8.B16, V4.B16, V4.B16
    VAND V9.B16, V5.B16, V5.B16
    VAND V10.B16, V6.B16, V6.B16
    VAND V11.B16, V7.B16, V7.B16
    VCMTST V20.B16, V4.B16, V8.B16
    VCMTST V20.B16, V5.B16, V9.B16
    VCMTST V20.B16, V6.B16, V10.B16
    VCMTST V20.B16, V7.B16, V11.B16
    VCMTST V19.B16, V4.B16, V4.B16
    VCMTST V19.B16, V5.B16, V5.B16
    VCMTST V19.B16, V6.B16, V6.B16
    VCMTST V19.B16, V7.B16, V7.B16
    VPACK2(V8, V9, V10, V11, V4, V5, V6, V7)
    VMOV V8.D[0], R4        // esc
    VMOV V8.D[1], R5        // u
    MOVD R4, 40(R2)
    MOVD R5, 48(R2)

valid_next:
    ADD $64, R2, R2
    SUB $1, R1, R1
    B valid_block

valid_done:
    RET
