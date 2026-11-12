//go:build arm64 && !purego

#include "textflag.h"

// Compare 16 bytes after applying an OR mask.
#define PAIR_EQ(src, mask, needle) \
	VORR mask.B16, src.B16, src.B16; \
	VCMEQ needle.B16, src.B16, src.B16

// func pairNEON(a, b *byte, n int, r1, r2, m1, m2 byte) int
// n >= 16. Each vector load stays within the n candidate positions.
TEXT ·pairNEON(SB), NOSPLIT, $0-40
	MOVD a+0(FP), R0
	MOVD b+8(FP), R1
	MOVD n+16(FP), R2
	MOVBU r1+24(FP), R4
	VMOV R4, V0.B16
	MOVBU r2+25(FP), R4
	VMOV R4, V1.B16
	MOVBU m1+26(FP), R4
	VMOV R4, V2.B16
	MOVBU m2+27(FP), R4
	VMOV R4, V3.B16
	MOVD $0, R3
	SUB $32, R2, R6
	SUB $16, R2, R7
	CMP $32, R2
	BLT pair16

pair32:
	ADD R3, R0, R4
	VLD1 (R4), [V4.B16, V5.B16]
	PAIR_EQ(V4, V2, V0)
	PAIR_EQ(V5, V2, V0)
	// Avoid the second load stream when the rare byte is absent.
	VORR V4.B16, V5.B16, V6.B16
	VUADDLV V6.B16, V6
	VMOV V6.H[0], R8
	CBZ R8, pairnext32
	ADD R3, R1, R4
	VLD1 (R4), [V6.B16, V7.B16]
	PAIR_EQ(V6, V3, V1)
	PAIR_EQ(V7, V3, V1)
	VAND V6.B16, V4.B16, V4.B16
	VAND V7.B16, V5.B16, V5.B16
	VMOV V4.D[0], R8
	VMOV V4.D[1], R9
	ORR R8, R9, R10
	CBNZ R10, pairfound
	VMOV V5.D[0], R8
	VMOV V5.D[1], R9
	ORR R8, R9, R10
	CBZ R10, pairnext32
	ADD $16, R3, R3
	B pairfound
pairnext32:
	ADD $32, R3, R3
	CMP R6, R3
	BLE pair32

pair16:
	CMP R7, R3
	BLE pairload16
	CMP R2, R3
	BGE pairnone
	// The final vector overlaps only positions already known not to
	// match, so its first match is also the first remaining match.
	MOVD R7, R3
pairload16:
	ADD R3, R0, R4
	VLD1 (R4), [V4.B16]
	ADD R3, R1, R4
	VLD1 (R4), [V5.B16]
	PAIR_EQ(V4, V2, V0)
	PAIR_EQ(V5, V3, V1)
	VAND V5.B16, V4.B16, V4.B16
	VMOV V4.D[0], R8
	VMOV V4.D[1], R9
	ORR R8, R9, R10
	CBNZ R10, pairfound
	ADD $16, R3, R3
	B pair16

pairfound:
	CBNZ R8, pairlow
	MOVD R9, R8
	ADD $8, R3, R3
pairlow:
	RBIT R8, R8
	CLZ R8, R8
	ADD R8>>3, R3, R3
	MOVD R3, ret+32(FP)
	RET
pairnone:
	MOVD $-1, R0
	MOVD R0, ret+32(FP)
	RET

// CLASS leaves 0xff in non-member lanes. The high-nibble table is zero
// for 8..15, so bytes above ASCII cannot match.
// V0 = low table, V1 = high table, V2 = 0x0f, V31 = zero.
#define CLASS(src, dst, tmp) \
	VUSHR $4, src.B16, tmp.B16; \
	VAND V2.B16, src.B16, dst.B16; \
	VTBL dst.B16, [V0.B16], dst.B16; \
	VTBL tmp.B16, [V1.B16], tmp.B16; \
	VAND tmp.B16, dst.B16, dst.B16; \
	VCMEQ V31.B16, dst.B16, dst.B16

// func spanNEON(p *byte, n int, lo, hi *[16]byte, inv byte) int
// n >= 16. inv is 0 for the first non-member, 255 for the first member.
TEXT ·spanNEON(SB), NOSPLIT, $0-48
	MOVD p+0(FP), R0
	MOVD n+8(FP), R1
	MOVD lo+16(FP), R2
	MOVD hi+24(FP), R3
	VLD1 (R2), [V0.B16]
	VLD1 (R3), [V1.B16]
	VMOVI $15, V2.B16
	MOVBU inv+32(FP), R2
	VMOV R2, V3.B16
	VEOR V31.B16, V31.B16, V31.B16
	MOVD $0, R2
	SUB $32, R1, R6
	SUB $16, R1, R7
	CMP $32, R1
	BLT span16

span32:
	ADD R2, R0, R3
	VLD1 (R3), [V4.B16, V5.B16]
	CLASS(V4, V4, V6)
	CLASS(V5, V5, V6)
	VEOR V3.B16, V4.B16, V4.B16
	VEOR V3.B16, V5.B16, V5.B16
	VORR V4.B16, V5.B16, V6.B16
	VUADDLV V6.B16, V6
	VMOV V6.H[0], R8
	CBZ R8, spannext32
	VMOV V4.D[0], R8
	VMOV V4.D[1], R9
	ORR R8, R9, R10
	CBNZ R10, spanfound
	VMOV V5.D[0], R8
	VMOV V5.D[1], R9
	ADD $16, R2, R2
	B spanfound
spannext32:
	ADD $32, R2, R2
	CMP R6, R2
	BLE span32

span16:
	CMP R7, R2
	BLE spanload16
	CMP R1, R2
	BGE spanend
	MOVD R7, R2
spanload16:
	ADD R2, R0, R3
	VLD1 (R3), [V4.B16]
	CLASS(V4, V4, V6)
	VEOR V3.B16, V4.B16, V4.B16
	VMOV V4.D[0], R8
	VMOV V4.D[1], R9
	ORR R8, R9, R10
	CBNZ R10, spanfound
	ADD $16, R2, R2
	B span16

spanfound:
	CBNZ R8, spanlow
	MOVD R9, R8
	ADD $8, R2, R2
spanlow:
	RBIT R8, R8
	CLZ R8, R8
	ADD R8>>3, R2, R2
	MOVD R2, ret+40(FP)
	RET
spanend:
	MOVD R1, ret+40(FP)
	RET

// Pack 16 membership flags into a 16-bit integer. After selecting one
// weight per lane, three pairwise sums collect each group of eight bytes.
// The groups sum to at most 255, so none of the byte additions overflow.
#define PACK(src, dst) \
	VEOR V3.B16, src.B16, src.B16; \
	VAND V16.B16, src.B16, src.B16; \
	VADDP src.B16, src.B16, src.B16; \
	VADDP src.B16, src.B16, src.B16; \
	VADDP src.B16, src.B16, src.B16; \
	VMOV src.H[0], dst

// func classMasksNEON(p *byte, n int, lo, hi *[16]byte, out *uint64)
TEXT ·classMasksNEON(SB), NOSPLIT, $0-40
	MOVD p+0(FP), R0
	MOVD n+8(FP), R1
	MOVD lo+16(FP), R2
	MOVD hi+24(FP), R3
	MOVD out+32(FP), R4
	VLD1 (R2), [V0.B16]
	VLD1 (R3), [V1.B16]
	VMOVI $15, V2.B16
	VMOVI $255, V3.B16
	VEOR V31.B16, V31.B16, V31.B16
	MOVD $0x8040201008040201, R5
	VMOV R5, V16.D2
	CMP $64, R1
	BLT masksdone
masksloop:
	VLD1.P 64(R0), [V4.B16, V5.B16, V6.B16, V7.B16]
	CLASS(V4, V4, V8)
	CLASS(V5, V5, V8)
	CLASS(V6, V6, V8)
	CLASS(V7, V7, V8)
	PACK(V4, R5)
	PACK(V5, R6)
	PACK(V6, R7)
	PACK(V7, R8)
	ORR R6<<16, R5, R5
	ORR R7<<32, R5, R5
	ORR R8<<48, R5, R5
	MOVD.P R5, 8(R4)
	SUB $64, R1, R1
	CMP $64, R1
	BGE masksloop
masksdone:
	RET
