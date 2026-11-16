//go:build arm64 && !purego

#include "textflag.h"

DATA escBitMask<>+0(SB)/8, $0x8040201008040201
DATA escBitMask<>+8(SB)/8, $0x8040201008040201
GLOBL escBitMask<>(SB), RODATA|NOPTR, $16

// escLetters holds, at the low four bits of each control byte that has an
// escape of a backslash and a letter, that letter.
DATA escLetters<>+0x00(SB)/8, $0
DATA escLetters<>+0x08(SB)/8, $0x00007266006e7462
GLOBL escLetters<>(SB), RODATA|NOPTR, $16

// unescLo and unescHi classify valid 2-byte escape second bytes by nibble.
DATA unescLo<>+0x00(SB)/8, $0x00040008000d0000
DATA unescLo<>+0x08(SB)/8, $0x0104000200000000
GLOBL unescLo<>(SB), RODATA|NOPTR, $16

DATA unescHi<>+0x00(SB)/8, $0x0804020000010000
DATA unescHi<>+0x08(SB)/8, $0
GLOBL unescHi<>(SB), RODATA|NOPTR, $16

// Control bytes for letters 'b','f','n' (high nibble 6) and 'r','t' (high nibble 7).
DATA unescBFN<>+0x00(SB)/8, $0x000c000000080000
DATA unescBFN<>+0x08(SB)/8, $0x000a000000000000
GLOBL unescBFN<>(SB), RODATA|NOPTR, $16

DATA unescRT<>+0x00(SB)/8, $0x00000009000d0000
DATA unescRT<>+0x08(SB)/8, $0
GLOBL unescRT<>(SB), RODATA|NOPTR, $16

// UTF-8 validation tables.
DATA utf8Hi1<>+0x00(SB)/8, $0x0202020202020202
DATA utf8Hi1<>+0x08(SB)/8, $0x4915012180808080
GLOBL utf8Hi1<>(SB), RODATA|NOPTR, $16

DATA utf8Lo1<>+0x00(SB)/8, $0xcbcbcb8b8383a3e7
DATA utf8Lo1<>+0x08(SB)/8, $0xcbcbdbcbcbcbcbcb
GLOBL utf8Lo1<>(SB), RODATA|NOPTR, $16

DATA utf8Hi2<>+0x00(SB)/8, $0x0101010101010101
DATA utf8Hi2<>+0x08(SB)/8, $0x01010101babaaee6
GLOBL utf8Hi2<>(SB), RODATA|NOPTR, $16

DATA utf8Max16<>+0x00(SB)/8, $0xffffffffffffffff
DATA utf8Max16<>+0x08(SB)/8, $0xbfdfefffffffffff
GLOBL utf8Max16<>(SB), RODATA|NOPTR, $16

// Tables for escCountNEON.
DATA escCountHi<>+0x00(SB)/8, $0x0000400020180403
DATA escCountHi<>+0x08(SB)/8, $0
GLOBL escCountHi<>(SB), RODATA|NOPTR, $16

DATA escCountLo<>+0x00(SB)/8, $0x06060606060e0606
DATA escCountLo<>+0x08(SB)/8, $0x0606054506050505
GLOBL escCountLo<>(SB), RODATA|NOPTR, $16

DATA escCountLoHTML<>+0x00(SB)/8, $0x06160606060e0606
DATA escCountLoHTML<>+0x08(SB)/8, $0x0626056506050505
GLOBL escCountLoHTML<>(SB), RODATA|NOPTR, $16

// ESCMASK16 marks in V1 the bytes of V0 that need escaping: <= 0x1f, '"', '\\',
// and with R9 != 0 '<', '>', '&'. Clobbers V2, V3.
// Uses V16='"', V17='\\', V18=0x1f, V20='<', V21='>', V22='&'.
#define ESCMASK16(label) \
    VUMIN V18.B16, V0.B16, V1.B16; \
    VCMEQ V1.B16, V0.B16, V1.B16; \
    VCMEQ V16.B16, V0.B16, V2.B16; \
    VORR V2.B16, V1.B16, V1.B16; \
    VCMEQ V17.B16, V0.B16, V2.B16; \
    VORR V2.B16, V1.B16, V1.B16; \
    CBZ R9, label; \
    VCMEQ V20.B16, V0.B16, V2.B16; \
    VCMEQ V21.B16, V0.B16, V3.B16; \
    VORR V3.B16, V2.B16, V2.B16; \
    VCMEQ V22.B16, V0.B16, V3.B16; \
    VORR V3.B16, V2.B16, V2.B16; \
    VORR V2.B16, V1.B16, V1.B16; \
label:

// UNESCMASK16 marks in V1 the bytes of V0 that stop unescape: <= 0x1f, '"', '\\'.
// Uses V16='"', V17='\\', V18=0x1f. Clobbers V2.
#define UNESCMASK16 \
    VUMIN V18.B16, V0.B16, V1.B16; \
    VCMEQ V1.B16, V0.B16, V1.B16; \
    VCMEQ V16.B16, V0.B16, V2.B16; \
    VORR V2.B16, V1.B16, V1.B16; \
    VCMEQ V17.B16, V0.B16, V2.B16; \
    VORR V2.B16, V1.B16, V1.B16

// VPACK16 packs a 16-byte 0xff/0x00 vector src into a 16-bit mask in dstReg.
// Uses V27 = escBitMask. Clobbers V4.
#define VPACK16(src, dstReg) \
    VAND V27.B16, src.B16, V4.B16; \
    VADDP V4.B16, V4.B16, V4.B16; \
    VADDP V4.B16, V4.B16, V4.B16; \
    VADDP V4.B16, V4.B16, V4.B16; \
    VMOV V4.H[0], dstReg

// EGROUP writes 8 bytes in V28.D[0] (V28.D[1] = backslashes) to (R2) with
// a backslash before each bit set in the low byte of R11, using exp at R12.
// Popcnt byte is in low byte of R13.
#define EGROUP \
    AND $0xff, R11, R4; \
    LSR $8, R11, R11; \
    LSL $4, R4, R4; \
    ADD R12, R4, R4; \
    VLD1 (R4), [V5.B16]; \
    VTBL V5.B16, [V28.B16], V5.B16; \
    VST1 [V5.B16], (R2); \
    AND $0xff, R13, R4; \
    LSR $8, R13, R13; \
    ADD $8, R4, R4; \
    ADD R4, R2, R2

// func escapeNEON(dst *byte, dn int, src *byte, n, i, w int, html bool, tab *[256]uint64, exp *[256][16]byte) (int, int)
TEXT ·escapeNEON(SB), NOSPLIT, $0-88
    MOVD dst+0(FP), R2
    MOVD dn+8(FP), R8
    MOVD src+16(FP), R0
    MOVD n+24(FP), R3
    MOVBU html+48(FP), R9
    MOVD tab+56(FP), R10
    MOVD exp+64(FP), R12

    ADD R2, R8, R8
    SUB $160, R8, R8            // fast loop dst limit (dn - 160)
    ADD R0, R3, R3
    SUB $32, R3, R3             // fast loop src limit (n - 32)
    MOVD i+32(FP), R4
    ADD R4, R0, R0
    MOVD w+40(FP), R4
    ADD R4, R2, R2

    VMOVI $0x22, V16.B16
    VMOVI $0x5c, V17.B16
    VMOVI $0x1f, V18.B16
    VMOVI $0x3c, V20.B16
    VMOVI $0x3e, V21.B16
    VMOVI $0x26, V22.B16
    MOVD $escLetters<>(SB), R4
    VLD1 (R4), [V24.B16]
    MOVD $escBitMask<>(SB), R4
    VLD1 (R4), [V27.B16]
    VMOV V17.B16, V28.B16       // high 8 bytes hold backslashes

esc_block:
    CMP R3, R0
    BHI esc_last
    CMP R8, R2
    BHI esc_last
    VLD1 (R0), [V0.B16]
    VST1 [V0.B16], (R2)
    ESCMASK16(esc_m0)
    VMOV V1.D[0], R4
    VMOV V1.D[1], R5
    ORR R5, R4, R4
    CBNZ R4, esc_expand
    ADD $16, R0, R0
    ADD $16, R2, R2
    B esc_block

esc_expand:
    // V4: letters for \b, \t, \n, \f, \r, 0 elsewhere.
    VTBL V0.B16, [V24.B16], V4.B16
    VCMTST V4.B16, V4.B16, V5.B16
    VCMEQ V16.B16, V0.B16, V2.B16
    VCMEQ V17.B16, V0.B16, V3.B16
    VORR V3.B16, V2.B16, V2.B16
    VORR V5.B16, V2.B16, V2.B16
    VEOR V2.B16, V1.B16, V6.B16
    VMOV V6.D[0], R4
    VMOV V6.D[1], R5
    ORR R5, R4, R4
    CBNZ R4, esc_steps
    VBIT V5.B16, V4.B16, V0.B16
    VPACK16(V1, R11)
    FMOVD R11, F7
    VCNT V7.B8, V7.B8
    VMOV V7.D[0], R13
    VMOV V0.D[0], R4
    VMOV R4, V28.D[0]
    EGROUP
    VMOV V0.D[1], R4
    VMOV R4, V28.D[0]
    EGROUP
    ADD $16, R0, R0
    B esc_block

esc_steps:
    VPACK16(V1, R11)
esc_step_loop:
    RBIT R11, R4
    CLZ R4, R4
    ADD R4, R0, R5
    MOVBU (R5), R6
    MOVD (R10)(R6<<3), R6
    ADD R4, R2, R7
    MOVD R6, (R7)
    LSR $56, R6, R6
    ADD R6, R2, R2
    SUB $1, R2, R2
    ADD $1, R5, R5
    VLD1 (R5), [V2.B16]
    ADD R4, R2, R7
    ADD $1, R7, R7
    VST1 [V2.B16], (R7)
    SUB $1, R11, R4
    AND R4, R11, R11
    CBNZ R11, esc_step_loop
    ADD $16, R0, R0
    ADD $16, R2, R2
    B esc_block

esc_last:
    ADD $16, R3, R3             // src limit: n - 16
    ADD $136, R8, R8            // dst limit: dn - 24
esc_lblock:
    CMP R3, R0
    BHI esc_done
    CMP R8, R2
    BHI esc_done
    VLD1 (R0), [V0.B16]
    VST1 [V0.B16], (R2)
    ESCMASK16(esc_m1)
    VMOV V1.D[0], R4
    VMOV V1.D[1], R5
    ORR R5, R4, R4
    CBNZ R4, esc_lfix
    ADD $16, R0, R0
    ADD $16, R2, R2
    B esc_lblock

esc_lfix:
    VPACK16(V1, R11)
    RBIT R11, R4
    CLZ R4, R4
    ADD R4, R0, R0
    ADD R4, R2, R2
    MOVBU (R0), R5
    MOVD (R10)(R5<<3), R5
    MOVD R5, (R2)
    LSR $56, R5, R5
    ADD R5, R2, R2
    ADD $1, R0, R0
    B esc_lblock

esc_done:
    MOVD src+16(FP), R4
    SUB R4, R0, R0
    MOVD dst+0(FP), R4
    SUB R4, R2, R2
    MOVD R0, ret+72(FP)
    MOVD R2, ret1+80(FP)
    RET

// func unescapeNEON(dst *byte, dn int, src *byte, n, i, w int, tab *unescapeTables) (int, int, bool)
TEXT ·unescapeNEON(SB), NOSPLIT, $0-73
    MOVD dst+0(FP), R0
    MOVD dn+8(FP), R8
    MOVD src+16(FP), R2
    MOVD n+24(FP), R9
    MOVD tab+48(FP), R10

    ADD R0, R8, R8
    SUB $64, R8, R8             // fast loop dst limit (dn - 64)
    ADD R2, R9, R9
    SUB $49, R9, R9             // fast loop src limit (n - 49)
    MOVD i+32(FP), R4
    ADD R4, R2, R2
    MOVD w+40(FP), R4
    ADD R4, R0, R0

    VMOVI $0x22, V16.B16
    VMOVI $0x5c, V17.B16
    VMOVI $0x1f, V18.B16
    VMOVI $0x0f, V19.B16
    MOVD $unescLo<>(SB), R4
    VLD1 (R4), [V20.B16]
    MOVD $unescHi<>(SB), R4
    VLD1 (R4), [V21.B16]
    MOVD $unescBFN<>(SB), R4
    VLD1 (R4), [V22.B16]
    MOVD $unescRT<>(SB), R4
    VLD1 (R4), [V23.B16]
    VMOVI $0x06, V24.B16
    VMOVI $0x07, V25.B16
    VEOR V26.B16, V26.B16, V26.B16
    MOVD $escBitMask<>(SB), R4
    VLD1 (R4), [V27.B16]
    VEOR V15.B16, V15.B16, V15.B16  // high-byte accumulator

unesc_block:
    CMP R9, R2
    BHI unesc_last
    CMP R8, R0
    BHI unesc_last
    VLD1 (R2), [V0.B16]
    VST1 [V0.B16], (R0)
    VORR V0.B16, V15.B16, V15.B16
    UNESCMASK16
    VMOV V1.D[0], R4
    VMOV V1.D[1], R5
    ORR R5, R4, R4
    CBNZ R4, unesc_compact
    ADD $16, R2, R2
    ADD $16, R0, R0
    B unesc_block

unesc_compact:
    VPACK16(V1, R11)            // R11: all stop bytes in 16B block
    VCMEQ V17.B16, V0.B16, V2.B16
    VPACK16(V2, R4)             // R4: backslashes
    LSL $1, R4, R5
    MOVD $0x5555555555555555, R6
    BIC R6, R4, R12
    BIC R5, R12, R12
    ADD R4, R12, R12
    LSL $1, R12, R12
    EOR R6, R12, R12
    AND R5, R12, R12
    MOVD $0x1ffff, R6
    AND R6, R12, R12            // R12: bytes escaped by preceding backslash (+ bit 16)

    VUSHR $4, V0.B16, V3.B16
    VAND V19.B16, V0.B16, V4.B16
    VTBL V4.B16, [V20.B16], V4.B16
    VTBL V3.B16, [V21.B16], V5.B16
    VAND V5.B16, V4.B16, V4.B16
    VCMEQ V26.B16, V4.B16, V4.B16
    VPACK16(V4, R13)            // R13: bytes not a valid 2-byte escape letter

    BIC R4, R11, R5
    BIC R12, R5, R5             // non-bslash stop not preceded by escaping bslash
    AND R12, R13, R6            // escaped byte that is not a 2-byte escape letter
    ORR R6, R5, R5
    CBNZ R5, unesc_steps

    // Fast compact path: replace b,f,n,r,t where R12 bit is set.
    VAND V19.B16, V0.B16, V6.B16
    VTBL V6.B16, [V22.B16], V4.B16
    VCMEQ V24.B16, V3.B16, V5.B16
    VAND V5.B16, V4.B16, V4.B16
    VTBL V6.B16, [V23.B16], V5.B16
    VCMEQ V25.B16, V3.B16, V6.B16
    VAND V6.B16, V5.B16, V5.B16
    VORR V5.B16, V4.B16, V4.B16

    // Turn R12[0:15] into 16-byte mask in V3.
    MOVD $0x0101010101010101, R5
    AND $0xff, R12, R6
    MUL R5, R6, R6
    UBFX $8, R12, $8, R7
    MUL R5, R7, R7
    VMOV R6, V3.D[0]
    VMOV R7, V3.D[1]
    VCMTST V27.B16, V3.B16, V3.B16
    VCMTST V4.B16, V4.B16, V5.B16
    VAND V5.B16, V3.B16, V3.B16
    VBIT V3.B16, V4.B16, V0.B16

    // Compact out backslashes at R12 >> 1.
    LSR $1, R12, R5
    AND $0xffff, R5, R5
    FMOVD R5, F7
    VCNT V7.B8, V7.B8
    VMOV V7.D[0], R13
    ADD $256, R10, R6           // &tab.comp[0]

    AND $0xff, R5, R4
    LSL $4, R4, R4
    ADD R6, R4, R4
    VLD1 (R4), [V5.B16]
    VTBL V5.B16, [V0.B16], V5.B16
    VMOV V5.D[0], R4
    MOVD R4, (R0)
    AND $0xff, R13, R4
    ADD $8, R0, R0
    SUB R4, R0, R0

    VEXT $8, V0.B16, V0.B16, V0.B16
    LSR $8, R5, R4
    LSL $4, R4, R4
    ADD R6, R4, R4
    VLD1 (R4), [V5.B16]
    VTBL V5.B16, [V0.B16], V5.B16
    VMOV V5.D[0], R4
    MOVD R4, (R0)
    LSR $8, R13, R4
    AND $0xff, R4, R4
    ADD $8, R0, R0
    SUB R4, R0, R0

    LSR $16, R12, R12
    ADD $16, R2, R2
    SUB R12, R2, R2
    B unesc_block

unesc_steps:
    MOVD $0, R12
unesc_step_loop:
    RBIT R11, R4
    CLZ R4, R4                  // ax: 0..15
    ADD R4, R2, R5
    MOVBU (R5), R6
    CMP $0x5c, R6
    BNE unesc_stop
    MOVBU 1(R5), R6
    MOVBU (R10)(R6), R7
    CBZ R7, unesc_u
    ADD $2, R5, R6
    VLD1 (R6), [V2.B16]
    ADD R4, R0, R6
    MOVB R7, (R6)
    ADD $1, R6, R6
    VST1 [V2.B16], (R6)
    CMP $15, R4
    BNE unesc_no_over
    MOVD $1, R12
unesc_no_over:
    SUB $1, R0, R0
    MOVD $3, R6
    LSL R4, R6, R6
    BIC R6, R11, R11
    CBNZ R11, unesc_step_loop
unesc_next:
    ADD $16, R2, R2
    ADD $16, R0, R0
    ADD R12, R2, R2
    ADD R12, R0, R0
    B unesc_block

unesc_u:
    // R5 = src + ax, R6 = R5[1]. Needs 'u' and at least 38 bytes left in src.
    // Since R9 = src_end - 49, R5 <= src_end - 38 iff R5 - 11 <= R9.
    CMP $0x75, R6
    BNE unesc_stop
    SUB $11, R5, R6
    CMP R9, R6
    BHI unesc_stop
    ADD $4352, R10, R13         // &tab.hex[0]
    MOVBU 2(R5), R6
    MOVW (R13)(R6<<2), R7
    MOVBU 3(R5), R6
    MOVW (R13)(R6<<2), R6
    LSL $4, R7, R7
    ORR R6, R7, R7
    MOVBU 4(R5), R6
    MOVW (R13)(R6<<2), R6
    LSL $4, R7, R7
    ORR R6, R7, R7
    MOVBU 5(R5), R6
    MOVW (R13)(R6<<2), R6
    LSL $4, R7, R7
    ORR R6, R7, R7
    MOVD $0xffff, R6
    CMP R6, R7
    BHI unesc_stop
    AND $0xf800, R7, R6
    CMP $0xd800, R6
    BEQ unesc_stop

    CMP $0x80, R7
    BLO unesc_u1
    CMP $0x800, R7
    BLO unesc_u2
    // 3-byte UTF-8: 0x8080e0 | ((r & 0x3f) << 16) | ((r & 0xfc0) << 2) | (r >> 12)
    LSR $12, R7, R13
    AND $0xfc0, R7, R6
    LSL $2, R6, R6
    ORR R6, R13, R13
    AND $0x3f, R7, R6
    LSL $16, R6, R6
    ORR R6, R13, R13
    MOVD $0x8080e0, R6
    ORR R6, R13, R13
    MOVD $3, R7
    B unesc_uput
unesc_u2:
    // 2-byte UTF-8: 0x80c0 | ((r & 0x3f) << 8) | (r >> 6)
    LSR $6, R7, R13
    AND $0x3f, R7, R6
    LSL $8, R6, R6
    ORR R6, R13, R13
    MOVD $0x80c0, R6
    ORR R6, R13, R13
    MOVD $2, R7
    B unesc_uput
unesc_u1:
    MOVD R7, R13
    MOVD $1, R7
unesc_uput:
    ADD R4, R0, R6
    MOVW R13, (R6)
    ADD $6, R5, R6
    VLD1 (R6), [V2.B16]
    ADD R7, R0, R0
    ADD R4, R0, R6
    VST1 [V2.B16], (R6)
    SUB $6, R0, R0
    CMP $10, R4
    BLS unesc_uin
    SUB $10, R4, R12
unesc_uin:
    SUB $1, R11, R6
    AND R6, R11, R11
    CBNZ R11, unesc_step_loop
    B unesc_next

unesc_stop:
    ADD R4, R2, R2
    ADD R4, R0, R0
    B unesc_done

unesc_last:
    ADD $15, R9, R9             // src limit: n - 34
    ADD $32, R8, R8             // dst limit: dn - 32
unesc_lblock:
    CMP R9, R2
    BHI unesc_done
    CMP R8, R0
    BHI unesc_done
    VLD1 (R2), [V0.B16]
    VST1 [V0.B16], (R0)
    VORR V0.B16, V15.B16, V15.B16
    UNESCMASK16
    VMOV V1.D[0], R4
    VMOV V1.D[1], R5
    ORR R5, R4, R4
    CBNZ R4, unesc_lstop
    ADD $16, R2, R2
    ADD $16, R0, R0
    B unesc_lblock

unesc_lstop:
    VPACK16(V1, R11)
    RBIT R11, R4
    CLZ R4, R4
    ADD R4, R2, R2
    ADD R4, R0, R0
    MOVBU (R2), R5
    CMP $0x5c, R5
    BNE unesc_done
    MOVBU 1(R2), R5
    MOVBU (R10)(R5), R6
    CBZ R6, unesc_done
    MOVB R6, (R0)
    ADD $2, R2, R2
    ADD $1, R0, R0
    B unesc_lblock

unesc_done:
    MOVD src+16(FP), R4
    SUB R4, R2, R2
    MOVD dst+0(FP), R4
    SUB R4, R0, R0
    MOVD R2, ret+56(FP)
    MOVD R0, ret1+64(FP)
    VMOV V15.D[0], R4
    VMOV V15.D[1], R5
    ORR R5, R4, R4
    MOVD $0x8080808080808080, R5
    TST R5, R4
    CSET NE, R4
    MOVB R4, ret2+72(FP)
    RET

// func utf8NEON(src *byte, n int) int
TEXT ·utf8NEON(SB), NOSPLIT, $0-24
    MOVD src+0(FP), R0
    MOVD n+8(FP), R1
    MOVD $0, R2

    MOVD $utf8Hi1<>(SB), R4
    VLD1 (R4), [V8.B16]
    MOVD $utf8Lo1<>(SB), R4
    VLD1 (R4), [V9.B16]
    MOVD $utf8Hi2<>(SB), R4
    VLD1 (R4), [V10.B16]
    VMOVI $0x0f, V11.B16
    VMOVI $0x60, V16.B16
    VMOVI $0x70, V17.B16
    VMOVI $0x80, V18.B16
    VMOVI $0xe2, V19.B16
    VMOVI $0xfe, V20.B16
    VMOVI $0xa8, V21.B16
    MOVD $utf8Max16<>(SB), R4
    VLD1 (R4), [V22.B16]
    MOVD $0x8080808080808080, R6

    VEOR V12.B16, V12.B16, V12.B16
    VEOR V13.B16, V13.B16, V13.B16
    VEOR V14.B16, V14.B16, V14.B16
    VEOR V15.B16, V15.B16, V15.B16

utf8_block:
    CMP R1, R2
    BGE utf8_done
    VLD1.P 16(R0), [V0.B16]
    ADD $16, R2, R2
    VMOV V0.D[0], R4
    VMOV V0.D[1], R5
    ORR R5, R4, R4
    TST R6, R4
    BNE utf8_high
    VORR V12.B16, V14.B16, V14.B16
    VEOR V12.B16, V12.B16, V12.B16
    VMOV V0.B16, V15.B16
    B utf8_block

utf8_high:
    VEXT $15, V0.B16, V15.B16, V2.B16
    VEXT $14, V0.B16, V15.B16, V3.B16
    VEXT $13, V0.B16, V15.B16, V4.B16
    VUSHR $4, V2.B16, V5.B16
    VTBL V5.B16, [V8.B16], V5.B16
    VAND V11.B16, V2.B16, V7.B16
    VTBL V7.B16, [V9.B16], V7.B16
    VAND V7.B16, V5.B16, V5.B16
    VUSHR $4, V0.B16, V7.B16
    VTBL V7.B16, [V10.B16], V7.B16
    VAND V7.B16, V5.B16, V5.B16

    VUMIN V16.B16, V3.B16, V7.B16
    VSUB V7.B16, V3.B16, V7.B16
    VUMIN V17.B16, V4.B16, V1.B16
    VSUB V1.B16, V4.B16, V1.B16
    VORR V1.B16, V7.B16, V7.B16
    VAND V18.B16, V7.B16, V7.B16
    VEOR V7.B16, V5.B16, V5.B16
    VORR V5.B16, V14.B16, V14.B16

    VCMEQ V18.B16, V2.B16, V7.B16
    VCMEQ V19.B16, V3.B16, V1.B16
    VAND V1.B16, V7.B16, V7.B16
    VAND V20.B16, V0.B16, V1.B16
    VCMEQ V21.B16, V1.B16, V1.B16
    VAND V1.B16, V7.B16, V7.B16
    VORR V7.B16, V13.B16, V13.B16

    VUMIN V22.B16, V0.B16, V12.B16
    VSUB V12.B16, V0.B16, V12.B16
    VMOV V0.B16, V15.B16
    B utf8_block

utf8_done:
    MOVD $0, R3
    VMOV V14.D[0], R4
    VMOV V14.D[1], R5
    ORR R5, R4, R4
    CBZ R4, utf8_sep
    MOVD $1, R3
utf8_sep:
    VMOV V13.D[0], R4
    VMOV V13.D[1], R5
    ORR R5, R4, R4
    CBZ R4, utf8_ret
    ORR $2, R3, R3
utf8_ret:
    MOVD R3, ret+16(FP)
    RET

// func escCountNEON(src *byte, n int, html bool) int
TEXT ·escCountNEON(SB), NOSPLIT, $0-32
    MOVD src+0(FP), R0
    MOVD n+8(FP), R1
    MOVBU html+16(FP), R2
    MOVD $0, R3
    MOVD $0, R4
    MOVD $0, R5

    MOVD $escCountHi<>(SB), R6
    VLD1 (R6), [V8.B16]
    MOVD $escCountLo<>(SB), R6
    CBZ R2, cnt_plain
    MOVD $escCountLoHTML<>(SB), R6
cnt_plain:
    VLD1 (R6), [V9.B16]
    VMOVI $0x0f, V11.B16
    VMOVI $0x49, V12.B16
    VMOVI $0x36, V13.B16

cnt_round:
    MOVD $255, R9
    VEOR V6.B16, V6.B16, V6.B16
    VEOR V7.B16, V7.B16, V7.B16
cnt_block:
    CMP R1, R3
    BGE cnt_sum
    VLD1.P 16(R0), [V0.B16]
    ADD $16, R3, R3
    VUSHR $4, V0.B16, V1.B16
    VAND V11.B16, V0.B16, V2.B16
    VTBL V1.B16, [V8.B16], V1.B16
    VTBL V2.B16, [V9.B16], V2.B16
    VAND V2.B16, V1.B16, V1.B16
    VCMTST V12.B16, V1.B16, V2.B16
    VSUB V2.B16, V6.B16, V6.B16
    VCMTST V13.B16, V1.B16, V2.B16
    VSUB V2.B16, V7.B16, V7.B16
    SUB $1, R9, R9
    CBNZ R9, cnt_block

cnt_sum:
    WORD $0x6e3038c6            // UADDLV H6, V6.16B
    VMOV V6.D[0], R6
    ADD R6, R4, R4
    WORD $0x6e3038e7            // UADDLV H7, V7.16B
    VMOV V7.D[0], R7
    ADD R7, R5, R5
    CMP R1, R3
    BLT cnt_round

    ADD R5<<2, R5, R5
    ADD R5, R4, R4
    MOVD R4, ret+24(FP)
    RET
