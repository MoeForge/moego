//go:build arm64 && !purego

#include "textflag.h"

// Bit weights for packing 16 byte comparison lanes into 2 mask bytes via VADDP.
DATA bitMask<>+0(SB)/8, $0x8040201008040201
DATA bitMask<>+8(SB)/8, $0x8040201008040201
GLOBL bitMask<>(SB), RODATA|NOPTR, $16

// Nibble tables of the path bytes: the class of a byte b is
// pathLo[b&15] & pathHi[b>>4]. Bit 4 marks '.'; bits 0 to 3 mark the bytes
// getComp handles: '"', '#' and '*' (bit 0), '?' (1), '@' (2), and '[',
// '\\', '{' and '|' (3). Every other byte, 0x80 and up included, has class 0.
DATA pathLo<>+0(SB)/8, $0x0000000001010004
DATA pathLo<>+8(SB)/8, $0x0210000808010000
GLOBL pathLo<>(SB), RODATA|NOPTR, $16
DATA pathHi<>+0(SB)/8, $0x0800080402110000
DATA pathHi<>+8(SB)/8, $0x0000000000000000
GLOBL pathHi<>(SB), RODATA|NOPTR, $16

// PREFIXXOR computes the prefix XOR of src into dst.
#define PREFIXXOR(src, dst) \
    EOR src<<1, src, dst; \
    EOR dst<<2, dst, dst; \
    EOR dst<<4, dst, dst; \
    EOR dst<<8, dst, dst; \
    EOR dst<<16, dst, dst; \
    EOR dst<<32, dst, dst

// POPCNT64 counts the set bits in reg using NEON CNT + UADDLV. Clobbers V4.
#define POPCNT64(reg) \
    FMOVD reg, F4; \
    VCNT V4.B8, V4.B8; \
    WORD $0x2e303884; \
    VMOV V4.D[0], reg

// CLASSIFY turns the 64 bytes in V0..V3 into the 64-bit masks of quotes (R4),
// backslashes (R12), opening brackets (R13) and closing brackets (R14).
// OR 0x20 folds '[' into '{' and ']' into '}', and nothing else into either.
// Clobbers V0..V11.
#define CLASSIFY \
    VCMEQ V16.B16, V0.B16, V4.B16; \
    VCMEQ V16.B16, V1.B16, V5.B16; \
    VCMEQ V16.B16, V2.B16, V6.B16; \
    VCMEQ V16.B16, V3.B16, V7.B16; \
    VAND V21.B16, V4.B16, V4.B16; \
    VAND V21.B16, V5.B16, V5.B16; \
    VAND V21.B16, V6.B16, V6.B16; \
    VAND V21.B16, V7.B16, V7.B16; \
    VADDP V5.B16, V4.B16, V4.B16; \
    VADDP V7.B16, V6.B16, V6.B16; \
    VADDP V6.B16, V4.B16, V4.B16; \
    VCMEQ V17.B16, V0.B16, V8.B16; \
    VCMEQ V17.B16, V1.B16, V9.B16; \
    VCMEQ V17.B16, V2.B16, V10.B16; \
    VCMEQ V17.B16, V3.B16, V11.B16; \
    VAND V21.B16, V8.B16, V8.B16; \
    VAND V21.B16, V9.B16, V9.B16; \
    VAND V21.B16, V10.B16, V10.B16; \
    VAND V21.B16, V11.B16, V11.B16; \
    VADDP V9.B16, V8.B16, V8.B16; \
    VADDP V11.B16, V10.B16, V10.B16; \
    VADDP V10.B16, V8.B16, V8.B16; \
    VADDP V8.B16, V4.B16, V4.B16; \
    VMOV V4.D[0], R4; \
    VMOV V4.D[1], R12; \
    VORR V18.B16, V0.B16, V0.B16; \
    VORR V18.B16, V1.B16, V1.B16; \
    VORR V18.B16, V2.B16, V2.B16; \
    VORR V18.B16, V3.B16, V3.B16; \
    VCMEQ V19.B16, V0.B16, V4.B16; \
    VCMEQ V19.B16, V1.B16, V5.B16; \
    VCMEQ V19.B16, V2.B16, V6.B16; \
    VCMEQ V19.B16, V3.B16, V7.B16; \
    VAND V21.B16, V4.B16, V4.B16; \
    VAND V21.B16, V5.B16, V5.B16; \
    VAND V21.B16, V6.B16, V6.B16; \
    VAND V21.B16, V7.B16, V7.B16; \
    VADDP V5.B16, V4.B16, V4.B16; \
    VADDP V7.B16, V6.B16, V6.B16; \
    VADDP V6.B16, V4.B16, V4.B16; \
    VCMEQ V20.B16, V0.B16, V8.B16; \
    VCMEQ V20.B16, V1.B16, V9.B16; \
    VCMEQ V20.B16, V2.B16, V10.B16; \
    VCMEQ V20.B16, V3.B16, V11.B16; \
    VAND V21.B16, V8.B16, V8.B16; \
    VAND V21.B16, V9.B16, V9.B16; \
    VAND V21.B16, V10.B16, V10.B16; \
    VAND V21.B16, V11.B16, V11.B16; \
    VADDP V9.B16, V8.B16, V8.B16; \
    VADDP V11.B16, V10.B16, V10.B16; \
    VADDP V10.B16, V8.B16, V8.B16; \
    VADDP V8.B16, V4.B16, V4.B16; \
    VMOV V4.D[0], R13; \
    VMOV V4.D[1], R14

// func skipNEON(p *byte, n int, st *skipState) int
//
// Finds the bracket that closes a container, 64 bytes per step, starting
// at st.pos with the depth, string and escape state in st. It returns the
// index after the closing bracket, -1 when the input ends first, with st
// holding the state at that point, or skipSlow at a backslash outside a
// string.
TEXT ·skipNEON(SB), NOSPLIT, $0-32
    MOVD p+0(FP), R0
    MOVD n+8(FP), R1
    MOVD st+16(FP), R2
    MOVD 0(R2), R8          // depth
    MOVD 8(R2), R9          // inStr: 0 or all ones
    MOVD 16(R2), R10        // esc: 0 or 1
    MOVD 24(R2), R3         // pos
    VMOVI $0x22, V16.B16
    VMOVI $0x5c, V17.B16
    VMOVI $0x20, V18.B16
    VMOVI $0x7b, V19.B16
    VMOVI $0x7d, V20.B16
    MOVD $bitMask<>(SB), R4
    VLD1 (R4), [V21.B16]

skip_block:
    ADD $64, R3, R5
    CMP R1, R5
    BHI skip_tail
    ADD R3, R0, R6
    VLD1 (R6), [V0.B16, V1.B16, V2.B16, V3.B16]
skip_classify:
    CLASSIFY
skip_masked:
    ORR R10, R12, R5
    CBNZ R5, skip_escapes
    CBZ R9, skip_plain
    CBZ R4, skip_instring   // all string contents
skip_plain:
    PREFIXXOR(R4, R5)
    EOR R9, R5, R5          // R5: bytes inside strings
skip_strings:
    ASR $63, R5, R9         // whether the next block starts in a string
    BIC R5, R13, R13
    BIC R5, R14, R14
skip_depth:
    CBZ R14, skip_next
    NEG R14, R5
    AND R14, R5, R5         // lowest closing bracket
    SUB $1, R5, R11
    AND R13, R11, R11       // opening brackets before it
    CBZ R11, skip_depth_dec
    BIC R11, R13, R13
    SUB $1, R11, R6
    ANDS R11, R6, R6
    BEQ skip_depth_one      // exactly one opening bracket before this closer
    POPCNT64(R11)
    ADD R11, R8, R8
skip_depth_dec:
    EOR R5, R14, R14
    SUBS $1, R8, R8
    BEQ skip_found
    B skip_depth
skip_depth_one:
    // One opening bracket and one closing bracket cancel out (+1 - 1 = 0).
    // When depth was 0 at the start of the container, it closes here.
    CBZ R8, skip_found
    EOR R5, R14, R14
    B skip_depth
skip_next:
    CBZ R13, skip_advance
    POPCNT64(R13)
    ADD R13, R8, R8
skip_advance:
    ADD $64, R3, R3
    B skip_block

skip_found:
    RBIT R5, R5
    CLZ R5, R5
    ADD R3, R5, R5
    ADD $1, R5, R5
    MOVD R5, ret+24(FP)
    RET

skip_instring:
    // Inside a string with no escape pending: only a quote or a backslash
    // can change the state, so look for those alone.
    ADD $64, R3, R3
skip_instring_loop:
    ADD $64, R3, R5
    CMP R1, R5
    BHI skip_tail
    ADD R3, R0, R6
    VLD1 (R6), [V0.B16, V1.B16, V2.B16, V3.B16]
    VCMEQ V16.B16, V0.B16, V4.B16
    VCMEQ V16.B16, V1.B16, V5.B16
    VCMEQ V16.B16, V2.B16, V6.B16
    VCMEQ V16.B16, V3.B16, V7.B16
    VCMEQ V17.B16, V0.B16, V8.B16
    VCMEQ V17.B16, V1.B16, V9.B16
    VCMEQ V17.B16, V2.B16, V10.B16
    VCMEQ V17.B16, V3.B16, V11.B16
    VORR V5.B16, V4.B16, V4.B16
    VORR V7.B16, V6.B16, V6.B16
    VORR V9.B16, V8.B16, V8.B16
    VORR V11.B16, V10.B16, V10.B16
    VORR V6.B16, V4.B16, V4.B16
    VORR V10.B16, V8.B16, V8.B16
    VORR V8.B16, V4.B16, V4.B16
    WORD $0x6e30a884        // UMAXV B4, V4.16B
    VMOV V4.B[0], R5
    CBNZ R5, skip_classify
    ADD $64, R3, R3
    B skip_instring_loop

skip_escapes:
    // R12: backslashes, R10: whether byte 0 is escaped.
    MOVD R12, R16           // save raw backslashes
    BIC R10, R12, R12       // an escaped byte 0 starts no run
    LSL $1, R12, R5
    BIC R5, R12, R11        // run starts
    MOVD $0x5555555555555555, R6
    AND R6, R11, R5         // starts on even positions
    ADD R12, R5, R5
    BIC R12, R5, R5         // ends of runs that started even
    MOVD $0xaaaaaaaaaaaaaaaa, R7
    AND R7, R5, R5          // ... escape an odd position
    AND R7, R11, R7         // starts on odd positions
    ADDS R12, R7, R7
    CSET HS, R11            // a run that started odd runs past the block
    BIC R12, R7, R7         // ends of runs that started odd
    AND R6, R7, R7          // ... escape an even position
    ORR R7, R5, R5
    ORR R10, R5, R5         // escaped bytes
    MOVD R11, R10
    BIC R5, R4, R4          // unescaped quotes
    PREFIXXOR(R4, R5)
    EOR R9, R5, R5          // R5: bytes inside strings
    // A backslash outside a string is invalid JSON: let scalar handle it.
    BIC R5, R16, R16
    CBNZ R16, skip_slow
    B skip_strings

skip_tail:
    // Fewer than 64 bytes remain.
    SUBS R3, R1, R5
    BLE skip_end
    ADD R3, R0, R6
    AND $4095, R6, R7
    CMP $(4096-64), R7
    BHI skip_tail_page
    // The 64 bytes at pos lie in one 4096-byte page: read them all and
    // drop the bits past the end.
    VLD1 (R6), [V0.B16, V1.B16, V2.B16, V3.B16]
    CLASSIFY
    MOVD $64, R6
    SUB R5, R6, R6
    MOVD $-1, R7
    LSR R6, R7, R7
    AND R7, R4, R4
    AND R7, R12, R12
    AND R7, R13, R13
    AND R7, R14, R14
    B skip_masked
skip_tail_page:
    // Read the 64 bytes that end at the end of the input and shift the
    // masks down so that bit 0 is byte pos.
    ADD R1, R0, R6
    SUB $64, R6, R6
    VLD1 (R6), [V0.B16, V1.B16, V2.B16, V3.B16]
    CLASSIFY
    MOVD $64, R6
    SUB R5, R6, R6
    LSR R6, R4, R4
    LSR R6, R12, R12
    LSR R6, R13, R13
    LSR R6, R14, R14
    B skip_masked

skip_end:
    MOVD R8, 0(R2)
    MOVD R9, 8(R2)
    MOVD R10, 16(R2)
    MOVD R3, 24(R2)
    MOVD $-1, R5
    MOVD R5, ret+24(FP)
    RET

skip_slow:
    MOVD $-4, R5
    MOVD R5, ret+24(FP)
    RET

// PATHCLASSIFY leaves the masks of the dots in the 64 bytes in V0..V3
// in R8, and of the other path bytes in R9. Clobbers V4..V11.
#define PATHCLASSIFY \
    VUSHR $4, V0.B16, V4.B16; \
    VUSHR $4, V1.B16, V5.B16; \
    VUSHR $4, V2.B16, V6.B16; \
    VUSHR $4, V3.B16, V7.B16; \
    VTBL V4.B16, [V17.B16], V4.B16; \
    VTBL V5.B16, [V17.B16], V5.B16; \
    VTBL V6.B16, [V17.B16], V6.B16; \
    VTBL V7.B16, [V17.B16], V7.B16; \
    VAND V18.B16, V0.B16, V8.B16; \
    VAND V18.B16, V1.B16, V9.B16; \
    VAND V18.B16, V2.B16, V10.B16; \
    VAND V18.B16, V3.B16, V11.B16; \
    VTBL V8.B16, [V16.B16], V8.B16; \
    VTBL V9.B16, [V16.B16], V9.B16; \
    VTBL V10.B16, [V16.B16], V10.B16; \
    VTBL V11.B16, [V16.B16], V11.B16; \
    VAND V8.B16, V4.B16, V4.B16; \
    VAND V9.B16, V5.B16, V5.B16; \
    VAND V10.B16, V6.B16, V6.B16; \
    VAND V11.B16, V7.B16, V7.B16; \
    VCMEQ V19.B16, V4.B16, V8.B16; \
    VCMEQ V19.B16, V5.B16, V9.B16; \
    VCMEQ V19.B16, V6.B16, V10.B16; \
    VCMEQ V19.B16, V7.B16, V11.B16; \
    VCMTST V18.B16, V4.B16, V4.B16; \
    VCMTST V18.B16, V5.B16, V5.B16; \
    VCMTST V18.B16, V6.B16, V6.B16; \
    VCMTST V18.B16, V7.B16, V7.B16; \
    VAND V21.B16, V8.B16, V8.B16; \
    VAND V21.B16, V9.B16, V9.B16; \
    VAND V21.B16, V10.B16, V10.B16; \
    VAND V21.B16, V11.B16, V11.B16; \
    VADDP V9.B16, V8.B16, V8.B16; \
    VADDP V11.B16, V10.B16, V10.B16; \
    VADDP V10.B16, V8.B16, V8.B16; \
    VAND V21.B16, V4.B16, V4.B16; \
    VAND V21.B16, V5.B16, V5.B16; \
    VAND V21.B16, V6.B16, V6.B16; \
    VAND V21.B16, V7.B16, V7.B16; \
    VADDP V5.B16, V4.B16, V4.B16; \
    VADDP V7.B16, V6.B16, V6.B16; \
    VADDP V6.B16, V4.B16, V4.B16; \
    VADDP V4.B16, V8.B16, V8.B16; \
    VMOV V8.D[0], R8; \
    VMOV V8.D[1], R9

// func pathMasksNEON(p *byte, n int) (dots, special uint64)
TEXT ·pathMasksNEON(SB), NOSPLIT, $0-32
    MOVD p+0(FP), R0
    MOVD n+8(FP), R1
    MOVD $pathLo<>(SB), R2
    VLD1 (R2), [V16.B16]
    MOVD $pathHi<>(SB), R2
    VLD1 (R2), [V17.B16]
    VMOVI $0x0f, V18.B16
    VMOVI $0x10, V19.B16
    MOVD $bitMask<>(SB), R2
    VLD1 (R2), [V21.B16]
    AND $4095, R0, R2
    CMP $(4096-64), R2
    BHI path_page
    VLD1 (R0), [V0.B16, V1.B16, V2.B16, V3.B16]
    PATHCLASSIFY
    MOVD $64, R2
    SUB R1, R2, R2
    MOVD $-1, R3
    LSR R2, R3, R3
    AND R3, R8, R8
    AND R3, R9, R9
    B path_done
path_page:
    ADD R1, R0, R2
    SUB $64, R2, R2
    VLD1 (R2), [V0.B16, V1.B16, V2.B16, V3.B16]
    PATHCLASSIFY
    MOVD $64, R2
    SUB R1, R2, R2
    LSR R2, R8, R8
    LSR R2, R9, R9
path_done:
    MOVD R8, dots+16(FP)
    MOVD R9, special+24(FP)
    RET

// func membersNEON(p *byte, n, i int, key *byte, klen int) (v, at int)
//
// memberValue on NEON loads: reads the members of an object from p[i],
// just after the opening brace or a comma, and returns the start of the
// value of the first whose key is the klen bytes at key, or -1, exactly as
// memberValue does. A member it leaves to memberValue makes it return
// membersSlow, with the start of that member in at.
TEXT ·membersNEON(SB), NOSPLIT, $0-56
    MOVD p+0(FP), R0
    MOVD n+8(FP), R1
    MOVD i+16(FP), R3
    MOVD key+24(FP), R25
    MOVD klen+32(FP), R19
    MOVD $0x100002600, R17      // whitespace: bits 9, 10, 13 and 32
    VMOVI $0x22, V16.B16
    VMOVI $0x5c, V17.B16
    VMOVI $0x20, V18.B16
    VMOVI $0x7b, V19.B16
    VMOVI $0x7d, V20.B16
    MOVD $bitMask<>(SB), R4
    VLD1 (R4), [V21.B16]

mem_member:
    MOVD R3, R20                // start of the member
mem_key_space:
    CMP R1, R3
    BGE mem_notfound
    MOVBU (R0)(R3), R5
    CMP $0x22, R5
    BEQ mem_key
    CMP $0x20, R5
    BHI mem_notfound
    LSR R5, R17, R6
    TBZ $0, R6, mem_notfound
    ADD $1, R3, R3
    B mem_key_space

mem_key:
    ADD $1, R3, R3
    MOVD R3, R11                // first byte of the key
    MOVD $0, R12                // set at a backslash
mem_key_block:
    ADD $16, R3, R5
    CMP R1, R5
    BHI mem_key_tail
    ADD R3, R0, R6
    VLD1 (R6), [V0.B16]
    VCMEQ V16.B16, V0.B16, V1.B16
    VCMEQ V17.B16, V0.B16, V2.B16
    WORD $0x0f0c8421            // SHRN V1.8B, V1.8H, #4
    WORD $0x0f0c8442            // SHRN V2.8B, V2.8H, #4
    VMOV V1.D[0], R5            // quotes (4 bits per byte)
    VMOV V2.D[0], R6            // backslashes (4 bits per byte)
    SUB $1, R5, R7
    EOR R5, R7, R7              // bits up to first quote, or all ones
    TST R7, R6
    BNE mem_key_escape
    CBZ R5, mem_key_next
    RBIT R5, R5
    CLZ R5, R5
    LSR $2, R5, R5
    ADD R5, R3, R3
    ADD $1, R3, R3              // past the closing quote
    B mem_key_end
mem_key_next:
    ADD $16, R3, R3
    B mem_key_block
mem_key_escape:
    MOVD $1, R12
    RBIT R6, R6
    CLZ R6, R6
    LSR $2, R6, R6
    ADD R6, R3, R3
    ADD $2, R3, R3              // past the backslash and the byte it escapes
    B mem_key_block
mem_key_tail:
    CMP R1, R3
    BGE mem_notfound
    MOVBU (R0)(R3), R5
    ADD $1, R3, R3
    CMP $0x22, R5
    BEQ mem_key_end
    CMP $0x5c, R5
    BNE mem_key_tail
    MOVD $1, R12
    ADD $1, R3, R3
    B mem_key_tail

mem_key_end:
    CBNZ R12, mem_slow          // a key with escapes is decoded to compare
    MOVD $0, R21                // set when the key matches
    SUB $1, R3, R5
    SUB R11, R5, R5
    CMP R19, R5
    BNE mem_colon
    ADD R11, R0, R12
    CMP $8, R19
    BLT mem_cmp_short
    MOVD $0, R6
mem_cmp8:
    ADD $8, R6, R5
    CMP R19, R5
    BHI mem_cmp8_tail
    MOVD (R12)(R6), R5
    MOVD (R25)(R6), R7
    CMP R7, R5
    BNE mem_colon
    ADD $8, R6, R6
    CMP R19, R6
    BEQ mem_match
    B mem_cmp8
mem_cmp8_tail:
    SUB $8, R19, R6
    MOVD (R12)(R6), R5
    MOVD (R25)(R6), R7
    CMP R7, R5
    BNE mem_colon
    B mem_match
mem_cmp_short:
    CMP $4, R19
    BLT mem_cmp_tiny
    MOVWU (R12), R5
    MOVWU (R25), R7
    CMP R7, R5
    BNE mem_colon
    SUB $4, R19, R6
    MOVWU (R12)(R6), R5
    MOVWU (R25)(R6), R7
    CMP R7, R5
    BNE mem_colon
    B mem_match
mem_cmp_tiny:
    CMP $2, R19
    BLT mem_cmp_one
    MOVHU (R12), R5
    MOVHU (R25), R7
    CMP R7, R5
    BNE mem_colon
    SUB $2, R19, R6
    MOVHU (R12)(R6), R5
    MOVHU (R25)(R6), R7
    CMP R7, R5
    BNE mem_colon
    B mem_match
mem_cmp_one:
    CBZ R19, mem_match
    MOVBU (R12), R5
    MOVBU (R25), R7
    CMP R7, R5
    BNE mem_colon
mem_match:
    MOVD $1, R21

mem_colon:
    CMP R1, R3
    BGE mem_notfound
    MOVBU (R0)(R3), R5
    CMP $0x3a, R5
    BEQ mem_value_space
    CMP $0x20, R5
    BHI mem_notfound
    LSR R5, R17, R6
    TBZ $0, R6, mem_notfound
    ADD $1, R3, R3
    B mem_colon
mem_value_space:
    ADD $1, R3, R3
    CMP R1, R3
    BGE mem_notfound
    MOVBU (R0)(R3), R5
    CMP $0x20, R5
    BHI mem_value
    LSR R5, R17, R6
    TBZ $0, R6, mem_value       // a control byte starts a literal
    B mem_value_space

mem_value:
    CBNZ R21, mem_found
    CMP $0x22, R5
    BEQ mem_string
    CMP $0x7b, R5
    BEQ mem_container
    CMP $0x5b, R5
    BEQ mem_container
    MOVD $·delim(SB), R11
mem_literal:
    MOVBU (R11)(R5), R6
    CBNZ R6, mem_comma
    ADD $1, R3, R3
    CMP R1, R3
    BGE mem_notfound
    MOVBU (R0)(R3), R5
    B mem_literal

mem_string:
    ADD $1, R3, R3
mem_string_block:
    ADD $16, R3, R5
    CMP R1, R5
    BHI mem_string_tail
    ADD R3, R0, R6
    VLD1 (R6), [V0.B16]
    VCMEQ V16.B16, V0.B16, V1.B16
    VCMEQ V17.B16, V0.B16, V2.B16
    WORD $0x0f0c8421            // SHRN V1.8B, V1.8H, #4
    WORD $0x0f0c8442            // SHRN V2.8B, V2.8H, #4
    VMOV V1.D[0], R5
    VMOV V2.D[0], R6
    SUB $1, R5, R7
    EOR R5, R7, R7
    TST R7, R6
    BNE mem_string_escape
    CBZ R5, mem_string_next
    RBIT R5, R5
    CLZ R5, R5
    LSR $2, R5, R5
    ADD R5, R3, R3
    ADD $1, R3, R3
    B mem_comma
mem_string_next:
    ADD $16, R3, R3
mem_string_long:
    // A long string, such as a base64 payload: look 64 bytes at a time for
    // a quote or a backslash, then let the step above handle it.
    ADD $64, R3, R5
    CMP R1, R5
    BHI mem_string_block
    ADD R3, R0, R6
    VLD1 (R6), [V0.B16, V1.B16, V2.B16, V3.B16]
    VCMEQ V16.B16, V0.B16, V4.B16
    VCMEQ V16.B16, V1.B16, V5.B16
    VCMEQ V16.B16, V2.B16, V6.B16
    VCMEQ V16.B16, V3.B16, V7.B16
    VCMEQ V17.B16, V0.B16, V8.B16
    VCMEQ V17.B16, V1.B16, V9.B16
    VCMEQ V17.B16, V2.B16, V10.B16
    VCMEQ V17.B16, V3.B16, V11.B16
    VORR V5.B16, V4.B16, V4.B16
    VORR V7.B16, V6.B16, V6.B16
    VORR V9.B16, V8.B16, V8.B16
    VORR V11.B16, V10.B16, V10.B16
    VORR V6.B16, V4.B16, V4.B16
    VORR V10.B16, V8.B16, V8.B16
    VORR V8.B16, V4.B16, V4.B16
    WORD $0x6e30a884            // UMAXV B4, V4.16B
    VMOV V4.B[0], R5
    CBNZ R5, mem_string_block
    ADD $64, R3, R3
    B mem_string_long
mem_string_escape:
    RBIT R6, R6
    CLZ R6, R6
    LSR $2, R6, R6
    ADD R6, R3, R3
    ADD $2, R3, R3
    B mem_string_block
mem_string_tail:
    CMP R1, R3
    BGE mem_notfound
    MOVBU (R0)(R3), R5
    ADD $1, R3, R3
    CMP $0x22, R5
    BEQ mem_comma
    CMP $0x5c, R5
    BNE mem_string_tail
    ADD $1, R3, R3
    B mem_string_tail

mem_container:
    MOVD $0, R8                 // depth
    MOVD $0, R9                 // inStr
    MOVD $0, R10                // esc
mskip_block:
    ADD $64, R3, R5
    CMP R1, R5
    BHI mskip_tail
    ADD R3, R0, R6
    VLD1 (R6), [V0.B16, V1.B16, V2.B16, V3.B16]
mskip_classify:
    CLASSIFY
mskip_masked:
    ORR R10, R12, R5
    CBNZ R5, mskip_escapes
    CBZ R9, mskip_plain
    CBZ R4, mskip_instring
mskip_plain:
    PREFIXXOR(R4, R5)
    EOR R9, R5, R5
mskip_strings:
    ASR $63, R5, R9
    BIC R5, R13, R13
    BIC R5, R14, R14
mskip_depth:
    CBZ R14, mskip_next
    NEG R14, R5
    AND R14, R5, R5
    SUB $1, R5, R11
    AND R13, R11, R11
    CBZ R11, mskip_depth_dec
    BIC R11, R13, R13
    SUB $1, R11, R6
    ANDS R11, R6, R6
    BEQ mskip_depth_one
    POPCNT64(R11)
    ADD R11, R8, R8
mskip_depth_dec:
    EOR R5, R14, R14
    SUBS $1, R8, R8
    BEQ mskip_found
    B mskip_depth
mskip_depth_one:
    CBZ R8, mskip_found
    EOR R5, R14, R14
    B mskip_depth
mskip_next:
    CBZ R13, mskip_advance
    POPCNT64(R13)
    ADD R13, R8, R8
mskip_advance:
    ADD $64, R3, R3
    B mskip_block
mskip_instring:
    ADD $64, R3, R3
mskip_instring_loop:
    ADD $64, R3, R5
    CMP R1, R5
    BHI mskip_tail
    ADD R3, R0, R6
    VLD1 (R6), [V0.B16, V1.B16, V2.B16, V3.B16]
    VCMEQ V16.B16, V0.B16, V4.B16
    VCMEQ V16.B16, V1.B16, V5.B16
    VCMEQ V16.B16, V2.B16, V6.B16
    VCMEQ V16.B16, V3.B16, V7.B16
    VCMEQ V17.B16, V0.B16, V8.B16
    VCMEQ V17.B16, V1.B16, V9.B16
    VCMEQ V17.B16, V2.B16, V10.B16
    VCMEQ V17.B16, V3.B16, V11.B16
    VORR V5.B16, V4.B16, V4.B16
    VORR V7.B16, V6.B16, V6.B16
    VORR V9.B16, V8.B16, V8.B16
    VORR V11.B16, V10.B16, V10.B16
    VORR V6.B16, V4.B16, V4.B16
    VORR V10.B16, V8.B16, V8.B16
    VORR V8.B16, V4.B16, V4.B16
    WORD $0x6e30a884
    VMOV V4.B[0], R5
    CBNZ R5, mskip_classify
    ADD $64, R3, R3
    B mskip_instring_loop
mskip_escapes:
    MOVD R12, R16
    BIC R10, R12, R12
    LSL $1, R12, R5
    BIC R5, R12, R11
    MOVD $0x5555555555555555, R6
    AND R6, R11, R5
    ADD R12, R5, R5
    BIC R12, R5, R5
    MOVD $0xaaaaaaaaaaaaaaaa, R7
    AND R7, R5, R5
    AND R7, R11, R7
    ADDS R12, R7, R7
    CSET HS, R11
    BIC R12, R7, R7
    AND R6, R7, R7
    ORR R7, R5, R5
    ORR R10, R5, R5
    MOVD R11, R10
    BIC R5, R4, R4
    PREFIXXOR(R4, R5)
    EOR R9, R5, R5
    BIC R5, R16, R16
    CBNZ R16, mem_slow
    B mskip_strings
mskip_tail:
    SUBS R3, R1, R5
    BLE mem_notfound
    ADD R3, R0, R6
    AND $4095, R6, R7
    CMP $(4096-64), R7
    BHI mskip_tail_page
    VLD1 (R6), [V0.B16, V1.B16, V2.B16, V3.B16]
    CLASSIFY
    MOVD $64, R6
    SUB R5, R6, R6
    MOVD $-1, R7
    LSR R6, R7, R7
    AND R7, R4, R4
    AND R7, R12, R12
    AND R7, R13, R13
    AND R7, R14, R14
    B mskip_masked
mskip_tail_page:
    ADD R1, R0, R6
    SUB $64, R6, R6
    VLD1 (R6), [V0.B16, V1.B16, V2.B16, V3.B16]
    CLASSIFY
    MOVD $64, R6
    SUB R5, R6, R6
    LSR R6, R4, R4
    LSR R6, R12, R12
    LSR R6, R13, R13
    LSR R6, R14, R14
    B mskip_masked
mskip_found:
    RBIT R5, R5
    CLZ R5, R5
    ADD R3, R5, R3
    ADD $1, R3, R3

mem_comma:
    CMP R1, R3
    BGE mem_notfound
    MOVBU (R0)(R3), R5
    CMP $0x2c, R5
    BEQ mem_next
    CMP $0x20, R5
    BHI mem_notfound
    LSR R5, R17, R6
    TBZ $0, R6, mem_notfound
    ADD $1, R3, R3
    B mem_comma
mem_next:
    ADD $1, R3, R3
    B mem_member

mem_found:
    MOVD R3, v+40(FP)
    MOVD $0, at+48(FP)
    RET
mem_notfound:
    MOVD $-1, R5
    MOVD R5, v+40(FP)
    MOVD $0, at+48(FP)
    RET
mem_slow:
    MOVD $-3, R5
    MOVD R5, v+40(FP)
    MOVD R20, at+48(FP)
    RET
