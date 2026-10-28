//go:build arm64 && !purego

#include "textflag.h"

// The structure loads and stores transpose sixteen independent base64 groups
// without explicit zip/shuffle instructions. Every load and store stays inside
// its complete source/output group. A 12-byte vector tail reduces scalar work.
DATA encode_tail_pack<>+0(SB)/8, $0x0405030401020001
DATA encode_tail_pack<>+8(SB)/8, $0x0a0b090a07080607
DATA encode_tail_pack<>+16(SB)/8, $0xfffafff6fffafff6
DATA encode_tail_pack<>+24(SB)/8, $0xfffafff6fffafff6
DATA encode_tail_pack<>+32(SB)/8, $0x0008000400080004
DATA encode_tail_pack<>+40(SB)/8, $0x0008000400080004
GLOBL encode_tail_pack<>(SB), (NOPTR+RODATA), $48

TEXT ·encodeBlocks(SB), NOSPLIT, $0-64
	MOVD dst_base+0(FP), R0
	MOVD src_base+24(FP), R1
	MOVD src_len+32(FP), R2
	MOVD R1, R3
	MOVD alphabet+48(FP), R4
	MOVD dst_len+8(FP), R6
	LSR $2, R6, R6
	MOVD $3, R5
	MUL R5, R6, R6
	CMP R6, R2
	CSEL LT, R2, R6, R2
	CMP $12, R2
	BLT encode_done
	VLD1 (R4), [V16.B16, V17.B16, V18.B16, V19.B16]
	VMOVI $63, V20.B16
	CMP $48, R2
	BLT encode_tail_setup
	CMP $96, R2
	BLT encode_loop

encode_pair:
	VLD3.P 48(R1), [V0.B16, V1.B16, V2.B16]
	VLD3.P 48(R1), [V8.B16, V9.B16, V10.B16]
	VUSHR $2, V0.B16, V4.B16
	VUSHR $2, V8.B16, V12.B16
	VUSHR $4, V1.B16, V5.B16
	VUSHR $4, V9.B16, V13.B16
	VUSHR $6, V2.B16, V6.B16
	VUSHR $6, V10.B16, V14.B16
	VSLI $4, V0.B16, V5.B16
	VSLI $4, V8.B16, V13.B16
	VSLI $2, V1.B16, V6.B16
	VSLI $2, V9.B16, V14.B16
	VAND V20.B16, V5.B16, V5.B16
	VAND V20.B16, V13.B16, V13.B16
	VAND V20.B16, V6.B16, V6.B16
	VAND V20.B16, V14.B16, V14.B16
	VAND V20.B16, V2.B16, V7.B16
	VAND V20.B16, V10.B16, V15.B16
	VTBL V4.B16, [V16.B16, V17.B16, V18.B16, V19.B16], V4.B16
	VTBL V12.B16, [V16.B16, V17.B16, V18.B16, V19.B16], V12.B16
	VTBL V5.B16, [V16.B16, V17.B16, V18.B16, V19.B16], V5.B16
	VTBL V13.B16, [V16.B16, V17.B16, V18.B16, V19.B16], V13.B16
	VTBL V6.B16, [V16.B16, V17.B16, V18.B16, V19.B16], V6.B16
	VTBL V14.B16, [V16.B16, V17.B16, V18.B16, V19.B16], V14.B16
	VTBL V7.B16, [V16.B16, V17.B16, V18.B16, V19.B16], V7.B16
	VTBL V15.B16, [V16.B16, V17.B16, V18.B16, V19.B16], V15.B16
	VST4.P [V4.B16, V5.B16, V6.B16, V7.B16], 64(R0)
	VST4.P [V12.B16, V13.B16, V14.B16, V15.B16], 64(R0)
	SUB $96, R2, R2
	CMP $96, R2
	BGE encode_pair
	CMP $48, R2
	BLT encode_tail_setup

encode_loop:
	VLD3.P 48(R1), [V0.B16, V1.B16, V2.B16]
	VUSHR $2, V0.B16, V4.B16
	VSHL $4, V0.B16, V5.B16
	VSRI $4, V1.B16, V5.B16
	VAND V20.B16, V5.B16, V5.B16
	VSHL $2, V1.B16, V6.B16
	VSRI $6, V2.B16, V6.B16
	VAND V20.B16, V6.B16, V6.B16
	VAND V20.B16, V2.B16, V7.B16
	VTBL V4.B16, [V16.B16, V17.B16, V18.B16, V19.B16], V4.B16
	VTBL V5.B16, [V16.B16, V17.B16, V18.B16, V19.B16], V5.B16
	VTBL V6.B16, [V16.B16, V17.B16, V18.B16, V19.B16], V6.B16
	VTBL V7.B16, [V16.B16, V17.B16, V18.B16, V19.B16], V7.B16
	VST4.P [V4.B16, V5.B16, V6.B16, V7.B16], 64(R0)
	SUB $48, R2, R2
	CMP $48, R2
	BGE encode_loop

encode_tail_setup:
	CMP $12, R2
	BLT encode_done
	MOVD $encode_tail_pack<>(SB), R4
	VLD1 (R4), [V21.B16, V22.B16, V23.B16]
	WORD $0x4f0787f8 // MOVI V24.8H, #255
encode_tail_loop:
	CMP $16, R2
	BLT encode_tail_exact
	VLD1 (R1), [V0.B16]
	B encode_tail_pack
encode_tail_exact:
	FMOVD (R1), F0
	MOVWU 8(R1), R5
	VMOV R5, V0.S[2]
encode_tail_pack:
	VTBL V21.B16, [V0.B16], V0.B16
	WORD $0x6e764402 // USHL V2.8H, V0.8H, V22.8H
	WORD $0x6e774403 // USHL V3.8H, V0.8H, V23.8H
	VBIT V24.B16, V2.B16, V3.B16
	VAND V20.B16, V3.B16, V3.B16
	VTBL V3.B16, [V16.B16, V17.B16, V18.B16, V19.B16], V3.B16
	VST1.P [V3.B16], 16(R0)
	ADD $12, R1, R1
	SUB $12, R2, R2
	CMP $12, R2
	BGE encode_tail_loop

encode_done:
	SUB R3, R1, R0
	MOVD R0, ret+56(FP)
	RET

