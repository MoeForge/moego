//go:build arm64 && !purego

#include "textflag.h"

// func hashAESNEON(key *[24]uint64, p *byte, n int, tail *[128]byte, total int) uint64
//
// Hashes p[:n], n a positive multiple of 128, and then tail, in eight
// lanes of 16 bytes using ARMv8 AESE/AESMC instructions, producing the exact
// same output as refHashAES / x86 hashAES.
TEXT ·hashAESNEON(SB), NOSPLIT, $0-48
    MOVD key+0(FP), R8
    MOVD p+8(FP), R1
    MOVD n+16(FP), R2
    MOVD tail+24(FP), R3

    VLD1 (R8), [V0.B16, V1.B16, V2.B16, V3.B16]
    ADD $64, R8, R4
    VLD1 (R4), [V4.B16, V5.B16, V6.B16, V7.B16]
    VEOR V15.B16, V15.B16, V15.B16

    // Pre-apply SubBytes+ShiftRows+MixColumns to initial lanes so each loop
    // iteration only needs AESE(V_data, V_lane) + AESMC(V_lane).
    AESE V15.B16, V0.B16
    AESMC V0.B16, V0.B16
    AESE V15.B16, V1.B16
    AESMC V1.B16, V1.B16
    AESE V15.B16, V2.B16
    AESMC V2.B16, V2.B16
    AESE V15.B16, V3.B16
    AESMC V3.B16, V3.B16
    AESE V15.B16, V4.B16
    AESMC V4.B16, V4.B16
    AESE V15.B16, V5.B16
    AESMC V5.B16, V5.B16
    AESE V15.B16, V6.B16
    AESMC V6.B16, V6.B16
    AESE V15.B16, V7.B16
    AESMC V7.B16, V7.B16

hash_loop:
    VLD1.P 64(R1), [V8.B16, V9.B16, V10.B16, V11.B16]
    VLD1.P 64(R1), [V12.B16, V13.B16, V14.B16, V15.B16]
    AESE V8.B16, V0.B16
    AESMC V0.B16, V0.B16
    AESE V9.B16, V1.B16
    AESMC V1.B16, V1.B16
    AESE V10.B16, V2.B16
    AESMC V2.B16, V2.B16
    AESE V11.B16, V3.B16
    AESMC V3.B16, V3.B16
    AESE V12.B16, V4.B16
    AESMC V4.B16, V4.B16
    AESE V13.B16, V5.B16
    AESMC V5.B16, V5.B16
    AESE V14.B16, V6.B16
    AESMC V6.B16, V6.B16
    AESE V15.B16, V7.B16
    AESMC V7.B16, V7.B16
    SUBS $128, R2, R2
    BNE hash_loop

    ADD $128, R8, R4
    VLD1 (R4), [V16.B16, V17.B16, V18.B16]
    VLD1 (R3), [V8.B16, V9.B16, V10.B16, V11.B16]
    ADD $64, R3, R4
    VLD1 (R4), [V12.B16, V13.B16, V14.B16, V15.B16]

    VEOR V16.B16, V8.B16, V8.B16
    VEOR V16.B16, V9.B16, V9.B16
    VEOR V16.B16, V10.B16, V10.B16
    VEOR V16.B16, V11.B16, V11.B16
    VEOR V16.B16, V12.B16, V12.B16
    VEOR V16.B16, V13.B16, V13.B16
    VEOR V16.B16, V14.B16, V14.B16
    VEOR V16.B16, V15.B16, V15.B16

    // Even lanes continue with AESE+AESMC; odd lanes finish with VEOR to become round keys.
    AESE V8.B16, V0.B16
    AESMC V0.B16, V0.B16
    VEOR V9.B16, V1.B16, V1.B16
    AESE V10.B16, V2.B16
    AESMC V2.B16, V2.B16
    VEOR V11.B16, V3.B16, V3.B16
    AESE V12.B16, V4.B16
    AESMC V4.B16, V4.B16
    VEOR V13.B16, V5.B16, V5.B16
    AESE V14.B16, V6.B16
    AESMC V6.B16, V6.B16
    VEOR V15.B16, V7.B16, V7.B16

    // Fold 8 -> 4 -> 2 -> 1 lanes.
    AESE V1.B16, V0.B16
    AESMC V0.B16, V0.B16
    VEOR V3.B16, V2.B16, V2.B16
    AESE V5.B16, V4.B16
    AESMC V4.B16, V4.B16
    VEOR V7.B16, V6.B16, V6.B16

    AESE V2.B16, V0.B16
    AESMC V0.B16, V0.B16
    VEOR V6.B16, V4.B16, V4.B16

    MOVD total+32(FP), R4
    VEOR V1.B16, V1.B16, V1.B16
    VMOV R4, V1.D[0]
    VEOR V1.B16, V4.B16, V4.B16

    AESE V4.B16, V0.B16
    AESMC V0.B16, V0.B16

    AESE V17.B16, V0.B16
    AESMC V0.B16, V0.B16
    AESE V18.B16, V0.B16
    AESMC V0.B16, V0.B16
    VEOR V17.B16, V0.B16, V0.B16

    VMOV V0.D[0], R0
    MOVD R0, ret+40(FP)
    RET
